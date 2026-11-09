package danmaku

import (
	"math"

	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// 观感参数：与 v1 zhenguojian/app/zhenguojian/danmaku.py 顶部常量对齐。
const (
	DefaultWidth  = 1920 // v1 DEFAULT_SIZE
	DefaultHeight = 1080
	FontRatio     = 0.045 // 字号 ≈ 画布短边 4.5%
	AreaRatio     = 0.5   // 弹幕只占画面上半部分
	MaxLanes      = 12    // 轨道上限
	ScrollSeconds = 8.0   // 滚动全程时长
	LaneGap       = 0.25  // 同轨追尾保护间隔（秒）
	MaxRunes      = 60    // 单条最大字符数，超出截断加省略号
	// TopHoldSeconds 顶部/底部持留默认时长。v1 线上没有持留弹幕，
	// 取官方客户端惯例 4s；Item.DurationMs 可逐条覆盖。
	TopHoldSeconds = 4.0
)

// AssOptions 一次构建的画布与样式参数。零值字段走 v1 默认。
type AssOptions struct {
	Width, Height int     // PlayResX/Y（对应 v1 size=(width,height)）
	Opacity       float64 // 0-1 不透明度；ASS alpha 反向（00 不透明 FF 全透明）
	FontName      string  // 默认 "Microsoft YaHei"（v1 样式行）
	FontRatio     float64 // 默认 0.045
	AreaRatio     float64 // 默认 0.5
	MaxLanes      int     // 默认 12
	ScrollSeconds float64 // 默认 8.0
	LaneGap       float64 // 默认 0.25
	TopHoldSecond float64 // 默认 4.0
}

// defaultAss 归一化 AssOptions，把零值填成 v1 常量。
func (o AssOptions) normalize() AssOptions {
	if o.Width <= 0 {
		o.Width = DefaultWidth
	}
	if o.Height <= 0 {
		o.Height = DefaultHeight
	}
	if o.Opacity <= 0 || o.Opacity > 1 {
		// v1: opacity = max(0.05, min(1.0, opacity or 1.0))，0/未设按 1.0
		if o.Opacity == 0 {
			o.Opacity = 1.0
		}
		o.Opacity = max(0.05, min(1.0, o.Opacity))
	}
	if o.FontName == "" {
		o.FontName = "Microsoft YaHei"
	}
	if o.FontRatio <= 0 {
		o.FontRatio = FontRatio
	}
	if o.AreaRatio <= 0 {
		o.AreaRatio = AreaRatio
	}
	if o.MaxLanes <= 0 {
		o.MaxLanes = MaxLanes
	}
	if o.ScrollSeconds <= 0 {
		o.ScrollSeconds = ScrollSeconds
	}
	if o.LaneGap <= 0 {
		o.LaneGap = LaneGap
	}
	if o.TopHoldSecond <= 0 {
		o.TopHoldSecond = TopHoldSeconds
	}
	return o
}

// assHeaderFmt 与 v1 ASS_HEADER 逐字一致（含 Style 行占位符顺序：
// fontsize、主色/副色/描边/阴影的 alpha 段）。
const assHeaderFmt = `[Script Info]
ScriptType: v4.00+
PlayResX: %d
PlayResY: %d
WrapStyle: 2
ScaledBorderAndShadow: yes

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Danmaku,%s,%d,&H%sFFFFFF,&H%sFFFFFF,&H%s000000,&H%s000000,0,0,0,0,100,100,0,0,1,2,1,7,0,0,0,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
`

// BuildAss 把弹幕列表编译成完整 ASS 文本；没有可用条目返回空串
// （v1 build_ass 同语义，调用方据此跳过 sub-reload）。
//
// durationMs 是整集时长：start 超出 durationMs+5s 的条目丢弃（脏数据防线，
// v1 同款）。轨道模型照抄 v1：共用一条 Danmaku 样式（Alignment 7 顶左），
// 滚动位置烘进 \move 坐标；轨道占用秒数 =
// start + ScrollSeconds*textW/(W+textW) + LaneGap，全轨占用则丢弃该条
// （和官方客户端一样不刷）。顶部/底部持留是 v1 超集：不写 \move，
// 用 \an8 / \an2 + MarginV 定位，持留 TopHoldSecond 或逐条 DurationMs。
func BuildAss(items []Item, durationMs int64, opts AssOptions) string {
	o := opts.normalize()
	alpha := alphaHex(o.Opacity)
	font := max(24, int(float64(min(o.Width, o.Height))*o.FontRatio))
	line := int(float64(font) * 1.25)
	lanes := min(o.MaxLanes, max(6, int(float64(o.Height)*o.AreaRatio)/line))
	topMargin := max(12, int(float64(font)*0.6))
	laneFree := make([]float64, lanes)

	limit := max(0.0, float64(durationMs)/1000.0)
	var rows []string
	sorted := sortByTime(items)
	for _, it := range sorted {
		text := cleanText(it.Text)
		if text == "" {
			continue
		}
		start := float64(it.TimeMs) / 1000.0
		if start < 0 || start > limit+5.0 {
			continue
		}
		size := it.Size
		if size <= 0 {
			size = font
		}
		mode := it.Mode
		if mode == 0 {
			mode = ModeScroll
		}
		tw := textWidth(text, size)
		if f := float64(o.Width) * 0.9; tw > f {
			tw = f
		}
		lane := firstFreeLane(laneFree, start)
		if lane < 0 {
			continue // 全轨占用：丢弃（v1 语义）
		}
		if mode == ModeTop || mode == ModeBottom {
			laneFree[lane] = start + max(o.TopHoldSecond, float64(it.DurationMs)/1000.0)
		} else {
			laneFree[lane] = start + o.ScrollSeconds*tw/(float64(o.Width)+tw) + o.LaneGap
		}
		top := topMargin + lane*line
		end := start
		switch mode {
		case ModeTop, ModeBottom:
			hold := o.TopHoldSecond
			if it.DurationMs > 0 {
				hold = float64(it.DurationMs) / 1000.0
			}
			end = start + hold
		default:
			end = start + o.ScrollSeconds
		}
		an := ""
		marginV := 0
		switch mode {
		case ModeTop:
			// {\an8} 顶中对齐：MarginV = 距顶（与滚动行同一条轨道线）
			an = `{\an8}`
			marginV = top
		case ModeBottom:
			// {\an2} 底中对齐：MarginV = 距底，自下而上叠轨
			an = `{\an2}`
			marginV = top
		}
		overrides := an
		if mode == ModeScroll {
			// v1: {\move(W+40,top,-textW-40,top,0,8000)} —— t 参数是移动时长 ms
			overrides = fmt.Sprintf(`{\move(%d,%d,%d,%d,0,%d)}`,
				o.Width+40, top, -int(tw)-40, top, int(o.ScrollSeconds*1000))
		}
		overrides += styleOverrides(o, it, alpha, size, font)
		var b strings.Builder
		b.WriteString("Dialogue: 0,")
		b.WriteString(stamp(start))
		b.WriteString(",")
		b.WriteString(stamp(end))
		b.WriteString(",Danmaku,,0,0,")
		b.WriteString(strconv.Itoa(marginV))
		b.WriteString(",,")
		b.WriteString(overrides)
		b.WriteString(text)
		b.WriteString("\n")
		rows = append(rows, b.String())
	}
	if len(rows) == 0 {
		return ""
	}
	header := fmt.Sprintf(assHeaderFmt, o.Width, o.Height, o.FontName, font,
		alpha, alpha, alpha, alpha)
	return header + strings.Join(rows, "")
}

// styleOverrides 逐条颜色/字号覆盖（v1 只有样式默认白，此为定案扩展）：
// "#RRGGBB" → {\c&H{alpha}BBGGRR&}；字号偏离默认时追加 \fs。非法颜色回退
// 样式白（不写 \c），与 v1 观感一致。
func styleOverrides(o AssOptions, it Item, alpha string, size, defaultFont int) string {
	var s strings.Builder
	if col, ok := hexToBGR(it.Color); ok {
		s.WriteString(`{\c&H` + alpha + col + `&}`)
	}
	if size != defaultFont {
		fmt.Fprintf(&s, `{\fs%d}`, size)
	}
	return s.String()
}

// firstFreeLane 返回第一条 free<=t 的轨道，占用则 -1（v1 next(...) 语义）。
func firstFreeLane(laneFree []float64, t float64) int {
	for i, free := range laneFree {
		if free <= t {
			return i
		}
	}
	return -1
}

// alphaHex 不透明度 → ASS alpha 十六段（v1：round((1-opacity)*255)）。
func alphaHex(opacity float64) string {
	return fmt.Sprintf("%02X", int(math.Round((1.0-opacity)*255)))
}

// stamp 秒 → "H:MM:SS.cc"（v1 _stamp：厘秒四舍五入）。
func stamp(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	centis := int(math.Round(seconds * 100))
	h := centis / 360000
	centis -= h * 360000
	m := centis / 6000
	centis -= m * 6000
	whole := centis / 100
	frac := centis - whole*100
	return fmt.Sprintf("%d:%02d:%02d.%02d", h, m, whole, frac)
}

// cleanText 平铺换行、全角替换花括号/反斜杠、MAX_RUNES 截断（逐字照 v1
// _clean，包括 （ ） ＼ 的全角替换）。
func cleanText(text string) string {
	text = strings.ReplaceAll(text, "\r", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.TrimSpace(text)
	text = strings.ReplaceAll(text, "{", "（")
	text = strings.ReplaceAll(text, "}", "）")
	text = strings.ReplaceAll(text, "\\", "＼")
	if utf8.RuneCountInString(text) > MaxRunes {
		runes := []rune(text)
		text = string(runes[:MaxRunes]) + "…"
	}
	return text
}

// textWidth 估滚动宽度：CJK 按全宽、其余 0.55 半宽（v1 _width），单位 px。
func textWidth(text string, size int) float64 {
	total := 0.0
	for _, r := range text {
		if r > 0x2E80 {
			total += float64(size)
		} else {
			total += float64(size) * 0.55
		}
	}
	return total
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// hexToBGR "#RRGGBB" → "BBGGRR"（大写）。非法格式 ok=false。
func hexToBGR(c string) (string, bool) {
	c = strings.TrimSpace(c)
	if !hexColor.MatchString(c) {
		return "", false
	}
	rr := c[1:3]
	gg := c[3:5]
	bb := c[5:7]
	return strings.ToUpper(bb + gg + rr), true
}

// WriteAss 原子写入 ASS 文本（v1 write_ass 语义：同 key 同名文件供
// sub-reload 原地重读；目录里按 mtime 保留最近 keep 个，多余删除）。
// dir 空则用 os.TempDir()/zhenguojian-danmaku；key 净化为文件名安全串。
func WriteAss(dir, key, text string, keep int) (string, error) {
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "zhenguojian-danmaku")
	}
	if keep <= 0 {
		keep = 6
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := sanitizeKey(key)
	path := filepath.Join(dir, name+".ass")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	prune(dir, keep)
	return path, nil
}

var unsafeKey = regexp.MustCompile(`[^0-9A-Za-z._-]+`)

// sanitizeKey 照抄 v1 write_ass：非法字符→_，取尾 60 字符，去头尾 _，
// 空则 "episode"。
func sanitizeKey(key string) string {
	safe := unsafeKey.ReplaceAllString(key, "_")
	if len(safe) > 60 {
		safe = safe[len(safe)-60:]
	}
	safe = strings.Trim(safe, "_")
	if safe == "" {
		safe = "episode"
	}
	return safe
}

// prune 只保留 mtime 最近的 keep 个 .ass（v1 同语义，尽力而为不报错）。
func prune(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type assFile struct {
		path string
		mod  int64
	}
	var files []assFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".ass") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, assFile{filepath.Join(dir, e.Name()), info.ModTime().UnixNano()})
	}
	if len(files) <= keep {
		return
	}
	// 按 mtime 降序
	for i := 1; i < len(files); i++ {
		for j := i; j > 0 && files[j].mod > files[j-1].mod; j-- {
			files[j], files[j-1] = files[j-1], files[j]
		}
	}
	for _, f := range files[keep:] {
		_ = os.Remove(f.path)
	}
}

func sortByTime(items []Item) []Item {
	out := make([]Item, len(items))
	copy(out, items)
	// 插入排序（v1 items 上游基本有序，窗口合并量 <2 万条时开销可忽略）
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].TimeMs < out[j-1].TimeMs; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
