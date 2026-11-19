// mcp/main.go —— 真果鉴 v3 容器 MCP server（stdio，AI 接入面）。
//
// 定位：让 AI（DSH/Claude/Codex 等任何 MCP 客户端）驱动 115 上跑的 zgj-v3
// 容器完成「搜剧 → 看详情 → 下载 → 等完成 → 合并 → 等完成 → 拿成品交接地址」
// 全链；上传侧（另一个 MCP 或脚本）消费 zgj_outputs 给的 fileUrl
// （容器 /file 端点，需登录 cookie，支持 Range）或 hostPath（root 属主，
// 仅 root/sudo 上下文可直读），本 server 不碰上传。
//
// 传输：stdio JSON-RPC 2.0（MCP 协议，newline-delimited）。只占 stdout 发
// JSON-RPC 帧，日志一律 stderr。
//
// 后端：容器 sidecar 的 /rpc WebSocket（与 Web 前端同一桥面、同一鉴权）：
//   - HTTP POST /login {user,pass} 拿 zgj_session cookie（env 注入凭据）
//   - WS ws://<host>/rpc 带 cookie，信封 {"id","method","args":[raw...]}
//   - string 形参=JSON 字符串（文档型参数如 dramaJSON 双重编码）、int/bool=字面量
//     —— 与 sidecar 反射解码侧（sidecar_main.go）严格对齐，P3 已执行级验证。
//
// 构建（仓库根）：
//   CGO_ENABLED=0 go build -o client-v2/build/bin/zgj-mcp ./client-v2/mcp
// 环境变量：
//   ZGJ_WEB_URL        容器 HTTP 地址（默认 http://127.0.0.1:9320）
//   ZGJ_WEB_USER       登录账号（必填）
//   ZGJ_WEB_PASS       登录密码（必填）
//   ZGJ_DATA_HOST_DIR  容器 /data 在 host 上的挂载点（默认 /home/ryen/Git/zhenguojian/client-v2/data）
//   ZGJ_MCP_POLL_MS    wait 类工具轮询间隔（默认 3000）
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ---------------------------------------------------------------- 配置

type config struct {
	baseURL   string
	user      string
	pass      string
	hostData  string // 容器 /data → host 前缀
	pollEvery time.Duration
}

func loadConfig() *config {
	c := &config{
		baseURL:   envOr("ZGJ_WEB_URL", "http://127.0.0.1:9320"),
		user:      os.Getenv("ZGJ_WEB_USER"),
		pass:      os.Getenv("ZGJ_WEB_PASS"),
		hostData:  envOr("ZGJ_DATA_HOST_DIR", "/home/ryen/Git/zhenguojian/client-v2/data"),
		pollEvery: 3000 * time.Millisecond,
	}
	if ms := os.Getenv("ZGJ_MCP_POLL_MS"); ms != "" {
		if n, err := strconv.Atoi(ms); err == nil && n >= 500 {
			c.pollEvery = time.Duration(n) * time.Millisecond
		}
	}
	c.baseURL = strings.TrimRight(c.baseURL, "/")
	c.hostData = strings.TrimRight(c.hostData, "/")
	return c
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func (c *config) check() error {
	if c.user == "" || c.pass == "" {
		return errors.New("缺少凭据：请设置 ZGJ_WEB_USER / ZGJ_WEB_PASS")
	}
	return nil
}

// toHost 把容器内 /data/... 路径换算成 host 挂载路径（交接契约的便捷形态）。
func (c *config) toHost(p string) string {
	if strings.HasPrefix(p, "/data/") {
		return c.hostData + strings.TrimPrefix(p, "/data")
	}
	return p // 非 /data 前缀原样返回（调用方自行判断）
}

// toFileURL 容器内路径 → 容器 /file 端点 URL（交接契约的**主形态**）。
// 为什么主形态是 URL 而不是 host 路径：容器以 root 跑，产物 root 属主、
// 数据树 0700——host 上的 AI（ryen uid）直接读文件会被 Permission denied
// （2026-11-19 真实链实测撞到的交接断点）。/file 端点由容器内 root 进程
// 出流（下载根白名单+原生 Range，P2 已验证），天然跨属主边界。
func (c *config) toFileURL(p string) string {
	return c.baseURL + "/file?p=" + url.QueryEscape(p)
}

// ---------------------------------------------------------------- WS 桥

type bridge struct {
	cfg    *config
	mu     sync.Mutex
	ws     *websocket.Conn
	cookie string
	idc    int64
}

func newBridge(cfg *config) *bridge { return &bridge{cfg: cfg} }

func (b *bridge) login() error {
	body, _ := json.Marshal(map[string]string{"user": b.cfg.user, "pass": b.cfg.pass})
	resp, err := http.Post(b.cfg.baseURL+"/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("登录失败（容器可达？）：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("登录被拒（%d），检查凭据", resp.StatusCode)
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == "zgj_session" {
			b.cookie = ck.Value
			return nil
		}
	}
	return errors.New("登录响应未带 zgj_session cookie")
}

func (b *bridge) dial() error {
	u := strings.Replace(b.cfg.baseURL, "http://", "ws://", 1) + "/rpc"
	hdr := http.Header{"Cookie": []string{"zgj_session=" + b.cookie}}
	ws, _, err := websocket.DefaultDialer.Dial(u, hdr)
	if err != nil {
		return err
	}
	b.ws = ws
	return nil
}

// ensure 保证有活连接（cookie 内存态，容器重启后会 401 → 重新登录重连）。
func (b *bridge) ensure() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ws != nil {
		return nil
	}
	if err := b.login(); err != nil {
		return err
	}
	if err := b.dial(); err != nil {
		b.drop()
		if strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "403") {
			if lerr := b.login(); lerr == nil {
				err = b.dial()
			}
		}
		if err != nil {
			return fmt.Errorf("连 /rpc 失败：%w", err)
		}
	}
	return nil
}

func (b *bridge) drop() {
	if b.ws != nil {
		b.ws.Close()
		b.ws = nil
	}
	b.cookie = ""
}

// call 发一次桥调用并等应答（串行语义，事件帧丢弃）。断连自动重登重试一次。
func (b *bridge) call(method string, args ...json.RawMessage) (string, error) {
	for attempt := 0; attempt < 2; attempt++ {
		if err := b.ensure(); err != nil {
			return "", err
		}
		b.idc++
		id := b.idc
		req, _ := json.Marshal(map[string]any{"id": id, "method": method, "args": args})
		b.mu.Lock()
		ws := b.ws
		b.mu.Unlock()
		if err := ws.WriteMessage(websocket.TextMessage, req); err != nil {
			b.drop()
			continue
		}
		_ = ws.SetReadDeadline(time.Now().Add(150 * time.Second))
		for {
			_, data, err := ws.ReadMessage()
			if err != nil {
				b.drop()
				if attempt == 0 {
					continue // 重连后重放本次调用
				}
				return "", fmt.Errorf("WS 读失败：%w", err)
			}
			var msg struct {
				ID     *int64          `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *string         `json:"error"`
			}
			if err := json.Unmarshal(data, &msg); err != nil || msg.ID == nil || *msg.ID != id {
				continue // 事件帧 / 解析噪音，丢弃
			}
			if msg.Error != nil {
				return "", errors.New(*msg.Error)
			}
			var s string
			if err := json.Unmarshal(msg.Result, &s); err != nil {
				return string(msg.Result), nil // 非字符串结果原样回
			}
			return s, nil
		}
	}
	return "", errors.New("WS 重连后仍失败")
}

// ---------------------------------------------------------------- 参数编码

// 与 P3 执行级验证过的 wire 口径一致：
//   - 普通 string 形参：json.Marshal(s)
//   - 文档型 string 形参（dramaJSON 等）：先 Marshal 出文档，再对文档串 Marshal
//   - int / bool：裸字面量

func strArg(s string) json.RawMessage { b, _ := json.Marshal(s); return b }
func intArg(n int64) json.RawMessage   { return json.RawMessage(strconv.FormatInt(n, 10)) }
func boolArg(v bool) json.RawMessage {
	if v {
		return json.RawMessage("true")
	}
	return json.RawMessage("false")
}
func objArg(v any) json.RawMessage {
	b1, err := json.Marshal(v)
	if err != nil {
		b1 = []byte("null")
	}
	b2, _ := json.Marshal(string(b1))
	return b2
}
func strListArg(v any) json.RawMessage { return objArg(v) }

// parseResult 把桥返回串解析为 any（JSON 文档→对象，否则原文）。
func parseResult(s string) any {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
		var v any
		if err := json.Unmarshal([]byte(t), &v); err == nil {
			return v
		}
	}
	return s
}

func mustMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func mustList(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}

func getS(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func getF(m map[string]any, k string) float64 {
	if v, ok := m[k].(float64); ok {
		return v
	}
	return 0
}

// ---------------------------------------------------------------- MCP 框架

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

var outMu sync.Mutex

func writeOut(v any) {
	b, _ := json.Marshal(v)
	outMu.Lock()
	_, _ = os.Stdout.Write(append(b, '\n'))
	outMu.Unlock()
}

func respond(id json.RawMessage, result any) {
	writeOut(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func respondErr(id json.RawMessage, code int, msg string) {
	writeOut(map[string]any{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": code, "message": msg},
	})
}

type toolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

// ---------------------------------------------------------------- 工具实现

// drama 对象形态（search/rankings 结果里的 items 元素）。
type drama = map[string]any

// dramaPass 原样透传 drama 对象（不裁剪字段）。为什么不做 brief：
// 下载/合并的 core 管线可能读 sourceId/metadataSchema/seasons 等 brief 之外的
// 字段，裁剪会让「搜索结果直接喂下载」在部分源上静默失败。宁可输出大一点，
// 也要保证 items 元素可**原样**传给 zgj_detail / zgj_download / zgj_merge。
func dramaPass(d drama) map[string]any { return d }

func (s *server) toolSources() (string, error) {
	raw, err := s.b.call("ListSources")
	if err != nil {
		return "", err
	}
	list := mustList(parseResult(raw))
	out := make([]map[string]any, 0, len(list))
	for _, it := range list {
		m := mustMap(it)
		out = append(out, map[string]any{
			"slug":       getS(m, "slug"),
			"name":       getS(m, "name"),
			"searchKind": getS(m, "searchKind"),
			"lanes":      m["lanes"],
			"enabled":    m["enabled"],
			"adult":      m["adult"],
		})
	}
	return pretty(out)
}

func (s *server) toolSearch(p map[string]any) (string, error) {
	query := strOf(p, "query")
	if query == "" {
		return "", errors.New("query 必填")
	}
	source := strOr(p, "source", "hongguo")
	page := intOr(p, "page", 1)
	raw, err := s.b.call("Catalog",
		strArg(source), strArg(query), strArg(""), intArg(int64(page)),
		strArg("search:"+source), boolArg(false))
	if err != nil {
		return "", err
	}
	m := mustMap(parseResult(raw))
	items := mustList(m["items"])
	out := make([]map[string]any, 0, len(items))
	for i, it := range items {
		if i >= 15 {
			break
		}
		out = append(out, dramaPass(mustMap(it)))
	}
	return pretty(map[string]any{
		"source": source, "page": page, "hasMore": m["hasMore"], "count": len(out),
		"items": out,
		"next":  "items 元素=drama 对象（原样透传，未裁剪）。需要分集/下载：把它原样作为 drama 参数传给 zgj_detail / zgj_download / zgj_merge",
	})
}

func (s *server) toolRankings(p map[string]any) (string, error) {
	board := strOr(p, "board", "hongguo-hot")
	page := intOr(p, "page", 1)
	raw, err := s.b.call("Rankings", strArg(board), intArg(int64(page)), boolArg(false))
	if err != nil {
		return "", err
	}
	m := mustMap(parseResult(raw))
	items := mustList(m["items"])
	out := make([]map[string]any, 0, len(items))
	for i, it := range items {
		if i >= 15 {
			break
		}
		d := dramaPass(mustMap(mustMap(it)["drama"]))
		if d["title"] == "" {
			d = dramaPass(mustMap(it)) // 兼容平铺形态
		}
		out = append(out, d)
	}
	return pretty(map[string]any{
		"board": board, "page": page, "count": len(out),
		"items": out,
		"next":  "items 元素=drama 对象（原样透传）。把它原样作为 drama 参数传给 zgj_detail / zgj_download / zgj_merge",
	})
}

func (s *server) toolDetail(p map[string]any) (string, error) {
	d, err := dramaOf(p)
	if err != nil {
		return "", err
	}
	raw, err := s.b.call("Detail", objArg(d))
	if err != nil {
		return "", err
	}
	m := mustMap(parseResult(raw))
	chs := mustList(m["chapters"])
	out := make([]map[string]any, 0, len(chs))
	for _, c := range chs {
		cm := mustMap(c)
		out = append(out, map[string]any{
			"index": cm["currentEpisode"], // 集号（1 基）
			"title": getS(cm, "title"),
		})
	}
	return pretty(map[string]any{
		"title":    getS(mustMap(m["drama"]), "title"),
		"chapters": out,
		"count":    len(out),
	})
}

func (s *server) toolDownload(p map[string]any) (string, error) {
	d, err := dramaOf(p)
	if err != nil {
		return "", err
	}
	eps := intList(p, "episodes")
	if len(eps) == 0 {
		return "", errors.New("episodes 必填（集号数组，1 基），例：[1,2,3]")
	}
	quality := intOr(p, "quality", 360)
	// 用 Detail 把集号 → chapter 对象（DownloadStart 要 {chapter,index} 对）。
	raw, err := s.b.call("Detail", objArg(d))
	if err != nil {
		return "", err
	}
	m := mustMap(parseResult(raw))
	byNum := map[float64]any{}
	for _, c := range mustList(m["chapters"]) {
		cm := mustMap(c)
		if n, ok := cm["currentEpisode"].(float64); ok {
			byNum[n] = c
		}
	}
	entries := make([]map[string]any, 0, len(eps))
	var missing []int
	for _, e := range eps {
		n := float64(e)
		ch, ok := byNum[n]
		if !ok {
			missing = append(missing, e)
			continue
		}
		entries = append(entries, map[string]any{"chapter": ch, "index": e})
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("以下集号在详情里不存在：%v", missing)
	}
	start, err := s.b.call("DownloadStart", objArg(d), strListArg(entries), intArg(int64(quality)))
	if err != nil {
		return "", err
	}
	sm := mustMap(parseResult(start))
	sm["quality"] = quality
	sm["episodes"] = eps
	sm["next"] = "用 zgj_wait_downloads 等这批集下载完成"
	return pretty(sm)
}

// jobDone 下载任务终态（P3 实网：completed；兼容 done/failed/error/cancel*）。
func jobDone(state string) bool {
	switch state {
	case "completed", "done", "failed", "error", "cancelled", "canceled", "aborted":
		return true
	}
	return false
}

func mergeDone(state string) bool {
	switch state {
	case "done", "failed", "canceled", "cancelled", "aborted":
		return true
	}
	return false
}

func (s *server) toolDownloads() (string, error) {
	raw, err := s.b.call("DownloadList")
	if err != nil {
		return "", err
	}
	return compactList(parseResult(raw))
}

func compactList(v any) (string, error) {
	m := mustMap(v)
	jobs := mustList(m["jobs"])
	jOut := make([]map[string]any, 0, len(jobs))
	for _, j := range jobs {
		jm := mustMap(j)
		d := mustMap(jm["drama"])
		jOut = append(jOut, map[string]any{
			"id":       getS(jm, "id"),
			"state":    getS(jm, "state"),
			"progress": jm["progress"],
			"ep":       jm["index"],
			"title":    getS(d, "title"),
			"quality":  jm["actualQuality"],
			"bytes":    jm["bytes"],
		})
	}
	mOut := make([]map[string]any, 0, len(mustList(m["merges"])))
	for _, x := range mustList(m["merges"]) {
		mm := mustMap(x)
		mOut = append(mOut, map[string]any{
			"id":         getS(mm, "id"),
			"state":      getS(mm, "state"),
			"stage":      getS(mm, "stage"),
			"progress":   mm["progress"],
			"title":      getS(mm, "title"),
			"output":     getS(mm, "output"),
			"outputName": getS(mm, "outputName"),
		})
	}
	return pretty(map[string]any{"jobs": jOut, "merges": mOut, "status": getS(m, "status")})
}

func (s *server) toolWaitDownloads(p map[string]any) (string, error) {
	timeout := time.Duration(intOr(p, "timeoutSec", 600)) * time.Second
	if timeout > 28*time.Minute {
		timeout = 28 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	for {
		raw, err := s.b.call("DownloadList")
		if err != nil {
			return "", err
		}
		m := mustMap(parseResult(raw))
		jobs := mustList(m["jobs"])
		active := 0
		for _, j := range jobs {
			if !jobDone(getS(mustMap(j), "state")) {
				active++
			}
		}
		if active == 0 {
			out, _ := compactList(mustMap(mustMap(parseResult(raw))))
			return pretty(map[string]any{
				"timedOut": false,
				"done":     len(jobs) == 0 || active == 0,
				"list":     jsonRaw(out),
				"next":     "下载完成后可 zgj_merge（dramas=[drama对象]）",
			})
		}
		if time.Now().After(deadline) {
			out, _ := compactList(mustMap(mustMap(parseResult(raw))))
			return pretty(map[string]any{
				"timedOut": true, "activeJobs": active, "list": jsonRaw(out),
				"next": "仍有任务进行中，可再次调用 zgj_wait_downloads 继续等",
			})
		}
		time.Sleep(s.b.cfg.pollEvery)
	}
}

func (s *server) toolMerge(p map[string]any) (string, error) {
	dramas := dramaList(p, "dramas")
	if len(dramas) == 0 {
		return "", errors.New("dramas 必填（drama 对象数组，来自 zgj_search/zgj_rankings 的 items）")
	}
	var raw string
	var err error
	if len(dramas) == 1 {
		raw, err = s.b.call("MergeStart", strArg("season"), objArg(dramas[0]), strArg("[]"))
	} else {
		raw, err = s.b.call("MergeStartMulti", strListArg(dramas))
	}
	if err != nil {
		return "", err
	}
	m := mustMap(parseResult(raw))
	m["next"] = "用 zgj_wait_merges 等合并完成，再 zgj_outputs 拿成品路径"
	return pretty(m)
}

func (s *server) toolWaitMerges(p map[string]any) (string, error) {
	timeout := time.Duration(intOr(p, "timeoutSec", 600)) * time.Second
	if timeout > 55*time.Minute {
		timeout = 55 * time.Minute
	}
	dramaID := strOf(p, "dramaId")
	deadline := time.Now().Add(timeout)
	for {
		raw, err := s.b.call("DownloadList")
		if err != nil {
			return "", err
		}
		m := mustMap(parseResult(raw))
		merges := mustList(m["merges"])
		targets := 0
		for _, x := range merges {
			mm := mustMap(x)
			if dramaID != "" && getS(mm, "dramaId") != dramaID {
				continue
			}
			if !mergeDone(getS(mm, "state")) {
				targets++
			}
		}
		if targets == 0 {
			out, _ := compactList(m)
			return pretty(map[string]any{
				"timedOut": false, "list": jsonRaw(out),
				"next":     "合并完成，用 zgj_outputs 拿成品 host 路径交接给上传侧",
			})
		}
		if time.Now().After(deadline) {
			out, _ := compactList(m)
			return pretty(map[string]any{
				"timedOut": true, "activeMerges": targets, "list": jsonRaw(out),
				"next": "仍有合并进行中，可再次调用 zgj_wait_merges 继续等",
			})
		}
		time.Sleep(s.b.cfg.pollEvery)
	}
}

// toolOutputs 交接面：合并成品（+可选分集成品）的容器路径 + host 路径。
func (s *server) toolOutputs(p map[string]any) (string, error) {
	raw, err := s.b.call("DownloadList")
	if err != nil {
		return "", err
	}
	m := mustMap(parseResult(raw))
	out := map[string]any{}
	var merges []map[string]any
	for _, x := range mustList(m["merges"]) {
		mm := mustMap(x)
		if getS(mm, "state") != "done" || getS(mm, "output") == "" {
			continue
		}
		cpath := getS(mm, "output")
		merges = append(merges, map[string]any{
			"title":         getS(mm, "title"),
			"fileUrl":       s.cfg.toFileURL(cpath), // 主交接形态
			"containerPath": cpath,
			"hostPath":      s.cfg.toHost(cpath),     // 便捷（root 属主，ryen 直读会拒）
			"bytes":          mm["bytes"],
		})
	}
	out["merged"] = merges
	if b, _ := p["includeEpisodes"].(bool); b {
		dirRaw, err := s.b.call("DownloadDirectory")
		var dir string
		if err == nil {
			dir = getS(mustMap(parseResult(dirRaw)), "directory")
		}
		var eps []map[string]any
		for _, j := range mustList(m["jobs"]) {
			jm := mustMap(j)
			if getS(jm, "state") != "completed" {
				continue
			}
			cpath := dir + "/" + getS(jm, "id") + "/media.mp4"
			d := mustMap(jm["drama"])
			eps = append(eps, map[string]any{
				"title":         getS(d, "title"),
				"ep":            jm["index"],
				"fileUrl":       s.cfg.toFileURL(cpath),
				"containerPath": cpath,
				"hostPath":      s.cfg.toHost(cpath),
				"bytes":          jm["bytes"],
			})
		}
		out["episodes"] = eps
	}
	out["auth"] = "fileUrl 需带 zgj_session cookie：先 POST " + s.cfg.baseURL + "/login {user,pass} 拿 cookie，再 GET fileUrl（支持 Range）。"
	out["note"] = "推荐走 fileUrl（容器内 root 出流，跨属主边界可靠）；hostPath 仅供 root/sudo 上下文直读（host 挂载 " + s.cfg.hostData + "）。"
	return pretty(out)
}

func (s *server) toolCancel(p map[string]any) (string, error) {
	kind := strOf(p, "kind")
	id := strOf(p, "id")
	if id == "" {
		return "", errors.New("id 必填（zgj_downloads 列表里的 id）")
	}
	var raw string
	var err error
	switch kind {
	case "download":
		raw, err = s.b.call("DownloadCancel", strArg(id))
	case "merge":
		raw, err = s.b.call("MergeCancel", strArg(id))
	default:
		return "", errors.New("kind 必须是 download 或 merge")
	}
	if err != nil {
		return "", err
	}
	return pretty(parseResult(raw))
}

// ---------------------------------------------------------------- 参数小工具

func strOf(p map[string]any, k string) string {
	if v, ok := p[k].(string); ok {
		return v
	}
	return ""
}

func strOr(p map[string]any, k, d string) string {
	if v := strOf(p, k); v != "" {
		return v
	}
	return d
}

func intOr(p map[string]any, k string, d int) int {
	switch v := p[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return d
}

func intList(p map[string]any, k string) []int {
	var out []int
	for _, v := range mustList(p[k]) {
		if n, ok := v.(float64); ok {
			out = append(out, int(n))
		}
	}
	return out
}

func dramaOf(p map[string]any) (drama, error) {
	d, ok := p["drama"].(map[string]any)
	if !ok || getS(d, "id") == "" {
		return nil, errors.New("drama 必填（对象，需含 id；取自 zgj_search/zgj_rankings 的 items 元素）")
	}
	return d, nil
}

func dramaList(p map[string]any, k string) []drama {
	var out []drama
	for _, v := range mustList(p[k]) {
		if m, ok := v.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func pretty(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	return string(b), err
}

// jsonRaw 把已格式化的 JSON 串以原始对象嵌进结果（避免转义）。
func jsonRaw(s string) any {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	return v
}

// ---------------------------------------------------------------- server + 工具表

type server struct {
	cfg *config
	b   *bridge
}

func schema(props map[string]any, required ...string) any {
	sc := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		sc["required"] = required
	}
	return sc
}

func (s *server) tools() []toolDef {
	dramaProp := map[string]any{
		"type": "object",
		"description": "drama 对象，原样取自 zgj_search / zgj_rankings 结果 items 的元素（必须含 id）",
	}
	return []toolDef{
		{Name: "zgj_sources",
			Description: "列出容器里所有视频源（slug/名称/搜索形态/是否启用）。zgj_search 的 source 参数就用这里的 slug。",
			InputSchema: schema(map[string]any{})},
		{Name: "zgj_search",
			Description: "按关键词搜索视频/短剧。返回剧列表（id/标题/集数/热度/封面，截前 15 条）。默认源 hongguo（红果）。",
			InputSchema: schema(map[string]any{
				"query":  map[string]any{"type": "string", "description": "搜索关键词"},
				"source": map[string]any{"type": "string", "description": "源 slug，默认 hongguo；可选值见 zgj_sources"},
				"page":   map[string]any{"type": "number", "description": "页码，默认 1"},
			}, "query")},
		{Name: "zgj_rankings",
			Description: "榜单找剧（如 hongguo-hot 热榜）。返回剧列表，元素结构同 zgj_search。",
			InputSchema: schema(map[string]any{
				"board": map[string]any{"type": "string", "description": "榜单 slug，默认 hongguo-hot"},
				"page":  map[string]any{"type": "number", "description": "页码，默认 1"},
			})},
		{Name: "zgj_detail",
			Description: "查一部剧的分集列表（集号 1 基）。下载前先查这里确认集号。",
			InputSchema: schema(map[string]any{"drama": dramaProp}, "drama")},
		{Name: "zgj_download",
			Description: "下载指定集（115 容器内执行，产物落容器下载目录）。episodes=集号数组（1 基），quality=清晰度（默认 360）。返回入队结果；接着用 zgj_wait_downloads 等完成。",
			InputSchema: schema(map[string]any{
				"drama":    dramaProp,
				"episodes": map[string]any{"type": "array", "items": map[string]any{"type": "number"}, "description": "集号（1 基），例 [1,2,3]"},
				"quality":  map[string]any{"type": "number", "description": "清晰度，默认 360"},
			}, "drama", "episodes")},
		{Name: "zgj_downloads",
			Description: "当前下载/合并任务快照（不等待）。jobs[]=下载任务（state/progress/集号/标题），merges[]=合并任务。",
			InputSchema: schema(map[string]any{})},
		{Name: "zgj_wait_downloads",
			Description: "阻塞等待所有下载任务到终态（completed/failed/cancelled）或超时。轮询间隔 3s。返回最终任务列表；timedOut=true 时可再次调用继续等。",
			InputSchema: schema(map[string]any{
				"timeoutSec": map[string]any{"type": "number", "description": "最长等待秒数，默认 600，上限 1680"},
			})},
		{Name: "zgj_merge",
			Description: "合并已下载的分集成整剧成品（容器内 ffmpeg）。1 部剧=按季合并（自动选已完成分集）；多部剧=批量按季。返回排队结果；接着用 zgj_wait_merges 等完成。",
			InputSchema: schema(map[string]any{
				"dramas": map[string]any{"type": "array", "items": dramaProp, "description": "drama 对象数组（1 基剧级，需先下载完成）"},
			}, "dramas")},
		{Name: "zgj_wait_merges",
			Description: "阻塞等待合并任务到终态（done/failed/canceled）或超时。可选 dramaId 只等某部剧。返回最终列表。",
			InputSchema: schema(map[string]any{
				"timeoutSec": map[string]any{"type": "number", "description": "最长等待秒数，默认 600"},
				"dramaId":    map[string]any{"type": "string", "description": "可选，只等该 dramaId 的合并"},
			})},
		{Name: "zgj_outputs",
			Description: "拿成品交接路径（给上传侧用）：merged[]=已完成合并成品的容器路径+hostPath+字节数；includeEpisodes=true 时附 episodes[]=已完成分集成品。hostPath 是 host 上可直接读的文件。",
			InputSchema: schema(map[string]any{
				"includeEpisodes": map[string]any{"type": "boolean", "description": "是否附带未合并的分集成品，默认 false"},
			})},
		{Name: "zgj_cancel",
			Description: "取消任务。kind=download|merge，id 取自 zgj_downloads 列表。",
			InputSchema: schema(map[string]any{
				"kind": map[string]any{"type": "string", "enum": []string{"download", "merge"}},
				"id":   map[string]any{"type": "string"},
			}, "kind", "id")},
	}
}

func (s *server) callTool(name string, pjson []byte) (string, bool) {
	var p map[string]any
	if len(pjson) > 0 {
		if err := json.Unmarshal(pjson, &p); err != nil {
			return "参数不是 JSON 对象: " + err.Error(), true
		}
	}
	if p == nil {
		p = map[string]any{}
	}
	var (
		out string
		err error
	)
	switch name {
	case "zgj_sources":
		out, err = s.toolSources()
	case "zgj_search":
		out, err = s.toolSearch(p)
	case "zgj_rankings":
		out, err = s.toolRankings(p)
	case "zgj_detail":
		out, err = s.toolDetail(p)
	case "zgj_download":
		out, err = s.toolDownload(p)
	case "zgj_downloads":
		out, err = s.toolDownloads()
	case "zgj_wait_downloads":
		out, err = s.toolWaitDownloads(p)
	case "zgj_merge":
		out, err = s.toolMerge(p)
	case "zgj_wait_merges":
		out, err = s.toolWaitMerges(p)
	case "zgj_outputs":
		out, err = s.toolOutputs(p)
	case "zgj_cancel":
		out, err = s.toolCancel(p)
	default:
		return "未知工具: "+name, true
	}
	if err != nil {
		return err.Error(), true
	}
	return out, false
}

// ---------------------------------------------------------------- main

var versionRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func main() {
	log.SetOutput(os.Stderr)
	log.SetFlags(log.Ltime)
	cfg := loadConfig()
	if err := cfg.check(); err != nil {
		log.Fatal(err)
	}
	srv := &server{cfg: cfg, b: newBridge(cfg)}
	log.Printf("zgj-mcp 启动：base=%s user=%s hostData=%s", cfg.baseURL, cfg.user, cfg.hostData)

	// 启动即预登录一次（快速暴露凭据/容器问题到 stderr，不阻塞协议）。
	if err := srv.b.ensure(); err != nil {
		log.Printf("预登录失败（首次工具调用会再试）：%v", err)
	}

	in := bufio.NewReader(os.Stdin)
	for {
		line, err := in.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimSpace(line)
			if len(line) > 0 {
				srv.handle(line)
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			log.Printf("stdin 读错：%v", err)
			break
		}
	}
	log.Printf("zgj-mcp 退出（stdin EOF）")
}

func (s *server) handle(line []byte) {
	var msg rpcMsg
	if err := json.Unmarshal(line, &msg); err != nil {
		respondErr(json.RawMessage("null"), -32700, "parse error: "+err.Error())
		return
	}
	id := msg.ID
	if len(id) == 0 || string(id) == "null" {
		return // 通知（notifications/initialized 等）：不应答
	}
	switch msg.Method {
	case "initialize":
		pv := "2024-11-05"
		var init struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if json.Unmarshal(msg.Params, &init) == nil && versionRe.MatchString(init.ProtocolVersion) {
			pv = init.ProtocolVersion // 回声客户端版本，SDK 自动协商兜底
		}
		respond(id, map[string]any{
			"protocolVersion": pv,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "zgj-v3-mcp", "version": "1.0.0"},
			"instructions": "真果鉴 v3 容器（115）AI 接入面。推荐链路：zgj_search 找剧 → zgj_detail 看分集 → zgj_download 下集 → zgj_wait_downloads → zgj_merge 合并 → zgj_wait_merges → zgj_outputs 拿 host 路径交接上传侧。",
		})
	case "tools/list":
		respond(id, map[string]any{"tools": s.tools()})
	case "tools/call":
		var call struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(msg.Params, &call); err != nil {
			respondErr(id, -32602, "invalid params: "+err.Error())
			return
		}
		out, isErr := s.callTool(call.Name, call.Arguments)
		respond(id, map[string]any{
			"content": []map[string]any{{"type": "text", "text": out}},
			"isError": isErr,
		})
	case "ping":
		respond(id, map[string]any{})
	default:
		respondErr(id, -32601, "method not found: "+msg.Method)
	}
}
