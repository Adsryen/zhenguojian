package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

// hongguoLandpage 拉取目录页。2026-11 起上游对 comic_series 场景拒绝 POST
// 表单（HTTP 200 + 空体），但对等价的 GET 查询串正常出数；真人剧/AI 剧两种
// 形态都可用。这里保持 POST 为主、空应答时改 GET 重试一次，两种场景形态
// 都能覆盖，也提前免疫其余场景的同类迁移。
// hongguoAppCatalogPath 是 App 网关目录页路径。
const hongguoAppCatalogPath = "/reading/distribution/category/landpage/v/"

func (downloader *Downloader) hongguoLandpage(ctx context.Context, payload map[string]any) (map[string]any, error) {
	result, err := downloader.hongguoAppRequest(ctx, http.MethodPost, hongguoAppCatalogPath, nil, payload)
	if err == nil || !isHongguoAppEmptyBody(err) {
		return result, err
	}
	result, getErr := downloader.hongguoAppRequest(ctx, http.MethodGet, hongguoAppCatalogPath, hongguoLandpageQuery(payload), nil)
	if getErr == nil {
		return result, nil
	}
	return result, err
}

// hongguoLandpageQuery 把 POST 表单字段搬进查询串；select_items 按上游 GET
// 形态以 JSON 字符串整体传递。
func hongguoLandpageQuery(payload map[string]any) url.Values {
	query := url.Values{}
	for key, value := range payload {
		switch typed := value.(type) {
		case string:
			query.Set(key, typed)
		case int:
			query.Set(key, strconv.Itoa(typed))
		case bool:
			query.Set(key, strconv.FormatBool(typed))
		default:
			if encoded, err := json.Marshal(value); err == nil {
				query.Set(key, string(encoded))
			}
		}
	}
	return query
}

func isHongguoAppEmptyBody(err error) bool {
	return errors.Is(err, errHongguoAppEmptyBody)
}
