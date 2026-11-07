package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const hongguoAppBaseURL = "https://api5-normal-sinfonlineb.fqnovel.com"

const hongguoAppUserAgent = "com.phoenix.read/73532 (Linux; U; Android 16; zh_CN; 25053RT47C; Build/BP2A.250605.031.A3; Cronet/TTNetVersion:04657795 2026-01-23 QuicVersion:c67e9834 2025-09-08)"

// 风控层会以 HTTP 200 + 空 body 静默拒绝一部分请求；下载任务要把这种拒绝
// 当成临时错误多试几轮，否则整集会被误判为失败。
var errHongguoAppEmptyBody = errors.New("红果 App 接口未返回有效数据")

// 取流失败时按线路轮换重试：主机由外层指定，内层不再叠加自己的重试。
type hongguoAppHostKey struct{}
type hongguoAppSingleAttemptKey struct{}
type hongguoAppFreshConnKey struct{}

func hongguoAppWithHost(ctx context.Context, host string) context.Context {
	return context.WithValue(ctx, hongguoAppHostKey{}, host)
}

func hongguoAppWithSingleAttempt(ctx context.Context) context.Context {
	return context.WithValue(ctx, hongguoAppSingleAttemptKey{}, true)
}

func hongguoAppWithFreshConn(ctx context.Context) context.Context {
	return context.WithValue(ctx, hongguoAppFreshConnKey{}, true)
}

// 设备注册下发的四条 cookie，顺序与参考客户端一致。
var hongguoDeviceCookieOrder = []string{"install_id", "ttreq", "store-region", "store-region-src"}

func hongguoDeviceCookieHeader(cookies map[string]string) string {
	parts := make([]string, 0, len(cookies))
	for _, name := range hongguoDeviceCookieOrder {
		if value := strings.TrimSpace(cookies[name]); value != "" {
			parts = append(parts, name+"="+value)
		}
	}
	return strings.Join(parts, "; ")
}

const hongguoAppDownloadEmptyAttempts = 6

// 红果 App 网关对突发请求会间歇性回「200 + 空体」，串行化并保持最小间隔后
// 同样的请求可稳定返回。窗口取 600ms，与上游客户端一致。
const hongguoAppMinInterval = 600 * time.Millisecond

var (
	hongguoAppThrottleMu  sync.Mutex
	hongguoAppLastRequest time.Time
)

func hongguoAppThrottle(ctx context.Context) error {
	hongguoAppThrottleMu.Lock()
	wait := hongguoAppMinInterval - time.Since(hongguoAppLastRequest)
	hongguoAppThrottleMu.Unlock()
	if wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	hongguoAppThrottleMu.Lock()
	hongguoAppLastRequest = time.Now()
	hongguoAppThrottleMu.Unlock()
	return nil
}

type hongguoCatalogCursor struct {
	Offset        int       `json:"offset"`
	SessionID     string    `json:"sessionId,omitempty"`
	LastID        string    `json:"lastId,omitempty"`
	PageSignature string    `json:"pageSignature,omitempty"`
	Initialized   bool      `json:"initialized"`
	Exhausted     bool      `json:"exhausted"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type hongguoCatalogState struct {
	Version   int    `json:"version"`
	DeviceID  string `json:"deviceId"`
	InstallID string `json:"installId"`
	// 红果播放接口只认已注册设备；DeviceToken 是注册时下发的凭证，一并持久化。
	DeviceToken string `json:"deviceToken,omitempty"`
	// 设备注册响应下发的四条 cookie（install_id / ttreq / store-region /
	// store-region-src）。真机取流会带上它们；丢掉等于未登录设备裸奔。
	DeviceCookies map[string]string `json:"deviceCookies,omitempty"`
	Registered    bool              `json:"registered,omitempty"`
	// 注册时间（Unix 秒）。播放被拒时用它判断是否值得换一台新设备。
	RegisteredAt int64                           `json:"registeredAt,omitempty"`
	Feeds        map[string]hongguoCatalogCursor `json:"feeds"`
}

type hongguoAppClient struct {
	mu             sync.Mutex
	identityMu     sync.Mutex
	catalogMu      sync.Mutex
	baseURL        string
	state          hongguoCatalogState
	details        map[string]hongguoDetailEntry
	pending        map[string]*hongguoDetailCall
	searches       map[string]hongguoSearchEntry
	searchPending  map[string]*hongguoSearchCall
	suggestions    map[string]hongguoSuggestionEntry
	suggestPending map[string]*hongguoSuggestionCall
	danmaku        map[string]hongguoDanmakuCacheEntry
	danmakuPending map[string]*hongguoDanmakuCall
	// 批量预取回来的播放信息：video_id → 片源，下载任务直接命中，避免逐集
	// 请求取流接口。prefetchMu 保证同一时间只有一批预取在发请求。
	mediaCache map[string]hongguoMediaCacheEntry
	appWorks   map[string]bool
	prefetchMu sync.Mutex
}

func (downloader *Downloader) hongguoClient() *hongguoAppClient {
	downloader.hongguoOnce.Do(func() {
		downloader.hongguo = &hongguoAppClient{
			baseURL: hongguoAppBaseURL,
			state:   hongguoCatalogState{Version: 1, DeviceID: newHongguoDeviceID(), InstallID: newHongguoDeviceID(), Feeds: map[string]hongguoCatalogCursor{}},
			details: map[string]hongguoDetailEntry{}, pending: map[string]*hongguoDetailCall{},
			searches: map[string]hongguoSearchEntry{}, searchPending: map[string]*hongguoSearchCall{},
			suggestions: map[string]hongguoSuggestionEntry{}, suggestPending: map[string]*hongguoSuggestionCall{},
			mediaCache: map[string]hongguoMediaCacheEntry{}, appWorks: map[string]bool{},
		}
	})
	return downloader.hongguo
}

func cloneHongguoCatalogState(state *hongguoCatalogState) *hongguoCatalogState {
	if state == nil {
		return nil
	}
	cloned := *state
	cloned.Feeds = make(map[string]hongguoCatalogCursor, len(state.Feeds))
	for name, cursor := range state.Feeds {
		cloned.Feeds[name] = cursor
	}
	cloned.DeviceCookies = make(map[string]string, len(state.DeviceCookies))
	for name, value := range state.DeviceCookies {
		cloned.DeviceCookies[name] = value
	}
	return &cloned
}

func (downloader *Downloader) restoreHongguoCatalog(state *hongguoCatalogState) {
	if state == nil || state.Version != 1 || !hongguoNumericID.MatchString(state.DeviceID) || !hongguoNumericID.MatchString(state.InstallID) {
		return
	}
	cloned := cloneHongguoCatalogState(state)
	if !hongguoNumericID.MatchString(cloned.DeviceID) || !hongguoNumericID.MatchString(cloned.InstallID) {
		cloned.Registered = false
	}
	for name, cursor := range cloned.Feeds {
		known := false
		for _, genre := range hongguoAppGenres {
			known = known || name == genre.key || name == "category:"+genre.key
		}
		if !known || !cursor.Initialized || cursor.Offset < 0 || cursor.Offset > 1_000_000 || len(cursor.SessionID) > 4096 || len(cursor.LastID) > 256 || len(cursor.PageSignature) > 64 {
			delete(cloned.Feeds, name)
		}
	}
	client := downloader.hongguoClient()
	client.catalogMu.Lock()
	defer client.catalogMu.Unlock()
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.state.Feeds) == 0 {
		client.state = *cloned
	}
}

func (downloader *Downloader) hongguoCatalogSnapshot() *hongguoCatalogState {
	client := downloader.hongguoClient()
	client.mu.Lock()
	defer client.mu.Unlock()
	return cloneHongguoCatalogState(&client.state)
}

func hongguoCatalogInitialized(state *hongguoCatalogState) bool {
	if state != nil && state.Version == 1 {
		for _, cursor := range state.Feeds {
			if cursor.Initialized {
				return true
			}
		}
	}
	return false
}

func hongguoCatalogHasMore(state *hongguoCatalogState) bool {
	for _, genre := range hongguoAppGenres {
		if state == nil || !state.Feeds[genre.key].Exhausted {
			return true
		}
	}
	return false
}

func isHongguoPlaybackPath(path string) bool {
	switch path {
	case hongguoAppPlaybackPath, "/novel/player/multi_video_model/v1/", "/novel/player/video_model/v1/":
		return true
	}
	return false
}

func (downloader *Downloader) hongguoAppRequest(ctx context.Context, method, path string, extra url.Values, payload any) (map[string]any, error) {
	// 只有播放类接口需要正式注册设备；目录/详情用随机设备即可，
	// 这里避免为每个请求都触发一次注册。
	if isHongguoPlaybackPath(path) {
		_ = downloader.ensureHongguoIdentity(ctx)
	}
	client := downloader.hongguoClient()
	client.mu.Lock()
	base, deviceID, installID := client.baseURL, client.state.DeviceID, client.state.InstallID
	cookieHeader := hongguoDeviceCookieHeader(client.state.DeviceCookies)
	client.mu.Unlock()
	if host, _ := ctx.Value(hongguoAppHostKey{}).(string); host != "" {
		base = "https://" + host
	}
	query := url.Values{
		"aid": {"8662"}, "app_name": {"novelread"}, "version_code": {hongguoAppVersionCode}, "version_name": {hongguoAppVersionName},
		"manifest_version_code": {hongguoAppVersionCode}, "update_version_code": {hongguoAppVersionCode}, "channel": {"update_64"},
		"device_platform": {"android"}, "os": {"android"}, "ssmix": {"a"}, "device_type": {"25053RT47C"},
		"device_brand": {"Redmi"}, "language": {"zh"}, "os_api": {"36"}, "os_version": {"16"},
		"resolution": {"1280*2772"}, "dpi": {"520"}, "ac": {"wifi"}, "device_id": {deviceID}, "iid": {installID},
		// 与 4.5.0 的请求保持一致：不带 host_abi / dragon_device_type 这类额外画像字段。
	}
	for key, values := range extra {
		query[key] = append([]string(nil), values...)
	}
	var body []byte
	var err error
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			return nil, err
		}
	}
	// 空体是网关的间歇性风控表现，单次失败不代表源不可用，至少重试 4 次。
	attempts := downloader.cfg.Retries
	if attempts < 4 {
		attempts = 4
	}
	if attempts > 5 {
		attempts = 5
	}
	// 外层按线路轮换时只发一次，避免内外两层重试叠加把请求量翻几倍。
	if single, _ := ctx.Value(hongguoAppSingleAttemptKey{}).(bool); single {
		attempts = 1
	}
	freshConn, _ := ctx.Value(hongguoAppFreshConnKey{}).(bool)
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if err := hongguoAppThrottle(ctx); err != nil {
			return nil, err
		}
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(attempt) * 700 * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if freshConn && attempt > 0 && downloader.client != nil {
			// 风控窗口可能跟连接绑定：重试前丢掉空闲连接，强制重新握手。
			downloader.client.CloseIdleConnections()
		}
		request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		if freshConn {
			request.Close = true
		}
		if cookieHeader != "" {
			request.Header.Set("Cookie", cookieHeader)
		}
		request.Header.Set("User-Agent", hongguoAppUserAgent)
		request.Header.Set("Accept", "application/json")
		request.Header.Set("X-XS-From-Web", "0")
		request.Header.Set("Sdk-Version", "2")
		// 新版客户端会带齐这几项；缺任一项时部分接口（尤其详情/取流）回空体。
		request.Header.Set("X-Tt-Request-Tag", "t=0;n=0")
		request.Header.Set("Passport-Sdk-Version", "50561")
		request.Header.Set("X-Vc-Bdturing-Sdk-Version", "3.7.2.cn")
		comment, _ := ctx.Value(hongguoCommentKey{}).(bool)
		var commentNonce hongguoCommentNonce
		if comment {
			commentNonce, err = newHongguoCommentNonce()
			if err != nil {
				return nil, errors.New("无法准备红果文字请求")
			}
			request.Header.Set("Comment-Source", "601")
			request.Header.Set("Server-Channel", "1000")
		}
		if payload != nil {
			request.Header.Set("Content-Type", "application/json; charset=utf-8")
		}
		response, err := downloader.doPreparedCatalogRequest(request, 20*time.Second, func(prepared *http.Request) {
			now := time.Now()
			query.Set("_rticket", strconv.FormatInt(now.UnixMilli(), 10))
			prepared.URL.RawQuery = query.Encode()
			if comment {
				signHongguoCommentRequest(prepared, commentNonce, now)
			} else {
				signHongguoRequest(prepared, body, now)
			}
		})
		if err != nil {
			lastErr = err
			var backoff *requestBackoff
			if errors.As(err, &backoff) || ctx.Err() != nil {
				return nil, err
			}
			continue
		}
		content, readErr := io.ReadAll(io.LimitReader(response.Body, providerMaxBodyBytes+1))
		response.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if response.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("红果 App 接口 HTTP %d", response.StatusCode)
			if response.StatusCode >= 400 && response.StatusCode < 500 {
				return nil, lastErr
			}
			continue
		}
		if len(content) > providerMaxBodyBytes {
			return nil, errors.New("红果 App 接口返回的数据过大，请稍后重试")
		}
		if len(content) == 0 {
			lastErr = errHongguoAppEmptyBody
			if _, downloading := ctx.Value(downloadResolveKey{}).(bool); downloading && attempts < hongguoAppDownloadEmptyAttempts {
				attempts = hongguoAppDownloadEmptyAttempts
			}
			continue
		}
		var result map[string]any
		decoder := json.NewDecoder(bytes.NewReader(content))
		decoder.UseNumber()
		if err := decoder.Decode(&result); err != nil || result == nil {
			// 风控页/截断响应同样会落在解析失败上，按可重试处理。
			lastErr = errors.New("红果 App 接口返回格式异常")
			continue
		}
		code := firstNonEmpty(mapString(result, "code", "Code", "status_code"), mapString(nestedMap(result, "BaseResp"), "StatusCode"))
		if code != "" && code != "0" {
			return nil, fmt.Errorf("红果 App 接口暂不可用（%s）", truncate(code, 20))
		}
		return result, nil
	}
	return nil, lastErr
}
