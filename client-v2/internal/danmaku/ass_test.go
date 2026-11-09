package danmaku

import (
	"os"
	"strings"
	"testing"
	"time"
)

// 画布 1920x1080 默认下 v1 参数的落地：font=48, line=60, lanes=9,
// topMargin=28；逐条断言 ASS 头、\move 坐标、轨道分配、时间戳。
func TestBuildAssScrollSnapshot(t *testing.T) {
	items := []Item{
		{ID: "a", TimeMs: 0, Text: "你好世界"},
		{ID: "b", TimeMs: 0, Text: "第二车道"},
		{ID: "c", TimeMs: 200, Text: "追尾测试"}, // lane0/1 占用中 → 第三条轨
		{ID: "d", TimeMs: 61500, Text: "带时间戳"},
		{ID: "e", TimeMs: 999_000, Text: "超整集丢弃"},
		{ID: "f", TimeMs: 300, Text: "   "}, // 空文本丢弃
	}
	ass := BuildAss(items, 120_000, AssOptions{})

	// 头部 = v1 样式行逐字
	for _, want := range []string{
		"ScriptType: v4.00+", "PlayResX: 1920", "PlayResY: 1080",
		"WrapStyle: 2", "ScaledBorderAndShadow: yes",
		"Style: Danmaku,Microsoft YaHei,48,&H00FFFFFF,&H00FFFFFF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,1,7,0,0,0,1",
	} {
		if !strings.Contains(ass, want) {
			t.Errorf("头部缺 %q\n%s", want, ass)
		}
	}

	// "你好世界" tw=4*48=192；v1 \move: (W+40, top, -tw-40, top, 0, 8000)
	want0 := `Dialogue: 0,0:00:00.00,0:00:08.00,Danmaku,,0,0,0,,{\move(1960,28,-232,28,0,8000)}你好世界`
	if !strings.Contains(ass, want0) {
		t.Errorf("缺滚动行 a:\n%s", ass)
	}
	// 同刻第二条 → 轨道1 top=28+60=88
	if !strings.Contains(ass, `{\move(1960,88,-232,88,0,8000)}第二车道`) {
		t.Errorf("缺轨道1行 b:\n%s", ass)
	}
	// t=0.2s：lane0 释放时刻 8*192/2112+0.25≈0.98 > 0.2，lane1 同 → lane2 top=148
	if !strings.Contains(ass, `{\move(1960,148,-232,148,0,8000)}追尾测试`) {
		t.Errorf("缺轨道2行 c:\n%s", ass)
	}
	// 时间戳厘秒对齐 + 丢弃脏时刻/空文本
	if !strings.Contains(ass, ",0:01:01.50,0:01:09.50,") {
		t.Errorf("缺 61.5s 行 d:\n%s", ass)
	}
	if strings.Contains(ass, "超整集丢弃") {
		t.Errorf("超界条目未丢弃")
	}
	if strings.Contains(ass, "Danmaku,,0,0,0,,{}") || strings.Count(ass, "Dialogue:") != 4 {
		t.Errorf("空文本不该产生行:\n%s", ass)
	}
}

// 全轨占用丢条（v1/官方客户端行为：挤不下就不刷）。
func TestBuildAssLaneDrop(t *testing.T) {
	var items []Item
	for i := 0; i < 30; i++ {
		items = append(items, Item{ID: "x", TimeMs: 0, Text: "挤爆"})
	}
	ass := BuildAss(items, 60_000, AssOptions{})
	if n := strings.Count(ass, "挤爆"); n != 9 { // lanes=9 @1080
		t.Fatalf("同刻 30 条应只落 9 条轨道，落了 %d", n)
	}
}

// 颜色覆盖 = v1 定案扩展：#RRGGBB → {\c&H{alpha}BBGGRR&}；非法色回退白。
func TestBuildAssColorAndOpacity(t *testing.T) {
	items := []Item{
		{ID: "1", TimeMs: 0, Text: "红", Color: "#FF0000"},
		{ID: "2", TimeMs: 0, Text: "橙", Color: "#FF9933"},
		{ID: "3", TimeMs: 0, Text: "脏色", Color: "red-ish"},
	}
	ass := BuildAss(items, 60_000, AssOptions{Opacity: 0.5}) // alpha=round(0.5*255)=128=80
	// "红" tw=48 → -48-40=-88；lane0 top=28
	if !strings.Contains(ass, `{\move(1960,28,-88,28,0,8000)}{\c&H800000FF&}红`) {
		t.Errorf("红色覆盖缺失:\n%s", ass)
	}
	// #FF9933 → BGR 3399FF
	if !strings.Contains(ass, `{\c&H803399FF&}橙`) {
		t.Errorf("橙色覆盖缺失:\n%s", ass)
	}
	if !strings.Contains(ass, "脏色") {
		t.Errorf("非法色条目应照常渲染（白）:\n%s", ass)
	}
	if n := strings.Count(ass, `\c&H`); n != 2 {
		t.Errorf("非法色不该产生 \\c 覆盖，出现 %d 次:\n%s", n, ass)
	}
	if !strings.Contains(ass, "Style: Danmaku,Microsoft YaHei,48,&H80FFFFFF") {
		t.Errorf("透明度未进样式行:\n%s", ass)
	}
}

// 顶部持留 = {\an8}+MarginV，无 \move；DurationMs 覆盖默认持留 4s。
func TestBuildAssTopHold(t *testing.T) {
	items := []Item{
		{ID: "1", TimeMs: 1000, Text: "顶", Mode: ModeTop},
		{ID: "2", TimeMs: 2000, Text: "底", Mode: ModeBottom, DurationMs: 6000},
	}
	ass := BuildAss(items, 60_000, AssOptions{})
	if !strings.Contains(ass, `Dialogue: 0,0:00:01.00,0:00:05.00,Danmaku,,0,0,28,,{\an8}顶`) {
		t.Errorf("顶部持留行缺失:\n%s", ass)
	}
	// t=2 时 lane0 被 1s 顶条占至 5s → 底部条走 lane1（top=88）
	if !strings.Contains(ass, `Dialogue: 0,0:00:02.00,0:00:08.00,Danmaku,,0,0,88,,{\an2}底`) {
		t.Errorf("底部持留行（6s 覆盖）缺失:\n%s", ass)
	}
	// 持留占用 = 6s（2s→8s）→ 3.9s 的滚动条避开 lane0 走 lane1（top=88）
	items2 := []Item{
		{ID: "1", TimeMs: 2000, Text: "底", Mode: ModeBottom, DurationMs: 6000},
		{ID: "2", TimeMs: 3900, Text: "另一条"},
	}
	ass2 := BuildAss(items2, 60_000, AssOptions{})
	if !strings.Contains(ass2, `{\move(1960,88,-184,88,0,8000)}另一条`) {
		t.Errorf("持留占用未生效，滚动条挤进被占轨道:\n%s", ass2)
	}
}

// 逐条字号偏离默认 → {\fs%d}，且 \move 按覆盖字号计算宽度。
func TestBuildAssSizeOverride(t *testing.T) {
	ass := BuildAss([]Item{{ID: "1", TimeMs: 0, Text: "大", Size: 72}}, 60_000, AssOptions{})
	if !strings.Contains(ass, `{\fs72}`) {
		t.Errorf("缺字号覆盖:\n%s", ass)
	}
	// "大" 72px 全宽 → tw=72 → -72-40=-112
	if !strings.Contains(ass, `{\move(1960,28,-112,28,0,8000)}`) {
		t.Errorf("move 终点未按覆盖字号计算:\n%s", ass)
	}
}

func TestCleanText(t *testing.T) {
	// v1 _clean：\r、\n 各换空格（不折叠），CRLF 得双空格后 strip 两端
	if got := cleanText("a\r\nb"); got != "a  b" {
		t.Errorf("换行平铺 = %q", got)
	}
	if got := cleanText(`{x}\y{z}`); got != `（x）＼y（z）` {
		t.Errorf("花括号/反斜杠转义 = %q", got)
	}
	long := strings.Repeat("弹", 65)
	got := cleanText(long)
	if len([]rune(got)) != 61 || !strings.HasSuffix(got, "…") {
		t.Errorf("60 字截断 = %q", got)
	}
}

func TestStamp(t *testing.T) {
	cases := map[float64]string{
		0:        "0:00:00.00",
		61.5:     "0:01:01.50",
		3661.004: "1:01:01.00",
		-1:       "0:00:00.00",
	}
	for in, want := range cases {
		if got := stamp(in); got != want {
			t.Errorf("stamp(%v) = %s, want %s", in, got, want)
		}
	}
}

func TestHexToBGR(t *testing.T) {
	if v, ok := hexToBGR("#FF9933"); !ok || v != "3399FF" {
		t.Errorf("FF9933 → %s %v", v, ok)
	}
	for _, bad := range []string{"", "red", "#fff", "#GGHHII", "#FF993333"} {
		if _, ok := hexToBGR(bad); ok {
			t.Errorf("非法色 %q 不该通过", bad)
		}
	}
}

// 无可用条目 → 空串（v1 语义：调用方跳过 sub-reload）。
func TestBuildAssEmpty(t *testing.T) {
	if BuildAss(nil, 60_000, AssOptions{}) != "" {
		t.Error("nil 应返回空串")
	}
	if BuildAss([]Item{{ID: "x", TimeMs: 0, Text: "   "}}, 60_000, AssOptions{}) != "" {
		t.Error("空白文本应返回空串")
	}
}

// WriteAss：文件名净化（v1 sanitize 语义）+ 同 key 原地重写 + keep 修剪。
func TestWriteAssAndPrune(t *testing.T) {
	dir := t.TempDir()
	p1, err := WriteAss(dir, "剧集/第1集?1", "文本甲", 2)
	if err != nil {
		t.Fatal(err)
	}
	// v1 净化只保留 ASCII 安全字符：CJK/斜杠/问号 → _，strip 后 "1_1"
	if !strings.HasSuffix(p1, "1_1.ass") {
		t.Errorf("净化名 = %s", p1)
	}
	// tmpfs mtime 粒度可到 ms 级，拉 20ms 保剪枝确定性
	time.Sleep(20 * time.Millisecond)
	if _, err := WriteAss(dir, "drama-2", "文本乙", 2); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	p3, err := WriteAss(dir, "drama-3", "文本丙", 2)
	if err != nil {
		t.Fatal(err)
	}
	ents := listAss(t, dir)
	if len(ents) != 2 {
		t.Fatalf("keep=2 应剩 2 个 .ass: %v", ents)
	}
	if !has(ents, "drama-3.ass") || !has(ents, "drama-2.ass") || has(ents, "1_1.ass") {
		t.Errorf("剪枝应保留最新两个（drama-2/drama-3）: %v", ents)
	}
	// 原地重写：同 key 覆盖内容不新增文件
	data, err := os.ReadFile(p3)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "文本丙" {
		t.Errorf("首写内容 = %q", data)
	}
	if _, err := WriteAss(dir, "drama-3", "文本丙更新", 2); err != nil {
		t.Fatal(err)
	}
	ents = listAss(t, dir)
	if len(ents) != 2 {
		t.Fatalf("同 key 重写不该增文件: %v", ents)
	}
	data, _ = os.ReadFile(p3)
	if string(data) != "文本丙更新" {
		t.Errorf("重写内容 = %q", data)
	}
}

func listAss(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".ass") {
			out = append(out, e.Name())
		}
	}
	return out
}

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
