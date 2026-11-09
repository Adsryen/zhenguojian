package watchstore

// Port of v1 watchstore.py:31-59 (_SEASON_RE / _CN_DIGITS / _chinese_number /
// season_number).  See PORTING.md for the two documented divergences:
// Python's re \s is Unicode-aware; the Go class adds U+00A0/U+3000 (the two
// spacings realistic in titles).  Python's NFKC fold is replaced by an
// explicit fullwidth-digit fold — the regex class admits only [0-9０-９] plus
// Chinese numerals, so fullwidth digits are the only NFKC-reachable form.

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// seasonRe mirrors v1 _SEASON_RE: r"第\s*([0-9０-９零〇一二两兩三四五六七八九十百]+)\s*[季部]"
// Go RE2's \s is ASCII-only ([\t\n\f\r ]), so the class explicitly adds the
// Unicode whitespaces Python's re \s would match that plausibly appear in
// titles: \v (0x0B), NBSP (U+00A0), ideographic space (U+3000).
var seasonRe = regexp.MustCompile("第[\\s\\v\u00A0\u3000]*([0-9０-９零〇一二两兩三四五六七八九十百]+)[\\s\\v\u00A0\u3000]*[季部]")

// cnDigits mirrors v1 _CN_DIGITS (十/百 are handled as multipliers below,
// exactly like the v1 elif chain).
var cnDigits = map[rune]int{
	'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '兩': 2,
	'三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9,
}

// foldFullwidthDigits replaces unicodedata.normalize("NFKC", …) for the
// characters the season regex can actually capture (０-９ → 0-9).
func foldFullwidthDigits(s string) string {
	needFold := false
	for _, r := range s {
		if r >= '０' && r <= '９' {
			needFold = true
			break
		}
	}
	if !needFold {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r >= '０' && r <= '９' {
			b.WriteRune(rune('0' + (r - '０')))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func asciiDigitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// chineseNumber ports v1 _chinese_number (watchstore.py:36-50): NFKC+strip,
// all-digit text → int; otherwise scan 零〇一二两兩…十…百 accumulating
// section+number.  A digit string beyond int64 (only reachable via very long
// fullwidth/ASCII digits) saturates instead of using Python bignums — the
// caller clamps to 1..200 anyway, and both languages then yield season 1.
func chineseNumber(text string) int {
	text = strings.TrimSpace(foldFullwidthDigits(text))
	if asciiDigitsOnly(text) {
		n, err := strconv.Atoi(text)
		if err != nil {
			return math.MaxInt32 // overflow: clamped to 1 by SeasonNumber
		}
		return n
	}
	section, number := 0, 0
	for _, ch := range text {
		if d, ok := cnDigits[ch]; ok {
			number = d
			continue
		}
		switch ch {
		case '十':
			mult := 1
			if number != 0 {
				mult = number
			}
			section += mult * 10
			number = 0
		case '百':
			mult := 1
			if number != 0 {
				mult = number
			}
			section += mult * 100
			number = 0
		}
	}
	return section + number
}

// SeasonNumber ports v1 season_number (watchstore.py:53-59): pull the N out of
// 「第N季 / 第N部」; no match, or out of 1..200, means season 1.
func SeasonNumber(title string) int {
	m := seasonRe.FindStringSubmatch(title)
	if m == nil {
		return 1
	}
	value := chineseNumber(m[1])
	if value < 1 || value > 200 {
		return 1
	}
	return value
}
