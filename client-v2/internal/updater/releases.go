package updater

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Asset 是发布资产（name + 下载地址 + 字节数，字节数用于下载完成校验）。
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
}

// Release 是 GitHub release 的领域模型（应答字段已重命名对齐前端契约）。
type Release struct {
	Tag         string  `json:"tag"`
	Version     string  `json:"version"`
	Prerelease  bool    `json:"prerelease"`
	PublishedAt string  `json:"publishedAt"`
	Body        string  `json:"body"`
	Assets      []Asset `json:"assets"`
}

// FindAsset 按名取资产；names 传空串取默认主程序资产。
func (r *Release) FindAsset(name string) (*Asset, error) {
	if r == nil {
		return nil, fmt.Errorf("无可用发布版本")
	}
	if name == "" {
		name = DefaultExeAsset
	}
	for i := range r.Assets {
		if strings.EqualFold(r.Assets[i].Name, name) {
			return &r.Assets[i], nil
		}
	}
	return nil, fmt.Errorf("发布 %s 中未找到资产 %s", r.Tag, name)
}

// SelectableAssets 只保留 v2 约定可下载的资产（主程序 exe 与 app.zip；runtime 不列）。
func (r *Release) SelectableAssets() []Asset {
	out := []Asset{}
	for _, a := range r.Assets {
		low := strings.ToLower(a.Name)
		if low == strings.ToLower(DefaultExeAsset) || low == strings.ToLower(DefaultAppZipAsset) {
			out = append(out, a)
		}
	}
	return out
}

// v2 发布资产命名约定（见任务书）：v1 资产流不管。
const (
	DefaultExeAsset     = "zhenGuoJian-v2.exe"
	DefaultAppZipAsset  = "zhenGuoJian-v2-app.zip"
	DefaultRuntimeAsset = "zhenGuoJian-v2-runtime.zip"
)

// IsV2Release 判定发布是否属于 v2 发布流：携带任一 v2 约定资产名。
// 仓库同时挂 v1.* 稳定发布（资产 zhenGuoJian-windows-x64.zip），必须排除，
// 否则 dev/低版本本地会收到 v1.x「更新」。
func IsV2Release(r Release) bool {
	for _, a := range r.Assets {
		low := strings.ToLower(a.Name)
		if low == strings.ToLower(DefaultExeAsset) ||
			low == strings.ToLower(DefaultAppZipAsset) ||
			low == strings.ToLower(DefaultRuntimeAsset) {
			return true
		}
	}
	return false
}

type rawAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type rawRelease struct {
	TagName     string     `json:"tag_name"`
	Prerelease  bool       `json:"prerelease"`
	PublishedAt string     `json:"published_at"`
	Body        string     `json:"body"`
	Assets      []rawAsset `json:"assets"`
}

// ParseReleases 解析 /releases?per_page=20 的 JSON 数组。
// tag 非 vX.Y.Z(-pre.N) 形态的发布整条跳过（v1.* 之外的杂物、草稿等）。
func ParseReleases(data []byte) ([]Release, error) {
	var raws []rawRelease
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, fmt.Errorf("发布列表解析失败：%w", err)
	}
	out := make([]Release, 0, len(raws))
	for _, rr := range raws {
		if !IsValidVersion(rr.TagName) {
			continue
		}
		rel := Release{
			Tag:         strings.TrimSpace(rr.TagName),
			Version:     strings.TrimSpace(rr.TagName),
			Prerelease:  rr.Prerelease,
			PublishedAt: rr.PublishedAt,
			Body:        rr.Body,
		}
		for _, a := range rr.Assets {
			rel.Assets = append(rel.Assets, Asset{Name: a.Name, URL: a.BrowserDownloadURL, Size: a.Size})
		}
		// v1 发布流不管：无 v2 约定资产（zhenGuoJian-v2*）的发布整条剔除。
		if !IsV2Release(rel) {
			continue
		}
		out = append(out, rel)
	}
	return out, nil
}

// PickLatest 在候选发布里按版本号取最新（不是 published_at——同 tag 重传不改时间的场景靠版本兜底）。
// includePre=false 时排除 prerelease。无候选返回 nil。
func PickLatest(releases []Release, includePre bool) *Release {
	var best *Release
	var bestV Version
	for i := range releases {
		r := &releases[i]
		if r.Prerelease && !includePre {
			continue
		}
		v, err := ParseVersion(r.Tag)
		if err != nil {
			continue // ParseReleases 已过滤，防御双保险
		}
		if best == nil || v.Compare(bestV) > 0 {
			best, bestV = r, v
		}
	}
	return best
}
