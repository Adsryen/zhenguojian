package native

import (
	"fmt"
	"time"
)

// Cover 走核心下载链路取封面本地缓存（v2 海报管线第一步，壳层再做 HEIC→JPEG 转码，
// 见 bindings_cover.go）。drama 传 catalog item 原样对象（字段即核心 nativeDrama JSON，
// guoapp-src/native/core/app_runtime.go:80）；force=true 强制回源重下。
// 返回信封 data 平铺：{"path": 本地缓存文件绝对路径, "heic": bool}——核心对 HEIC 只标记
// 不解码（engine.loadCover 读文件头 256B 判 magic，app_cover_repair.go:120；
// nativeIsCoverImage 把 HEIC 也算合法封面，app_covers.go:279）。
// 信封解包走同包 Call（actions.go 现有模式：payload → composeInput → 解 {"ok":…}）。
// 超时 60s：Call 是进程内同步分发，超时仅代表「不再等待」，core goroutine 自行收尾
// （与 v1 core_client 读超时同语义，避免封面下载卡死拖住 webview 调用方）。
func Cover(drama map[string]any, force bool) (map[string]any, error) {
	if drama == nil {
		return nil, fmt.Errorf("封面请求缺少 drama 对象")
	}
	return callWithTimeout("cover", map[string]any{"drama": drama, "force": force}, 60*time.Second)
}

// callWithTimeout 在 goroutine 里跑同步 Call 并计时。纯新增包装，不改 Call 语义；
// 同包既有动作（actions.go）保持同步直调，只有封面管线需要限时。
func callWithTimeout(action string, payload map[string]any, timeout time.Duration) (map[string]any, error) {
	type result struct {
		data map[string]any
		err  error
	}
	done := make(chan result, 1)
	go func() {
		data, err := Call(action, payload)
		done <- result{data, err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-done:
		return r.data, r.err
	case <-timer.C:
		return nil, fmt.Errorf("封面请求超时（%s）", timeout)
	}
}
