package updater

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GitHub Releases 列表端点（per_page=20 足够覆盖 v2 全部预发布 + 稳定版）。
const ReleasesAPIURL = "https://api.github.com/repos/Adsryen/zhenguojian/releases?per_page=20"

// defaultMirrors 国内加速前缀候选（决策约定顺序），直连永远排在它们之前。
var defaultMirrors = []string{
	"https://gh-proxy.com/",
	"https://ghproxy.net/",
	"https://mirror.ghproxy.com/",
}

// perCandidateTimeout 每候选 10s 超时（决策约定）。
const perCandidateTimeout = 10 * time.Second

// NormalizePrefix 把用户输入的加速前缀归一为以 / 结尾的 https 前缀；空输入 → 空（用默认链）。
func NormalizePrefix(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if !strings.HasPrefix(p, "http://") && !strings.HasPrefix(p, "https://") {
		p = "https://" + p
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}

// ChainCandidates 生成候选 URL 链：用户前缀（若有）→ 直连 → 三个默认镜像。
// 首个成功即止；直连放最前是决策约定（能直连就直连）。
func ChainCandidates(rawURL, mirrorPrefix string) []string {
	out := make([]string, 0, 5)
	if p := NormalizePrefix(mirrorPrefix); p != "" {
		out = append(out, p+rawURL)
	}
	out = append(out, rawURL)
	for _, m := range defaultMirrors {
		out = append(out, m+rawURL)
	}
	return out
}

// FetchJSON 依次尝试候选链，首个 2xx 应答即返回其 body；每候选独立 10s 超时。
// 外层 ctx 取消则立即中止（不再走后续候选）。全部失败时汇总每候选原因。
func FetchJSON(ctx context.Context, client *http.Client, urls []string, limit int64) ([]byte, error) {
	if len(urls) == 0 {
		return nil, fmt.Errorf("候选链为空")
	}
	if client == nil {
		client = http.DefaultClient
	}
	if limit <= 0 {
		limit = 4 << 20 // 4MB 足够 releases?per_page=20
	}
	tries := make([]string, 0, len(urls))
	for i, u := range urls {
		body, err := fetchOnce(ctx, client, u, limit)
		if err == nil {
			return body, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		tries = append(tries, fmt.Sprintf("候选%d(%s): %v", i+1, hostOf(u), err))
	}
	return nil, fmt.Errorf("全部候选失败：%s", strings.Join(tries, "；"))
}

func fetchOnce(ctx context.Context, client *http.Client, u string, limit int64) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, perCandidateTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("请求构造失败: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "zhenguojian-v2-updater")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("网络失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, fmt.Errorf("应答读取失败: %w", err)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("应答为空")
	}
	return body, nil
}

func hostOf(u string) string {
	if parsed, err := url.Parse(u); err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return u
}
