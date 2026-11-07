package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// 网页播放器的公开兜底接口：无需签名、无需登录，只要求 X-Xs-From-Web。
// 返回的 main_url 是未加密的 H.264 MP4（目前固定 360p），用作 App 接口
// 被风控静默拒绝时的最后一条官方通路。
const (
	hongguoPublicPlaybackURL = "https://hongguoduanju.com/api/video/protected/fallback-play-info?aid=8662"
	hongguoPublicUserAgent   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	hongguoPublicReferer     = "https://hongguoduanju.com/"
)

func (d *Downloader) resolveHongguoPublicPlayback(ctx context.Context, videoID string) (providerMedia, error) {
	if !hongguoNumericID.MatchString(videoID) {
		return providerMedia{}, errors.New("红果视频 ID 无效")
	}
	payload, err := json.Marshal(map[string]string{"item_id": videoID})
	if err != nil {
		return providerMedia{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, hongguoPublicPlaybackURL, bytes.NewReader(payload))
	if err != nil {
		return providerMedia{}, err
	}
	request.Header.Set("User-Agent", hongguoPublicUserAgent)
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Xs-From-Web", "1")
	request.Header.Set("Referer", hongguoPublicReferer)
	response, err := d.doCatalogRequestWithTimeout(request, 15*time.Second)
	if err != nil {
		return providerMedia{}, err
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return providerMedia{}, err
	}
	if response.StatusCode != http.StatusOK {
		return providerMedia{}, fmt.Errorf("红果公开播放接口 HTTP %d", response.StatusCode)
	}
	var result struct {
		MainURL   string `json:"main_url"`
		URLExpire int64  `json:"url_expire"`
		BaseResp  struct {
			StatusCode    int    `json:"StatusCode"`
			StatusMessage string `json:"StatusMessage"`
		} `json:"base_resp"`
	}
	if err := json.Unmarshal(content, &result); err != nil {
		return providerMedia{}, errors.New("红果公开播放接口返回格式异常")
	}
	if result.BaseResp.StatusCode != 0 {
		return providerMedia{}, fmt.Errorf("红果公开播放接口暂不可用（%d）", result.BaseResp.StatusCode)
	}
	mediaURL := strings.TrimSpace(result.MainURL)
	if !isProviderHTTPMediaURL(mediaURL) {
		return providerMedia{}, errors.New("红果公开播放接口未返回有效媒体地址")
	}
	return providerMedia{
		URL:        mediaURL,
		Referer:    hongguoPublicReferer,
		Quality:    360,
		Definition: "360p",
	}, nil
}
