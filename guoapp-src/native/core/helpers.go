package core

import (
	"net/url"

	"regexp"

	"strings"
)

var (
	badNameChars = regexp.MustCompile(`[\\/\?%\*:|"<>\x00-\x1f]+`)
	spaceChars   = regexp.MustCompile(`\s+`)
	rePublicURL  = regexp.MustCompile(`(?i)(?:https?|socks5h?)://[^\s"'<>]+`)
)

func publicError(err error) error {
	if err == nil {
		return nil
	}
	return redactedError{cause: err}
}

type redactedError struct{ cause error }

func (err redactedError) Error() string {
	return friendlyError(redactErrorString(err.cause.Error()))
}
func (err redactedError) Unwrap() error { return err.cause }

// friendlyErrors maps transport/provider wording to something a user can act
// on.  The raw strings ("context deadline exceeded", "dial tcp ...") used to
// be shown verbatim in the UI.
var friendlyErrors = []struct{ needle, text string }{
	{"context deadline exceeded", "请求超时，请检查网络后重试"},
	{"context canceled", "请求已取消"},
	{"i/o timeout", "网络连接超时，请检查网络或代理后重试"},
	{"tls handshake timeout", "安全连接超时，请检查网络或代理后重试"},
	{"connection reset by peer", "连接被服务器断开，请重试"},
	{"connection refused", "服务器拒绝连接，请稍后重试"},
	{"no such host", "找不到服务器，请检查网络或代理"},
	{"dial tcp", "无法连接服务器，请检查网络或代理后重试"},
	{"huangdou response too short", "站源返回的数据不完整，请稍后重试"},
	{"huangdou response exceeds", "站源返回的数据过大，请稍后重试"},
	{"response exceeds", "站源返回的数据过大，请稍后重试"},
	{"huangdou ciphertext is not block aligned", "站源返回的数据格式异常，请稍后重试"},
	{"hongguo detail is empty", "站源没有返回剧集详情，请稍后重试"},
	{"empty hongguo sourceid", "站源返回的剧集标识为空，请刷新后重试"},
	{"empty huangguo-video sourceid", "站源返回的剧集标识为空，请刷新后重试"},
	{"empty huangguoai sourceid", "站源返回的剧集标识为空，请刷新后重试"},
	{"unsupported provider source", "不支持这个站源"},
}

func friendlyError(message string) string {
	lower := strings.ToLower(message)
	for _, entry := range friendlyErrors {
		if strings.Contains(lower, entry.needle) {
			return entry.text
		}
	}
	return message
}

func redactErrorString(s string) string {
	return rePublicURL.ReplaceAllStringFunc(s, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			return "[无效 URL 已隐藏]"
		}
		if u.Host == "" {
			return raw
		}
		if u.User != nil {
			u.User = url.User("redacted")
		}
		if u.RawQuery != "" {
			u.RawQuery = "[redacted]"
		}
		if u.Fragment != "" {
			u.Fragment = "redacted"
		}
		return u.String()
	})
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
