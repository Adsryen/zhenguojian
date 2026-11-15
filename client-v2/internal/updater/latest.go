package updater

// D1 主路径：读随 release 发布的 latest.json 资产发现新版本，全程不碰
// api.github.com。依据（实测，dev-plan-2026-11.md 第三节）：公开 gh 加速镜像只
// 代理 github.com 形状 URL——api.github.com 经 gh-proxy.com 必然 403 rate limit、
// 经 ghproxy.net 403 Invalid input；而 releases.atom / releases/latest/download
// 经同一前缀 200。所以元数据也走 github.com 形状，镜像链才能整体生效。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// 仓库发布面 URL（github.com 形状；镜像代理的就是这一侧）。
const (
	RepoReleaseBase = "https://github.com/Adsryen/zhenguojian/releases"
	// LatestJSONStableURL 稳定通道：GitHub 重定向到最新【非预发布】release 的该资产。
	LatestJSONStableURL = RepoReleaseBase + "/latest/download/latest.json"
	// ReleasesAtomURL 发布 Atom 订阅（最新 10 条，新→旧），预发布通道用它拿最新 tag。
	ReleasesAtomURL = RepoReleaseBase + ".atom"
)

// LatestJSONSchema 本客户端认识的 latest.json 契约版本（契约见 dev-plan-2026-11.md 第三节）。
// schema 不认必须明确报错，绝不静默当作「无更新」。
const LatestJSONSchema = 1

// metaFetchLimit atom / latest.json 应答体积上限（feed 实测 10 entry ≈27KB，1MB 富余）。
const metaFetchLimit int64 = 1 << 20

// ReleaseDownloadURL 合成 tag 下某资产的 github.com 形状下载地址（镜像可代理）。
// tag/name 均已在调用侧过合法性校验，字符集限 [A-Za-z0-9._-]，无需再转义。
func ReleaseDownloadURL(tag, name string) string {
	return RepoReleaseBase + "/download/" + strings.TrimSpace(tag) + "/" + strings.TrimSpace(name)
}

// latestJSONURL 指定 tag 的 latest.json 地址（预发布通道经 atom 解析出 tag 后使用）。
func latestJSONURL(tag string) string {
	return ReleaseDownloadURL(tag, "latest.json")
}

// IsPrereleaseTag 依 tag 文本判预发布（含 -alpha/-beta/-rc 等 semver 前缀段）。
// latest.json 通道不用 API 的 prerelease 字段（我们根本不打 API）；契约里的
// 布尔值只做一致性对账，判定以 tag 文本为准。非法版本号一律 false。
func IsPrereleaseTag(tag string) bool {
	v, err := ParseVersion(tag)
	return err == nil && v.Pre != ""
}

// ---------- releases.atom 解析 ----------

// atomLinkTagRe 抓 <link … href="https://github.com/<owner>/<repo>/releases/tag/<tag>"/>。
// 基于原始字节匹配：属性顺序、命名空间前缀（atom:/media:）、<content> 里的转义
// HTML（&lt;a href=&quot;…&quot;&gt; 原始形态没有裸引号）都不构成干扰；feed 级
// href 以 …/releases 或 …/releases.atom 结尾，天然不匹配。
var atomLinkTagRe = regexp.MustCompile(`href="[^"]*/releases/tag/([^"'<>?\s]+)"`)

// atomIDTagRe 兜底：GitHub 实测 <id>tag:github.com,2008:Repository/<数字>/<tag></id>，
// link 缺失/被改写时从 id 取。
var atomIDTagRe = regexp.MustCompile(`Repository/\d+/([^<>"'\s]+)`)

// LatestTagFromAtom 从 releases.atom 取最新 release tag（条目文档序即新→旧）。
// 返回第一个形态合法（IsValidVersion）的 tag；v1.* 也可能混在 feed 里，这里不
// 判 v2——由后续 latest.json 校验（schema/资产/prerelease 对账）把关，不合格
// 整体走错误→兜底，不在本函数里静默吞掉。
func LatestTagFromAtom(data []byte) (string, error) {
	for _, m := range atomLinkTagRe.FindAllSubmatch(data, -1) {
		if tag := strings.TrimSpace(string(m[1])); IsValidVersion(tag) {
			return tag, nil
		}
	}
	for _, m := range atomIDTagRe.FindAllSubmatch(data, -1) {
		if tag := strings.TrimSpace(string(m[1])); IsValidVersion(tag) {
			return tag, nil
		}
	}
	return "", fmt.Errorf("releases.atom 应答中未解析到合法发布 tag")
}

// ---------- latest.json 解析与校验 ----------

// latestManifestAsset 契约资产项。exe 要求 url/size/sha256 全齐；其余资产缺 url
// 时按 tag+name 合成下载地址（github.com 形状，镜像可代理）。
type latestManifestAsset struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	URL    string `json:"url"`
}

type latestManifest struct {
	Schema      int    `json:"schema"`
	GeneratedAt string `json:"generatedAt"`
	Tag         string `json:"tag"`
	Version     string `json:"version"`
	Prerelease  bool   `json:"prerelease"`
	Notes       string `json:"notes"`
	Assets      struct {
		Exe        latestManifestAsset `json:"exe"`
		AppZip     latestManifestAsset `json:"appZip"`
		RuntimeZip latestManifestAsset `json:"runtimeZip"`
		BuildInfo  latestManifestAsset `json:"buildInfo"`
	} `json:"assets"`
}

// sha256HexRe 64 位十六进制（大小写均可，CI 产出未约定 case）。
var sha256HexRe = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

func isSHA256Hex(s string) bool {
	return sha256HexRe.MatchString(strings.TrimSpace(s))
}

func isHTTPURL(u string) bool {
	u = strings.ToLower(strings.TrimSpace(u))
	return strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://")
}

// sameVersionText 归一 v 前缀后比较（契约示例 version 不带 v、tag 带，两者必须同义）。
func sameVersionText(a, b string) bool {
	trim := func(s string) string { return strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V") }
	return strings.EqualFold(trim(a), trim(b))
}

// ParseLatestJSON 解析并校验 latest.json → Release。任何契约不合格都带原因明确
// 报错（schema 不认 / tag 非法 / version-tag 不一致 / exe 缺 url|size|sha256 /
// prerelease 布尔与 tag 文本冲突 / 无 v2 约定资产），调用方不得当「无更新」吞掉。
func ParseLatestJSON(data []byte) (*Release, error) {
	var m latestManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("latest.json 解析失败：%v", err)
	}
	if m.Schema != LatestJSONSchema {
		return nil, fmt.Errorf("latest.json schema=%d 不受支持（本客户端只认 schema=%d，请升级客户端）", m.Schema, LatestJSONSchema)
	}
	tag := strings.TrimSpace(m.Tag)
	if !IsValidVersion(tag) {
		return nil, fmt.Errorf("latest.json tag %q 不是合法版本号（期望 vX.Y.Z[-pre.N]）", m.Tag)
	}
	if v := strings.TrimSpace(m.Version); v != "" && !sameVersionText(v, tag) {
		return nil, fmt.Errorf("latest.json version %q 与 tag %q 不一致", m.Version, m.Tag)
	}
	rel := &Release{
		Tag:         tag,
		Version:     tag,
		Prerelease:  IsPrereleaseTag(tag),
		PublishedAt: strings.TrimSpace(m.GeneratedAt),
		Body:        m.Notes,
	}
	exe := m.Assets.Exe
	if exe.Name = strings.TrimSpace(exe.Name); exe.Name == "" {
		exe.Name = DefaultExeAsset
	}
	if !isHTTPURL(exe.URL) {
		return nil, fmt.Errorf("latest.json assets.exe.url 缺失或不是 http(s) 地址：%q", exe.URL)
	}
	if exe.Size <= 0 {
		return nil, fmt.Errorf("latest.json assets.exe.size 缺失或非正数：%d", exe.Size)
	}
	if !isSHA256Hex(exe.SHA256) {
		return nil, fmt.Errorf("latest.json assets.exe.sha256 缺失或不是 64 位十六进制：%q", exe.SHA256)
	}
	rel.Assets = append(rel.Assets, Asset{Name: exe.Name, URL: strings.TrimSpace(exe.URL), Size: exe.Size})
	for _, a := range []latestManifestAsset{m.Assets.AppZip, m.Assets.RuntimeZip, m.Assets.BuildInfo} {
		name := strings.TrimSpace(a.Name)
		if name == "" {
			continue
		}
		if a.SHA256 != "" && !isSHA256Hex(a.SHA256) {
			return nil, fmt.Errorf("latest.json 资产 %s 的 sha256 格式非法：%q", name, a.SHA256)
		}
		url := strings.TrimSpace(a.URL)
		if url == "" {
			url = ReleaseDownloadURL(tag, name)
		}
		rel.Assets = append(rel.Assets, Asset{Name: name, URL: url, Size: a.Size})
	}
	// v2 发布流把关（同 ParseReleases）：不含 v2 约定资产名的整份拒绝。
	if !IsV2Release(*rel) {
		return nil, fmt.Errorf("latest.json（tag %s）不含任何 v2 约定资产，拒绝按 v2 更新处理", tag)
	}
	// 契约 prerelease 布尔与 tag 文本判定必须一致（CI 产错即报错，不各信各的）。
	if m.Prerelease != rel.Prerelease {
		return nil, fmt.Errorf("latest.json prerelease=%v 与 tag %s 按文本判定=%v 冲突", m.Prerelease, tag, rel.Prerelease)
	}
	return rel, nil
}

// ---------- 取数编排（走现有 ChainCandidates，不新写候选逻辑） ----------

// fetchLatestRelease 主路径两段式：
//   - 稳定通道：releases/latest/download/latest.json（GitHub 语义=最新非预发布，
//     预发布与否以 tag 文本复核，异常即报错）。
//   - 测试版通道：releases.atom → 最新 tag → releases/download/<tag>/latest.json。
//
// 每个 URL 都过 ChainCandidates（用户前缀→直连→内置三镜像）。
func fetchLatestRelease(ctx context.Context, client *http.Client, set Settings) (*Release, error) {
	if !set.Prerelease {
		data, err := FetchJSON(ctx, client, ChainCandidates(LatestJSONStableURL, set.MirrorPrefix), metaFetchLimit)
		if err != nil {
			return nil, fmt.Errorf("稳定通道 latest.json 获取失败：%w", err)
		}
		rel, err := ParseLatestJSON(data)
		if err != nil {
			return nil, err
		}
		if rel.Prerelease {
			return nil, fmt.Errorf("稳定通道返回了预发布 tag %s（releases/latest 语义异常）", rel.Tag)
		}
		return rel, nil
	}
	atom, err := FetchJSON(ctx, client, ChainCandidates(ReleasesAtomURL, set.MirrorPrefix), metaFetchLimit)
	if err != nil {
		return nil, fmt.Errorf("发布 Atom 订阅获取失败：%w", err)
	}
	tag, err := LatestTagFromAtom(atom)
	if err != nil {
		return nil, err
	}
	data, err := FetchJSON(ctx, client, ChainCandidates(latestJSONURL(tag), set.MirrorPrefix), metaFetchLimit)
	if err != nil {
		return nil, fmt.Errorf("tag %s 的 latest.json 获取失败：%w", tag, err)
	}
	rel, err := ParseLatestJSON(data)
	if err != nil {
		return nil, err
	}
	// atom 发现的 tag 与 manifest 自述 tag 必须一致（防止把旧 release 的
	// latest.json 误传到新 tag 下这类 CI 事故静默用错版本）。
	if !strings.EqualFold(rel.Tag, tag) {
		return nil, fmt.Errorf("latest.json tag %q 与 Atom 发现的最新 tag %q 不一致", rel.Tag, tag)
	}
	return rel, nil
}
