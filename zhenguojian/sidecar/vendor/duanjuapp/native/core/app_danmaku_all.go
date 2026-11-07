package core

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"time"
)

// nativeDanmakuDocument is the on-disk/whole-episode form used when the user
// asks the downloader to keep danmaku.  It deliberately contains only plain
// data so it can be written next to the video and read back without Go.
type nativeDanmakuDocument struct {
	EpisodeID string               `json:"episodeId"`
	SeriesID  string               `json:"seriesId,omitempty"`
	Total     int64                `json:"total,omitempty"`
	FetchedAt int64                `json:"fetchedAt,omitempty"`
	Items     []hongguoDanmakuItem `json:"items"`
}

// hongguoDanmakuAll walks the episode's 30-second windows and returns one
// deduplicated document.  The cursor's danmaku_window_count histogram is used
// to skip windows the server already says are empty; that is much cheaper than
// asking for every window and waiting for an empty reply.
func (downloader *Downloader) hongguoDanmakuAll(ctx context.Context, seriesID, videoID string, duration int64) (nativeDanmakuDocument, error) {
	document := nativeDanmakuDocument{EpisodeID: videoID, SeriesID: seriesID, Items: []hongguoDanmakuItem{}}
	if !hongguoNumericID.MatchString(seriesID) || !hongguoNumericID.MatchString(videoID) {
		return document, errors.New("弹幕请求参数无效")
	}
	if duration <= 0 || duration > danmakuMaxDurationMS {
		return document, errors.New("弹幕时间范围无效")
	}
	seen := map[string]bool{}
	start := int64(0)
	failures := 0
	for page := 0; page < 200; page++ {
		if err := ctx.Err(); err != nil {
			return document, err
		}
		window, err := downloader.hongguoDanmakuBulkWindow(ctx, seriesID, videoID, start, duration)
		if err != nil {
			if ctx.Err() != nil {
				return document, err
			}
			// A sampled/empty reply is not fatal for the whole episode: skip
			// this 30-second window and keep going.  Three consecutive failures
			// mean the endpoint is actually unavailable, so surface the error
			// instead of silently returning a half-empty document.
			failures++
			if failures >= 3 {
				if len(document.Items) == 0 {
					return document, err
				}
				break
			}
			next := start + danmakuWindowMS
			if next >= duration {
				break
			}
			start = next
			select {
			case <-ctx.Done():
				return document, ctx.Err()
			case <-time.After(600 * time.Millisecond):
			}
			continue
		}
		failures = 0
		if window.Total > document.Total {
			document.Total = window.Total
		}
		for _, item := range window.Items {
			if item.ID == "" || seen[item.ID] {
				continue
			}
			seen[item.ID] = true
			document.Items = append(document.Items, item)
		}
		next := window.NextMS
		if len(window.Windows) > 0 {
			jump := nextDanmakuWindow(window.Windows, start)
			if jump == 0 {
				// A histogram that covers later windows but reports no positive
				// count means the rest of the episode is empty; stop instead of
				// asking the server again for every remaining 30 seconds.
				if hasDanmakuWindowAfter(window.Windows, start) {
					break
				}
			} else if jump > next && jump < duration {
				next = jump
			}
		}
		if next <= start || next >= duration {
			break
		}
		start = next
	}
	sort.SliceStable(document.Items, func(i, j int) bool {
		return document.Items[i].TimeMS < document.Items[j].TimeMS
	})
	document.FetchedAt = time.Now().UnixMilli()
	return document, nil
}

// hongguoDanmakuBulkWindow fetches one window and retries once when the
// server's histogram says there should be comments but the page came back
// sampled/empty.  The duration is nudged by 1ms so the retry misses the
// in-memory cache keyed by (series, video, start, duration).
func (downloader *Downloader) hongguoDanmakuBulkWindow(ctx context.Context, seriesID, videoID string, start, duration int64) (hongguoDanmakuPage, error) {
	window, err := downloader.hongguoDanmaku(ctx, seriesID, videoID, start, duration)
	if err != nil {
		select {
		case <-ctx.Done():
			return window, ctx.Err()
		case <-time.After(900 * time.Millisecond):
		}
		retryDuration := duration
		if retryDuration < danmakuMaxDurationMS {
			retryDuration++
		}
		retry, retryErr := downloader.hongguoDanmaku(ctx, seriesID, videoID, start, retryDuration)
		if retryErr != nil {
			return window, err
		}
		return retry, nil
	}
	expected := window.Windows[strconv.FormatInt(start, 10)]
	if expected <= 0 || int64(len(window.Items)) >= expected {
		return window, nil
	}
	select {
	case <-ctx.Done():
		return window, ctx.Err()
	case <-time.After(900 * time.Millisecond):
	}
	retryDuration := duration
	if retryDuration < danmakuMaxDurationMS {
		retryDuration++
	}
	retry, retryErr := downloader.hongguoDanmaku(ctx, seriesID, videoID, start, retryDuration)
	if retryErr == nil && len(retry.Items) > len(window.Items) {
		return retry, nil
	}
	return window, nil
}

// nextDanmakuWindow returns the nearest non-empty 30-second window after
// "after", or 0 when the remaining windows are all empty.
func nextDanmakuWindow(windows map[string]int64, after int64) int64 {
	next := int64(0)
	for key, count := range windows {
		if count <= 0 {
			continue
		}
		value, err := strconv.ParseInt(key, 10, 64)
		if err != nil || value <= after || value > danmakuMaxDurationMS {
			continue
		}
		if next == 0 || value < next {
			next = value
		}
	}
	return next
}

func hasDanmakuWindowAfter(windows map[string]int64, after int64) bool {
	for key := range windows {
		value, err := strconv.ParseInt(key, 10, 64)
		if err == nil && value > after && value <= danmakuMaxDurationMS {
			return true
		}
	}
	return false
}

// nativeDanmakuAll is the FFI action used by the downloader.  Like
// nativeDanmaku it resolves the red-book ids through the playback session, so
// a caller cannot ask for arbitrary series/video ids.
func (engine *nativeEngine) nativeDanmakuAll(ctx context.Context, input nativeInput) (nativeDanmakuDocument, error) {
	if input.DurationMS <= 0 || input.DurationMS > danmakuMaxDurationMS {
		return nativeDanmakuDocument{}, errors.New("弹幕时间范围无效")
	}
	engine.mu.Lock()
	choice, exists := engine.playbacks[input.PlaybackSession]
	engine.mu.Unlock()
	if !exists || time.Since(choice.created) > 12*time.Hour {
		return nativeDanmakuDocument{}, errors.New("播放会话已过期")
	}
	if choice.danmakuSeries == "" || choice.danmakuVideo == "" {
		return nativeDanmakuDocument{}, errors.New("本集暂不支持弹幕")
	}
	document, err := engine.downloader.hongguoDanmakuAll(ctx, choice.danmakuSeries, choice.danmakuVideo, input.DurationMS)
	if err != nil {
		return nativeDanmakuDocument{}, err
	}
	engine.mu.Lock()
	_, exists = engine.playbacks[input.PlaybackSession]
	engine.mu.Unlock()
	if !exists {
		return nativeDanmakuDocument{}, errors.New("播放会话已过期")
	}
	return document, ctx.Err()
}
