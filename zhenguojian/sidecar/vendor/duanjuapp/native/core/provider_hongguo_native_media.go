package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// 详情页场景：参考客户端的主路径，不带 dr_scene。
	hongguoAppDirectPath = "/novel/player/multi_video_model/v1/"
	// 预加载场景：带 dr_scene=preload，作为详情页失败后的兜底。
	hongguoAppPlaybackPath = "/novel/player/multi_video_model/preload/v1"
)

// 服务端按 device_level 决定下发哪套编码：3 是常态（低档位 bytevc2、1080P 档
// bytevc1），但少数集在 3 下只给 bytevc2，标准播放器一档都解不了；换 4 服务端
// 会改发 bytevc1/h264。所以先用 3 拿最好的档位，全不可解时再用 4 兜一次。
const (
	hongguoAppPlaybackLevel = 3
	hongguoAppFallbackLevel = 4
	// 最保守档：服务端在 1 下会把读不出视频轨的 bytevc2 换成 bytevc1/h264，
	// 1080P 档位不受影响。参考客户端 4.5.5 直接用它取流。
	hongguoAppConservativeLevel = 1
	// mixed_video_id_map 一次最多塞 15 个 ID，16 个开始直接返回参数错误。
	hongguoAppBatchLimit = 15
)

// 取流失败时轮换的线路。参考客户端 4.5.5 的表里有 a/c/lf/lq/hl；b 是我们
// 一直在用的默认节点，放在第一位，失败后再按顺序换。
var hongguoAppPlaybackHosts = []string{
	"api5-normal-sinfonlineb.fqnovel.com",
	"api5-normal-sinfonlinea.fqnovel.com",
	"api5-normal-sinfonlinec.fqnovel.com",
	"api5-normal-lf.fqnovel.com",
	"api5-normal-lq.fqnovel.com",
	"api5-normal-hl.fqnovel.com",
}

// 与 4.5.5 二进制里的退避一致（实测 0.6s / 1.8s）。
var hongguoAppRetryDelays = []time.Duration{600 * time.Millisecond, 1800 * time.Millisecond}

var errHongguoAppNoPlayableVariant = errors.New("红果 App 未返回兼容的媒体，已跳过不支持的编码")

// hongguoQuality 是一集「下下来是什么画质」的摘要：Definition 是服务端标的档位，
// Width/Height 是真实像素（两者会不一致），Bitrate 单位 bps。
type hongguoQuality struct {
	Definition string `json:"definition,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	Bitrate    int    `json:"bitrate,omitempty"`
	Quality    int    `json:"quality,omitempty"`
	Playable   bool   `json:"playable"`
}

func (downloader *Downloader) resolveHongguoAppMedia(ctx context.Context, videoID string) (providerMedia, error) {
	if !hongguoNumericID.MatchString(videoID) {
		return providerMedia{}, errors.New("红果视频 ID 无效")
	}
	if media, cached := downloader.cachedHongguoAppMedia(videoID); cached {
		return media, nil
	}
	// 服务端的空包窗口通常和连接/主机绑定：同一身份换线路、每次新建连接，
	// 间隔 0.6s / 1.8s。换设备实测无效（新注册的身份反而更容易空包），
	// 所以这里不再 refreshHongguoIdentity。
	var firstErr error
	for attempt, host := range hongguoAppPlaybackHosts {
		if attempt > 0 {
			delay := hongguoAppRetryDelays[min(attempt-1, len(hongguoAppRetryDelays)-1)]
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return providerMedia{}, ctx.Err()
			}
			if downloader.client != nil {
				downloader.client.CloseIdleConnections()
			}
		}
		attemptCtx := hongguoAppWithSingleAttempt(hongguoAppWithHost(ctx, host))
		if attempt > 0 {
			// 首次请求保持长连接；只有重试才强制重新握手。
			attemptCtx = hongguoAppWithFreshConn(attemptCtx)
		}
		media, err := downloader.hongguoAppMediaDirect(attemptCtx, videoID, hongguoAppPlaybackLevel)
		if err == nil {
			return downloader.cacheHongguoAppMedia(ctx, videoID, media), nil
		}
		if firstErr == nil {
			firstErr = err
		}
		if errors.Is(err, errHongguoAppNoPlayableVariant) {
			// 保守档兜底：device_level=1 会把不可解的 bytevc2 换成 bytevc1。
			if conservative, conservativeErr := downloader.hongguoAppMediaDirect(attemptCtx, videoID, hongguoAppConservativeLevel); conservativeErr == nil {
				return downloader.cacheHongguoAppMedia(ctx, videoID, conservative), nil
			}
			if fallback, fallbackErr := downloader.hongguoAppMediaDirect(attemptCtx, videoID, hongguoAppFallbackLevel); fallbackErr == nil {
				return downloader.cacheHongguoAppMedia(ctx, videoID, fallback), nil
			}
			// 三档都不可解，换主机不会改变档位供给，直接走旧接口。
			break
		}
		if !errors.Is(err, errHongguoAppEmptyBody) {
			break
		}
	}
	// 旧版直取接口走另一条路径，能在风控空包时救回一部分剧集。
	if legacy, legacyErr := downloader.hongguoAppLegacyMedia(ctx, videoID); legacyErr == nil {
		return downloader.cacheHongguoAppMedia(ctx, videoID, legacy), nil
	}
	return providerMedia{}, firstErr
}

// cacheHongguoAppMedia 把 App 接口解析出的片源按集缓存。低档位先按
// device_level=4 再问一次：服务端在 4 下会把 bytevc2 档位换成可解的
// bytevc1/h264，避免明明有 1080P 却只下到 360P。
func (downloader *Downloader) cacheHongguoAppMedia(ctx context.Context, videoID string, media providerMedia) providerMedia {
	if media.Quality > 0 && media.Quality < hongguoMediaLowQuality && ctx.Err() == nil {
		if better, err := downloader.hongguoAppMediaDirect(ctx, videoID, hongguoAppFallbackLevel); err == nil && better.Quality > media.Quality {
			media = better
		}
	}
	downloader.storeHongguoAppMedia(videoID, media)
	return media
}

// hongguoAppMediaDirect 走详情页场景，不携带 dr_scene。
func (downloader *Downloader) hongguoAppMediaDirect(ctx context.Context, videoID string, deviceLevel int) (providerMedia, error) {
	models, err := downloader.hongguoAppModelsAt(ctx, []string{videoID}, deviceLevel, hongguoAppDirectPath, false)
	if err != nil {
		return providerMedia{}, err
	}
	model := models[videoID]
	if model == nil {
		return providerMedia{}, errors.New("红果 App 未返回该集的播放信息")
	}
	return selectHongguoAppMedia(model)
}

func (downloader *Downloader) hongguoAppLegacyMedia(ctx context.Context, videoID string) (providerMedia, error) {
	payload := map[string]any{
		"video_id": videoID, "content_type": 1,
		"biz_param": map[string]any{"need_all_video_definition": true, "video_platform": 3},
	}
	result, err := downloader.hongguoAppRequest(ctx, http.MethodPost, "/novel/player/video_model/v1/", nil, payload)
	if err != nil {
		return providerMedia{}, err
	}
	data := nestedMap(result, "data")
	model, _ := data["video_model"].(map[string]any)
	if encoded, ok := data["video_model"].(string); ok {
		var decoded map[string]any
		decoder := json.NewDecoder(strings.NewReader(encoded))
		decoder.UseNumber()
		if err := decoder.Decode(&decoded); err != nil {
			return providerMedia{}, errors.New("红果 App 播放信息格式异常")
		}
		model = decoded
	}
	if model == nil {
		return providerMedia{}, errors.New("红果 App 未返回该集的播放信息")
	}
	return selectHongguoAppMedia(model)
}

func (downloader *Downloader) hongguoAppMediaAtLevel(ctx context.Context, videoID string, deviceLevel int) (providerMedia, error) {
	models, err := downloader.hongguoAppModels(ctx, []string{videoID}, deviceLevel)
	if err != nil {
		return providerMedia{}, err
	}
	model := models[videoID]
	if model == nil {
		return providerMedia{}, errors.New("红果 App 未返回该集的播放信息")
	}
	return selectHongguoAppMedia(model)
}

// hongguoAppModels 一次问一批 video_model，返回 视频ID -> video_model。
// 单集缺失不报错——调用方按缺少处理即可。
//
// 2026-10-29 起 preload 场景（dr_scene=preload）对同一份 body 稳定回 200 空包，
// 参考客户端也只走详情页场景，所以统一用 direct 路径。
func (downloader *Downloader) hongguoAppModels(ctx context.Context, videoIDs []string, deviceLevel int) (map[string]map[string]any, error) {
	return downloader.hongguoAppModelsAt(ctx, videoIDs, deviceLevel, hongguoAppDirectPath, false)
}

// hongguoAppModelsAt 按指定场景批量取 video_model：path 决定端点，
// preloadScene 决定是否附带 dr_scene=preload。
func (downloader *Downloader) hongguoAppModelsAt(ctx context.Context, videoIDs []string, deviceLevel int, path string, preloadScene bool) (map[string]map[string]any, error) {
	if len(videoIDs) == 0 {
		return map[string]map[string]any{}, nil
	}
	payload := map[string]any{
		"biz_param": map[string]any{
			"detail_page_version": 0, "device_level": deviceLevel, "disable_digg_stat": false,
			"need_all_video_definition": true,
			"need_mp4_align":            false, "use_os_player": false, "use_server_dns": false,
			"video_platform": 1024,
		},
		"mixed_video_id_map": map[string]any{"1004": videoIDs},
	}
	if preloadScene {
		payload["dr_scene"] = "preload"
	}
	result, err := downloader.hongguoAppRequest(ctx, http.MethodPost, path, nil, payload)
	if err != nil {
		return nil, err
	}
	data := nestedMap(result, "data")
	models := make(map[string]map[string]any, len(videoIDs))
	for _, videoID := range videoIDs {
		entry := nestedMap(data, videoID)
		if entry == nil {
			continue
		}
		if model, ok := entry["video_model"].(map[string]any); ok {
			models[videoID] = model
			continue
		}
		encoded, ok := entry["video_model"].(string)
		if !ok {
			continue
		}
		var model map[string]any
		decoder := json.NewDecoder(strings.NewReader(encoded))
		decoder.UseNumber()
		if err := decoder.Decode(&model); err != nil {
			continue
		}
		models[videoID] = model
	}
	return models, nil
}

// sampleHongguoQualities 批量取画质，用于详情页头部标注。按接口上限切批、
// 3 批并发；单批失败只是那几集没有数据，不影响其它集。
func (downloader *Downloader) sampleHongguoQualities(ctx context.Context, videoIDs []string) map[string]hongguoQuality {
	unique := make([]string, 0, len(videoIDs))
	seen := map[string]bool{}
	for _, videoID := range videoIDs {
		if hongguoNumericID.MatchString(videoID) && !seen[videoID] {
			seen[videoID] = true
			unique = append(unique, videoID)
		}
	}
	sampled := map[string]hongguoQuality{}
	if len(unique) == 0 {
		return sampled
	}
	var mu sync.Mutex
	var group sync.WaitGroup
	slots := make(chan struct{}, 3)
	for start := 0; start < len(unique); start += hongguoAppBatchLimit {
		end := min(start+hongguoAppBatchLimit, len(unique))
		batch := unique[start:end]
		group.Add(1)
		go func(batch []string) {
			defer group.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			ready := make(map[string]hongguoQuality, len(batch))
			models, err := downloader.hongguoAppModels(ctx, batch, hongguoAppPlaybackLevel)
			if err == nil {
				for _, videoID := range batch {
					model := models[videoID]
					if model == nil {
						continue
					}
					media, err := selectHongguoAppMedia(model)
					if err != nil {
						ready[videoID] = hongguoQuality{}
						continue
					}
					ready[videoID] = hongguoQuality{
						Definition: media.Definition, Width: media.Width, Height: media.Height,
						Bitrate: media.Bitrate, Quality: media.Quality, Playable: true,
					}
				}
			}
			mu.Lock()
			defer mu.Unlock()
			for videoID, quality := range ready {
				sampled[videoID] = quality
			}
		}(batch)
	}
	group.Wait()
	return sampled
}

func selectHongguoAppMedia(model map[string]any) (providerMedia, error) {
	variants := anyList(model["video_list"])
	if rows, ok := model["video_list"].(map[string]any); ok && len(variants) == 0 {
		keys := make([]string, 0, len(rows))
		for key := range rows {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			variants = append(variants, rows[key])
		}
	}
	duration, _ := strconv.ParseFloat(mapString(model, "video_duration", "duration"), 64)
	var keyErr error
	type scoredMedia struct {
		media providerMedia
		score int
	}
	var choices []scoredMedia
	for _, row := range variants {
		variant, _ := row.(map[string]any)
		meta := nestedMap(variant, "video_meta")
		if meta == nil {
			meta = variant
		}
		codec := strings.ToLower(mapString(meta, "codec_type"))
		if codec == "bytevc2" || strings.Contains(strings.ToLower(mapString(variant, "gear_des_key")), "bytevc2") {
			continue
		}
		addresses := hongguoMediaAddresses(variant)
		if len(addresses) == 0 {
			continue
		}
		media := providerMedia{Referer: "https://novel.snssdk.com/", Duration: time.Duration(duration * float64(time.Second))}
		encryption := nestedMap(variant, "encrypt_info")
		spade := mapString(encryption, "spade_a")
		if spade == "" {
			spade = mapString(variant, "spade_a")
		}
		if spade != "" || encryption["encrypt"] == true || mapString(encryption, "encryption_method") == "cenc-aes-ctr" {
			var err error
			media.CENCKey, err = hongguoContentKey(spade)
			if err != nil {
				keyErr = err
				continue
			}
		}
		height, _ := strconv.Atoi(mapString(meta, "vheight"))
		if definition, err := strconv.Atoi(hongguoQualityNumber.FindString(mapString(meta, "definition"))); err == nil && definition > 0 {
			height = definition
		} else if width, _ := strconv.Atoi(mapString(meta, "vwidth")); width > 0 && (height == 0 || width < height) {
			height = width
		}
		media.Quality = height
		media.Definition = mapString(meta, "definition")
		media.Width, _ = strconv.Atoi(mapString(meta, "vwidth"))
		media.Height, _ = strconv.Atoi(mapString(meta, "vheight"))
		media.Bitrate, _ = strconv.Atoi(mapString(meta, "bitrate", "real_bitrate"))
		quality := height * 10
		if codec == "h264" || codec == "avc1" {
			quality++
		}
		for _, address := range addresses {
			media.URL = address
			choices = append(choices, scoredMedia{media: media, score: quality})
		}
	}
	if len(choices) > 0 {
		sort.SliceStable(choices, func(i, j int) bool { return choices[i].score > choices[j].score })
		selected := choices[0].media
		for _, choice := range choices {
			selected.Variants = append(selected.Variants, choice.media)
		}
		return selected, nil
	}
	if keyErr != nil {
		return providerMedia{}, fmt.Errorf("红果 App 媒体密钥不可用: %w", keyErr)
	}
	return providerMedia{}, errHongguoAppNoPlayableVariant
}

func hongguoMediaAddresses(info map[string]any) []string {
	var addresses []string
	seen := map[string]bool{}
	var add func(any)
	add = func(value any) {
		switch value := value.(type) {
		case string:
			address := strings.TrimSpace(value)
			if len(address) > 8192 {
				return
			}
			if !isProviderHTTPMediaURL(address) {
				decoded, err := decodeHongguoBase64(address)
				if err != nil {
					return
				}
				address = strings.TrimSpace(string(decoded))
			}
			if isProviderHTTPMediaURL(address) && !seen[address] {
				seen[address] = true
				addresses = append(addresses, address)
			}
		case []any:
			for _, item := range value {
				add(item)
			}
		}
	}
	for _, key := range []string{"main_url", "backup_url", "backup_url_1", "backup_url_2", "backup_urls", "url_list"} {
		add(info[key])
	}
	return addresses
}
