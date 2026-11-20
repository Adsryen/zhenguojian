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
//   ZGJ_MCP_LISTEN     非空=HTTP 模式（streamable-http，如 0.0.0.0:9330）；空=stdio 模式
//   ZGJ_MCP_TOKEN      HTTP 模式的 Bearer 请求头鉴权 token（HTTP 模式必填）
package main

import (
	"bufio"
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
	listen    string // 非空=HTTP(streamable-http)模式监听地址，如 0.0.0.0:9330
	token     string // HTTP 模式的 Bearer 鉴权 token
	olBase    string // openlist（115 推送通道）地址，如 https://alist.prlrr.com；空=zgj_publish 只回签名 URL
	olUser    string
	olPass    string
	publicURL string // 对外可达的容器基地址（签名 URL 给外部调用方时用）；空=用 baseURL
}

func loadConfig() *config {
	c := &config{
		baseURL:   envOr("ZGJ_WEB_URL", "http://127.0.0.1:9320"),
		user:      os.Getenv("ZGJ_WEB_USER"),
		pass:      os.Getenv("ZGJ_WEB_PASS"),
		hostData:  envOr("ZGJ_DATA_HOST_DIR", "/home/ryen/Git/zhenguojian/client-v2/data"),
		pollEvery: 3000 * time.Millisecond,
		listen:    os.Getenv("ZGJ_MCP_LISTEN"),
		token:     os.Getenv("ZGJ_MCP_TOKEN"),
		olBase:    os.Getenv("ZGJ_OL_BASE"),
		olUser:    os.Getenv("ZGJ_OL_USER"),
		olPass:    os.Getenv("ZGJ_OL_PASS"),
		publicURL: envOr("ZGJ_PUBLIC_URL", os.Getenv("ZGJ_WEB_URL")),
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
	if c.listen != "" && c.token == "" {
		return errors.New("HTTP 模式必须设置 ZGJ_MCP_TOKEN（Bearer 鉴权 token）")
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

func rpcOK(id json.RawMessage, result any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
}

func rpcErr(id json.RawMessage, code int, msg string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": code, "message": msg},
	}
}

// srvWriteEnvelope stdio 模式下写一条 JSON-RPC 信封（nil=通知，不写）。
var outMu sync.Mutex

func srvWriteEnvelope(env map[string]any) {
	if env == nil {
		return
	}
	b, _ := json.Marshal(env)
	outMu.Lock()
	_, _ = os.Stdout.Write(append(b, '\n'))
	outMu.Unlock()
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
	// ③ id-only：drama 可为对象或 id 字符串（如 hongguo:7675932141238111256，
	// 服务端 Detail 自查 title/source，空标题拒绝）。
	var d drama
	if v, ok := p["drama"].(string); ok && strings.Contains(v, ":") {
		d, _ = s.normDrama(v)
	} else {
		var err error
		d, err = dramaOf(p)
		if err != nil {
			return "", err
		}
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

// jobDramaIDOf 从 job 里取 dramaId（drama.id；兼容平铺）。
func jobDramaIDOf(jm map[string]any) string {
	if d, ok := jm["drama"].(map[string]any); ok {
		return getS(d, "id")
	}
	return getS(jm, "dramaId")
}

// jobIndexOf 取 job 集号（int，取不到 -1）。
func jobIndexOf(jm map[string]any) int {
	switch v := jm["index"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return -1
}

// jobBrief 单条 job 的紧凑视图（摘要 head 用；比 compactList 少 drama 全对象）。
func jobBrief(jm map[string]any) map[string]any {
	d := mustMap(jm["drama"])
	return map[string]any{
		"id":        getS(jm, "id"),
		"state":     getS(jm, "state"),
		"progress":  jm["progress"],
		"ep":        jm["index"],
		"title":     getS(d, "title"),
		"quality":   jm["actualQuality"],
		"bytes":     jm["bytes"],
		"dramaId":   jobDramaIDOf(jm),
		"created":   jm["created"],
	}
}

// toolDownloads ②+⑦：dramaId/jobIds 过滤 + 大列表默认摘要（total+ids+前 5，full=true 全量）。
func (s *server) toolDownloads(p map[string]any) (string, error) {
	raw, err := s.b.call("DownloadList")
	if err != nil {
		return "", err
	}
	m := mustMap(parseResult(raw))
	dramaID := strOf(p, "dramaId")
	jobIDSet := map[string]bool{}
	for _, v := range mustList(p["jobIds"]) {
		if id, ok := v.(string); ok {
			jobIDSet[id] = true
		}
	}

	jobs := mustList(m["jobs"])
	var jStates []string
	var jIDs []string
	var jFull []map[string]any
	for _, j := range jobs {
		jm := mustMap(j)
		if dramaID != "" && jobDramaIDOf(jm) != dramaID {
			continue
		}
		if len(jobIDSet) > 0 && !jobIDSet[getS(jm, "id")] {
			continue
		}
		jStates = append(jStates, getS(jm, "state"))
		jIDs = append(jIDs, getS(jm, "id"))
		jFull = append(jFull, jobBrief(jm))
	}

	merges := mustList(m["merges"])
	var mOut []map[string]any
	for _, x := range merges {
		mm := mustMap(x)
		if dramaID != "" && getS(mm, "dramaId") != dramaID {
			continue
		}
		mOut = append(mOut, map[string]any{
			"id":         getS(mm, "id"),
			"state":      getS(mm, "state"),
			"stage":      getS(mm, "stage"),
			"progress":   mm["progress"],
			"title":      getS(mm, "title"),
			"output":     getS(mm, "output"),
			"outputName": getS(mm, "outputName"),
			"bytes":      mm["bytes"],
		})
	}

	full, _ := p["full"].(bool)
	out := map[string]any{"status": getS(m, "status"), "merges": mOut}
	if full {
		out["jobs"] = jFull
	} else {
		head := jFull
		if len(head) > summaryLimit {
			head = head[:summaryLimit]
		}
		out["jobs"] = map[string]any{
			"total":        len(jIDs),
			"stateCounts":  stateCounts(jStates),
			"ids":          jIDs,
			"head":         head,
			"full":         "jobs 默认只回 total+stateCounts+ids+前 5 条（防网关截断）；要全量传 full=true，或用 dramaId/jobIds 精过滤",
		}
	}
	if dramaID != "" {
		out["dramaId"] = dramaID
	}
	return pretty(out)
}

// compactList 紧凑视图；dramaID 非空时只回该剧的 jobs/merges（② 快照要按过滤
// 形态返回，否则混入别的剧的 job 误导调用方）。
func compactList(v any, dramaID string) (string, error) {
	m := mustMap(v)
	jobs := mustList(m["jobs"])
	jOut := make([]map[string]any, 0, len(jobs))
	for _, j := range jobs {
		jm := mustMap(j)
		if dramaID != "" && jobDramaIDOf(jm) != dramaID {
			continue
		}
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
		if dramaID != "" && getS(mm, "dramaId") != dramaID {
			continue
		}
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

// toolWaitDownloads ②：增量等待 + dramaId/jobIds 过滤 + 默认 45s。
// 为什么默认 ≤45s：mcphub 对阻塞调用 ~60s 就掐，旧默认 600s 必然被掐死
// 且调用方拿不到中间快照。增量等待=超时返回 timedOut:true + 当前快照，
// 调用方（AI agent）循环再调即可——这是网关超时下的正确姿势。
func (s *server) toolWaitDownloads(p map[string]any) (string, error) {
	timeout := time.Duration(intOr(p, "timeoutSec", 45)) * time.Second
	if timeout > 120*time.Second {
		timeout = 120 * time.Second // 硬上限 120s：两次增量窗口之间调用方还有时间思考
	}
	dramaID := strOf(p, "dramaId")
	jobIDSet := map[string]bool{}
	for _, v := range mustList(p["jobIds"]) {
		if id, ok := v.(string); ok {
			jobIDSet[id] = true
		}
	}
	deadline := time.Now().Add(timeout)
	for {
		raw, err := s.b.call("DownloadList")
		if err != nil {
			return "", err
		}
		m := mustMap(parseResult(raw))
		var active, failed, total int
		for _, j := range mustList(m["jobs"]) {
			jm := mustMap(j)
			if dramaID != "" && jobDramaIDOf(jm) != dramaID {
				continue
			}
			if len(jobIDSet) > 0 && !jobIDSet[getS(jm, "id")] {
				continue
			}
			total++
			switch st := getS(jm, "state"); {
			case jobDone(st):
				if st != "completed" && st != "done" {
					failed++
				}
			default:
				active++
			}
		}
		listJSON, _ := pretty(map[string]any{"status": getS(m, "status")})
		_ = listJSON
		snapshot, _ := s.toolDownloads(p) // 复用过滤+摘要逻辑（full 参数透传）
		if active == 0 {
			return pretty(map[string]any{
				"timedOut": false,
				"done":     active == 0,
				"failed":   failed,
				"total":    total,
				"list":     jsonRaw(snapshot),
				"next":     "下载完成：可 zgj_merge（drama 传对象或 id 字符串）；有 failed 时看 list 里的 stateCounts",
			})
		}
		if time.Now().After(deadline) {
			return pretty(map[string]any{
				"timedOut":   true,
				"activeJobs": active,
				"failed":     failed,
				"total":      total,
				"list":       jsonRaw(snapshot),
				"next":       "增量等待超时：再次调用 zgj_wait_downloads（同参数）继续等，或 zgj_downloads 看快照",
			})
		}
		time.Sleep(s.b.cfg.pollEvery)
	}
}

// toolMerge ③+④+⑤：dramas 元素可为对象或 id 字符串；chunkSize>0 时按集切块
// （每块一个「剧名 全NNN-MMM集.mkv」，selected mode 多次入队）；幂等=同区段
// 重合并核心按 entry ID（dramaID#first-last）覆盖刷新，扩区段是**新名字**的
// 新成品（旧的不自动删——要删用 zgj_cleanup kind=outputs）。
func (s *server) toolMerge(p map[string]any) (string, error) {
	refs := dramaRefs(p, "dramas")
	if len(refs) == 0 {
		return "", errors.New("dramas 必填（drama 对象或 id 字符串数组，如 [\"hongguo:7675932141238111256\"]）")
	}
	chunk := intOr(p, "chunkSize", 0)
	// 归一化：id 字符串自查成完整对象（③ id-only；空标题在 dramaRefByID 拒绝）。
	norm := make([]drama, 0, len(refs))
	for _, it := range refs {
		d, e := s.normDrama(it)
		if e != nil {
			return "", e
		}
		norm = append(norm, d)
	}
	// 多部剧=按剧整季合并（MergeStartMulti 不支持切块，chunkSize 只对单部有效）。
	if len(norm) > 1 {
		if chunk > 0 {
			return "", errors.New("chunkSize 只对单部剧有效（多部剧按整季合并）")
		}
		raw, err := s.b.call("MergeStartMulti", strListArg(norm))
		if err != nil {
			return "", err
		}
		m := mustMap(parseResult(raw))
		m["next"] = "用 zgj_wait_merges 等合并完成，再 zgj_outputs 拿成品路径"
		return pretty(m)
	}
	dm := norm[0]
	dramaID := getS(dm, "id")

	if chunk <= 1 {
		// 整季合并（默认；≤1 视为整季）
		raw, err := s.b.call("MergeStart", strArg("season"), objArg(dm), strArg("[]"))
		if err != nil {
			return "", err
		}
		m := mustMap(parseResult(raw))
		m["next"] = "用 zgj_wait_merges（dramaId 过滤）等合并完成，再 zgj_outputs 拿成品路径"
		return pretty(m)
	}

	// chunkSize 切块：拉该剧全部 completed jobs，按集号升序切每 chunk 个一组，
	// 逐组 MergeStart(selected, 显式 jobIDs)。核心要求同组集号连续
	// （merge.go runMerge first..last）——completed jobs 的集号天然唯一，
	// 切出的组只要组内连续就合法；有缺集时组内跳号会被核心拒绝，
	// 报错会带组号，调用方可按报错补下载后重发。
	raw, err := s.b.call("DownloadList")
	if err != nil {
		return "", err
	}
	m := mustMap(parseResult(raw))
	type epJob struct {
		ep   int
		id   string
	}
	var jobs []epJob
	for _, j := range mustList(m["jobs"]) {
		jm := mustMap(j)
		if getS(jm, "state") != "completed" || jobDramaIDOf(jm) != dramaID {
			continue
		}
		jobs = append(jobs, epJob{ep: jobIndexOf(jm), id: getS(jm, "id")})
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ep < jobs[j].ep })
	if len(jobs) < 2 {
		return "", fmt.Errorf("《%s》可合并的已完成分集不足 2 集（现有 %d）", getS(dm, "title"), len(jobs))
	}
	// 切块：核心要求每组 ≥2 集且连续——尾组若只剩 1 集，并入上一组
	// （221 集 chunk=50 → [1-50][51-100][101-150][151-200][201-221]，
	// 而非 …[201-250 越界][221 单集被拒]）。
	var groups [][2]int // [start, end) 切片下标
	for i := 0; i < len(jobs); i += chunk {
		groups = append(groups, [2]int{i, min(i+chunk, len(jobs))})
	}
	if n := len(groups); n > 1 && groups[n-1][1]-groups[n-1][0] < 2 {
		last := groups[n-1]
		groups[n-2][1] = last[1] // 尾组并入上一组
		groups = groups[:n-1]
	}
	var queued []map[string]any
	for gi, g := range groups {
		group := jobs[g[0]:g[1]]
		ids := make([]string, 0, len(group))
		for _, gj := range group {
			ids = append(ids, gj.id)
		}
		r2, err := s.b.call("MergeStart", strArg("selected"), objArg(dm), strListArg(ids))
		if err != nil {
			return "", fmt.Errorf("第 %d-%d 集（组 %d/%d）合并入队失败：%w",
				group[0].ep, group[len(group)-1].ep, gi+1, len(groups), err)
		}
		queued = append(queued, mustMap(parseResult(r2)))
	}
	return pretty(map[string]any{
		"title":    getS(dm, "title"),
		"dramaId":  dramaID,
		"chunks":   queued,
		"totalEp":  len(jobs),
		"chunkSize": chunk,
		"next":     "用 zgj_wait_merges（dramaId 过滤）等全部块完成，再 zgj_outputs 拿成品路径；" +
			"重发同区段合并=覆盖同名成品（幂等），扩区段会产生新名字，旧成品用 zgj_cleanup 清",
	})
}

func (s *server) toolWaitMerges(p map[string]any) (string, error) {
	// ② 增量等待：默认 45s（mcphub 阻塞 ~60s 掐线），上限 120s 硬限；
	// 超时回 timedOut+快照，调用方循环再调。
	timeout := time.Duration(intOr(p, "timeoutSec", 45)) * time.Second
	if timeout > 120*time.Second {
		timeout = 120 * time.Second
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
			out, _ := compactList(m, dramaID)
			return pretty(map[string]any{
				"timedOut": false, "list": jsonRaw(out),
				"next":     "合并完成，用 zgj_outputs 拿成品 host 路径交接给上传侧",
			})
		}
		if time.Now().After(deadline) {
			out, _ := compactList(m, dramaID)
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
	// ② 过滤：dramaId（按剧）+ jobIds（按合并条目 id 精确）。
	dramaID := strOf(p, "dramaId")
	jobIDSet := strSetOf(p, "jobIds")
	out := map[string]any{}
	var merges []map[string]any
	seenOut := map[string]bool{} // 同 output 路径去重（历史累积的同名条目只回一次）
	for _, x := range mustList(m["merges"]) {
		mm := mustMap(x)
		if getS(mm, "state") != "done" || getS(mm, "output") == "" {
			continue
		}
		if dramaID != "" && getS(mm, "dramaId") != dramaID {
			continue
		}
		if jobIDSet != nil && !jobIDSet[getS(mm, "id")] {
			continue
		}
		cpath := getS(mm, "output")
		if seenOut[cpath] {
			continue
		}
		seenOut[cpath] = true
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
			if dramaID != "" && jobDramaIDOf(jm) != dramaID {
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
	// ① m05712 禁令：不要求调用方自己 POST /login 拿 cookie——推荐 zgj_publish
	// 直接推 115（凭证 MCP 层内部解决），或 zgj_publish 无 openlist 时回签名 URL。
	out["auth"] = "fileUrl 需容器 session cookie——调用方不要自己登录：用 zgj_publish 推送（115 通道凭证在 MCP 层内部），或调一次 zgj_publish 拿一次性签名 URL（无需登录、Range 支持）。"
	out["note"] = "推荐 zgj_publish（115 直推）；fileUrl 仅供容器同网络/有 cookie 的场景（容器 root 出流，跨属主可靠）；hostPath 仅供 root/sudo 上下文直读（host 挂载 " + s.cfg.hostData + "）。"
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

// ---------------------------------------------------------------- ① zgj_publish

// toolPublish 把合并成品推送到 openlist（115 网盘）。
//
// 无人值守口径（m05712 ①）：调用方不碰容器 cookie、不碰 115 凭证——
//   - files 缺省 = 该剧最新合并成品（DownloadList merges 里 state=done+output 非空，
//     同剧多块取集数最大/最新一块）
//   - 有 openlist 配置：流式 PUT 到 /api/fs/put（10GB 级不落本地第二份），
//     回读 size 校验；115 存储 token 死时返回明确的重授权指引（不静默）
//   - 无 openlist 配置：兜底 FileSign 一次性签名 URL（Range 支持），
//     host 按 ZGJ_WEB_URL 重写为对外可达
func (s *server) toolPublish(p map[string]any) (string, error) {
	dramaID := strOf(p, "dramaId")
	targetDir := strings.Trim(strOf(p, "targetDir"), "/")
	if targetDir == "" {
		targetDir = "zgj"
	}
	tpl := strOf(p, "fileNameTemplate")
	del := false
	if v, ok := p["deleteLocalAfterSuccess"].(bool); ok {
		del = v
	}
	// 选文件：显式 files[] 优先；缺省=该剧最新合并成品。
	var cands []map[string]any
	if fl, ok := p["files"].([]any); ok && len(fl) > 0 {
		for _, f := range fl {
			if fs, ok := f.(string); ok && fs != "" {
				cands = append(cands, map[string]any{"output": fs, "title": filepath.Base(fs)})
			}
		}
	} else {
		if dramaID == "" {
			return "", errors.New("dramaId 必填（或显式传 files 数组）")
		}
		d, err := s.normDrama(dramaID)
		if err != nil {
			return "", err
		}
		dramaID = getS(d, "id")
		dramaTitle := getS(d, "title")
		raw, err := s.b.call("DownloadList")
		if err != nil {
			return "", err
		}
		m := mustMap(parseResult(raw))
		type outInfo struct {
			path  string
			title string
			first int
			last  int
		}
		var infos []outInfo
		reOut := regexp.MustCompile(`全(\d{3,})-(\d{3,})集\.`)
		for _, x := range mustList(m["merges"]) {
			mm := mustMap(x)
			if getS(mm, "state") != "done" || getS(mm, "output") == "" || getS(mm, "dramaId") != dramaID {
				continue
			}
			info := outInfo{path: getS(mm, "output"), title: getS(mm, "title")}
			// 集数区间优先从 merges entry 的 episodes 字段取（1 基连续集号），
			// 回落解析文件名。host 侧 root 属主 0700 数据树读不到 mtime——
			// 排序不依赖 mtime（切块按集号升序，last 最大=最新一块）。
			eps := mustIntList(mm["episodes"])
			if len(eps) > 0 {
				info.first, info.last = eps[0], eps[len(eps)-1]
			} else if mm2 := reOut.FindStringSubmatch(filepath.Base(info.path)); mm2 != nil {
				fmt.Sscanf(mm2[1], "%d", &info.first)
				fmt.Sscanf(mm2[2], "%d", &info.last)
			}
			infos = append(infos, info)
		}
		if len(infos) == 0 {
			return "", fmt.Errorf("《%s》没有已完成的合并成品（先 zgj_merge + zgj_wait_merges）", dramaTitle)
		}
		// 最新一块=集数最大（同剧切块时最后一块集号最大）。
		sort.Slice(infos, func(i, j int) bool {
			if infos[i].last != infos[j].last {
				return infos[i].last > infos[j].last
			}
			return infos[i].first > infos[j].first
		})
		for _, info := range infos {
			cands = append(cands, map[string]any{"output": info.path, "title": info.title})
		}
	}
	if len(cands) == 0 {
		return "", errors.New("files 为空")
	}
	// 缺省语义=最新一块（cands 已按集号降序，取第一个）；显式 files 时全推。
	explicit := len(mustList(p["files"])) > 0
	var published []map[string]any
	failures := []string{}
	for i, c := range cands {
		if i > 0 && !explicit {
			break
		}
		src := getS(c, "output")
		name := publishFileName(tpl, c, src)
		target := "/" + targetDir + "/" + name
		res, err := s.pushOneFile(src, name, target, del)
		if err != nil {
			msg := err.Error()
			// 115 存储 token 死=唯一需要人工的一次性操作，给明确指引（不静默）。
			if strings.Contains(msg, "refresh_token") || strings.Contains(msg, "40140137") {
				msg += "；指引：到 " + s.cfg.olBase + " 登录 → 存储 → 115 重新授权一次（token 型凭证，无法程序化续期）"
			}
			failures = append(failures, name+": "+msg)
			continue
		}
		published = append(published, res)
	}
	channel := "openlist"
	if s.olClient() == nil {
		channel = "signedUrl（openlist 未配置，返回一次性签名 URL 供上游自传）"
	}
	out := map[string]any{
		"channel": channel,
		"files":   published,
	}
	if len(failures) > 0 {
		out["failures"] = failures
	}
	if len(published) == 0 {
		return "", fmt.Errorf("推送全部失败：%s；channel 降级指引见 errors 字段", strings.Join(failures, " | "))
	}
	return pretty(out)
}

// pushOneFile 单文件：openlist 流式上传（配置齐时）或 FileSign 签名 URL 兜底。
func (s *server) pushOneFile(src, name, target string, del bool) (map[string]any, error) {
	ol := s.olClient()
	if ol != nil {
		// 流式：先 FileSign 拿一次性签名 URL（MCP 无容器 cookie，/file 要登录会 401；
		// /file-signed 无鉴权+单次有效，正好一次取流），容器 root 进程出流，
		// 边读边 PUT 给 openlist——本地不落第二份。
		olc := ol
		signRaw, err := s.b.call("FileSign", strArg(src), intArg(600))
		if err != nil {
			return nil, fmt.Errorf("FileSign 失败（%s）：%w", src, err)
		}
		sm := mustMap(parseResult(signRaw))
		tok := getS(sm, "token")
		// 取流走 ZGJ_WEB_URL（对外可达产，MCP 与容器同机；/file-signed 无鉴权）。
		fileURL := rewriteHost("/file-signed?token="+tok, s.cfg.baseURL)
		httpc := &http.Client{}
		r, err := httpc.Get(fileURL)
		if err != nil {
			return nil, fmt.Errorf("取本地流出错（%s）：%w", fileURL, err)
		}
		size := r.ContentLength
		if size <= 0 {
			r.Body.Close()
			return nil, fmt.Errorf("取本地流拿不到文件大小（Content-Length=%d）", size)
		}
		// 先建目标目录（逐级，幂等）。
		dir, _ := splitOL(target)
		if err := olc.mkdirAll(dir); err != nil {
			r.Body.Close()
			return nil, fmt.Errorf("openlist 建目录失败：%w", err)
		}
		if err := olc.put(target, size, "", r.Body); err != nil {
			return nil, err
		}
		st, err := olc.statFile(target)
		if err != nil {
			return nil, err
		}
		if st.Size != size {
			return nil, fmt.Errorf("openlist 回读 size=%d 与源 %d 不一致（上传可能不完整）", st.Size, size)
		}
		res := map[string]any{
			"local":   s.cfg.toFileURL(src),
			"remote":  target,
			"size":    size,
			"sha1":    st.Sha1,
			"deleted": false,
		}
		if del {
			if raw, err := s.b.call("FileDelete", strArg(src)); err == nil {
				res["deleted"] = true
				_ = parseResult(raw)
			} else {
				res["deleteError"] = err.Error()
			}
		}
		return res, nil
	}
	// 兜底：FileSign 一次性签名 URL（调用方/上游 agent 自己取流上传到 115）。
	raw, err := s.b.call("FileSign", strArg(src), intArg(3600))
	if err != nil {
		return nil, err
	}
	sm := mustMap(parseResult(raw))
	tok := getS(sm, "token")
	ext := filepath.Ext(src)
	// 签名 URL 给**外部**调用方（mcphub/云侧）用 → ZGJ_PUBLIC_URL（对外基地址）；
	// 缺省回落 baseURL（127.0.0.1 时调用方需自行改写 host）。
	signedBase := s.cfg.publicURL
	if signedBase == "" {
		signedBase = s.cfg.baseURL
	}
	res := map[string]any{
		"local":           s.cfg.toFileURL(src),
		"containerPath":   getS(sm, "path"),
		"signedUrl":       rewriteHost("/file-signed?token="+tok, signedBase),
		"ttlSec":          sm["ttlSec"],
		"once":            true,
		"targetName":      name,
		"suggestedTarget": target,
		"note": "未配置 openlist（ZGJ_OL_BASE/USER/PASS）：签名 URL 单次有效+Range 支持，由调用方自行 PUT 到 115。" + ext,
	}
	return res, nil
}

func (s *server) olClient() *olClient {
	cfg := olConfig{
		base: strings.TrimRight(s.cfg.olBase, "/"),
		user: s.cfg.olUser,
		pass: s.cfg.olPass,
	}
	if !cfg.enabled() {
		return nil
	}
	return newOLClient(cfg)
}

// rewriteHost 把容器内路径（如 /file-signed?token=…）拼到 ZGJ_WEB_URL 的
// 对外 host 上（mcphub/公网视角可达；ZGJ_WEB_URL 本身就是对外可达产）。
func rewriteHost(path, base string) string {
	bu, err := url.Parse(base)
	if err != nil || bu.Host == "" {
		return path
	}
	return bu.Scheme + "://" + bu.Host + path
}

// publishFileName 套 fileNameTemplate（缺省「{剧名} 合并版 {E起}-{E止}{ext}」）。
// 占位符：{title} {E起} {E止} {ext}；模板缺区间信息时退回源文件名。
func publishFileName(tpl string, c map[string]any, src string) string {
	srcBase := filepath.Base(src)
	ext := filepath.Ext(srcBase)
	title, _ := c["title"].(string)
	if title == "" {
		title = strings.TrimSuffix(srcBase, ext)
	}
	// 从源文件名抽集数区间
	reOut := regexp.MustCompile(`全(\d{3,})-(\d{3,})集\.`)
	first, last := 0, 0
	if mm := reOut.FindStringSubmatch(srcBase); mm != nil {
		fmt.Sscanf(mm[1], "%d", &first)
		fmt.Sscanf(mm[2], "%d", &last)
	}
	if tpl == "" {
		if first > 0 && last > 0 {
			return title + " 合并版 " + fmt.Sprintf("%03d", first) + "-" + fmt.Sprintf("%03d", last) + ext
		}
		return srcBase
	}
	out := tpl
	out = strings.ReplaceAll(out, "{title}", title)
	out = strings.ReplaceAll(out, "{E起}", fmt.Sprintf("%03d", first))
	out = strings.ReplaceAll(out, "{E止}", fmt.Sprintf("%03d", last))
	out = strings.ReplaceAll(out, "{ext}", strings.TrimPrefix(ext, "."))
	if first == 0 && last == 0 {
		out = strings.ReplaceAll(out, "-000", ext) // 无区间信息：去掉残留占位
		out = strings.ReplaceAll(out, "000", "")
	}
	if out == "" {
		return srcBase
	}
	return out
}

// ---------------------------------------------------------------- ④ zgjan_cleanup

// toolCleanup 按剧清理：清分集缓存（DownloadRemoveBatch）/ 旧合并成品
// （DownloadPurgeOutputs 区间包含规则）。
func (s *server) toolCleanup(p map[string]any) (string, error) {
	dramaID := strOf(p, "dramaId")
	if dramaID == "" {
		return "", errors.New("dramaId 必填（如 hongguo:7675932141238111256）")
	}
	doEpisodes := boolOf(p, "episodes", true) // 缺省连分集缓存一起清
	doOutputs := boolOf(p, "outputs", true)    // 缺省连旧成品一起清
	out := map[string]any{"dramaId": dramaID}
	if doEpisodes {
		raw, err := s.b.call("DownloadList")
		if err != nil {
			return "", err
		}
		m := mustMap(parseResult(raw))
		var ids []string
		for _, j := range mustList(m["jobs"]) {
			jm := mustMap(j)
			if jobDramaIDOf(jm) == dramaID && getS(jm, "state") == "completed" {
				ids = append(ids, getS(jm, "id"))
			}
		}
		if len(ids) == 0 {
			out["episodesRemoved"] = 0
		} else {
			raw2, err := s.b.call("DownloadRemoveBatch", strListArg(ids))
			if err != nil {
				return "", fmt.Errorf("分集缓存删除失败：%w", err)
			}
			out["episodes"] = parseResult(raw2)
		}
	}
	if doOutputs {
		raw, err := s.b.call("DownloadPurgeOutputs", strArg(""))
		if err != nil {
			return "", fmt.Errorf("旧成品清理失败（115 侧 refresh_token 死不影响本地清理）：%w", err)
		}
		out["purge"] = parseResult(raw)
	}
	return pretty(out)
}

// ---------------------------------------------------------------- 限流重试 zgj_retry

// toolRetry 把 failed 的下载任务恢复重试（failed=resume，core 语义）。
// 无人值守下源限流是常态（hongguo 403「N 秒后重试」），限流窗口过后调一次即可。
func (s *server) toolRetry(p map[string]any) (string, error) {
	dramaID := strOf(p, "dramaId")
	var only []string
	if ids, ok := p["jobIds"].([]any); ok {
		for _, v := range ids {
			if s, ok := v.(string); ok && s != "" {
				only = append(only, s)
			}
		}
	}
	if dramaID == "" && len(only) == 0 {
		return "", errors.New("dramaId 或 jobIds 至少一个（缺省会重推所有 failed 任务）")
	}
	raw, err := s.b.call("DownloadList")
	if err != nil {
		return "", err
	}
	m := mustMap(parseResult(raw))
	var retried []string
	for _, j := range mustList(m["jobs"]) {
		jm := mustMap(j)
		if getS(jm, "state") != "failed" {
			continue
		}
		id := getS(jm, "id")
		if dramaID != "" && jobDramaIDOf(jm) != dramaID {
			continue
		}
		if len(only) > 0 && !containsStr(only, id) {
			continue
		}
		if _, err := s.b.call("DownloadRetry", strArg(id)); err != nil {
			return "", fmt.Errorf("重试 %s（第 %v 集）失败：%w", id, jm["index"], err)
		}
		retried = append(retried, id)
	}
	return pretty(map[string]any{
		"retried": retried,
		"count":   len(retried),
		"note":    "已 resume。等 20-60s 限流窗口后再调 zgj_wait_downloads（timeoutSec≤45）看进度。",
	})
}

// ---------------------------------------------------------------- 参数小工具

func mustIntList(v any) []int {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []int
	for _, x := range arr {
		if f, ok := x.(float64); ok {
			out = append(out, int(f))
		}
	}
	return out
}

// strSetOf 取字符串数组参数为集合（② jobIds 精确过滤用）；空/缺省返回 nil。
func strSetOf(p map[string]any, k string) map[string]bool {
	set := map[string]bool{}
	for _, v := range mustList(p[k]) {
		if s, ok := v.(string); ok && s != "" {
			set[s] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

func boolOf(p map[string]any, k string, d bool) bool {
	if v, ok := p[k].(bool); ok {
		return v
	}
	return d
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

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

// dramaRef 接受「drama 对象」或「id 字符串」（如 hongguo:7675932141238111256）。
// 为什么支持 id-only：mcphub 转发链路上 AI agent 经常只记住剧 id 就丢掉完整
// drama 对象；id 足够 core 定位（splitProviderDramaID→source+sourceID，
// title 由 Detail 抓取回填），服务端自查比要求调用方原样回传可靠。
// 空标题守卫：Detail 若回填不出 title（源异常/剧下线），拒绝继续——
// 否则合并成品名会变成「 全001-050集.mkv」（空《》），下游网盘里无法辨认。
func (s *server) dramaRef(p map[string]any, key string) (drama, error) {
	v := p[key]
	if id, ok := v.(string); ok && strings.Contains(id, ":") {
		d := map[string]any{"id": id}
		raw, err := s.b.call("Detail", objArg(d))
		if err != nil {
			return nil, fmt.Errorf("按 id 查详情失败（%s）：%w", id, err)
		}
		m := mustMap(parseResult(raw))
		full := mustMap(m["drama"])
		if full == nil {
			full = m
		}
		title := getS(full, "title")
		if title == "" {
			return nil, fmt.Errorf("drama %s 详情里查不到标题（源异常或剧已下线），拒绝继续", id)
		}
		return full, nil
	}
	d, ok := v.(map[string]any)
	if !ok || getS(d, "id") == "" {
		return nil, fmt.Errorf("%s 必填：drama 对象（含 id）或 id 字符串（如 hongguo:7675932141238111256）", key)
	}
	return d, nil
}

// dramaRefByID 按已知 id 自查完整 drama（chunk 合并/输出命名等内部复用）。
func (s *server) dramaRefByID(id string) (drama, error) {
	d := map[string]any{"id": id}
	raw, err := s.b.call("Detail", objArg(d))
	if err != nil {
		return nil, fmt.Errorf("按 id 查详情失败（%s）：%w", id, err)
	}
	m := mustMap(parseResult(raw))
	full := mustMap(m["drama"])
	if full == nil {
		full = m
	}
	if getS(full, "title") == "" {
		return nil, fmt.Errorf("drama %s 详情里查不到标题", id)
	}
	return full, nil
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

// dramaRefs ③：dramas 元素可为对象或 id 字符串（原样保留，归一由调用方做）。
func dramaRefs(p map[string]any, k string) []any {
	var out []any
	for _, v := range mustList(p[k]) {
		switch x := v.(type) {
		case map[string]any:
			if getS(x, "id") != "" {
				out = append(out, x)
			}
		case string:
			if strings.Contains(x, ":") {
				out = append(out, x)
			}
		}
	}
	return out
}

// normDrama 把「对象或 id 字符串」归一成完整 drama 对象（id 走 Detail 自查）。
func (s *server) normDrama(v any) (drama, error) {
	if id, ok := v.(string); ok && strings.Contains(id, ":") {
		return s.dramaRefByID(id)
	}
	d, ok := v.(map[string]any)
	if !ok || getS(d, "id") == "" {
		return nil, errors.New("drama 需为对象（含 id）或 id 字符串（如 hongguo:7675932141238111256）")
	}
	return d, nil
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

// ---------------------------------------------------------------- 大列表摘要（mcphub 截断防护）

// 为什么必须有摘要：mcphub 网关对工具返回值会截断/压缩（>250KB 或超长），
// 阻塞调用 ~60s 被掐。《逆江湖》222 话的 jobs 全量 ~222 条 × 每条约 200 字节
// ≈ 45KB 勉强，但加上 merges 明细与 episodes 就超。规则：
//   - 默认只回 total + id 清单（string，极小）+ 前 N 条完整项 + 「full=true 取全量」提示；
//   - id 清单让调用方能用 jobIds 精过滤，不用全量扫。
const summaryLimit = 5

type listSummary struct {
	total   int
	ids     []string
	hits    []map[string]any // 过滤后前 summaryLimit 条
	extra   map[string]any   // 附加统计（stateCounts 等）
	fullMsg string           // 「full=true 取全量」提示文案
}

func (s *listSummary) render() map[string]any {
	out := map[string]any{
		"total": s.total,
		"ids":   s.ids,
		"head":  s.hits,
	}
	if s.extra != nil {
		for k, v := range s.extra {
			out[k] = v
		}
	}
	out["full"] = s.fullMsg
	return out
}

// stateCounts 给一组 state 字符串计数（{completed: 220, downloading: 2}）。
func stateCounts(states []string) map[string]int {
	c := map[string]int{}
	for _, st := range states {
		c[st]++
	}
	return c
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
		{Name: "zgj_publish",
			Description: "把合并成品推送到 115 网盘（openlist 通道，凭证在 MCP 层内部解决）。files 缺省=该剧最新合并成品。" +
				"配置了 openlist 时流式直传并回读校验，返回远端路径清单；未配置时返回一次性签名 URL（Range 支持）供上游自行上传。" +
				"115 存储 token 失效时会返回重授权指引（需人工到 alist 站点重新授权一次）。",
			InputSchema: schema(map[string]any{
				"dramaId":  map[string]any{"type": "string", "description": "剧 id（如 hongguo:7675932141238111256）；files 缺省时用它找最新合并成品"},
				"files":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "显式要发布的容器内文件路径数组（缺省=最新合并成品）"},
				"targetDir": map[string]any{"type": "string", "description": "115 上的目标目录（缺省 zgj）"},
				"fileNameTemplate": map[string]any{"type": "string", "description": "命名模板，占位符 {title} {E起} {E止} {ext}；缺省「{title} 合并版 {E起}-{E止}.{ext}」"},
				"deleteLocalAfterSuccess": map[string]any{"type": "boolean", "description": "推送成功后删本地文件（缺省 false）"},
			})},
		{Name: "zgj_cleanup",
			Description: "按剧清理：completed 分集缓存（DownloadRemoveBatch 口径）+「合并」目录下被更大区段覆盖的旧成品（区间包含规则）。",
			InputSchema: schema(map[string]any{
				"dramaId":  map[string]any{"type": "string"},
				"episodes": map[string]any{"type": "boolean", "description": "是否清分集缓存（缺省 true）"},
				"outputs":  map[string]any{"type": "boolean", "description": "是否清旧合并成品（缺省 true）"},
			}, "dramaId")},
		{Name: "zgj_retry",
			Description: "把 failed 的下载任务恢复重试（源限流 403 是常态：等 20-60s 后调一次，再 zgj_wait_downloads 看进度）。",
			InputSchema: schema(map[string]any{
				"dramaId": map[string]any{"type": "string"},
				"jobIds":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			})},
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
		out, err = s.toolDownloads(p)
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
	case "zgj_publish":
		out, err = s.toolPublish(p)
	case "zgj_cleanup":
		out, err = s.toolCleanup(p)
	case "zgj_retry":
		out, err = s.toolRetry(p)
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
	log.Printf("zgj-mcp 启动：base=%s user=%s hostData=%s mode=%s",
		cfg.baseURL, cfg.user, cfg.hostData, modeName(cfg))

	// 启动即预登录一次（快速暴露凭据/容器问题到 stderr，不阻塞协议）。
	if err := srv.b.ensure(); err != nil {
		log.Printf("预登录失败（首次工具调用会再试）：%v", err)
	}

	if cfg.listen != "" {
		srv.runHTTP()
		return
	}
	srv.runStdio()
}

func modeName(cfg *config) string {
	if cfg.listen != "" {
		return "http(" + cfg.listen + ")"
	}
	return "stdio"
}

// dispatch 处理一条 JSON-RPC 帧，返回应答信封（通知返回 nil）。
func (s *server) dispatch(line []byte) map[string]any {
	var msg rpcMsg
	if err := json.Unmarshal(line, &msg); err != nil {
		return rpcErr(json.RawMessage("null"), -32700, "parse error: "+err.Error())
	}
	id := msg.ID
	if len(id) == 0 || string(id) == "null" {
		return nil // 通知（notifications/initialized 等）：不应答
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
		return rpcOK(id, map[string]any{
			"protocolVersion": pv,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "zgj-v3-mcp", "version": "1.1.0"},
			"instructions": "真果鉴 v3 容器（115）AI 接入面。推荐链路：zgj_search 找剧 → zgj_detail 看分集 → zgj_download 下集 → zgj_wait_downloads → zgj_merge 合并 → zgj_wait_merges → zgj_outputs 拿 fileUrl/hostPath 交接上传侧。",
		})
	case "tools/list":
		return rpcOK(id, map[string]any{"tools": s.tools()})
	case "tools/call":
		var call struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(msg.Params, &call); err != nil {
			return rpcErr(id, -32602, "invalid params: "+err.Error())
		}
		out, isErr := s.callTool(call.Name, call.Arguments)
		return rpcOK(id, map[string]any{
			"content": []map[string]any{{"type": "text", "text": out}},
			"isError": isErr,
		})
	case "ping":
		return rpcOK(id, map[string]any{})
	default:
		return rpcErr(id, -32601, "method not found: "+msg.Method)
	}
}

// ---------------------------------------------------------------- stdio 模式

func (s *server) runStdio() {
	in := bufio.NewReader(os.Stdin)
	for {
		line, err := in.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimSpace(line)
			if len(line) > 0 {
				srvWriteEnvelope(s.dispatch(line))
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

// ---------------------------------------------------------------- HTTP 模式（streamable-http，供 mcphub 等网关）

// 无状态单端点：每个 POST /mcp 自带完整请求，不保存会话状态（与 magnet 先例
// 「无状态 streamable HTTP」同形态）。响应形态跟随 Accept 头：
//   - 含 application/json → 直接 JSON 应答
//   - 含 text/event-stream → SSE 单事件应答
// 鉴权=Authorization: Bearer <ZGJ_MCP_TOKEN>，恒定时间比较，缺失/不符=401。
// GET /mcp 返回 405（本实现不开 GET SSE 流）；GET /healthz 供探活（不需 token）。

func (s *server) runHTTP() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"mode":"http"}`)
	})
	mux.HandleFunc("/mcp", s.handleMCP)
	log.Printf("zgj-mcp HTTP 监听 %s/mcp（Bearer 鉴权已启用）", s.cfg.listen)
	srv := &http.Server{
		Addr:              s.cfg.listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	// wait 类工具最长 55min：不设 WriteTimeout（网关侧 timeout 自管）。
	err := srv.ListenAndServe()
	log.Printf("zgj-mcp HTTP 退出：%v", err)
}

func (s *server) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.Header().Set("Allow", "POST, OPTIONS")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		http.Error(w, "method not allowed: use POST", http.StatusMethodNotAllowed)
		return
	}
	// Bearer 鉴权（恒定时间比较）。
	if !s.tokenOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	env := s.dispatch(body)
	accept := r.Header.Get("Accept")
	wantsSSE := strings.Contains(accept, "text/event-stream")
	if env == nil {
		// 通知：202 无应答
		w.WriteHeader(http.StatusAccepted)
		return
	}
	b, _ := json.Marshal(env)
	if wantsSSE {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", b)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(append(b, '\n'))
}

func (s *server) tokenOK(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") || s.cfg.token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(auth, "Bearer ")), []byte(s.cfg.token)) == 1
}
