package downloadsvc

// multi_test.go —— 「按季合并选中」的规则表（v1 downloads.py:1940-1974）。
//
// `buildMultiTargets` 是纯函数，所以季号、命名、冲突后缀、
// "不足 2 集丢掉"这些最容易在移植里写歪的东西都能在假核心之外单独钉住；
// `MergeStartMulti` 那半段用假核心跑一遍排队、跳过与报错三条路径。

import (
	"strconv"
	"strings"
	"testing"
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
	// Output 字段真的落到条目上（单剧那条走的是"全N集"，别互相污染）
	if h.svc.merges[0].Output != "某剧 S01E01.mkv" {
		t.Fatalf("条目 Output 不对：%q", h.svc.merges[0].Output)
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
	if err == nil || !strings.Contains(err.Error(), "至少两部要合并的剧集") {
		t.Fatalf("该给 v1 那句提示，得 %v", err)
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
	if _, err := h.svc.MergeStartMulti([]map[string]any{}); err == nil {
		t.Fatal("空数组同样该报错")
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
