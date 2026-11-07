package core

// 青空账号模块单测：全链路走 httptest 风格的内存传输（sourceFixtureTransport），
// 不触碰真实网络与真实凭据。fixture 复刻上游语义：
//   - 验证码 verify：id/尺寸/轨迹点数不符 → code 4001；
//   - 登录：captchaToken 或密码不符 → code 500；
//   - 刷新：Bearer 不等于当前 refreshToken → code 401（业务码走 HTTP 200）；
//   - 取流：Bearer 在发牌表里 → canPlay，否则 canPlay=false + "请先登录"。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func soraniFixtureToken(name string, expires time.Time) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"jti":"` + name + `","exp":` + strconv.FormatInt(expires.Unix(), 10) + `}`))
	return "eyJhbGciOiJIUzI1NiJ9." + payload + ".sig"
}

type soraniFixtureServer struct {
	access      map[string]bool
	refresh     string
	accessLife  time.Duration
	issued      int
	refreshes   int
	logouts     int
	captchas    int
	verifies    int
	loginBodies []map[string]any
	lastBearer  string
}

func (server *soraniFixtureServer) issue() map[string]any {
	server.issued++
	access := soraniFixtureToken("acc", time.Now().Add(server.accessLife))
	refresh := soraniFixtureToken("ref", time.Now().Add(7*24*time.Hour))
	server.access = map[string]bool{access: true}
	server.refresh = refresh
	return map[string]any{"token": access, "refreshToken": refresh,
		"user": map[string]any{"username": "123521", "displayName": "友人"}}
}

func (server *soraniFixtureServer) transport(t *testing.T) sourceFixtureTransport {
	t.Helper()
	return func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "api.sorani.cc" {
			t.Errorf("sorani fixture got unexpected host %q", request.URL.Host)
		}
		bearer := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
		server.lastBearer = bearer
		decode := func() map[string]any {
			var body map[string]any
			if request.Body != nil {
				_ = json.NewDecoder(request.Body).Decode(&body)
			}
			return body
		}
		write := func(status int, payload any) (*http.Response, error) {
			data, _ := json.Marshal(payload)
			return sourceFixtureResponse(request, status, string(data)), nil
		}
		switch {
		case request.URL.Path == "/sorani-cms/api/auth/behavior-captcha/generate":
			server.captchas++
			return write(http.StatusOK, map[string]any{"code": 200, "data": map[string]any{
				"id": "SLIDER_abc", "type": "SLIDER",
				"backgroundImage": "data:image/png;base64,BG", "templateImage": "data:image/png;base64,TP",
				"backgroundImageWidth": 300, "backgroundImageHeight": 200,
				"templateImageWidth": 60, "templateImageHeight": 50,
			}})
		case request.URL.Path == "/sorani-cms/api/auth/behavior-captcha/verify":
			server.verifies++
			body := decode()
			data, _ := body["data"].(map[string]any)
			trackList, _ := data["trackList"].([]any)
			if body["id"] != "SLIDER_abc" || data["bgImageWidth"] != float64(300) || len(trackList) != 3 {
				return write(http.StatusOK, map[string]any{"code": 4001, "message": "验证码错误或已过期"})
			}
			return write(http.StatusOK, map[string]any{"code": 200, "data": map[string]any{"token": "captcha-ok"}})
		case request.URL.Path == "/sorani-cms/api/auth/login":
			body := decode()
			server.loginBodies = append(server.loginBodies, body)
			if body["captchaToken"] != "captcha-ok" || body["password"] != "secret" {
				return write(http.StatusOK, map[string]any{"code": 500, "message": "用户名或密码错误"})
			}
			return write(http.StatusOK, map[string]any{"code": 200, "data": server.issue()})
		case request.URL.Path == "/sorani-cms/api/auth/refresh":
			server.refreshes++
			if bearer == "" || bearer != server.refresh {
				return write(http.StatusOK, map[string]any{"code": 401, "message": "登录已失效"})
			}
			return write(http.StatusOK, map[string]any{"code": 200, "data": server.issue()})
		case request.URL.Path == "/sorani-cms/api/auth/logout":
			server.logouts++
			return write(http.StatusOK, map[string]any{"code": 200, "data": true})
		case request.URL.Path == "/sorani-cms/api/video/episode/64334/play":
			if server.access[bearer] {
				return write(http.StatusOK, map[string]any{"code": 200, "data": map[string]any{
					"canPlay": true, "lineCode": "anime_jp_m3u8",
					"playUrl": "https://www.sorani-vids.xyz/v/index.m3u8?key=1",
				}})
			}
			return write(http.StatusOK, map[string]any{"code": 200, "data": map[string]any{
				"canPlay": false, "message": "请先登录", "playUrl": nil,
			}})
		}
		t.Errorf("sorani fixture unmatched path %q", request.URL.Path)
		return write(http.StatusOK, map[string]any{"code": 500, "message": "fixture miss"})
	}
}

func soraniFixtureTrack() soraniCaptchaTrack {
	return soraniCaptchaTrack{BgImageWidth: 300, BgImageHeight: 200, TemplateImageWidth: 60, TemplateImageHeight: 50,
		StartTime: 1, StopTime: 900, TrackList: []soraniCaptchaPoint{
			{X: 8, Y: 30, Type: "down", T: 0}, {X: 120, Y: 31, Type: "move", T: 450}, {X: 187, Y: 30, Type: "up", T: 899},
		}}
}

func soraniFixtureLoginInput() soraniAccountInput {
	return soraniAccountInput{Identifier: " 123521 ", Password: "secret", CaptchaID: "SLIDER_abc", Track: soraniFixtureTrack()}
}

func soraniFixturePlay(client *soraniAPIClient) (any, error) {
	return client.soraniPlay(context.Background(), "/video/episode/64334/play", url.Values{"lineCode": {"anime_jp_m3u8"}}, 1<<20)
}

func soraniFixturePlayURL(t *testing.T, value any) string {
	t.Helper()
	row, _ := soraniPayload(value).(map[string]any)
	if row == nil {
		t.Fatalf("sorani play payload not an object: %#v", value)
	}
	return mapString(row, "playUrl")
}

func TestSoraniAccountLoginRenewAndExpiry(t *testing.T) {
	server := &soraniFixtureServer{access: map[string]bool{}, accessLife: time.Hour}
	d := sourceFixtureDownloader(t, server.transport(t))
	client := d.soraniClient()

	// 未登录：明确报「需要登录」，不是泛化的“暂无可用播放地址”。
	if _, err := soraniFixturePlay(client); !errors.Is(err, errSoraniLoginRequired) {
		t.Fatalf("anonymous play err = %v, want errSoraniLoginRequired", err)
	}

	captcha, err := client.accountCaptcha(context.Background())
	if err != nil || captcha.ID != "SLIDER_abc" || captcha.BackgroundWidth != 300 || captcha.TemplateHeight != 50 ||
		!strings.HasPrefix(captcha.BackgroundImage, "data:image/") {
		t.Fatalf("accountCaptcha = %#v, err = %v", captcha, err)
	}

	// 密码错误：登录被上游拒绝，不落会话。
	bad := soraniFixtureLoginInput()
	bad.Password = "wrong"
	if _, err := client.accountLogin(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "用户名或密码错误") {
		t.Fatalf("bad password err = %v, want upstream message", err)
	}
	if _, statErr := os.Stat(client.accountSessionPath()); !os.IsNotExist(statErr) {
		t.Fatalf("session file written after failed login: %v", statErr)
	}

	// 登录成功：identifier 去空白后透传，状态展示 displayName。
	if _, err := client.accountLogin(context.Background(), soraniFixtureLoginInput()); err != nil {
		t.Fatalf("login: %v", err)
	}
	if got := server.loginBodies[len(server.loginBodies)-1]["identifier"]; got != "123521" {
		t.Fatalf("login identifier = %v, want trimmed 123521", got)
	}
	if status := client.accountStatus(); !status.LoggedIn || status.Name != "友人" {
		t.Fatalf("accountStatus = %#v", status)
	}
	if _, err := os.Stat(client.accountSessionPath()); err != nil {
		t.Fatalf("session file missing after login: %v", err)
	}

	// 已登录取流：带 Bearer 直通，不触发续期。
	before := server.refreshes
	value, err := soraniFixturePlay(client)
	if err != nil || !strings.Contains(soraniFixturePlayURL(t, value), "index.m3u8") {
		t.Fatalf("logged-in play = %#v, err = %v", value, err)
	}
	if server.refreshes != before {
		t.Fatalf("healthy token triggered refresh %d -> %d", before, server.refreshes)
	}

	// accessToken 被服务端吊销且临期：续期一次并重试成功。
	server.access = map[string]bool{}
	server.accessLife = time.Minute
	value, err = soraniFixturePlay(client)
	if err != nil || soraniFixturePlayURL(t, value) == "" {
		t.Fatalf("renewed play = %#v, err = %v", value, err)
	}
	if server.refreshes != before+1 {
		t.Fatalf("refreshes = %d, want %d", server.refreshes, before+1)
	}

	// 重启进程（新 Downloader 共享 dataDir）：会话从磁盘恢复并自动续期。
	restarted := sourceFixtureDownloader(t, server.transport(t))
	restarted.cfg.dataDir = d.cfg.dataDir
	value, err = soraniFixturePlay(restarted.soraniClient())
	if err != nil || soraniFixturePlayURL(t, value) == "" {
		t.Fatalf("restarted play = %#v, err = %v", value, err)
	}
	if server.refreshes != before+2 {
		t.Fatalf("refreshes after restart = %d, want %d", server.refreshes, before+2)
	}

	// 服务端 refreshToken 作废：refresh 401 → 登录失效 + 会话文件清除。
	server.refresh = ""
	if _, err := soraniFixturePlay(restarted.soraniClient()); !errors.Is(err, errSoraniLoginExpired) {
		t.Fatalf("expired refresh err = %v, want errSoraniLoginExpired", err)
	}
	if _, statErr := os.Stat(restarted.soraniClient().accountSessionPath()); !os.IsNotExist(statErr) {
		t.Fatalf("session file kept after refresh 401: %v", statErr)
	}
	if status := restarted.soraniClient().accountStatus(); status.LoggedIn {
		t.Fatalf("status after expiry = %#v, want logged out", status)
	}
}

func TestSoraniAccountLogoutAndCaptchaChecks(t *testing.T) {
	server := &soraniFixtureServer{access: map[string]bool{}, accessLife: time.Hour}
	d := sourceFixtureDownloader(t, server.transport(t))
	client := d.soraniClient()

	// 轨迹点类型非法：本地自检拦截，不打上游。
	track := soraniFixtureTrack()
	track.TrackList[1].Type = "jump"
	input := soraniFixtureLoginInput()
	input.Track = track
	if _, err := client.accountLogin(context.Background(), input); err == nil || !strings.Contains(err.Error(), "安全验证数据无效") {
		t.Fatalf("bad track type err = %v, want local invalid", err)
	}
	if server.verifies != 0 || len(server.loginBodies) != 0 {
		t.Fatalf("bad track must not hit upstream: verifies=%d logins=%d", server.verifies, len(server.loginBodies))
	}

	// 轨迹点数不足（服务端要 3 个点，本地只保底 2 个）：verify 被拒 → 不发登录。
	input = soraniFixtureLoginInput()
	input.Track.TrackList = input.Track.TrackList[:2]
	if _, err := client.accountLogin(context.Background(), input); err == nil || !strings.Contains(err.Error(), "安全验证未通过") {
		t.Fatalf("short track err = %v, want verify rejected", err)
	}
	if len(server.loginBodies) != 0 {
		t.Fatalf("verify rejection must not send credentials, logins=%d", len(server.loginBodies))
	}

	// captchaId 格式非法：同样本地拦截。
	input = soraniFixtureLoginInput()
	input.CaptchaID = "SLIDER/../abc"
	if _, err := client.accountLogin(context.Background(), input); err == nil || !strings.Contains(err.Error(), "安全验证数据无效") {
		t.Fatalf("bad captcha id err = %v, want local invalid", err)
	}

	// 登录 → 登出：上游收到 accessToken Bearer，本地会话清除。
	if _, err := client.accountLogin(context.Background(), soraniFixtureLoginInput()); err != nil {
		t.Fatalf("login: %v", err)
	}
	loggedIn := client.loadAccountSession().AccessToken
	if status := client.accountLogout(context.Background()); status.LoggedIn {
		t.Fatalf("logout status = %#v", status)
	}
	if server.logouts != 1 || server.lastBearer != loggedIn {
		t.Fatalf("logout bearer = %q, logouts=%d, want issued access", server.lastBearer, server.logouts)
	}
	if _, err := os.Stat(client.accountSessionPath()); !os.IsNotExist(err) {
		t.Fatalf("session file kept after logout: %v", err)
	}
	if _, err := soraniFixturePlay(client); !errors.Is(err, errSoraniLoginRequired) {
		t.Fatalf("post-logout play err = %v, want errSoraniLoginRequired", err)
	}

	// soraniAccount 分发器：状态命令返回登录态载荷。
	value, err := d.soraniAccount(context.Background(), "", soraniAccountInput{})
	if err != nil {
		t.Fatalf("status command: %v", err)
	}
	if status, ok := value.(soraniAccountStatus); !ok || status.LoggedIn {
		t.Fatalf("status value = %#v", value)
	}
	if _, err := d.soraniAccount(context.Background(), "bogus", soraniAccountInput{}); err == nil {
		t.Fatal("unknown command must fail")
	}
}

func TestSoraniAccountPlayWiringGuards(t *testing.T) {
	server := &soraniFixtureServer{access: map[string]bool{}, accessLife: time.Hour}
	d := sourceFixtureDownloader(t, server.transport(t))
	client := d.soraniClient()

	// 线路被下架（canPlay=false 且与登录无关）：透传上游原因。
	if _, err := soraniFixturePlay(client); !errors.Is(err, errSoraniLoginRequired) {
		t.Fatalf("anonymous play err = %v", err)
	}
	if _, err := client.accountLogin(context.Background(), soraniFixtureLoginInput()); err != nil {
		t.Fatalf("login: %v", err)
	}
	rejected := map[string]any{"canPlay": false, "message": "该线路暂未开放"}
	if !soraniLoginRejected(map[string]any{"canPlay": false, "message": "请先登录"}) || soraniLoginRejected(rejected) {
		t.Fatal("soraniLoginRejected misclassified")
	}

	// soraniPlayRoute 只命中取流路径，浏览路径保持匿名。
	for route, want := range map[string]bool{
		"/video/episode/64334/play": true,
		"/video/episode/64334":      false,
		"/video":                    false,
		"/video/search":             false,
	} {
		if got := soraniPlayRoute(route); got != want {
			t.Fatalf("soraniPlayRoute(%q) = %v, want %v", route, got, want)
		}
	}

	// 上游 code 非 200 → soraniAPIError 承载业务码（refresh 判定依赖它）。
	if _, err := client.soraniAuthCall(context.Background(), http.MethodPost, "/auth/refresh", nil, nil, "bogus", 64<<10); err == nil {
		t.Fatal("bogus refresh must fail")
	} else {
		var apiErr *soraniAPIError
		if !errors.As(err, &apiErr) || apiErr.code != "401" {
			t.Fatalf("refresh err = %v (%T), want soraniAPIError 401", err, err)
		}
	}

	// token 解析：结构不符 / 无 exp / 超长输入都不 panic。
	if !soraniTokenExpiry("").IsZero() || !soraniTokenExpiry("a.b").IsZero() || !soraniTokenExpiry("a.b.c").IsZero() {
		t.Fatal("soraniTokenExpiry must zero malformed tokens")
	}
	if got := soraniTokenExpiry(soraniFixtureToken("x", time.Unix(1893456000, 0))); got.Unix() != 1893456000 {
		t.Fatalf("soraniTokenExpiry = %v", got)
	}
}
