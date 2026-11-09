// Package native 是 v2 壳对 v1 核心（duanjuapp/native/core）的门面。
//
// 协议照抄 v1 行协议（zhenguojian/app/zhenguojian/core_client.py +
// zhenguojian/sidecar/main.go 的路由），但**不经管道/子进程**：
// 直接进程内调用 core 的导出入口：
//
//   - core.NativeRequest(json)      —— 常规动作（initialize/catalog/detail/resolve/…）
//   - core.NativeSourcesCommand(json) —— listSources / setSourceEnabled（注册表层，
//     不在 NativeRequest 的 dispatch 里！见 sidecar/main.go:80）
//   - NativeDiscovery 桥 —— browseFilters / browseFiltered / dramaCreators /
//     creatorProfile / creatorWorks。这组入口在 v1 是构建期 overlay 合入 core 包
//     （zhenguojian/sidecar/core_overlay，package core），guoapp-src 本体并不导出；
//     v2 默认构建走 discovery_stub.go 返回明确错误，加 -tags v2overlay -overlay
//     构建时才接真实现（discovery_overlay.go）。前端对「filters 未就绪」有兜底。
//
// 信封统一为 {"ok":bool,"data":…,"error":…,"code":…,"retryAt":…}，
// 本包解包后把 data 平铺返回，错误转成带 Code 的 Error。
package native

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"duanjuapp/native/core"
)

// Error 是 core 信封里的错误透传，Code 目前已知值："local_media"、
// "source_blocked"（RetryAt 给出恢复时间，RFC3339）。
type Error struct {
	Message string
	Code    string
	RetryAt string
}

func (e *Error) Error() string {
	base := e.Message
	if e.Code == "source_blocked" && e.RetryAt != "" {
		base = fmt.Sprintf("%s（源被限流，预计 %s 后恢复）", e.Message, e.RetryAt)
	}
	if e.Code != "" {
		// wails 只把 message 串传回 JS，尾部附 [code] 供前端提取
		base += " [" + e.Code + "]"
	}
	return base
}

// 与 v1 CoreClient 相同的会话/序号语义：
// core 会取消「同 session 的更早读」，所以剧库与搜索必须分 lane；
// resolve 用 sequence 分配播放槽位，必须全局单调递增。
var (
	initOnce sync.Once
	initErr  error

	owner    = fmt.Sprintf("v2-%d", os.Getpid())
	sequence = atomic.Int64{}

	// v1 core_client._call_once 的豁免名单：这些 action 不带 session/sequence。
	noSessionActions = map[string]bool{
		"initialize": true, "cancelRead": true, "release": true,
		"cancelPlayback": true, "controlDownloads": true, "workLease": true,
		"controlDownloadBatch": true,
	}
	// v1 sidecar main.go 的路由表：注册表层命令不进 NativeRequest。
	sourcesActions = map[string]bool{"listSources": true, "setSourceEnabled": true}
	// v1 sidecar 把 discovery 组路由给 core_overlay 的 NativeDiscovery。
	discoveryActions = map[string]bool{
		"dramaCreators": true, "creatorProfile": true, "creatorWorks": true,
		"browseFilters": true, "browseFiltered": true,
	}
)

// DataDir 返回 v2 的引擎数据目录：env ZHENGUO_DATA 优先；
// Windows 用 %LOCALAPPDATA%\ZhenguoJian，其它系统用 UserConfigDir/ZhenguoJian。
func DataDir() (string, error) {
	if d := strings.TrimSpace(os.Getenv("ZHENGUO_DATA")); d != "" {
		return d, nil
	}
	if runtime.GOOS == "windows" {
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, "ZhenguoJian"), nil
		}
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ZhenguoJian"), nil
}

// Init 幂等初始化 core 引擎（sync.Once），app.startup 与 Call 兜底都会调它。
// 并发安全性：core.NativeRequest 自身可并发调用——v1 sidecar 就是 goroutine
// 池并发分发（main.go slots+group），内部用 nativeState.Mutex/engine.mu 保护；
// 本包只在 initialize 上用 Once 保证只发一次。
func Init() error {
	initOnce.Do(func() {
		dir, err := DataDir()
		if err != nil {
			initErr = err
			return
		}
		_, err = Call("initialize", map[string]any{"directory": dir})
		initErr = err
	})
	return initErr
}

// ResetInitForTest 让单测能在不同临时目录重新 initialize（勿在生产调用）。
func ResetInitForTest() {
	initOnce = sync.Once{}
	initErr = nil
}

// Call 组装 v1 行协议 input、路由到对应 core 入口、解信封。
// 返回 data（平铺后的对象；core 对部分动作返回 bool/数组等非对象 data 时，
// 包装成 {"value": …}）。
func Call(action string, payload map[string]any) (map[string]any, error) {
	input := composeInput(action, payload)
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("请求序列化失败: %w", err)
	}
	var out string
	switch {
	case sourcesActions[action]:
		out = core.NativeSourcesCommand(string(raw))
	case discoveryActions[action]:
		out = discoveryRequest(string(raw))
	default:
		out = core.NativeRequest(string(raw))
	}
	return decodeEnvelope(out)
}

// composeInput 把 payload 补全成 v1 input（action/session/sequence），
// 纯函数，表驱动测试覆盖。
func composeInput(action string, payload map[string]any) map[string]any {
	input := make(map[string]any, len(payload)+3)
	for k, v := range payload {
		input[k] = v
	}
	input["action"] = action
	if !noSessionActions[action] {
		if _, ok := input["session"]; !ok {
			input["session"] = owner + ":" + action
		}
		if _, ok := input["sequence"]; !ok {
			input["sequence"] = sequence.Add(1)
		}
	}
	return input
}

// laneSession 复刻 v1 catalog 的 lane 语义：指定 lane 时 session 变为
// "owner:catalog:lane"，同 lane 内新请求取消旧请求，跨 lane 互不干扰。
func laneSession(action, lane string) string {
	if lane == "" {
		return ""
	}
	return fmt.Sprintf("%s:%s:%s", owner, action, lane)
}

// decodeEnvelope 解析 {"ok":…} 信封：ok=true 平铺 data；否则 Error。
func decodeEnvelope(out string) (map[string]any, error) {
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		s := out
		if len(s) > 200 {
			s = s[:200] + "…"
		}
		return nil, fmt.Errorf("非 JSON 应答(%d bytes): %s", len(out), s)
	}
	if env["ok"] != true {
		msg, _ := env["error"].(string)
		if msg == "" {
			msg = "本地核心处理失败"
		}
		code, _ := env["code"].(string)
		retryAt := ""
		if v, ok := env["retryAt"].(string); ok {
			retryAt = v
		}
		return nil, &Error{Message: msg, Code: code, RetryAt: retryAt}
	}
	switch data := env["data"].(type) {
	case map[string]any:
		return data, nil
	case nil:
		return map[string]any{}, nil
	default:
		return map[string]any{"value": data}, nil
	}
}
