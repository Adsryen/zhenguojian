package core

import (
	"math"
	"strconv"
	"strings"
	"time"
)

var providerChinaTime = time.FixedZone("CST", 8*60*60)

func hongguoHeat(row map[string]any) string {
	hot := nestedMap(row, "hot_score_data")

	if score := firstNonEmpty(mapString(hot, "score"), mapString(row, "hot_score")); score != "" {
		value, err := strconv.ParseFloat(score, 64)
		if err == nil && value >= 0 && !math.IsInf(value, 0) && !math.IsNaN(value) {
			return formatHeat(value)
		}
	}
	return mapString(hot, "text")
}

// formatHeat 把热度数字缩写成「314.3万」这种短形式。

// 接口对有的剧只给数字（hot_score）、有的给现成的 text（"128.5万"）；数字那条
// 以前原样返回，于是详情页上一部剧写「128.5万」、下一部写「3104269」，看着像 bug。
func formatHeat(value float64) string {
	unit, suffix := 0.0, ""
	switch {
	case value >= 100000000:
		unit, suffix = 100000000, "亿"
	case value >= 10000:
		unit, suffix = 10000, "万"
	default:
		return strconv.FormatFloat(value, 'f', -1, 64)
	}
	text := strconv.FormatFloat(value/unit, 'f', 1, 64)
	return strings.TrimSuffix(text, ".0") + suffix
}

func hongguoOnlineDate(row map[string]any, now time.Time) string {
	if date := providerTimestampDate(mapString(row, "first_visible_time")); date != "" {
		return date
	}
	for _, value := range anyList(row["sub_title_list"]) {
		label := mapString(nestedMap(value), "content")
		day := now.In(providerChinaTime)
		switch label {
		case "今日上新":
			return day.Format("2006-01-02")
		case "昨日上新":
			return day.AddDate(0, 0, -1).Format("2006-01-02")
		default:
			if strings.HasSuffix(label, "上新") {
				date := normalizeDate(label)
				if _, err := time.Parse("2006-01-02", date); err == nil {
					return date
				}
			}
		}
	}
	return ""
}
