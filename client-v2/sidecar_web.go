//go:build zgj_sidecar

// sidecar_web.go —— v3 容器 web 模式入口（sidecar P0，spec=
// research/web-container-design.md §1/§2）。
//
// 与 runStdioSidecar（Electron stdio 模式）走**同一个 sidecarRuntime**：
// 方法表/并发槽/收尾链零分叉，这里只换传输（WS 帧=stdio 信封原样 JSON）。
//
// 端点（容器单端口 8080，compose 映射 9320）：
//
//	GET  /            前端静态（ZGJ_WEB_STATIC 磁盘目录，hash 路由无需深链回退）
//	POST /login       账号密码（ZGJ_WEB_USER/ZGJ_WEB_PASS）→ session cookie
//	POST /logout      销毁 session
//	GET  /health      无鉴权存活探针（返回 {ok,mode:"web"}）
//	GET  /rpc         WS 升级；帧=protoRequest / 响应行 / 事件行（与 stdio 同构）
//
// 鉴权：env 缺失→启动即拒（防裸奔）；session=内存 map（容器重启=全员重登，
// 强度=内部工具级，spec §7）。/video/seg、/file 代理端点在 P1/P2 批次加入
// （同一 mux + 同一鉴权中间件，此处先留接口注释位）。
package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// webAuthEnv 是容器鉴权的最小凭证面。缺失其一容器拒绝启动。
type webAuthEnv struct {
	User string
	Pass string
}

func loadWebAuthEnv() (webAuthEnv, error) {
	var a webAuthEnv
	a.User = strings.TrimSpace(os.Getenv("ZGJ_WEB_USER"))
	a.Pass = os.Getenv("ZGJ_WEB_PASS")
	if a.User == "" || a.Pass == "" {
		return a, fmt.Errorf("web 模式必须设置 ZGJ_WEB_USER 与 ZGJ_WEB_PASS（防裸奔，spec §2）")
	}
	return a, nil
}

// webSession 是内存态登录会话（容器重启即失效，可接受）。
type webSession struct {
	token   string
	created time.Time
}

type webAuthStore struct {
	mu  sync.Mutex
	by  map[string]webSession
	crc int
}

func newWebAuthStore() *webAuthStore {
	return &webAuthStore{by: make(map[string]webSession)}
}

func (s *webAuthStore) add() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b)
	s.mu.Lock()
	s.by[tok] = webSession{token: tok, created: time.Now()}
	s.crc++
	if s.crc%64 == 0 { // 惰性清理 30 天前会话，防长期运行无界增长
		s.gcLocked()
	}
	s.mu.Unlock()
	return tok, nil
}

func (s *webAuthStore) valid(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.by[token]
	if !ok {
		return false
	}
	if time.Since(s.by[token].created) > 30*24*time.Hour {
		delete(s.by, token)
		return false
	}
	return true
}

func (s *webAuthStore) gcLocked() {
	for t, v := range s.by {
		if time.Since(v.created) > 30*24*time.Hour {
			delete(s.by, t)
		}
	}
}

func (s *webAuthStore) remove(token string) {
	s.mu.Lock()
	delete(s.by, token)
	s.mu.Unlock()
}

const sessionCookie = "zgj_session"

// wsHub 是 web 模式的侧车事件广播器：把 app 侧的下行事件扇出到所有已认证
// WS 连接。stdio 模式是「单前端」；web 允许多浏览器同时连（事件都发）。
type wsHub struct {
	up   websocket.Upgrader
	mu   sync.Mutex
	cons map[*websocket.Conn]struct{}
}

func newWSHub() *wsHub {
	return &wsHub{
		up: websocket.Upgrader{
			ReadBufferSize:  1 << 14,
			WriteBufferSize: 1 << 14,
			// 同源即可（前端与 /rpc 同端口）；跨源由浏览器同源策略兜底。
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		cons: make(map[*websocket.Conn]struct{}),
	}
}

// broadcast 把一行事件 JSON 扇出给所有连接。写超时防半开连接卡住扇出。
func (h *wsHub) broadcast(line string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.cons {
		_ = c.SetWriteDeadline(time.Now().Add(3 * time.Second))
		if err := c.WriteMessage(websocket.TextMessage, []byte(line)); err != nil {
			_ = c.Close()
			delete(h.cons, c)
		}
	}
}

// serve 升级 /rpc 并接一个前端会话：读帧→handleProtoRequest，响应帧回写，
// 事件 sink 经 hub 扇出。respond 的 id 语义与 stdio 完全一致。
func (h *wsHub) serve(auth *webAuthStore, rt *sidecarRuntime, w http.ResponseWriter, r *http.Request) {
	// 鉴权：WS 握手带 cookie（与页面同源）。
	c, err := r.Cookie(sessionCookie)
	if err != nil || !auth.valid(c.Value) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := h.up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	h.mu.Lock()
	h.cons[conn] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.cons, conn)
		h.mu.Unlock()
		_ = conn.Close()
	}()

	// 单写者：一个前端会话的响应帧与扇出事件帧共用一条 conn，互斥写。
	var wmu sync.Mutex
	respond := func(id *json.RawMessage, result string, err error) {
		if id == nil {
			return
		}
		idv, _ := json.Marshal(*id)
		wmu.Lock()
		defer wmu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err != nil {
			_ = conn.WriteJSON(map[string]any{"id": json.RawMessage(idv), "error": err.Error()})
			return
		}
		_ = conn.WriteJSON(map[string]any{"id": json.RawMessage(idv), "result": result})
	}

	// 读循环：TextMessage 一帧一个 protoRequest（与 stdio「一行一个」同构）。
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		if len(raw) == 0 {
			continue
		}
		var req protoRequest
		if json.Unmarshal(raw, &req) != nil {
			fmt.Fprintf(os.Stderr, "[sidecar-web] 跳过非协议帧: %.120s\n", raw)
			continue
		}
		if rt.handleProtoRequest(req, respond) {
			// __shutdown：容器版随进程退出。
			break
		}
	}
}

// buildWebMux 构建 web 模式的 HTTP 路由表（抽函数是为了 httptest 可注入）。
// 鉴权中间件：除 /health、/login、静态外，/rpc 在握手时查 cookie。
func buildWebMux(auth webAuthEnv, rt *sidecarRuntime, hub *wsHub, store *webAuthStore, staticDir string) *http.ServeMux {
	mux := http.NewServeMux()
	// 无鉴权存活探针（编排/健康检查用，不泄密）。
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"mode":"web"}`))
	})
	// 登录：常量时间比对，防时序侧信道（内部工具级，成本极低）。
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			User string `json:"user"`
			Pass string `json:"pass"`
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		uOK := subtle.ConstantTimeCompare([]byte(body.User), []byte(auth.User)) == 1
		pOK := subtle.ConstantTimeCompare([]byte(body.Pass), []byte(auth.Pass)) == 1
		if !uOK || !pOK {
			http.Error(w, "bad credentials", http.StatusUnauthorized)
			return
		}
		tok, err := store.add()
		if err != nil {
			http.Error(w, "internal", http.StatusInternalServerError)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookie,
			Value:    tok,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   int(30 * 24 * time.Hour / time.Second),
		})
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(sessionCookie); err == nil {
			store.remove(c.Value)
		}
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	// 登录态探测（前端启动时区分「登录页」与「主应用」）。
	mux.HandleFunc("/whoami", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		c, err := r.Cookie(sessionCookie)
		if err != nil || !store.valid(c.Value) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		b, _ := json.Marshal(map[string]any{"user": auth.User, "mode": "web"})
		_, _ = w.Write(b)
	})
	// WS RPC 桥面。
	mux.HandleFunc("/rpc", func(w http.ResponseWriter, r *http.Request) {
		hub.serve(store, rt, w, r)
	})
	// 静态前端（hash 路由，/ 回退 index.html）。
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.ServeFile(w, r, staticDir+"/index.html")
			return
		}
		http.StripPrefix("/", http.FileServer(http.Dir(staticDir))).ServeHTTP(w, r)
	})
	return mux
}

// runWebSidecar 是 web 模式服务主体：起 HTTP mux + WS hub，阻塞直到进程退出。
// 事件 sink 接到 hub.broadcast（appLogEvent 之外，app 侧的 player:tick 等下行）。
func runWebSidecar(addr string, staticDir string) int {
	auth, err := loadWebAuthEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[sidecar-web] %v\n", err)
		return 2
	}
	rt := newSidecarRuntime()
	hub := newWSHub()
	store := newWebAuthStore()

	// 事件 sink 接到 WS 扇出（stdio 与 web 各自接线同一全局，语义一致）。
	sidecarEventSink = func(name string, data any) {
		b, _ := json.Marshal(protoEvent{Event: name, Data: data})
		hub.broadcast(string(b) + "\n")
	}

	mux := buildWebMux(auth, rt, hub, store, staticDir)
	// 无鉴权存活探针（编排/健康检查用，不泄密）。
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	fmt.Fprintf(os.Stderr, "[sidecar-web] listening %s (static=%s user=%s)\n", addr, staticDir, auth.User)
	// 监听错误（端口占用等）要能传出去，而不是静默。
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[sidecar-web] listen %s: %v\n", addr, err)
		return 2
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	<-errCh // 容器信号/主循环退出前一直跑；serve 返回即收尾。
	rt.finish()
	return 0
}
