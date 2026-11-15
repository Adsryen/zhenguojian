package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

func NativePosterMetadata(payload string) string {
	var input struct {
		Dramas []nativeDrama `json:"dramas"`
	}
	var result any
	err := json.Unmarshal([]byte(payload), &input)
	if err == nil {
		nativeState.Lock()
		engine := nativeState.engine
		nativeState.Unlock()
		if engine == nil {
			err = errors.New("本地核心尚未启动")
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			result, err = engine.posterMetadata(ctx, input.Dramas)
			cancel()
		}
	}
	envelope := map[string]any{"ok": err == nil}
	if err != nil {
		envelope["error"] = publicError(err).Error()
	} else {
		envelope["data"] = result
	}
	encoded, _ := json.Marshal(envelope)
	return string(encoded)
}

func (engine *nativeEngine) posterMetadata(ctx context.Context, dramas []nativeDrama) (any, error) {
	if len(dramas) == 0 || len(dramas) > 18 {
		return nil, errors.New("海报信息每批需要 1–18 部剧")
	}
	ids := make([]string, 0, len(dramas))
	seen := map[string]bool{}
	for _, drama := range dramas {
		source, id, valid := splitProviderDramaID(drama.ID)
		if !valid || source != sourceHongguo || !hongguoNumericID.MatchString(id) {
			return nil, errors.New("海报剧集 ID 无效")
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	body, err := engine.downloader.hongguoAppRequest(ctx, http.MethodPost,
		"/novel/player/multi_video_detail/preload/v1", nil, map[string]any{
			"series_id": strings.Join(ids, ","),
			"dr_scene":  "preload",
			"biz_param": map[string]any{
				"caller_scene": "three_col", "detail_page_version": 0,
				"disable_digg_stat": false, "disable_video_relate_book": true,
				"need_all_video_definition": false, "need_mp4_align": false,
				"screen_width_px": "1080", "source": 7,
				"use_os_player": false, "use_server_dns": false,
			},
		})
	if err != nil {
		return nil, err
	}
	data := nestedMap(body, "data")
	items := make([]nativeDrama, 0, len(ids))
	for _, drama := range dramas {
		_, id, _ := splitProviderDramaID(drama.ID)
		raw := hongguoDramaFromAny(data[id], "")
		if raw.ID != drama.ID {
			continue
		}
		items = append(items, mergeNativeDrama(drama, nativeNormalize(raw)))
	}
	if len(items) == 0 {
		return nil, errors.New("批量海报信息未返回所请求的剧集")
	}
	return map[string]any{"items": items}, nil
}

func posterHeat(row map[string]any) string {
	for _, key := range []string{"sub_title_list", "series_sub_title_list"} {
		for _, value := range anyList(row[key]) {
			label := nestedMap(value)
			if mapInt64(label, "data_type") == 27 {
				if text := mapString(label, "content"); text != "" {
					return text
				}
			}
		}
	}
	return hongguoHeat(row)
}
