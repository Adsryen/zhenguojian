package core

import (
	"context"
	"sort"
	"strings"
	"time"
)

// 下载队列一次会排几十上百集，每集单独请求一次取流接口既慢，也更容易踩到
// 风控的「200 + 空 body」。这里按接口上限把同一部剧待下载的集数合成一批
// （15 个 ID 一次）预先解析，结果按 video_id 缓存；真正的下载任务再命中
// 缓存，基本不再发单集请求。缓存只管 App 接口拿到的片源，网页/公开兜底
// 不入缓存，避免把 360P 当成正常结果反复复用。
const (
	hongguoMediaCacheTTL   = 10 * time.Minute
	hongguoMediaCacheLimit = 4096
	// 低于这个档位就认为可能被降级：先按 device_level=4 再要一次，服务端
	// 在 4 下会把 bytevc2 档位换成可解的 bytevc1/h264。
	hongguoMediaLowQuality = 720
)

type hongguoMediaCacheEntry struct {
	media   providerMedia
	expires time.Time
}

func hongguoVideoIDOf(chapter Chapter) string {
	if !strings.HasPrefix(chapter.VideoURL, "hongguo-cenc://") {
		return ""
	}
	id := strings.TrimPrefix(chapter.VideoURL, "hongguo-cenc://")
	if !hongguoNumericID.MatchString(id) {
		return ""
	}
	return id
}

func (downloader *Downloader) cachedHongguoAppMedia(videoID string) (providerMedia, bool) {
	client := downloader.hongguoClient()
	client.mu.Lock()
	defer client.mu.Unlock()
	entry, found := client.mediaCache[videoID]
	if !found || time.Now().After(entry.expires) {
		if found {
			delete(client.mediaCache, videoID)
		}
		return providerMedia{}, false
	}
	return entry.media, true
}

func (downloader *Downloader) storeHongguoAppMedia(videoID string, media providerMedia) {
	if videoID == "" || media.URL == "" {
		return
	}
	client := downloader.hongguoClient()
	now := time.Now()
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.mediaCache) >= hongguoMediaCacheLimit {
		for id, entry := range client.mediaCache {
			if now.After(entry.expires) {
				delete(client.mediaCache, id)
			}
		}
		if len(client.mediaCache) >= hongguoMediaCacheLimit {
			client.mediaCache = map[string]hongguoMediaCacheEntry{}
		}
	}
	client.mediaCache[videoID] = hongguoMediaCacheEntry{media: media, expires: now.Add(hongguoMediaCacheTTL)}
}

// markHongguoAppSeriesWorks 记住「这部剧用 App 接口成功拿到过片源」。之后
// 同一部剧某一集突然只剩网页 360P 时，下载队列就能判断这是临时风控，而不是
// 这部剧本身只有低清。
func (downloader *Downloader) markHongguoAppSeriesWorks(seriesID string) {
	if seriesID == "" {
		return
	}
	client := downloader.hongguoClient()
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.appWorks == nil {
		client.appWorks = map[string]bool{}
	}
	client.appWorks[seriesID] = true
}

func (downloader *Downloader) hongguoAppSeriesWorks(seriesID string) bool {
	client := downloader.hongguoClient()
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.appWorks[seriesID]
}

// prefetchHongguoAppMedia 把一批 video_id 合成 ≤15 个一组预先解析并缓存。
// 返回实际缓存成功的集数；单批失败只是少缓存几集，不影响调用方。
func (downloader *Downloader) prefetchHongguoAppMedia(ctx context.Context, videoIDs []string) int {
	client := downloader.hongguoClient()
	client.prefetchMu.Lock()
	defer client.prefetchMu.Unlock()
	unique := make([]string, 0, len(videoIDs))
	seen := map[string]bool{}
	for _, id := range videoIDs {
		if !hongguoNumericID.MatchString(id) || seen[id] {
			continue
		}
		seen[id] = true
		if _, cached := downloader.cachedHongguoAppMedia(id); cached {
			continue
		}
		unique = append(unique, id)
	}
	sort.Strings(unique)
	stored := 0
	low := make([]string, 0, len(unique))
	for start := 0; start < len(unique); start += hongguoAppBatchLimit {
		end := min(start+hongguoAppBatchLimit, len(unique))
		models, err := downloader.hongguoAppModelsAt(ctx, unique[start:end], hongguoAppPlaybackLevel, hongguoAppDirectPath, false)
		if err != nil {
			return stored
		}
		for id, model := range models {
			if model == nil {
				continue
			}
			media, err := selectHongguoAppMedia(model)
			if err != nil {
				continue
			}
			if media.Quality > 0 && media.Quality < hongguoMediaLowQuality {
				low = append(low, id)
				continue
			}
			downloader.storeHongguoAppMedia(id, media)
			stored++
		}
	}
	for start := 0; start < len(low); start += hongguoAppBatchLimit {
		end := min(start+hongguoAppBatchLimit, len(low))
		models, err := downloader.hongguoAppModelsAt(ctx, low[start:end], hongguoAppFallbackLevel, hongguoAppDirectPath, false)
		if err != nil {
			return stored
		}
		for _, id := range low[start:end] {
			model := models[id]
			if model == nil {
				continue
			}
			media, err := selectHongguoAppMedia(model)
			if err != nil || media.Quality <= 0 {
				continue
			}
			downloader.storeHongguoAppMedia(id, media)
			stored++
		}
	}
	return stored
}

// pendingHongguoVideoIDs 收集同一部剧里排队/下载中的分集 ID，当前任务排在最前。
func (manager *nativeDownloads) pendingHongguoVideoIDs(job nativeDownloadJob, limit int) []string {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	type entry struct {
		index   int
		id      string
		current bool
	}
	entries := make([]entry, 0, len(manager.jobs))
	for _, record := range manager.jobs {
		if record.Archived || record.Drama.ID != job.Drama.ID {
			continue
		}
		if record.State != "queued" && record.State != "downloading" {
			continue
		}
		id := hongguoVideoIDOf(record.Chapter)
		if id == "" {
			continue
		}
		entries = append(entries, entry{index: record.Index, id: id, current: record.ID == job.ID})
	}
	sort.SliceStable(entries, func(left, right int) bool {
		if entries[left].current != entries[right].current {
			return entries[left].current
		}
		return entries[left].index < entries[right].index
	})
	ids := make([]string, 0, min(limit, len(entries)))
	for _, item := range entries {
		ids = append(ids, item.id)
		if len(ids) >= limit {
			break
		}
	}
	return ids
}
