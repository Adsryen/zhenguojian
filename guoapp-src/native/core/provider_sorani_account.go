package core

// 青空次元（sorani）账号模块：behavior-captcha 登录闭环、会话持久化、
// access token 自动续期，以及带会话头的取流入口 soraniPlay。
//
// 上游接口（实测 2026-11-07，证据见任务回报）：
//
//	POST /auth/behavior-captcha/generate?scene=login   滑块验证码（data:image base64 + 尺寸 + id）
//	POST /auth/behavior-captcha/verify                 提交拖动轨迹，成功换 captchaToken
//	POST /auth/login                                   identifier+password+captchaToken → token/refreshToken
//	POST /auth/refresh                                 Bearer refreshToken → 新 accessToken
//	POST /auth/logout                                  Bearer accessToken
//	GET  /video/episode/{id}/play?lineCode=            Bearer accessToken；未登录返回 canPlay=false
//
// 会话文件 sorani-session.json 落在 cfg.dataDir，与 huangguo-session.json
// （provider_legacy_session.go:114）同目录约定，凭据不进仓库。
//
// 所有权边界：本文件是 Q5 唯一可写模块，provider_sorani.go /
// provider_sorani_client.go 均未改动。因此：
//   - 鉴权请求由 soraniAuthCall 自行构造（与 client.go 的 get 同一套安全约束）；
//   - 会话状态不落 soraniAPIClient 字段，改为「磁盘文件即状态」，
//     写文件走 nativeDownloadWrite 原子替换，跨进程/重启自然生效；
//   - 取流接线（resolveSoraniMedia 改走 soraniPlay）在回报里列为依赖请求，
//     未接线前青空保持「能逛不能播」，行为不回退。

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	soraniSessionFile  = "sorani-session.json"
	soraniCaptchaScene = "login"
	soraniRenewBefore  = 5 * time.Minute
	soraniSessionLimit = 32 * 1024
)

var (
	errSoraniLoginRequired = errors.New("青空次元需要登录后才能播放，请在 设置 > 站源 > 站源账号 中登录青空账号")
	errSoraniLoginExpired  = errors.New("青空次元登录已失效，请在 设置 > 站源 > 站源账号 中重新登录")
	soraniCaptchaIDFormat  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

	// soraniAccountMu 串行化登录/续期/登出与会话文件写入：读盘→上游→写盘
	// 是一个临界区，否则并发请求各刷一次新 token，旧 refreshToken 被轮换后
	// 输掉的一方会拿到 401 并把会话清掉。锁只保护这些低频动作（人手频率）；
	// 取流路径不在锁内等网络（见 soraniPlay / accountTokenHint）。
	soraniAccountMu sync.Mutex
)

// soraniAPIError 承载上游业务错误码（HTTP 200 但 code != 200）。
// 续期链路需要区分 401/403（会话作废）与其它错误，普通 error 无法承载。
type soraniAPIError struct{ code, message string }

func (err *soraniAPIError) Error() string { return "青空次元接口错误：" + err.message }

type soraniSession struct {
	Version      int    `json:"version"`
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	Name         string `json:"name"`
}

type soraniAccountStatus struct {
	LoggedIn bool   `json:"loggedIn"`
	Name     string `json:"name"`
}

// soraniAccountInput 是 GUI 登录提交的载荷：identifier/password 加滑块轨迹。
type soraniAccountInput struct {
	Identifier string             `json:"identifier"`
	Password   string             `json:"password"`
	CaptchaID  string             `json:"captchaId"`
	Track      soraniCaptchaTrack `json:"track"`
}

type soraniCaptchaTrack struct {
	BgImageWidth        int                  `json:"bgImageWidth"`
	BgImageHeight       int                  `json:"bgImageHeight"`
	TemplateImageWidth  int                  `json:"templateImageWidth"`
	TemplateImageHeight int                  `json:"templateImageHeight"`
	StartTime           int64                `json:"startTime"`
	StopTime            int64                `json:"stopTime"`
	TrackList           []soraniCaptchaPoint `json:"trackList"`
}

type soraniCaptchaPoint struct {
	X    int    `json:"x"`
	Y    int    `json:"y"`
	Type string `json:"type"`
	T    int64  `json:"t"`
}

type soraniCaptcha struct {
	ID               string `json:"id"`
	BackgroundImage  string `json:"backgroundImage"`
	TemplateImage    string `json:"templateImage"`
	BackgroundWidth  int    `json:"backgroundWidth"`
	BackgroundHeight int    `json:"backgroundHeight"`
	TemplateWidth    int    `json:"templateWidth"`
	TemplateHeight   int    `json:"templateHeight"`
}

// soraniAccount 是账号命令入口："" 查状态 / "captcha" 取验证码 /
// "login" 登录 / "logout" 登出。GUI 侧接线（nativeInput.Account 字段 +
// action 分发 + source_login 错误码）属 V1 批次，本任务只交付 Go 闭环。
func (d *Downloader) soraniAccount(ctx context.Context, command string, input soraniAccountInput) (any, error) {
	client := d.soraniClient()
	switch command {
	case "":
		return client.accountStatus(), nil
	case "captcha":
		return client.accountCaptcha(ctx)
	case "login":
		return client.accountLogin(ctx, input)
	case "logout":
		return client.accountLogout(ctx), nil
	}
	return nil, errors.New("不支持的应用操作")
}

// soraniTokenExpiry 解析 JWT payload 的 exp；结构不合法或无 exp 返回零值。
func soraniTokenExpiry(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(token) > 4096 || len(parts) != 3 || parts[2] == "" {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp <= 0 {
		return time.Time{}
	}
	return time.Unix(claims.Exp, 0)
}

func (client *soraniAPIClient) accountSessionPath() string {
	return filepath.Join(client.downloader.cfg.dataDir, soraniSessionFile)
}

// loadAccountSession 读取磁盘会话；版本不符、accessToken 不可解析、
// refreshToken 已过期都按未登录处理（不删文件，留给下次写入覆盖）。
func (client *soraniAPIClient) loadAccountSession() soraniSession {
	file, err := os.Open(client.accountSessionPath())
	if err != nil {
		return soraniSession{}
	}
	defer file.Close()
	var stored soraniSession
	if json.NewDecoder(io.LimitReader(file, soraniSessionLimit)).Decode(&stored) != nil || stored.Version != 1 ||
		soraniTokenExpiry(stored.AccessToken).IsZero() || !time.Now().Before(soraniTokenExpiry(stored.RefreshToken)) {
		return soraniSession{}
	}
	return stored
}

// accountTokenHint 只读快照：accessToken 尚有余量时直接返回它，
// 让正常取流完全走无锁路径（登录态在磁盘上，无需常驻内存）。
// 返回 false 表示需要进临界区做续期。
func (client *soraniAPIClient) accountTokenHint() (string, bool) {
	soraniAccountMu.Lock()
	defer soraniAccountMu.Unlock()
	session := client.loadAccountSession()
	if session.RefreshToken == "" {
		return "", true
	}
	if time.Until(soraniTokenExpiry(session.AccessToken)) > soraniRenewBefore {
		return session.AccessToken, true
	}
	return "", false
}

func (client *soraniAPIClient) saveAccountSession(session soraniSession) error {
	if session.RefreshToken == "" {
		if err := os.Remove(client.accountSessionPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := json.Marshal(session)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(client.downloader.cfg.dataDir, 0o700); err != nil {
		return err
	}
	return nativeDownloadWrite(client.accountSessionPath(), data)
}

func (client *soraniAPIClient) accountStatus() soraniAccountStatus {
	soraniAccountMu.Lock()
	defer soraniAccountMu.Unlock()
	session := client.loadAccountSession()
	return soraniAccountStatus{LoggedIn: session.RefreshToken != "", Name: session.Name}
}

// accountAccessToken 返回当前 accessToken：未登录返回 ""（浏览/取流匿名可用），
// 距过期不足 soraniRenewBefore 时进临界区续期。
func (client *soraniAPIClient) accountAccessToken(ctx context.Context) (string, error) {
	if token, ready := client.accountTokenHint(); ready {
		return token, nil
	}
	soraniAccountMu.Lock()
	defer soraniAccountMu.Unlock()
	session := client.loadAccountSession()
	if session.RefreshToken == "" {
		return "", nil
	}
	if time.Until(soraniTokenExpiry(session.AccessToken)) > soraniRenewBefore {
		return session.AccessToken, nil
	}
	return client.refreshAccountLocked(ctx, session)
}

// accountRenewToken 处理「服务端已拒绝该 token」：磁盘上已是更新的
// accessToken 就直接复用（并发续期已生效），否则续期一次。
func (client *soraniAPIClient) accountRenewToken(ctx context.Context, rejected string) (string, error) {
	soraniAccountMu.Lock()
	defer soraniAccountMu.Unlock()
	session := client.loadAccountSession()
	if session.RefreshToken == "" {
		return "", errSoraniLoginExpired
	}
	if session.AccessToken != rejected {
		return session.AccessToken, nil
	}
	return client.refreshAccountLocked(ctx, session)
}

func (client *soraniAPIClient) refreshAccountLocked(ctx context.Context, session soraniSession) (string, error) {
	value, err := client.soraniAuthCall(ctx, http.MethodPost, "/auth/refresh", nil, nil, session.RefreshToken, 256<<10)
	var apiErr *soraniAPIError
	if errors.As(err, &apiErr) && (apiErr.code == "401" || apiErr.code == "403") {
		_ = client.saveAccountSession(soraniSession{})
		return "", errSoraniLoginExpired
	}
	if err != nil {
		return "", err
	}
	renewed, err := soraniSessionFromLogin(value, session.Name)
	if err != nil {
		return "", err
	}
	if err := client.saveAccountSession(renewed); err != nil {
		return "", errors.New("青空次元续期成功，但未能保存登录状态，请检查存储空间后重试")
	}
	return renewed.AccessToken, nil
}

func soraniSessionFromLogin(value any, name string) (soraniSession, error) {
	row, _ := soraniPayload(value).(map[string]any)
	access, refresh := mapString(row, "token"), mapString(row, "refreshToken")
	if soraniTokenExpiry(access).IsZero() || soraniTokenExpiry(refresh).IsZero() {
		return soraniSession{}, errors.New("青空次元未返回有效的登录凭证")
	}
	if user, ok := row["user"].(map[string]any); ok {
		if display := mapString(user, "displayName", "username"); display != "" {
			name = display
		}
	}
	return soraniSession{Version: 1, AccessToken: access, RefreshToken: refresh, Name: truncate(name, 64)}, nil
}

// accountCaptcha 取滑块验证码。返回的背景/模板图与尺寸交 GUI 渲染，
// 拖动轨迹由 accountLogin 的 verify 步消费。
func (client *soraniAPIClient) accountCaptcha(ctx context.Context) (soraniCaptcha, error) {
	value, err := client.soraniAuthCall(ctx, http.MethodPost, "/auth/behavior-captcha/generate",
		url.Values{"scene": {soraniCaptchaScene}}, map[string]any{}, "", 2<<20)
	if err != nil {
		return soraniCaptcha{}, err
	}
	row, _ := soraniPayload(value).(map[string]any)
	if kind := mapString(row, "type"); kind != "SLIDER" {
		return soraniCaptcha{}, fmt.Errorf("青空次元返回了暂不支持的验证方式（%s），请稍后重试", truncate(kind, 32))
	}
	size := func(key string) int {
		number, err := strconv.Atoi(nativeText(row[key]))
		if err != nil || number <= 0 || number > 4000 {
			return 0
		}
		return number
	}
	captcha := soraniCaptcha{
		ID:               mapString(row, "id"),
		BackgroundImage:  mapString(row, "backgroundImage"),
		TemplateImage:    mapString(row, "templateImage"),
		BackgroundWidth:  size("backgroundImageWidth"),
		BackgroundHeight: size("backgroundImageHeight"),
		TemplateWidth:    size("templateImageWidth"),
		TemplateHeight:   size("templateImageHeight"),
	}
	if !soraniCaptchaIDFormat.MatchString(captcha.ID) ||
		!strings.HasPrefix(captcha.BackgroundImage, "data:image/") || !strings.HasPrefix(captcha.TemplateImage, "data:image/") ||
		captcha.BackgroundWidth == 0 || captcha.BackgroundHeight == 0 || captcha.TemplateWidth == 0 || captcha.TemplateHeight == 0 {
		return soraniCaptcha{}, errors.New("青空次元验证码数据无效，请重试")
	}
	return captcha, nil
}

// valid 是提交给 verify 前的本地轨迹自检：尺寸/时间窗合法，
// 至少两个点且类型只允许 down/move/up，避免把明显异常的数据发给上游。
func (track soraniCaptchaTrack) valid() bool {
	if track.BgImageWidth <= 0 || track.BgImageWidth > 4000 || track.BgImageHeight <= 0 || track.BgImageHeight > 4000 ||
		track.TemplateImageWidth <= 0 || track.TemplateImageWidth > 4000 || track.TemplateImageHeight <= 0 || track.TemplateImageHeight > 4000 ||
		track.StartTime <= 0 || track.StopTime < track.StartTime || len(track.TrackList) < 2 || len(track.TrackList) > 5000 {
		return false
	}
	for _, point := range track.TrackList {
		if point.Type != "down" && point.Type != "move" && point.Type != "up" || point.T < 0 {
			return false
		}
	}
	return true
}

// accountLogin 走 verify→login 两步。verify 未通过（含上游判定轨迹为机器人）
// 时不会把账号密码发出去；成功后持久化会话并返回展示名。
func (client *soraniAPIClient) accountLogin(ctx context.Context, input soraniAccountInput) (soraniAccountStatus, error) {
	identifier := strings.TrimSpace(input.Identifier)
	if identifier == "" || len(identifier) > 128 {
		return soraniAccountStatus{}, errors.New("请输入青空次元用户名或邮箱")
	}
	if input.Password == "" || len(input.Password) > 128 {
		return soraniAccountStatus{}, errors.New("请输入青空次元密码")
	}
	if !soraniCaptchaIDFormat.MatchString(input.CaptchaID) || !input.Track.valid() {
		return soraniAccountStatus{}, errors.New("安全验证数据无效，请重新拖动滑块")
	}
	verified, err := client.soraniAuthCall(ctx, http.MethodPost, "/auth/behavior-captcha/verify",
		url.Values{"scene": {soraniCaptchaScene}}, map[string]any{"id": input.CaptchaID, "data": input.Track}, "", 64<<10)
	if err != nil {
		var apiErr *soraniAPIError
		if errors.As(err, &apiErr) {
			return soraniAccountStatus{}, errors.New("安全验证未通过，请重新拖动滑块")
		}
		return soraniAccountStatus{}, err
	}
	row, _ := soraniPayload(verified).(map[string]any)
	captchaToken := mapString(row, "token")
	if captchaToken == "" || len(captchaToken) > 4096 {
		return soraniAccountStatus{}, errors.New("安全验证未通过，请重新拖动滑块")
	}
	value, err := client.soraniAuthCall(ctx, http.MethodPost, "/auth/login", nil,
		map[string]any{"identifier": identifier, "password": input.Password, "captchaToken": captchaToken}, "", 256<<10)
	if err != nil {
		return soraniAccountStatus{}, err
	}
	session, err := soraniSessionFromLogin(value, identifier)
	if err != nil {
		return soraniAccountStatus{}, err
	}
	soraniAccountMu.Lock()
	defer soraniAccountMu.Unlock()
	if err := client.saveAccountSession(session); err != nil {
		return soraniAccountStatus{}, errors.New("青空次元登录成功，但未能保存登录状态，请检查存储空间后重试")
	}
	return soraniAccountStatus{LoggedIn: true, Name: session.Name}, nil
}

// accountLogout 先清本地会话再通知上游，保证上游不可达时也不会残留登录态。
func (client *soraniAPIClient) accountLogout(ctx context.Context) soraniAccountStatus {
	soraniAccountMu.Lock()
	access := client.loadAccountSession().AccessToken
	_ = client.saveAccountSession(soraniSession{})
	soraniAccountMu.Unlock()
	if access != "" {
		_, _ = client.soraniAuthCall(ctx, http.MethodPost, "/auth/logout", nil, nil, access, 64<<10)
	}
	return soraniAccountStatus{}
}

// soraniPlayRoute 判断是否取流路径（/video/episode/{id}/play）。
// 只有这条路径需要会话头，目录/搜索/详情保持匿名，避免登录态异常
// 连带拖垮既有可浏览能力（PRD 验收：浏览路径不回退）。
func soraniPlayRoute(route string) bool {
	return strings.HasPrefix(route, "/video/episode/") && strings.HasSuffix(route, "/play")
}

// soraniLoginRejected 识别 canPlay=false 里「未登录/登录失效」这一类。
func soraniLoginRejected(row map[string]any) bool {
	canPlay, ok := row["canPlay"].(bool)
	return ok && !canPlay && strings.Contains(mapString(row, "message"), "登录")
}

// soraniPlay 取流并补齐会话头：
//  1. 已登录且 accessToken 临期 → 先续期（正常 token 走无锁快照路径）；
//  2. 带 Bearer 请求；若上游仍报「需登录」且此前带了 token → 续期后重试一次；
//  3. 仍不可播时返回可操作错误（需要登录 / 登录已失效 / 上游原因）；
//     可播时原样返回上游 JSON，交 resolveSoraniMedia 解析 playUrl。
//
// 接线方式（依赖请求，见回报）：soraniRequest 在 soraniPlayRoute(route) 时
// 改调本方法，其余路径维持 client.get 匿名请求，resolveSoraniMedia 本体不动。
func (client *soraniAPIClient) soraniPlay(ctx context.Context, route string, query url.Values, limit int64) (any, error) {
	token, err := client.accountAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	value, err := client.soraniAuthCall(ctx, http.MethodGet, route, query, nil, token, limit)
	if err != nil {
		return nil, err
	}
	row, _ := soraniPayload(value).(map[string]any)
	if row == nil {
		return nil, errors.New("青空次元播放数据格式无效")
	}
	if soraniLoginRejected(row) && token != "" {
		if token, err = client.accountRenewToken(ctx, token); err != nil {
			return nil, err
		}
		if value, err = client.soraniAuthCall(ctx, http.MethodGet, route, query, nil, token, limit); err != nil {
			return nil, err
		}
		if row, _ = soraniPayload(value).(map[string]any); row == nil {
			return nil, errors.New("青空次元播放数据格式无效")
		}
	}
	if canPlay, ok := row["canPlay"].(bool); ok && !canPlay {
		switch {
		case soraniLoginRejected(row) && token == "":
			return nil, errSoraniLoginRequired
		case soraniLoginRejected(row):
			return nil, errSoraniLoginExpired
		case mapString(row, "message") != "":
			return nil, errors.New("青空次元：" + truncate(mapString(row, "message"), 120))
		default:
			return nil, errors.New("青空次元该分集暂不可播放")
		}
	}
	return value, nil
}

// soraniAuthCall 是带 method/body/bearer 的青空请求，安全约束与
// provider_sorani_client.go 的 get 逐条对齐：地址校验、限流、无 Cookie Jar、
// 仅同源重定向、超时、响应大小上限、code!=200 转 soraniAPIError。
// 之所以复制一份：get 写死 GET 且不设 Authorization，
// 而 client.go 不在本任务所有权内，不能就地扩展。
func (client *soraniAPIClient) soraniAuthCall(ctx context.Context, method, route string, query url.Values, body any, bearer string, limit int64) (any, error) {
	if limit <= 0 {
		limit = 4 << 20
	}
	base, err := url.Parse(client.base)
	if err != nil || !isProviderHTTPMediaURL(client.base) || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("青空次元接口地址无效")
	}
	timeout := 15 * time.Second
	if background, _ := ctx.Value(backgroundCatalogKey{}).(bool); background {
		timeout = 8 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	address := client.base + route
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, errors.New("无法创建青空次元请求")
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, address, payload)
	if err != nil {
		return nil, errors.New("无法创建青空次元请求")
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	request.Header.Set("User-Agent", soraniUserAgent)
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	request.Header.Set("Referer", client.site+"/")
	request.Header.Set("Origin", client.site)

	d := client.downloader
	if d.limiter != nil {
		release, err := d.limiter.acquire(ctx, request)
		if err != nil {
			return nil, err
		}
		defer release()
	}
	transport := *d.client
	transport.Jar = nil
	previousRedirect := transport.CheckRedirect
	transport.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 5 || providerMediaOrigin(next.URL) != providerMediaOrigin(base) {
			return errors.New("青空次元接口重定向地址异常")
		}
		if previousRedirect != nil {
			return previousRedirect(next, via)
		}
		return nil
	}
	response, err := transport.Do(request)
	if err != nil {
		return nil, fmt.Errorf("青空次元连接失败：%w", publicError(err))
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if d.limiter != nil {
		d.limiter.observe(request, response)
	}
	observeSourceResponse(ctx, response)
	blocked := catalogResponseBlockReason(response, data) != ""
	if response.StatusCode < 200 || response.StatusCode >= 300 || blocked {
		return nil, d.catalogResponseError(request, response, data)
	}
	if readErr != nil || int64(len(data)) > limit {
		return nil, errors.New("青空次元返回的数据过大或读取失败")
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, errors.New("青空次元返回的数据格式无效")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("青空次元返回的数据格式无效")
	}
	// 上游统一用 code=200 表示成功；非 200 携带 code 供 401/403 判定。
	if row, ok := decoded.(map[string]any); ok {
		if code := nativeText(row["code"]); code != "" && code != "200" {
			message := mapString(row, "message", "msg")
			if message == "" {
				message = "上游返回错误码 " + code
			}
			return nil, &soraniAPIError{code: code, message: truncate(message, 120)}
		}
	}
	return decoded, nil
}
