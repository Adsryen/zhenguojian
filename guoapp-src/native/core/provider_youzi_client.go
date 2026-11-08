package core

// 柚子（youzidj.com）站源客户端：访客会话与播放凭证。
// 该站页面为服务端渲染 HTML（无 JS 挑战），但 /media/ 下的 m3u8/mp4 受播放
// 凭证网关保护：裸 GET 一律 403（{"error":"播放凭证已过期，请重新开始播放"}）。
// 取流必须走浏览器同款三步（2026-11 实测）：
//  1. POST /api/account/bootstrap（体 {}，必须带 Origin，否则 403 请求来源无效）
//     返回访客身份并下发 Set-Cookie eden_account=<64hex>（Max-Age 一年）。
//  2. POST /api/playback/{videoUUID}?mode=mse（体 {}，带 eden_account Cookie +
//     Origin + Referer；无 Cookie 时 401 请刷新页面以恢复账户）。返回
//     {"url":"/media/{videoUUID}/{versionUUID}/master.m3u8","expires_at":unix}，
//     url 为约 16~24 小时有效的凭证化地址。
//  3. 带同一 Cookie GET 该 url 才拿得到播放列表。
// 会话 Cookie 与页面抓取相互独立：目录/详情/分集页不带 Cookie 也可 GET。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	sourceYouzi     = "youzi"
	youziUserAgent  = "Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36"
	youziCookieName = "eden_account"
	// youziPlayCookiePrefix 是 /api/playback 按视频下发的短期播放 Cookie 前缀。
	youziPlayCookiePrefix = "eden_play_"
	// eden_account 的 Max-Age 是一年，这里保守缓存 12 小时；401 会即时作废。
	youziSessionTTL = 12 * time.Hour
	// 播放凭证 expires_at 冗余 60 秒（与站方播放器脚本的复用判断一致）。
	youziTokenSkew = 60 * time.Second
	youziMaxDepth  = 3 // sourceID 最多段数：dramas/{slug}/season-1
)

var (
	// youziSiteBase 是包级站点根地址，httptest 用例会临时重指向本地服务。
	youziSiteBase = "https://youzidj.com"

	// youziSession 缓存访客会话 Cookie；按 base 隔离，站点被切换时自动失效。
	youziSession struct {
		mu      sync.Mutex
		base    string
		cookie  string
		expires time.Time
	}

	reYouziShortID    = regexp.MustCompile(`^[a-z0-9]{8}$`)
	reYouziUUIDID     = regexp.MustCompile(`^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$`)
	reYouziSlug       = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,96}$`)
	reYouziSeasonTail = regexp.MustCompile(`^season-([1-9][0-9]{0,3})$`)
	reYouziMediaVideo = regexp.MustCompile(`/media/([0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12})/`)
)

func youziSite() string {
	return strings.TrimRight(youziSiteBase, "/")
}

// errYouziNotFound 站内 404（越界页码/已下线作品），供上层区分"没有更多"与网络故障。
var errYouziNotFound = errors.New("柚子页面不存在")

// youziHostAllowed 校验待请求地址的站点根与当前配置的柚子站一致，
// 防止把会话 Cookie/播放凭证发往第三方主机（httptest 重定向后同样放行）。
func youziHostAllowed(rawURL string) bool {
	site, err := url.Parse(youziSite())
	if err != nil {
		return false
	}
	address, err := url.Parse(rawURL)
	if err != nil || address.Scheme != site.Scheme || address.Host != site.Host {
		return false
	}
	return true
}

// validYouziSourceID 校验详情 sourceID：
//   - "ai-adult/{8位短id|uuid}"、"ai-face-swap/{8位短id|uuid}"（单视频）
//   - "dramas/{slug}/season-{N}"（剧集季页）
func validYouziSourceID(sourceID string) bool {
	segments := strings.Split(sourceID, "/")
	if len(segments) < 2 || len(segments) > youziMaxDepth {
		return false
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	switch segments[0] {
	case "ai-adult", "ai-face-swap":
		return len(segments) == 2 && (reYouziShortID.MatchString(segments[1]) || reYouziUUIDID.MatchString(segments[1]))
	case "dramas":
		return len(segments) == 3 && reYouziSlug.MatchString(segments[1]) && reYouziSeasonTail.MatchString(segments[2])
	}
	return false
}

// youziDetailURL 拼详情/分集页地址（只由已校验的 sourceID/key 构造，不消费外部 URL）。
func youziDetailURL(sourceID string) string {
	return youziSite() + "/" + strings.Trim(sourceID, "/") + "/"
}

// youziFetchText GET 柚子页面文本，返回正文与最终地址。
func (d *Downloader) youziFetchText(ctx context.Context, address, referer string) (string, string, error) {
	if !youziHostAllowed(address) {
		return "", "", errors.New("柚子请求地址无效")
	}
	if referer == "" {
		referer = youziSite() + "/"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return "", "", err
	}
	request.Header.Set("User-Agent", youziUserAgent)
	request.Header.Set("Referer", referer)
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	response, err := d.doCatalogRequestWithTimeout(request, providerTimeout)
	if err != nil {
		return "", "", err
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, providerMaxBodyBytes+1))
	closeErr := response.Body.Close()
	if readErr != nil {
		return "", "", readErr
	}
	if closeErr != nil {
		return "", "", closeErr
	}
	if len(body) > providerMaxBodyBytes {
		return "", "", fmt.Errorf("柚子页面超过 %d 字节", providerMaxBodyBytes)
	}
	if response.StatusCode == http.StatusNotFound {
		return "", "", fmt.Errorf("%w：%s", errYouziNotFound, request.URL.Path)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", "", d.catalogResponseError(request, response, body)
	}
	if reason := catalogResponseBlockReason(response, body); reason != "" {
		return "", "", fmt.Errorf("柚子请求被拦截：%s", reason)
	}
	finalURL := address
	if response.Request != nil && response.Request.URL != nil {
		finalURL = response.Request.URL.String()
	}
	return string(body), finalURL, nil
}

// youziPostJSON 以 JSON 体 POST 站内 API（Origin 头是站方硬性校验项，缺失即 403）。
// cookie 可为空；返回响应正文、状态码与响应头。
func (d *Downloader) youziPostJSON(ctx context.Context, address, referer, cookie string) (string, int, http.Header, error) {
	if !youziHostAllowed(address) {
		return "", 0, nil, errors.New("柚子请求地址无效")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, strings.NewReader("{}"))
	if err != nil {
		return "", 0, nil, err
	}
	request.Header.Set("User-Agent", youziUserAgent)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Origin", youziSite())
	if referer != "" {
		request.Header.Set("Referer", referer)
	}
	if cookie != "" {
		request.Header.Set("Cookie", cookie)
	}
	response, err := d.doCatalogRequestWithTimeout(request, providerTimeout)
	if err != nil {
		return "", 0, nil, err
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	closeErr := response.Body.Close()
	if readErr != nil {
		return "", response.StatusCode, response.Header, readErr
	}
	if closeErr != nil {
		return "", response.StatusCode, response.Header, closeErr
	}
	return string(body), response.StatusCode, response.Header, nil
}

// youziBootstrapSession 获取（并缓存）访客会话 Cookie 头值 "eden_account=..."。
func (d *Downloader) youziBootstrapSession(ctx context.Context) (string, error) {
	site := youziSite()
	youziSession.mu.Lock()
	defer youziSession.mu.Unlock()
	if youziSession.base == site && youziSession.cookie != "" && time.Now().Before(youziSession.expires) {
		return youziSession.cookie, nil
	}
	body, status, header, err := d.youziPostJSON(ctx, site+"/api/account/bootstrap", site+"/", "")
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("柚子访客会话获取失败：%s", youziAPIError(body, status))
	}
	cookie := youziCookieFromResponse(header)
	if cookie == "" {
		return "", errors.New("柚子访客会话响应缺少凭证 Cookie")
	}
	youziSession.base = site
	youziSession.cookie = cookie
	youziSession.expires = time.Now().Add(youziSessionTTL)
	return cookie, nil
}

// youziForgetSession 作废缓存会话（收到 401 时调用，也供测试复位）。
func youziForgetSession() {
	youziSession.mu.Lock()
	defer youziSession.mu.Unlock()
	youziSession.base = ""
	youziSession.cookie = ""
	youziSession.expires = time.Time{}
}

// youziGrantCookie 提取 /api/playback 应答下发的播放凭证 Cookie（eden_play_<videoId>=…，
// Path=/media/<videoId>/，Max-Age 900）。m3u8 仅带 eden_account 会 403
// 「播放凭证已过期，请重新开始播放」，必须把它并入媒体请求 Cookie。
func youziGrantCookie(header http.Header) string {
	for _, line := range header.Values("Set-Cookie") {
		name, _, ok := strings.Cut(line, ";")
		if !ok {
			continue
		}
		key, val, _ := strings.Cut(name, "=")
		if !strings.HasPrefix(strings.TrimSpace(key), youziPlayCookiePrefix) || val == "" {
			continue
		}
		return strings.TrimSpace(key) + "=" + val
	}
	return ""
}

// youziCookieFromResponse 从 Set-Cookie 头提取 eden_account 值，返回完整 Cookie 头。
func youziCookieFromResponse(header http.Header) string {
	for _, line := range header.Values("Set-Cookie") {
		name, value, _ := strings.Cut(line, "=")
		if !strings.EqualFold(strings.TrimSpace(name), youziCookieName) {
			continue
		}
		value, _, _ = strings.Cut(value, ";")
		value = strings.Trim(strings.TrimSpace(value), `"`)
		if value != "" {
			return youziCookieName + "=" + value
		}
	}
	return ""
}

// youziAPIError 提取站方 JSON 错误里的 error 文案，附带状态码兜底。
func youziAPIError(body string, status int) string {
	var envelope struct {
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(body), &envelope) == nil && strings.TrimSpace(envelope.Error) != "" {
		return fmt.Sprintf("%s（HTTP %d）", strings.TrimSpace(envelope.Error), status)
	}
	return fmt.Sprintf("HTTP %d", status)
}

// youziPlaybackToken 换取播放地址；会话失效（401）时自动重建一次。
func (d *Downloader) youziPlaybackToken(ctx context.Context, videoID, referer string) (string, time.Time, string, error) {
	if !reYouziUUIDID.MatchString(videoID) {
		return "", time.Time{}, "", errors.New("柚子播放目标无效")
	}
	site := youziSite()
	address := site + "/api/playback/" + url.PathEscape(videoID) + "?mode=mse"
	cookie, err := d.youziBootstrapSession(ctx)
	if err != nil {
		return "", time.Time{}, "", err
	}
	body, status, header, err := d.youziPostJSON(ctx, address, referer, cookie)
	if err != nil {
		return "", time.Time{}, "", err
	}
	if status == http.StatusUnauthorized {
		youziForgetSession()
		if cookie, err = d.youziBootstrapSession(ctx); err != nil {
			return "", time.Time{}, "", err
		}
		body, status, header, err = d.youziPostJSON(ctx, address, referer, cookie)
		if err != nil {
			return "", time.Time{}, "", err
		}
	}
	if status != http.StatusOK {
		return "", time.Time{}, "", fmt.Errorf("柚子播放凭证获取失败：%s", youziAPIError(body, status))
	}
	var payload struct {
		URL       string `json:"url"`
		ExpiresAt int64  `json:"expires_at"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return "", time.Time{}, "", fmt.Errorf("柚子播放凭证解析失败：%w", err)
	}
	mediaURL := strings.TrimSpace(payload.URL)
	if mediaURL == "" {
		return "", time.Time{}, "", errors.New("柚子播放凭证未返回地址")
	}
	mediaURL = youziResolveURL(site+"/", mediaURL)
	if !youziHostAllowed(mediaURL) || !isProviderHTTPMediaURL(mediaURL) {
		return "", time.Time{}, "", errors.New("柚子播放凭证地址无效")
	}
	expires := time.Now().Add(youziSessionTTL)
	if payload.ExpiresAt > 0 {
		expires = time.Unix(payload.ExpiresAt, 0).Add(-youziTokenSkew)
	}
	return mediaURL, expires, youziGrantCookie(header), nil
}
