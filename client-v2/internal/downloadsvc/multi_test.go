package downloadsvc

// multi_test.go —— 「按季合并选中」的规则表（v1 downloads.py:1940-1974）。
//
// `buildMultiTargets` 是纯函数，所以季号、命名、冲突后缀、
// "不足 2 集丢掉"这些最容易在移植里写歪的东西都能在假核心之外单独钉住；
// `MergeStartMulti` 那半段用假核心跑一遍排队、跳过与报错三条路径。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"zhenguojian/client-v2/internal/watchstore"
)

func multiJob(id, dramaID, dramaTitle string, index int, state string) map[string]any {
	return map[string]any{
		"id":     id,
		"state":  state,
		"index":  float64(index),
		"drama":  map[string]any{"id": dramaID, "title": dramaTitle, "source": "hongguo"},
		"chapter": map[string]any{"id": "c" + id, "name": "第" + id + "集", "duration": float64(60)},
	}
}

func dramaOf(id, title string) map[string]any {
	return map[string]any{"id": id, "title": title, "source": "hongguo"}
}

func TestMultiSeasonOf(t *testing.T) {
	cases := []struct {
		title string
		want  int
	}{
		{"某剧", 1},                    // 没有标记按第一季（v1 同）
		{"某剧 第二季", 2},               // 中文数字
		{"某剧 第十二季", 12},             // "十X" 这种形态真实存在，窄实现会算错就漏了
		{"某剧 第3季", 3},                // 阿拉伯数字
		{"某剧 S04", 4},                 // 只有 S 标记
		{"某剧 第零季", 1},               // 零不是合法季号 → 回落 S 标记 → 都没有则 1（v1 `if number:` 同）
		{"某剧 S07 第二季", 2},            // 两种标记都在时，第N季优先（v1 先找 _SEASON_RE）
	}
	for _, c := range cases {
		if got := multiSeasonOf(c.title); got != c.want {
			t.Errorf("season_of(%q) = %d，期望 %d", c.title, got, c.want)
		}
	}
}

func TestMultiStripSeasonAndSafeName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"某剧 第二季", "某剧"},
		{"某剧第二季", "某剧"},          // 紧贴也要剥
		// v1 是两步：`_SEASON_RE.sub` 之后先 `.strip(" 　-·~～")`（**两端**都吃连字符），
		// 再 `re.sub(r"[Ss]\d+$")` + 纯 `.strip()`。
		// 我第一版把期望写成 "某剧 -"，以为"中间的连字符留着"——
		// CI 报"实得 某剧"才看清：那个连字符在剥掉季标记之后已经**位于结尾**，
		// 所以照样被第一刀吃掉。期望错的是我，不是实现。
		{" 某剧 - 第3季 ", "某剧"},
		{"某剧-第二季", "某剧"},            // 同上：紧贴的连字符也在第一刀范围内
		{"某剧 - 中间 - 第二季", "某剧 - 中间"}, // 中间那根留着，结尾那根被吃
		{"某剧 S02", "某剧"},            // 结尾的 Sxx 清掉
		{"第二季", "合集"},              // 剥完空了给「合集」
		{"", "合集"},
	}
	for _, c := range cases {
		if got := multiStripSeason(c.in); got != c.want {
			t.Errorf("strip_season(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
	safeCases := []struct{ in, want string }{
		{`a/b\c:d*e?f"g<h>i|j`, "a_b_c_d_e_f_g_h_i_j"}, // v1 `_safe_name` 那一组字符
		{"  有空格  ", "有空格"},
		{"", "合集"},
	}
	for _, c := range safeCases {
		if got := multiSafeName(c.in); got != c.want {
			t.Errorf("safe_name(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

func TestBuildMultiTargetsOrderAndNaming(t *testing.T) {
	jobs := []map[string]any{
		// 第一季：3 集完成 + 1 集在下载（不该进）
		multiJob("s1e1", "hongguo:1", "某剧 第一季", 1, "completed"),
		multiJob("s1e2", "hongguo:1", "某剧 第一季", 2, "completed"),
		multiJob("s1e3", "hongguo:1", "某剧 第一季", 7, "completed"),
		multiJob("s1e4", "hongguo:1", "某剧 第一季", 8, "downloading"),
		// 第二季：2 集
		multiJob("s2e1", "hongguo:2", "某剧 第二季", 1, "completed"),
		multiJob("s2e2", "hongguo:2", "某剧 第二季", 2, "completed"),
		// 第三季：只有 1 集完成 → 整个丢掉（v1 的 >=2）
		multiJob("s3e1", "hongguo:3", "某剧 第三季", 1, "completed"),
	}
	// 传入顺序故意是"三、二、一"，验证结果按季号升序而不是传入序
	dramas := []map[string]any{
		dramaOf("hongguo:3", "某剧 第三季"),
		dramaOf("hongguo:2", "某剧 第二季"),
		dramaOf("hongguo:1", "某剧 第一季"),
	}
	got := buildMultiTargets(dramas, jobs)
	if len(got) != 2 {
		t.Fatalf("该只剩两季（第三季不足 2 集）：%+v", got)
	}
	if got[0].Season != 1 || got[1].Season != 2 {
		t.Fatalf("排序没按季号：%+v", got)
	}
	// 命名：`<去季号安全名> S%02dE%02d.mkv`，E 取最小集号（v1 _multi_targets:1957-1960）
	if got[0].OutputName != "某剧 S01E01.mkv" {
		t.Errorf("第一季命名不对：%q", got[0].OutputName)
	}
	if got[1].OutputName != "某剧 S02E01.mkv" {
		t.Errorf("第二季命名不对：%q", got[1].OutputName)
	}
	// 只数 completed 的集号
	if strings.Join(intsToStrings(got[0].Episodes), ",") != "1,2,7" {
		t.Errorf("分集清单不对：%v", got[0].Episodes)
	}
}

func TestBuildMultiTargetsCollisionSuffix(t *testing.T) {
	// v1：同名 base 依次加 " (2)"、" (3)"
	// 真实情形是两部**不同 id** 的剧去完季号后同名（红果常见"某剧"/"某剧 第二季"混标）
	jobs := []map[string]any{
		multiJob("a1", "hongguo:1", "某剧", 1, "completed"),
		multiJob("a2", "hongguo:1", "某剧", 2, "completed"),
		multiJob("b1", "hongguo:2", "某剧", 1, "completed"),
		multiJob("b2", "hongguo:2", "某剧", 2, "completed"),
		multiJob("c1", "hongguo:3", "某剧", 1, "completed"),
		multiJob("c2", "hongguo:3", "某剧", 2, "completed"),
	}
	got := buildMultiTargets([]map[string]any{
		dramaOf("hongguo:1", "某剧"),
		dramaOf("hongguo:2", "某剧"),
		dramaOf("hongguo:3", "某剧"),
	}, jobs)
	want := []string{"某剧 S01E01.mkv", "某剧 S01E01 (2).mkv", "某剧 S01E01 (3).mkv"}
	if len(got) != 3 {
		t.Fatalf("该有三个目标：%+v", got)
	}
	for i, w := range want {
		if got[i].OutputName != w {
			t.Errorf("第 %d 个命名 %q，期望 %q", i+1, got[i].OutputName, w)
		}
	}
}

func TestMergeStartMultiQueuesEachSeason(t *testing.T) {
	h := newHarness(t)
	h.core.handlers["downloads"] = func(map[string]any) (map[string]any, error) {
		return jobsResult(
			multiJob("s1e1", "hongguo:1", "某剧 第一季", 1, "completed"),
			multiJob("s1e2", "hongguo:1", "某剧 第一季", 2, "completed"),
			multiJob("s2e1", "hongguo:2", "某剧 第二季", 1, "completed"),
			multiJob("s2e2", "hongguo:2", "某剧 第二季", 2, "completed"),
		), nil
	}
	res, err := h.svc.MergeStartMulti([]map[string]any{
		dramaOf("hongguo:1", "某剧 第一季"),
		dramaOf("hongguo:2", "某剧 第二季"),
	})
	if err != nil {
		t.Fatalf("MergeStartMulti: %v", err)
	}
	if res["queued"] != 2 {
		t.Fatalf("该排入两季：%v", res)
	}
	names := res["outputs"].([]string)
	if names[0] != "某剧 S01E01.mkv" || names[1] != "某剧 S02E01.mkv" {
		t.Fatalf("输出名不对：%v", names)
	}
	if len(h.svc.merges) != 2 {
		t.Fatalf("merges 表该有两条：%d", len(h.svc.merges))
	}
	// ⚠ 这条断言上一轮写的是 `Output == "某剧 S01E01.mkv"`，
	// 那是把 bug 当规格钉：`Output` 的语义是"已落盘的绝对路径"，
	// 排队阶段它必须是空，名字属于 `OutputName`。
	// （同一个错在实现层的后果更糟：runMerge 从来不读 Output，
	//  所以那个名字既不会出现在磁盘上，UI 又会在 done 之前显示一个不存在的路径。）
	if h.svc.merges[0].Output != "" {
		t.Fatalf("排队阶段 Output 该是空，实得 %q", h.svc.merges[0].Output)
	}
	if h.svc.merges[0].OutputName != "某剧 S01E01.mkv" {
		t.Fatalf("条目 OutputName 不对：%q", h.svc.merges[0].OutputName)
	}
	if h.core.count("downloads") == 0 {
		t.Fatal("没刷新核心任务表就排任务，可能拿到过期状态")
	}
}

func TestMergeStartMultiNeedsAtLeastTwoSeasons(t *testing.T) {
	h := newHarness(t)
	h.core.handlers["downloads"] = func(map[string]any) (map[string]any, error) {
		return jobsResult(
			multiJob("s1e1", "hongguo:1", "某剧 第一季", 1, "completed"),
			multiJob("s1e2", "hongguo:1", "某剧 第一季", 2, "completed"),
			// 第二季只完成 1 集 → 不算目标 → 总数不足两部
			multiJob("s2e1", "hongguo:2", "某剧 第二季", 1, "completed"),
		), nil
	}
	_, err := h.svc.MergeStartMulti([]map[string]any{
		dramaOf("hongguo:1", "某剧 第一季"),
		dramaOf("hongguo:2", "某剧 第二季"),
	})
	// v1 原话去掉"左侧"两个字，其余照抄
	// 句子本体 + 错误码尾巴都要在：一个给日志，一个给界面（前端按 code 取 i18n）
	if err == nil || !strings.Contains(err.Error(), "至少两部要合并的剧集") ||
		!strings.Contains(err.Error(), "[multi_targets]") {
		t.Fatalf("该给 v1 那句提示并带 [multi_targets]，得 %v", err)
	}
	if len(h.svc.merges) != 0 {
		t.Fatal("整批不成立却排了任务")
	}
}

func TestMergeStartMultiSkipsRunningSeason(t *testing.T) {
	// v1 `_start_batch(skip_running=True)`：一批里有一季正在合，
	// 应该**跳过那一季**继续排其余，而不是整批失败。
	h := newHarness(t)
	h.core.handlers["downloads"] = func(map[string]any) (map[string]any, error) {
		return jobsResult(
			multiJob("s1e1", "hongguo:1", "某剧 第一季", 1, "completed"),
			multiJob("s1e2", "hongguo:1", "某剧 第一季", 2, "completed"),
			multiJob("s2e1", "hongguo:2", "某剧 第二季", 1, "completed"),
			multiJob("s2e2", "hongguo:2", "某剧 第二季", 2, "completed"),
		), nil
	}
	h.svc.mu.Lock()
	h.svc.merges = []*MergeEntry{{ID: "hongguo:1#001-002", DramaID: "hongguo:1",
		Title: "某剧 第一季", State: MergeRunning, Episodes: []int{1, 2}, Count: 2}}
	h.svc.mergesLoaded = true
	h.svc.mu.Unlock()

	res, err := h.svc.MergeStartMulti([]map[string]any{
		dramaOf("hongguo:1", "某剧 第一季"),
		dramaOf("hongguo:2", "某剧 第二季"),
	})
	if err != nil {
		t.Fatalf("有一季在跑不该让整批失败：%v", err)
	}
	if res["queued"] != 1 || res["skipped"] != 1 {
		t.Fatalf("该排一季、跳一季：%v", res)
	}
	if h.svc.merges[0].State != MergeRunning {
		t.Fatal("在跑那条被覆盖了")
	}
}

func TestMergeStartMultiEmptySelection(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.MergeStartMulti(nil); err == nil {
		t.Fatal("没勾选该报错")
	}
	if _, err := h.svc.MergeStartMulti([]map[string]any{}); err == nil ||
		!strings.Contains(err.Error(), "[multi_none]") {
		t.Fatalf("空数组该报 [multi_none]，得 %v", err)
	}
}

// TestMergeStartMultiNamesFileOnDisk 是最该有的一条，也是上一轮缺的那条：
// 上一轮我只测了 `buildMultiTargets` 算出的名字（纯函数），
// 而**合并执行那一步从来不读那个名字**——它自己按 "剧名 全001-002集.mkv" 拼，
// 所以 SxxExx 从没落到磁盘上，测试却还是全绿（我上一轮的提交信息里
// 甚至写了"命名逐条照 v1"）。这条直接跑完整管线，看磁盘上那个文件叫什么。
func TestMergeStartMultiNamesFileOnDisk(t *testing.T) {
	h := newHarness(t)
	// mergeFixture 造的是单剧两集（d1#001-002）；再补一部"第二季"，
	// 让 buildMultiTargets 真能凑够两部（v1 的门槛就是 ≥2 部）。
	ep1, ep2 := mergeFixture(t, h)
	root := filepath.Dir(ep1)
	secondJobs := []map[string]any{
		testJob("k1", "completed", 1), testJob("k2", "completed", 2),
	}
	for _, j := range secondJobs {
		// testJob 里剧目是写死的 d1/测试剧；这里改成第二部，
		// 其余键（chapter.currentEpisode 等）保持与夹具一致，
		// 不然 episodeOfJob 解析出来的集号会跟真实路径不一样。
		j["drama"] = map[string]any{"id": "d2", "title": "测试剧 第二季", "source": "hongguo"}
		j["folder"] = "d2/" + j["id"].(string)
	}
	h.core.handlers["downloads"] = func(map[string]any) (map[string]any, error) {
		return jobsResult(
			testJob("j1", "completed", 1), testJob("j2", "completed", 2),
			secondJobs[0], secondJobs[1],
		), nil
	}
	h.core.handlers["localPlayback"] = func(p map[string]any) (map[string]any, error) {
		idx, _ := p["index"].(int)
		if idx < 1 || idx > 2 {
			return nil, fmt.Errorf("夹具只准备两集，index=%v", p["index"])
		}
		return map[string]any{"local": true, "url": []string{ep1, ep2}[idx-1]}, nil
	}
	h.exec.progress = true

	ctx := context.Background()
	h.svc.OnStartup(ctx)
	defer h.svc.OnShutdown(ctx)

	out, err := h.svc.MergeStartMulti([]map[string]any{
		{"id": "d1", "title": "测试剧 第一季", "source": "hongguo"},
		{"id": "d2", "title": "测试剧 第二季", "source": "hongguo"},
	})
	if err != nil {
		t.Fatalf("MergeStartMulti: %v", err)
	}
	if out["queued"] != 2 {
		t.Fatalf("该排两季：%v", out)
	}
	waitFor(t, func() bool {
		m1, ok1 := h.mergeState("d1#001-002")
		m2, ok2 := h.mergeState("d2#001-002")
		return ok1 && ok2 && m1["state"] == MergeDone && m2["state"] == MergeDone
	}, "两季都该跑完")

	for id, want := range map[string]string{
		"d1#001-002": "测试剧 S01E01.mkv",
		"d2#001-002": "测试剧 S02E01.mkv",
	} {
		m, _ := h.mergeState(id)
		got, _ := m["output"].(string)
		// 断言两件事：**磁盘上的文件名**是 SxxExx，且 MergeList 回报的路径就是那个文件
		if filepath.Base(got) != want {
			t.Fatalf("%s 产物名 = %q，期望 %q", id, got, want)
		}
		if st, serr := os.Stat(got); serr != nil || st.Size() == 0 {
			t.Fatalf("%s 磁盘上没有这个成品：%v", id, serr)
		}
		if filepath.Dir(got) != filepath.Join(root, "合并") {
			t.Fatalf("%s 成品目录不对：%q", id, got)
		}
	}
}

// TestMergeOutputNameBasenameGuard 测 v1 media.py:1033-1038 那条安全守卫：
// 名字是从剧名拼出来的，只取 basename——一个标题不能把成品写到 下载/合并/ 之外去。
func TestMergeOutputNameBasenameGuard(t *testing.T) {
	cases := []struct {
		in   string
		want string
		bad  bool
	}{
		{"测试剧 S01E01.mkv", "测试剧 S01E01.mkv", false},
		{"..\\..\\windows\\evil.mkv", "evil.mkv", false},   // Windows 反斜杠也要当分隔符切
		{"/etc/passwd", "passwd", false},                      // 正斜杠同理
		{"...", "", true},                                     // 洗完是空的 → 报错，不退回默认名
		{"   ", "", true},
	}
	for _, c := range cases {
		clean := filepath.Base(strings.ReplaceAll(c.in, "\\", "/"))
		clean = strings.Trim(clean, " .")
		if c.bad {
			if clean != "" {
				t.Errorf("%q 应该被洗成空（要走报错分支），实得 %q", c.in, clean)
			}
			continue
		}
		if clean != c.want {
			t.Errorf("%q 洗出来是 %q，期望 %q", c.in, clean, c.want)
		}
	}
}

// TestMergeStartMultiClearsOutputOnlyOnDisk 钉住字段分工：
// 排队时 Output 必须是空（那是"已落盘的路径"），只有 OutputName 有值。
// 上一轮我就是把文件名塞进 Output，UI 的 done 分支会显示一个还不存在的路径。
func TestMergeStartMultiFieldSplit(t *testing.T) {
	h := newHarness(t)
	root := h.tmp + "/dl"
	ep := filepath.Join(root, "ep1.mp4")
	ep2 := filepath.Join(root, "ep2.mp4")
	for _, p := range []string{ep, ep2} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("source"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mkJob := func(id, did, title string, idx float64) map[string]any {
		return map[string]any{
			"id": id, "state": "completed", "index": idx,
			"drama":   map[string]any{"id": did, "title": title, "source": "hongguo"},
			"chapter": map[string]any{"id": id + "c", "name": "第1集", "duration": float64(60), "currentEpisode": idx},
			"folder":  did,
		}
	}
	h.core.handlers["downloads"] = func(map[string]any) (map[string]any, error) {
		return jobsResult(
			mkJob("a1", "d1", "拆分剧 第一季", 1), mkJob("a2", "d1", "拆分剧 第一季", 2),
			mkJob("b1", "d2", "拆分剧 第二季", 1), mkJob("b2", "d2", "拆分剧 第二季", 2),
		), nil
	}
	h.core.handlers["downloadDirectory"] = func(map[string]any) (map[string]any, error) {
		return map[string]any{"directory": root}, nil
	}
	h.core.handlers["localPlayback"] = func(map[string]any) (map[string]any, error) {
		return nil, errors.New("不给本地路径，走 folder/media.mp4 兜底")
	}

	// 不让它真跑完：只看排队后的字段分工
	out, err := h.svc.MergeStartMulti([]map[string]any{
		{"id": "d1", "title": "拆分剧 第一季", "source": "hongguo"},
		{"id": "d2", "title": "拆分剧 第二季", "source": "hongguo"},
	})
	if err != nil {
		t.Fatalf("MergeStartMulti: %v", err)
	}
	_ = out
	h.svc.mu.Lock()
	defer h.svc.mu.Unlock()
	n := 0
	for _, m := range h.svc.merges {
		if m.State != MergeQueued {
			continue
		}
		n++
		if m.Output != "" {
			t.Errorf("%s 排队阶段 Output 该是空（它表示已落盘路径），实得 %q", m.ID, m.Output)
		}
		if m.OutputName == "" {
			t.Errorf("%s 排队阶段该有 OutputName", m.ID)
		}
	}
	if n != 2 {
		t.Fatalf("该排两条：%d", n)
	}
}

// TestArchiveDramaKey 钉住归档归组键的解析（v1 downloads.py:89-95）。
// 上一笔接通 SxxExx 命名后才发现的问题：v2 扫描原来只切 `" 全"` 一种中缀，
// 于是按季合并产出的 `剧名 S01E01.mkv` 会自成一组 `m:剧名 S01E01`，
// 剧名组反而丢掉「含合并」徽标——**同一个文件在 v1/v2 归档页归属不同**。
func TestArchiveDramaKey(t *testing.T) {
	cases := []struct {
		stem, want string
	}{
		// v1 那三种剥法 + 去季号
		{"测试剧 全001-002集", "测试剧"},
		{"测试剧 全001-004集（未完整校验）", "测试剧"}, // 中缀之后还有尾巴也一起剥
		{"测试剧 S01E01", "测试剧"},
		{"测试剧 S02E007", "测试剧"},
		{"测试剧 007", "测试剧"},        // 结尾纯数字
		{"测试剧 第二季", "测试剧"},      // 只有季号：去季后留剧名
		{"第二季 测试剧", "测试剧"},      // 季号在前也要剥（v1 的 sub 不锚结尾）
		{"无名", "无名"},
		{"   ", "合集"},                // 剥空给「合集」（v1 最后一道 or）
		{"S01E01", "合集"},
		{"第3季", "合集"},
		// ⚠ 上一版我把 archiveDramaKey 写成"绕过 strip_season 的空返回"
		// （`if stripped != "" && stripped != "合集" { … }`），
		// 那条 `or stem` 在 v1 是**死分支**（strip_season 空时已返回「合集」），
		// 绕过它就把死分支变活了：`第3季` 会被归成 `第3季` 而不是「合集」。
		{"测试剧 S02", "测试剧"},
		// v1 strip_season 的第二刀（`[Ss]\d+$`）。
		// archive_scan 就地写过一份窄实现，漏的正是这一刀——
		// `测试剧 S02.mkv` 在 v1 归「测试剧」，那版会留着 ` S02` 自成一派。
		// ⚠ 这一条我原本期望写成 "S01E01"，理由是 v1 那句
		// `strip_season(stem.strip()) or stem.strip() or "合集"` 里"退回原 stem"。
		// 但 v1 的 `strip_season` 自己在空时就已经返回「合集」
		// （downloads.py:283-288 最后一行 `return title or "合集"`），
		// 所以中间那道 `or stem.strip()` 是**永远走不到的死分支**。
		// 一个纯集号的文件名（只有 S01E01、没有剧名）在 v1 就归进「合集」组。
	}
	for _, c := range cases {
		if got := archiveDramaKey(c.stem); got != c.want {
			t.Errorf("archiveDramaKey(%q) = %q，期望 %q", c.stem, got, c.want)
		}
	}
}

// TestSeasonSpecsAreTwoDifferentSpecs 钉住一件容易做错的事：
// 本包的季号解析与 `internal/watchstore` 的那份**不是重复实现，是 v1 的两份不同规格**。
// 谁哪天"顺手重构合成一个函数"，下面这几条会立刻红。
func TestSeasonSpecsAreTwoDifferentSpecs(t *testing.T) {
	// downloads.py:250 的窄规格：只认「第N季」。
	if got := seasonOfTitle("某剧 第2部"); got != 1 {
		t.Errorf("窄规格不该认「第2部」，实得 %d", got)
	}
	if got := seasonOfTitle("某剧 第２季"); got != 1 {
		t.Errorf("窄规格不该认全角数字，实得 %d", got)
	}
	// watchstore.py:31 的宽规格：认「第N部」与全角。
	if got := watchstore.SeasonNumber("某剧 第2部"); got != 2 {
		t.Errorf("宽规格该认「第2部」，实得 %d", got)
	}
	if got := watchstore.SeasonNumber("某剧 第２季"); got != 2 {
		t.Errorf("宽规格该认全角数字，实得 %d", got)
	}
	// 两者对普通「第N季」结论一致（不一致才叫真漂了）
	for _, title := range []string{"某剧 第三季", "某剧 第12季", "某剧 S03"} {
		if a, b := seasonOfTitle(title), watchstore.SeasonNumber(title); a != b {
			t.Errorf("%q 两份实现给出不同结果：窄 %d / 宽 %d", title, a, b)
		}
	}
}

// TestStripSeasonTitleMatchesV1 是本包 strip 那一步的规格表。
func TestStripSeasonTitleMatchesV1(t *testing.T) {
	cases := []struct{ in, want string }{
		{"测试剧 第二季", "测试剧"},
		{"第二季 测试剧", "测试剧"},  // 不锚位置（v1 用 sub）
		{"测试剧 - 第3季 ", "测试剧"}, // 两端连字符一起吃
		{"测试剧 S02", "测试剧"},     // 第二刀：结尾 Sxx
		{"测试剧 S02E01", "测试剧 S02E01"}, // 第二刀只切结尾 `S\d+`，E 尾巴不归它管
		{"第3季", "合集"},
		{"   ", "合集"},
	}
	for _, c := range cases {
		if got := stripSeasonTitle(c.in); got != c.want {
			t.Errorf("stripSeasonTitle(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

func intsToStrings(xs []int) []string {
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		// 用 strconv，不手写十进制拆位——第一版我随手写了
		// `string(rune('0'+x/10%10)) + string(rune('0'+x%10))`，
		// 三位数就会拆错，而断言恰恰靠它比对集号清单。
		out = append(out, strconv.Itoa(x))
	}
	return out
}
