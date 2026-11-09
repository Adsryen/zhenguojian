// Package danmaku 是 v2 播放器的弹幕核心（从 spike/danmaku 毕业升级）：
//
//   - Item            弹幕条目，字段对齐核心 danmaku 动作返回
//     （id/text/timeMs；color/mode 是渲染扩展位，上游当前不下发，零值即
//     默认观感，保持 v1 zhenguojian/app/zhenguojian/danmaku.py 的行为）。
//   - BuildAss        []Item → ASS 字幕文本。观感参数 1:1 取自 v1
//     build_ass：动态 PlayRes、字号=短边×FONT_RATIO、只占上半区、
//     轨道占用模型、\move 滚动；顶部/底部持留与逐条颜色按 design.md
//     「弹幕走 ASS 字幕轨」定案补齐（v1 线上只有滚动，属超集）。
//   - Fetcher         在线懒抓取：按 v1 main.py 循环——30s 窗口对齐、
//     预取下一窗、按 id 去重、抽样重试（duration 加 bust 绕缓存）、
//     失败退避 10s/30s/三次放弃、MIN_GAP 限速（接口对连续请求抽样）。
//   - Scheduler       视频时间轴 → 待显示队列（二分窗口，seek 可重定位）。
//
// 渲染链路：Fetcher 攒齐窗口 → BuildAss 全量重建 → WriteAss 原子落盘
// （同集同名，供 mpv sub-reload 增量重读）→ Player.sub-add/sub-reload。
package danmaku

import "strconv"

// Mode 弹幕显示模式。对齐 bilibili 惯例编号（v1 协议曾下发 mode 字段，
// 现核心 hongguo 链路不下发；缺省按滚动处理，与 v1 观感一致）。
type Mode int

const (
	ModeScroll Mode = 1 // 从右向左滚动（默认）
	ModeTop    Mode = 4 // 顶部持留
	ModeBottom Mode = 5 // 底部持留
)

// Item 一条弹幕。
type Item struct {
	ID     string `json:"id"`
	TimeMs int64  `json:"timeMs"` // 出现时刻（相对视频时间轴）
	Text   string `json:"text"`
	// Color "#RRGGBB"，空=样式默认白。
	Color string `json:"color,omitempty"`
	// Mode 0 按 ModeScroll 处理。
	Mode Mode `json:"mode,omitempty"`
	// DurationMs 顶部/底部持留时长，<=0 用 AssOptions.TopHoldSeconds。
	DurationMs int64 `json:"durationMs,omitempty"`
	// Size 字号 px，<=0 按画布短边×FontRatio 计算。
	Size int `json:"size,omitempty"`
}

// FromMap 把核心 danmaku 动作返回的单条 item（JSON 解出 map[string]any）
// 转成 Item；timeMs 兼容 float64/string 两种 JSON 形态。
func FromMap(m map[string]any) Item {
	it := Item{}
	if v, ok := m["id"].(string); ok {
		it.ID = v
	} else if v, ok := m["id"].(float64); ok {
		it.ID = strconv.FormatInt(int64(v), 10)
	}
	it.TimeMs = asInt64(m["timeMs"])
	if t, ok := m["text"].(string); ok {
		it.Text = t
	}
	if c, ok := m["color"].(string); ok {
		it.Color = c
	}
	if mode := asInt64(m["mode"]); mode != 0 {
		it.Mode = Mode(mode)
	}
	it.DurationMs = asInt64(m["durationMs"])
	if s := asInt64(m["size"]); s > 0 {
		it.Size = int(s)
	}
	return it
}

// FromMaps 批量转换并丢弃无文本/非法时刻的条目（v1 fetch_window 同语义：
// 按 timeMs 升序稳定排序交给调用方做）。
func FromMaps(rows []map[string]any) []Item {
	out := make([]Item, 0, len(rows))
	for _, m := range rows {
		it := FromMap(m)
		if it.Text == "" {
			continue
		}
		out = append(out, it)
	}
	return out
}

func asInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case string:
		parsed, err := strconv.ParseInt(n, 10, 64)
		if err != nil {
			return 0
		}
		return parsed
	}
	return 0
}
