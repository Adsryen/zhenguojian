package downloadsvc

// sidecar_test.go —— 「下载时保存弹幕」的客户端侧。
// 覆盖：跃迁判定、两个旗标同文件的互相覆盖、开关关时什么都不做、
// busy 防重入、以及一次真采集把两个旁挂文件写出来。
//
// 为什么这些能在 CI 上跑而没有网络：resolve/danmakuAll/downloadDirectory/localPlayback
// 全走 `s.call`，测试注入假核心；写文件写到 t.TempDir()。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"zhenguojian/client-v2/internal/danmaku"
)

func jobRow(id, state string) map[string]any {
	return map[string]any{
		"id":     id,
		"state":  state,
		"folder": id,
		"index":  float64(3),
		"drama":  map[string]any{"id": "hongguo:1", "title": "测试剧", "sourceId": "7788"},
		"chapter": map[string]any{"id": "c3", "duration": float64(120)},
	}
}

func TestNoteCompletedTransition(t *testing.T) {
	jobs := []map[string]any{
		jobRow("j1", "completed"),
		jobRow("j2", "downloading"),
		jobRow("j3", "completed"),
		jobRow("", "completed"), // 没 id 的行不参与
	}

	// 第一遍（prev 为空）：一个都不算跃迁——否则每次启动都把历史重抓一遍
	fresh, next := noteCompletedTransition(nil, jobs)
	if len(fresh) != 0 {
		t.Fatalf("首遍不该有跃迁，得 %d 条", len(fresh))
	}
	if next["j1"] != "completed" || len(next) != 3 {
		t.Fatalf("状态表不对：%v", next)
	}

	// j2 从 downloading→completed 才算；j1/j3 保持 completed 不重复抓
	prev := map[string]string{"j1": "completed", "j2": "downloading", "j3": "completed"}
	fresh, _ = noteCompletedTransition(prev, jobs)
	if len(fresh) != 1 || fresh[0]["id"] != "j2" {
		t.Fatalf("跃迁集合该只有 j2，得 %v", fresh)
	}

	// 顺序稳定：多集同时跃迁时按 id 排（日志与测试都靠它）
	prev2 := map[string]string{"j1": "downloading", "j3": "paused"}
	fresh2, _ := noteCompletedTransition(prev2, jobs)
	if len(fresh2) != 2 || fresh2[0]["id"] != "j1" || fresh2[1]["id"] != "j3" {
		t.Fatalf("跃迁没按 id 排序：%v", fresh2)
	}
}

func TestClientFlagsShareOneFileWithoutClobbering(t *testing.T) {
	// 这一条是给"两个旗标同一个文件"上的保险：
	// 原先 saveVerifyState 整文件只写 mergeVerify，加第二个键之后
	// 谁后写谁就把对方抹掉——这种 bug 不会报错，只会让开关"自己变回去"。
	h := newHarness(t)
	if _, err := h.svc.SaveSettings(2, true, false); err != nil { // mergeVerify=false
		t.Fatal(err)
	}
	if err := h.svc.SetDanmakuDownload(true); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.SaveSettings(3, false, false); err != nil { // 再写一次 mergeVerify
		t.Fatal(err)
	}

	// 新服务从盘上恢复：两个都得在
	h2 := New(Options{Call: h.core.Call, DataDirFunc: func() (string, error) { return h.tmp, nil }, Executor: h.exec})
	st, err := h2.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if st["mergeVerify"] != false {
		t.Fatalf("mergeVerify 没恢复：%v", st["mergeVerify"])
	}
	if st["danmakuDownload"] != true {
		t.Fatalf("danmakuDownload 被 mergeVerify 的写入抹掉了：%v", st)
	}

	// 文件里确实只有这两个键、都是布尔
	body, err := os.ReadFile(filepath.Join(h.tmp, "downloads.state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("状态文件坏了：%v %s", err, body)
	}
	if len(raw) != 2 {
		t.Fatalf("键不该被别处写进来：%v", raw)
	}
}

func TestQueueSidecarsDisabledDoesNothing(t *testing.T) {
	h := newHarness(t) // 默认关
	fresh, _ := noteCompletedTransition(map[string]string{"j2": "downloading"},
		[]map[string]any{jobRow("j2", "completed")})
	h.svc.queueSidecars(nil, fresh)
	h.svc.wg.Wait()
	if len(h.svc.SidecarReports()) != 0 {
		t.Fatalf("开关没开却跑了：%+v", h.svc.SidecarReports())
	}
	if h.core.count("resolve") != 0 || h.core.count("danmakuAll") != 0 {
		t.Fatalf("开关没开却打了核心：resolve=%d all=%d", h.core.count("resolve"), h.core.count("danmakuAll"))
	}
}

func TestQueueSidecarsSkipsBusyJob(t *testing.T) {
	h := newHarness(t)
	if err := h.svc.SetDanmakuDownload(true); err != nil {
		t.Fatal(err)
	}
	job := jobRow("j9", "completed")
	h.svc.mu.Lock()
	if h.svc.sidecarBusy == nil {
		h.svc.sidecarBusy = map[string]bool{}
	}
	h.svc.sidecarBusy["j9"] = true // 模拟上一轮还在跑
	h.svc.mu.Unlock()

	h.svc.queueSidecars(nil, []map[string]any{job})
	h.svc.wg.Wait()
	if len(h.svc.SidecarReports()) != 0 {
		t.Fatalf("busy 的 job 不该重跑：%+v", h.svc.SidecarReports())
	}
}

func TestCollectSidecarForJobWritesBothFiles(t *testing.T) {
	h := newHarness(t)
	if err := h.svc.SetDanmakuDownload(true); err != nil {
		t.Fatal(err)
	}
	root := h.tmp
	mediaDir := filepath.Join(root, "j7")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(mediaDir, "media.mp4")
	if err := os.WriteFile(media, []byte("fake media"), 0o644); err != nil {
		t.Fatal(err)
	}

	// localPlayback 不给本地路径 → 走 <root>/<folder>/media.mp4 兜底那条（与合并同规则）
	h.core.handlers["localPlayback"] = func(map[string]any) (map[string]any, error) {
		return map[string]any{"url": "https://example.test/v.m3u8"}, nil
	}
	h.core.handlers["downloadDirectory"] = func(map[string]any) (map[string]any, error) {
		return map[string]any{"directory": root}, nil
	}
	h.core.handlers["resolve"] = func(payload map[string]any) (map[string]any, error) {
		// v2 的 resolve 没有 force/session（v1 有），入参形状就这四样
		for _, k := range []string{"drama", "chapter", "index", "quality"} {
			if _, ok := payload[k]; !ok {
				t.Errorf("resolve 载荷缺键 %s：%v", k, payload)
			}
		}
		if _, ok := payload["force"]; ok {
			t.Error("v2 契约没有 force，别把 v1 的参数名抄过来")
		}
		return map[string]any{"session": "sess-1", "danmakuId": "ep-77"}, nil
	}
	h.core.handlers["danmakuAll"] = func(payload map[string]any) (map[string]any, error) {
		if payload["playbackSession"] != "sess-1" {
			t.Errorf("会话没传对：%v", payload)
		}
		if _, ok := payload["durationMs"]; ok {
			t.Error("v2 的 danmakuAll 不收 durationMs（v1 收），别抄错契约")
		}
		return map[string]any{
			"total": float64(2),
			"items": []any{
				// 故意乱序：CollectSidecar 得按 timeMs 排
				map[string]any{"id": "b", "timeMs": float64(90000), "text": "后"},
				map[string]any{"id": "a", "timeMs": float64(1000), "text": "前"},
			},
		}, nil
	}

	rep := h.svc.collectSidecarForJob(nil, jobRow("j7", "completed"))
	if rep.Outcome != string(SidecarRan) || rep.Items != 2 {
		t.Fatalf("结果不对：%+v", rep)
	}
	ass, doc := danmaku.SidecarPaths(media)
	body, err := os.ReadFile(ass)
	if err != nil {
		t.Fatalf(".ass 没写出来: %v", err)
	}
	// chapter.duration=120s 必须当事件上限，且乱序输入排好了
	if !strings.Contains(string(body), "PlayResX") {
		t.Fatalf(".ass 不像合法文件：%s", firstLinesOf(string(body), 2))
	}
	if strings.Index(string(body), "前") > strings.Index(string(body), "后") {
		t.Fatal(".ass 里弹幕没按时间排")
	}
	rawDoc, err := os.ReadFile(doc)
	if err != nil {
		t.Fatalf(".danmaku.json 没写出来: %v", err)
	}
	var sd danmaku.SidecarDocument
	if err := json.Unmarshal(rawDoc, &sd); err != nil {
		t.Fatalf("json 解析: %v / %s", err, rawDoc)
	}
	if sd.EpisodeID != "ep-77" || sd.SeriesID != "7788" || len(sd.Items) != 2 {
		t.Fatalf("document 内容不对：%+v", sd)
	}

	// 再跑一次：已经存过 → 不重复打核心（v1 的跳过判定在 resolve 之前）
	before := h.core.count("resolve")
	rep2 := h.svc.collectSidecarForJob(nil, jobRow("j7", "completed"))
	if rep2.Outcome != string(SidecarSkippedReady) {
		t.Fatalf("第二次该跳过：%+v", rep2)
	}
	if h.core.count("resolve") != before {
		t.Fatalf("跳过还去 resolve，白跑网络：%d → %d", before, h.core.count("resolve"))
	}
}

func TestCollectSidecarEmptyItemsWritesNoFiles(t *testing.T) {
	// 核心返回空 items 时**不能**写出空的 .ass：
	// 写了 SidecarReady 就变真，这一集永远补不上（v1 是直接 return 不写）。
	h := newHarness(t)
	if err := h.svc.SetDanmakuDownload(true); err != nil {
		t.Fatal(err)
	}
	mediaDir := filepath.Join(h.tmp, "j8")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(mediaDir, "media.mp4")
	if err := os.WriteFile(media, []byte("m"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.core.handlers["downloadDirectory"] = func(map[string]any) (map[string]any, error) {
		return map[string]any{"directory": h.tmp}, nil
	}
	h.core.handlers["resolve"] = func(map[string]any) (map[string]any, error) {
		return map[string]any{"session": "s"}, nil
	}
	h.core.handlers["danmakuAll"] = func(map[string]any) (map[string]any, error) {
		return map[string]any{"items": []any{}, "total": float64(0)}, nil
	}

	rep := h.svc.collectSidecarForJob(nil, jobRow("j8", "completed"))
	if rep.Outcome != string(SidecarEmpty) {
		t.Fatalf("该走 empty 分支：%+v", rep)
	}
	ass, doc := danmaku.SidecarPaths(media)
	for _, p := range []string{ass, doc} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("无弹幕却写了 %s", filepath.Base(p))
		}
	}
	if danmaku.SidecarReady(media) {
		t.Fatal("无弹幕后 SidecarReady 竟为真")
	}
}

func TestCollectSidecarReportsFailureWithoutTouchingFiles(t *testing.T) {
	h := newHarness(t)
	if err := h.svc.SetDanmakuDownload(true); err != nil {
		t.Fatal(err)
	}
	mediaDir := filepath.Join(h.tmp, "j5")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(mediaDir, "media.mp4")
	if err := os.WriteFile(media, []byte("m"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.core.handlers["downloadDirectory"] = func(map[string]any) (map[string]any, error) {
		return map[string]any{"directory": h.tmp}, nil
	}
	h.core.handlers["resolve"] = func(map[string]any) (map[string]any, error) {
		return nil, os.ErrDeadlineExceeded // 解析超时
	}
	rep := h.svc.collectSidecarForJob(nil, jobRow("j5", "completed"))
	if rep.Outcome != string(SidecarFailed) || rep.Err == "" {
		t.Fatalf("失败要如实回报：%+v", rep)
	}
	ass, _ := danmaku.SidecarPaths(media)
	if _, err := os.Stat(ass); err == nil {
		t.Fatal("失败了却留下半套 .ass")
	}
}

func TestSidecarVideoPathMissingMedia(t *testing.T) {
	h := newHarness(t)
	if err := h.svc.SetDanmakuDownload(true); err != nil {
		t.Fatal(err)
	}
	h.core.handlers["downloadDirectory"] = func(map[string]any) (map[string]any, error) {
		return map[string]any{"directory": h.tmp}, nil
	}
	rep := h.svc.collectSidecarForJob(nil, jobRow("nope", "completed"))
	if rep.Outcome != string(SidecarFailed) || !strings.Contains(rep.Err, "找不到") {
		t.Fatalf("找不到成品文件时该如实说：%+v", rep)
	}
}

func firstLinesOf(s string, n int) string {
	parts := strings.Split(s, "\n")
	if len(parts) > n {
		parts = parts[:n]
	}
	return strings.Join(parts, "\n")
}
