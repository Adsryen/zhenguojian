package core

// 黄果AI edge/request 信封层（对齐 guoapp 参照 reference/guoapp native/core/provider_huangguo_api.go）。
//
// 站点把 /api/* 数据端点收拢进单一边缘代理：POST {base}/api/edge/request，
// 请求体 {"method":"GET","path":"<内部路径>","body":"","content_type":""}，
// 响应 {"status":1,"msg":"","data":...}；status!=1 即业务失败。
// 2026-11-07 线上实测：裸 GET /api/videos/category/... 已失效（返回 {"error":"请求无法处理"}），
// 信封 POST 在存活镜像（thu.lbvrhyir.cc / thu.agdkczeyx.cc / 漂移域 26d.gitiecgad.cc）上返回 status:1。
//
// 域名漂移处理：主域 huangguoai.com 超时时，池内镜像 thu.lbvrhyir.cc、thu.agdkczeyx.cc 的页面会
// 301 到未被池收录的 26d.gitiecgad.cc。本文件在重定向链上「收割」落地域名（huangguoAILearnDrift），
// 后续边缘调用优先打收割到的域名；POST 重定向默认会被 net/http 降级为 GET 并丢体，
// 因此这里用独立 scoped client 在 CheckRedirect 中把 301/302/303 重新签回 POST+信封体。
//
// 2026-11-08 实测（代理出口、信封内部路径用本仓 /api/videos/category/ai-duanju）：同族 fork
// （kuoluoss/zhenguojian，guoapp3 系）使用的备用域名家族 huangguoai.com、fdu/pku/thu/ttvoij.
// ediayikma.cc、thu.agdkczeyx.cc 全部存活（status:1），且已全部收录在 huangguoAIBaseMirrors
// 硬镜像段（provider_huangguo.go，池序 301 漂移池→硬镜像→生成域→cfg 不变，本次无需增改）；
// huangguoai.fit 对 /api/edge/request 返回 404 HTML，判死不入池。四族后缀的 sha256 生成域
// 上游均识别（实测各族首个生成域 f312a.lbvrhyir.cc / 7ef2d0.xxbgwypx.cc / e2fa.ediayikma.cc /
// 82cccb.agdkczeyx.cc 全部 status:1），huangguoAIMirrorDomains 无需扩充。注意：fork 的内部
// 路径 /api/v1/drama/* 本上游不识别（一律返回「请求无法处理」），探活必须用本仓 /api/videos/* 内部路径。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	huangguoAIEdgePath       = "/api/edge/request"
	huangguoAISearchPageSize = 20
	huangguoAIDriftPoolCap   = 6
	huangguoAIDriftHopMax    = 5
	huangguoAIMirrorProbeTTL = 5 * time.Second
	huangguoAISourceIDMaxLen = 256
)

var (
	// 漂移域名池：scheme://host[:port]，最近收割到的在前。跨 Downloader 实例共享
	// （生产单 Downloader；测试通过 huangguoAIDriftReset 隔离）。
	huangguoAIDriftMu    sync.Mutex
	huangguoAIDriftHosts []string

	// auto-envelope 冷却：整池网络级失败后，huangguoAIAutoEnvelope 暂停一段时间，
	// 让 /api/* GET 直接走旧语义，避免每次分类抓取都重扫一遍死池。
	huangguoAIEdgeCooldownMu     sync.Mutex
	huangguoAIEdgeCooldownUntil  time.Time
	huangguoAIEdgeCooldownWindow = 60 * time.Second
)

func huangguoAIEdgeInCooldown() bool {
	huangguoAIEdgeCooldownMu.Lock()
	defer huangguoAIEdgeCooldownMu.Unlock()
	return time.Now().Before(huangguoAIEdgeCooldownUntil)
}

func huangguoAIEdgeMarkCooldown() {
	huangguoAIEdgeCooldownMu.Lock()
	huangguoAIEdgeCooldownUntil = time.Now().Add(huangguoAIEdgeCooldownWindow)
	huangguoAIEdgeCooldownMu.Unlock()
}

func huangguoAIEdgeClearCooldown() {
	huangguoAIEdgeCooldownMu.Lock()
	huangguoAIEdgeCooldownUntil = time.Time{}
	huangguoAIEdgeCooldownMu.Unlock()
}

// huangguoAIEdgeFailure 表示整池候选都没能给出合法信封应答（网络级失败），
// 与「API 业务拒绝（status!=1）」区分开：前者不必再走 HTML 兜底重扫一遍死池。
type huangguoAIEdgeFailure struct {
	tried int
	last  error
}

func (e *huangguoAIEdgeFailure) Error() string {
	return fmt.Sprintf("黄果AI边缘接口请求失败：已尝试 %d 个域名，最后错误：%v", e.tried, e.last)
}

func (e *huangguoAIEdgeFailure) Unwrap() error { return e.last }

func huangguoAIEdgeExhausted(err error) bool {
	var failure *huangguoAIEdgeFailure
	return errors.As(err, &failure)
}

func huangguoAILearnDrift(target *url.URL) {
	if target == nil {
		return
	}
	host := strings.ToLower(target.Hostname())
	if host == "" || host == "huangguoai.com" {
		return
	}
	entry := target.Scheme + "://" + target.Host
	huangguoAIDriftMu.Lock()
	defer huangguoAIDriftMu.Unlock()
	for _, existing := range huangguoAIDriftHosts {
		if strings.EqualFold(existing, entry) {
			return
		}
	}
	huangguoAIDriftHosts = append([]string{entry}, huangguoAIDriftHosts...)
	if len(huangguoAIDriftHosts) > huangguoAIDriftPoolCap {
		huangguoAIDriftHosts = huangguoAIDriftHosts[:huangguoAIDriftPoolCap]
	}
}

func huangguoAIDriftSnapshot() []string {
	huangguoAIDriftMu.Lock()
	defer huangguoAIDriftMu.Unlock()
	return append([]string(nil), huangguoAIDriftHosts...)
}

func huangguoAIDriftReset() {
	huangguoAIDriftMu.Lock()
	huangguoAIDriftHosts = nil
	huangguoAIDriftMu.Unlock()
}

// huangguoAIReplyLooksValid 判定响应是否为信封协议应答（带 status 字段的 JSON 对象）。
// 未启用边缘代理的旧版服务返回 {"error":"请求无法处理"} 或 HTML —— 无 status 字段，
// 视为「该域名不可用」，换下一个候选。
func huangguoAIReplyLooksValid(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}
	var probe struct {
		Status *int `json:"status"`
	}
	if err := json.Unmarshal(trimmed, &probe); err != nil {
		return false
	}
	return probe.Status != nil
}

// huangguoAIEdgeCandidates 构造信封调用候选（顺序即优先级）：
// 漂移收割域名 → 镜像池（主域在前，参照在 index 1 后注入收割生成域名）→ 配置基址兜底。
// 仅当基址主机仍是 huangguoai.com 时注入 generatedHuangguoAIMirrors，测试桩基址（httptest
// 主机）不会产生随机域名候选，保证离线。
func (d *Downloader) huangguoAIEdgeCandidates() []string {
	base := d.providerBaseURL(sourceHuangguoAI)
	out := make([]string, 0, len(huangguoAIBaseMirrors)+16)
	seen := map[string]bool{}
	add := func(root string) {
		root = strings.TrimRight(strings.TrimSpace(root), "/")
		if root == "" {
			return
		}
		candidate := root + huangguoAIEdgePath
		if seen[candidate] {
			return
		}
		seen[candidate] = true
		out = append(out, candidate)
	}
	for _, drift := range huangguoAIDriftSnapshot() {
		add(drift)
	}
	baseHost := ""
	if parsed, err := url.Parse(base); err == nil {
		baseHost = strings.ToLower(parsed.Hostname())
	}
	for i, mirror := range huangguoAIBaseMirrors {
		if i == 1 && baseHost == "huangguoai.com" {
			for _, generated := range generatedHuangguoAIMirrors(base+huangguoAIEdgePath, 8) {
				add(generated)
			}
		}
		add(mirror)
	}
	add(base)
	if len(out) == 0 {
		return []string{base + huangguoAIEdgePath}
	}
	return out
}

// huangguoAIScopedClient 为信封 POST 构造一次性 client：继承 d.client 的 Transport/Timeout，
// 但自定义 CheckRedirect —— Go 默认在 301/302/303 把 POST 降级为 GET 并丢弃请求体，
// 边缘调用必须把重定向后的请求重新签成 POST+原信封体，并顺手收割重定向落地域名。
func (d *Downloader) huangguoAIScopedClient(envelope []byte) *http.Client {
	transport := http.RoundTripper(nil)
	timeout := time.Duration(0)
	if d.client != nil {
		transport = d.client.Transport
		timeout = d.client.Timeout
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	bodyLen := int64(len(envelope))
	client := &http.Client{Transport: transport, Timeout: timeout}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= huangguoAIDriftHopMax {
			return fmt.Errorf("黄果AI边缘接口重定向次数过多")
		}
		if len(via) > 0 && !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
			req.Header.Del("X-Preview-Token")
		}
		previous := via[len(via)-1]
		if previous.Method == http.MethodPost {
			// Go 在 301/302/303 把 POST 降级为 GET 且清空 Body/GetBody，
			// 信封必须用闭包持有的原始字节重新签成 POST。
			req.Method = http.MethodPost
			req.Body = io.NopCloser(bytes.NewReader(envelope))
			req.ContentLength = bodyLen
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json")
		}
		huangguoAILearnDrift(req.URL)
		return nil
	}
	return client
}

// fetchHuangguoAIEdge 对候选域名逐个 POST 信封（每域名一次尝试，多候选时单域名 5s 预算），
// 返回首个合法信封应答原文。成功时把最终落地 origin 记入 providerHosts（与参照
// fetchProviderRequest 一致），供 URL 重写与后续调用排序使用。
func (d *Downloader) fetchHuangguoAIEdge(ctx context.Context, envelope []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	base := d.providerBaseURL(sourceHuangguoAI)
	candidates := d.huangguoAIEdgeCandidates()
	timeout := providerTimeout
	if len(candidates) > 1 {
		timeout = huangguoAIMirrorProbeTTL
	}
	referer := strings.TrimRight(base, "/") + "/"
	client := d.huangguoAIScopedClient(envelope)
	var lastErr error
	tried := 0
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		tried++
		requestCtx, cancel := context.WithTimeout(ctx, timeout)
		request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, candidate, bytes.NewReader(envelope))
		if err != nil {
			cancel()
			lastErr = err
			continue
		}
		request.ContentLength = int64(len(envelope))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json")
		request.Header.Set("User-Agent", userAgent)
		if agent, ok := requestCtx.Value(providerTextUserAgentKey{}).(string); ok && agent != "" {
			request.Header.Set("User-Agent", agent)
		}
		request.Header.Set("Referer", providerRefererForURL(candidate, referer))
		request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
		if noCache, _ := requestCtx.Value(providerTextNoCacheKey{}).(bool); noCache {
			request.Header.Set("Cache-Control", "no-cache")
		}
		response, err := client.Do(request)
		if err != nil {
			cancel()
			lastErr = err
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, providerMaxBodyBytes+1))
		closeErr := response.Body.Close()
		cancel()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if closeErr != nil {
			lastErr = closeErr
			continue
		}
		if len(body) > providerMaxBodyBytes {
			lastErr = fmt.Errorf("response exceeds %d bytes", providerMaxBodyBytes)
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 || !huangguoAIReplyLooksValid(body) {
			if response.StatusCode >= 400 {
				lastErr = fmt.Errorf("%s 状态码 %d", huangguoAIEdgePath, response.StatusCode)
			} else {
				lastErr = fmt.Errorf("%s 未返回信封应答（状态 %d）", huangguoAIEdgePath, response.StatusCode)
			}
			continue
		}
		landing := request.URL
		if response.Request != nil && response.Request.URL != nil {
			landing = response.Request.URL
		}
		huangguoAILearnDrift(landing)
		d.providerMu.Lock()
		if d.providerHosts == nil {
			d.providerHosts = map[string]string{}
		}
		d.providerHosts[sourceHuangguoAI] = landing.Scheme + "://" + landing.Host
		d.providerMu.Unlock()
		huangguoAIEdgeClearCooldown()
		return string(body), nil
	}
	if lastErr == nil {
		lastErr = errors.New("无可用的黄果AI边缘域名")
	}
	// 整池网络级失败：进入冷却，auto-envelope 暂时退回旧 GET 语义，避免反复重扫死池
	huangguoAIEdgeMarkCooldown()
	return "", &huangguoAIEdgeFailure{tried: tried, last: lastErr}
}

// huangguoAIAPI 发送一次边缘信封调用并把 data 解入 out（out 传 *json.RawMessage 可拿原文）。
func (d *Downloader) huangguoAIAPI(ctx context.Context, path string, out any) error {
	envelope, err := json.Marshal(map[string]string{
		"method":       http.MethodGet,
		"path":         path,
		"body":         "",
		"content_type": "",
	})
	if err != nil {
		return err
	}
	body, err := d.fetchHuangguoAIEdge(ctx, envelope)
	if err != nil {
		return err
	}
	var reply struct {
		Status int             `json:"status"`
		Msg    string          `json:"msg"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &reply); err != nil {
		return fmt.Errorf("黄果接口返回格式无效：%w", err)
	}
	if reply.Status != 1 {
		return fmt.Errorf("黄果接口返回失败：%s", firstNonEmpty(reply.Msg, "未知错误"))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(reply.Data, out); err != nil {
		return fmt.Errorf("黄果接口数据格式已变化：%w", err)
	}
	return nil
}

func (d *Downloader) huangguoAIAPIHostMatch(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return false
	}
	if providerSourceForURL(rawURL) == sourceHuangguoAI {
		return true
	}
	// 注意：不匹配 cfg 基址本身——非官方 host（httptest/自定义部署）保持旧直连语义，
	// 避免信封把测试/私有部署流量导向生产镜像池。canonical huangguoai.com 已由
	// providerSourceForURL 覆盖。
	for _, entry := range huangguoAIDriftSnapshot() {
		if parsedEntry, entryErr := url.Parse(entry); entryErr == nil && strings.EqualFold(parsedEntry.Hostname(), host) {
			return true
		}
	}
	for _, domain := range huangguoAIMirrorDomains {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	for _, mirror := range huangguoAIBaseMirrors {
		if parsedMirror, mirrorErr := url.Parse(mirror); mirrorErr == nil && strings.EqualFold(parsedMirror.Hostname(), host) {
			return true
		}
	}
	return false
}

// huangguoAIAutoEnvelope 让既有的 fetchProviderText GET 调用（如分类目录
// /api/videos/category/{slug}?sort=hot&page=N&size=24）透明改走边缘信封：
// 命中黄果AI域名且路径以 /api/ 开头时，把 GET URL 拆成 path+query 装进信封发送，
// 返回 data 原文。其余 URL（含 HTML 页面、其它来源）不受影响。
func (d *Downloader) huangguoAIAutoEnvelope(ctx context.Context, rawURL string) (string, bool, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || !strings.HasPrefix(parsed.Path, "/api/") || !d.huangguoAIAPIHostMatch(rawURL) {
		return "", false, nil
	}
	if huangguoAIEdgeInCooldown() {
		return "", false, nil // 冷却期内保持旧 GET 直连语义（行为等同对齐前）
	}
	path := parsed.Path
	if parsed.RawQuery != "" {
		path += "?" + parsed.RawQuery
	}
	var data json.RawMessage
	if err := d.huangguoAIAPI(ctx, path, &data); err != nil {
		return "", true, err
	}
	return string(data), true, nil
}

func huangguoAIEpisodeNumber(row map[string]any) int {
	for _, key := range []string{"ep_num", "episode", "num", "index"} {
		if number, err := strconv.Atoi(mapString(row, key)); err == nil && number >= 1 {
			return number
		}
	}
	return 0
}

// huangguoAIChapterAligned 生成与参照 provider_huangguo_api.go huangguoAIChapter 同构的章节：
// key auto-1 / ep-N，PageURL {base}/video/{id}/ 或 {base}/video/{id}/ep-N/。
// 该 key 规则与本地 HTML 解析（aiEpisodeKey → 尾段 ep-N，裸页 auto-N）一致，
// 两种来源产生的 Chapter.ID 相同，播放页兜底路径不受影响。
func huangguoAIChapterAligned(base, sourceID string, episode int, title string) Chapter {
	key := "auto-1"
	pageURL := strings.TrimRight(base, "/") + "/video/" + sourceID + "/"
	if episode > 1 {
		key = "ep-" + strconv.Itoa(episode)
		pageURL += key + "/"
	}
	return Chapter{
		ID:             providerChapterID(sourceHuangguoAI, sourceID, key),
		Source:         sourceHuangguoAI,
		Title:          firstNonEmpty(strings.TrimSpace(title), fmt.Sprintf("第%d集", episode)),
		PageURL:        pageURL,
		CurrentEpisode: rawEpisode(episode),
	}
}

// fetchHuangguoAIDetailJSON 走信封拉取 /api/videos/{id} 详情并构建剧集列表。
func (d *Downloader) fetchHuangguoAIDetailJSON(ctx context.Context, sourceID string) (Drama, []Chapter, error) {
	if huangguoAIEdgeInCooldown() {
		return Drama{}, nil, errors.New("黄果AI边缘接口冷却中")
	}
	sourceID = strings.TrimPrefix(strings.Trim(strings.TrimSpace(sourceID), "/"), "detail/")
	if !rankingSourceID.MatchString(sourceID) {
		return Drama{}, nil, errors.New("无效的黄果剧集 ID")
	}
	var detail map[string]any
	if err := d.huangguoAIAPI(ctx, "/api/videos/"+url.PathEscape(sourceID), &detail); err != nil {
		return Drama{}, nil, err
	}
	base := d.providerBaseURL(sourceHuangguoAI)
	drama, ok := dramaFromAIMap(detail, base+"/", "")
	if !ok || drama.SourceID != sourceID {
		return Drama{}, nil, errors.New("黄果详情与请求剧集不符")
	}
	var chapters []Chapter
	rows, _ := detail["episodes"].([]any)
	for _, value := range rows {
		row, _ := value.(map[string]any)
		if row == nil {
			continue
		}
		episode := huangguoAIEpisodeNumber(row)
		if episode < 1 {
			continue
		}
		title := firstNonEmpty(mapString(row, "title", "name", "episode_title"), fmt.Sprintf("第%d集", episode))
		chapters = append(chapters, huangguoAIChapterAligned(base, sourceID, episode, title))
	}
	if len(chapters) == 0 {
		chapters = append(chapters, huangguoAIChapterAligned(base, sourceID, 1, drama.DisplayTitle()))
	}
	sortProviderChapters(chapters)
	return drama, uniqueChapters(chapters), nil
}

// huangguoAIPlayURLAligned 尝试 JSON 播放接口解析任务集数；chapters 无论来自
// JSON 详情还是旧 HTML 页解析均可用（EpisodeString 优先取 CurrentEpisode，回退
// task.Index）。ok=false 时调用方应沿用 HTML 页面解析兜底路径。
func (d *Downloader) huangguoAIPlayURLAligned(ctx context.Context, task Task, chapter Chapter) (string, bool) {
	_, sourceID, valid := splitProviderDramaID(task.DramaID)
	if !valid {
		return "", false
	}
	episode, err := strconv.Atoi(chapter.EpisodeString(task.Index))
	if err != nil || episode < 1 {
		return "", false
	}
	resolved, err := d.fetchHuangguoAIPlayURL(ctx, sourceID, episode)
	if err != nil || !isProviderHTTPMediaURL(resolved) {
		return "", false
	}
	return resolved, true
}

// fetchHuangguoAIPlayURL 走信封解析第 episode 集真实播放地址（参照 api.go fetchHuangguoAIPlayURL）。
func (d *Downloader) fetchHuangguoAIPlayURL(ctx context.Context, sourceID string, episode int) (string, error) {
	sourceID = strings.TrimPrefix(strings.Trim(strings.TrimSpace(sourceID), "/"), "detail/")
	if !rankingSourceID.MatchString(sourceID) || episode < 1 {
		return "", errors.New("黄果集数无效")
	}
	var play struct {
		VideoURL string `json:"video_url"`
	}
	path := fmt.Sprintf("/api/videos/%s/play?ep=%d&page_texts=0", url.PathEscape(sourceID), episode)
	if err := d.huangguoAIAPI(ctx, path, &play); err != nil {
		return "", err
	}
	return normalizeProviderMediaURL(d.providerBaseURL(sourceHuangguoAI)+"/", play.VideoURL), nil
}

// searchHuangguoAI 在线关键词搜索（参照 api.go searchHuangguoAI）；
// 当前 app_runtime 查询路径只走本机缓存，函数先行落地供其接线（见任务报告）。
func (d *Downloader) searchHuangguoAI(ctx context.Context, page int, query string) ([]Drama, bool, error) {
	query = strings.TrimSpace(query)
	if query == "" || len(query) > huangguoAISourceIDMaxLen {
		return nil, false, errors.New("黄果搜索关键词无效")
	}
	if page < 1 {
		page = 1
	}
	values := url.Values{}
	values.Set("q", query)
	values.Set("page", strconv.Itoa(page))
	values.Set("size", strconv.Itoa(huangguoAISearchPageSize))
	var result struct {
		Items      json.RawMessage `json:"items"`
		Pagination struct {
			Pages int `json:"pages"`
		} `json:"pagination"`
	}
	if err := d.huangguoAIAPI(ctx, "/api/search?"+values.Encode(), &result); err != nil {
		return nil, false, err
	}
	items := parseHuangguoAIJSONCards(result.Items, d.providerBaseURL(sourceHuangguoAI)+"/", "")
	return items, page < result.Pagination.Pages, nil
}
