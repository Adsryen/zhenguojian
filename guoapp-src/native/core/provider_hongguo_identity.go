package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 红果 App 播放接口只对「已注册设备」下发片源：
//   - 未注册的随机 device_id/iid：HTTP 200 + 0 字节（目录/详情不受影响）；
//   - 注册后的 install_id：返回完整 video_model（含 1080p + spade_a）。
//
// 注册走字节 applog 的 device_register，无需签名，但请求体必须带
// openudid/udid 两个设备指纹字段，否则固定回 device_id=0。
// 4.5.5 起参考客户端改走 log5-applog-lf。实测该域名配原有查询参数即可注册
// 成功，并下发 install_id/ttreq/store-region/store-region-src 四条 cookie；
// 参考 URL 里额外的 tt_data/cronet_version/ttnet_version/use_store_region_cookie
// 加上后反而固定回 device_id=0，所以这里不带。旧 log.snssdk.com 已开始回 0。
const hongguoRegisterURL = "https://log5-applog-lf.fqnovel.com/service/2/device_register/"

const (
	hongguoAppVersionCode = "73532"
	hongguoAppVersionName = "7.3.5.32"
)

var errHongguoRegisterRejected = errors.New("红果设备注册未返回有效设备号")

func hongguoRandomHex(length int) string {
	const digits = "0123456789abcdef"
	out := make([]byte, length)
	for index := range out {
		out[index] = digits[rand.Intn(len(digits))]
	}
	return string(out)
}

// ensureHongguoIdentity 确保客户端持有已注册设备；失败时保留原随机设备，
// 目录/详情仍可用，只是播放会退到网页公开兜底。
func (downloader *Downloader) ensureHongguoIdentity(ctx context.Context) error {
	client := downloader.hongguoClient()
	client.identityMu.Lock()
	defer client.identityMu.Unlock()

	client.mu.Lock()
	registered := client.state.Registered && hongguoNumericID.MatchString(client.state.DeviceID) && hongguoNumericID.MatchString(client.state.InstallID)
	client.mu.Unlock()
	if registered {
		return nil
	}

	deviceID, installID, token, cookies, err := downloader.registerHongguoDevice(ctx)
	if err != nil {
		return err
	}
	client.mu.Lock()
	client.state.DeviceID = deviceID
	client.state.InstallID = installID
	client.state.DeviceToken = token
	client.state.DeviceCookies = cookies
	client.state.Registered = true
	client.state.RegisteredAt = time.Now().Unix()
	snapshot := cloneHongguoCatalogState(&client.state)
	client.mu.Unlock()
	if downloader.onHongguoIdentity != nil {
		downloader.onHongguoIdentity(snapshot)
	}
	return nil
}

func hongguoDeviceRegisterPayload() (url.Values, []byte) {
	query := url.Values{
		"device_platform": {"android"}, "aid": {"8662"}, "app_name": {"novelread"},
		"version_code": {hongguoAppVersionCode}, "version_name": {hongguoAppVersionName},
		"channel": {"update_64"}, "device_type": {"25053RT47C"}, "device_brand": {"Redmi"},
		"os_version": {"16"}, "os_api": {"36"}, "resolution": {"1280*2772"},
		"dpi": {"520"}, "language": {"zh"}, "ac": {"wifi"},
	}
	header := map[string]any{
		"aid": 8662, "app_name": "novelread", "package": "com.phoenix.read",
		"version_code": 73532, "version_name": hongguoAppVersionName,
		"manifest_version_code": 73932, "update_version_code": 73932,
		"channel": "update_64", "device_platform": "android",
		"device_type": "25053RT47C", "device_brand": "Redmi", "device_model": "25053RT47C",
		"device_manufacturer": "Xiaomi", "os": "Android", "os_version": "16", "os_api": 36,
		"resolution": "1280*2772", "dpi": 520, "language": "zh", "region": "CN",
		"timezone": 8, "timezone_name": "Asia/Shanghai", "access": "wifi", "ac": "wifi",
		"cpu_abi": "arm64-v8a", "sdk_version": "6.16.0",
		"openudid": hongguoRandomHex(16), "udid": hongguoRandomHex(40),
	}
	payload := map[string]any{
		"magic_tag": "ss_app_log",
		"header":    header,
		"_gen_time": time.Now().UnixMilli(),
	}
	body, _ := json.Marshal(payload)
	return query, body
}

func (downloader *Downloader) registerHongguoDevice(ctx context.Context) (string, string, string, map[string]string, error) {
	query, body := hongguoDeviceRegisterPayload()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, hongguoRegisterURL+"?"+query.Encode(), bytes.NewReader(body))
	if err != nil {
		return "", "", "", nil, err
	}
	request.Header.Set("User-Agent", hongguoAppUserAgent)
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("Accept", "application/json")

	response, err := downloader.doCatalogRequestWithTimeout(request, 15*time.Second)
	if err != nil {
		return "", "", "", nil, err
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, providerMaxBodyBytes))
	if err != nil {
		return "", "", "", nil, err
	}
	if response.StatusCode != http.StatusOK {
		return "", "", "", nil, fmt.Errorf("红果设备注册 HTTP %d", response.StatusCode)
	}
	var result map[string]any
	if err := json.Unmarshal(content, &result); err != nil {
		return "", "", "", nil, errors.New("红果设备注册返回格式异常")
	}
	deviceID := firstNonEmpty(mapString(result, "device_id_str"), mapString(result, "device_id"))
	installID := firstNonEmpty(mapString(result, "install_id_str"), mapString(result, "install_id"))
	if !hongguoNumericID.MatchString(deviceID) || !hongguoNumericID.MatchString(installID) || deviceID == "0" || installID == "0" {
		return "", "", "", nil, errHongguoRegisterRejected
	}
	token := mapString(result, "device_token")
	// 真机取流会带注册响应下发的四条 cookie；以前只读 JSON、把 Set-Cookie
	// 丢了，等于未登录设备直接请求取流接口。
	cookies := map[string]string{}
	for _, cookie := range response.Cookies() {
		switch cookie.Name {
		case "install_id", "ttreq", "store-region", "store-region-src":
			if value := strings.TrimSpace(cookie.Value); value != "" {
				cookies[cookie.Name] = value
			}
		}
	}
	return deviceID, installID, token, cookies, nil
}
