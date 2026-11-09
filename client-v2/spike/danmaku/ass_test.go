package danmaku

import (
	"regexp"
	"strings"
	"testing"
)

func TestRenderSingleItem(t *testing.T) {
	out := Render([]Item{{TimeMs: 1000, Text: "你好弹幕", Color: "#FF8800", Size: 45}})
	if !strings.Contains(out, "[Script Info]") || !strings.Contains(out, "[V4+ Styles]") || !strings.Contains(out, "[Events]") {
		t.Fatal("缺 ASS 三段头")
	}
	// 时间：start 1000ms → 0:00:01.00；end +4000 → 0:00:05.00
	if !strings.Contains(out, "0:00:01.00,0:00:05.00") {
		t.Fatalf("Dialogue 起止时间错:\n%s", out)
	}
	// 颜色 #FF8800 → &H0088FF&
	if !strings.Contains(out, `\c&H0088FF&`) {
		t.Fatalf("颜色转换错:\n%s", out)
	}
	if !strings.Contains(out, `\fs45}`) {
		t.Fatal("字号未写入")
	}
	if !strings.Contains(out, "你好弹幕") {
		t.Fatal("文本未输出")
	}
}

func TestLaneCollision(t *testing.T) {
	// 三条同刻长弹幕 → 必须分 3 轨（MarginV 互异）
	items := []Item{
		{TimeMs: 0, Text: strings.Repeat("甲", 30)},
		{TimeMs: 0, Text: strings.Repeat("乙", 30)},
		{TimeMs: 0, Text: strings.Repeat("丙", 30)},
	}
	out := Render(items)
	re := regexp.MustCompile(`Dialogue: 0,[^,]+,[^,]+,Default,,0,0,(\d+),`)
	ms := re.FindAllStringSubmatch(out, -1)
	if len(ms) != 3 {
		t.Fatalf("应 3 条 Dialogue，得 %d", len(ms))
	}
	seen := map[string]bool{}
	for _, m := range ms {
		if seen[m[1]] {
			t.Fatalf("同刻弹幕撞轨: MarginV=%s 重复\n%s", m[1], out)
		}
		seen[m[1]] = true
	}
	// 上一条滚出碰撞区后应复用轨 0（首条 0+width(30*38*0.55=627px)/v → ≈2085ms 后自由）
	out2 := Render([]Item{
		{TimeMs: 0, Text: strings.Repeat("甲", 30)},
		{TimeMs: 3000, Text: strings.Repeat("乙", 5)},
	})
	ms2 := re.FindAllStringSubmatch(out2, -1)
	if len(ms2) != 2 || ms2[0][1] != ms2[1][1] {
		t.Fatalf("3000ms 后的短弹幕应复用原轨:\n%s", out2)
	}
}

func TestEmptyAndSort(t *testing.T) {
	out := Render(nil)
	if !strings.Contains(out, "[Events]") || strings.Contains(out, "Dialogue:") {
		t.Fatal("空输入不应有 Dialogue")
	}
	// 乱序输入按时间排
	out = Render([]Item{{TimeMs: 5000, Text: "后"}, {TimeMs: 100, Text: "先"}})
	i1 := strings.Index(out, "先")
	i2 := strings.Index(out, "后")
	if !(i1 >= 0 && i2 > i1) {
		t.Fatalf("未按时间排序:\n%s", out)
	}
}
