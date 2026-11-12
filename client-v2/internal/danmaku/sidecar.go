package danmaku

// sidecar.go —— 「下载时保存弹幕」的同名旁挂文件（R22a 第③件的地基）。
//
// 对端是 v1 的三段：
//   · `library.py:413-475 _queue_danmaku/_collect_danmaku`：
//     每集解密完成后，若客户端开着这个选项，就 resolve 一次拿会话、
//     整集抓一次弹幕，把 `<视频名>.ass` 与 `<视频名>.danmaku.json`
//     写在**成品文件旁边**（不是播放器的临时 ASS 目录，见 ass.go 的 WriteAss）；
//   · `danmaku.py:78-86 document()`：json 的字段形状，注释原话「和核心的
//     nativeDanmakuDocument 一致」；
//   · `danmaku.py:89-101 write_file()`：原子写（.tmp → flush → fsync → replace）。
//
// 为什么要单独抽出来、并且把 resolve/fetch 做成注入：
// 这两步要打核心与网络，本机没有 Go 工具链、CI 也连不上核心；
// 把"抓完之后怎么写"这半段做成纯函数，就能在单测里覆盖到
// 跳过已有、时长缺失、会话缺失、无弹幕、排序、缩进、失败残留这些具体形状。
// 真正的联网那半段（native.Resolve / native.DanmakuAll）由调用方接线，
// 属于"引擎在、接线待补"里剩下的那一小块，不在这笔假装已经跑过。

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SidecarSuffixAss / SidecarSuffixDoc 是旁挂文件的后缀，逐字照 v1（base + ".ass"）。
const (
	SidecarSuffixAss = ".ass"
	SidecarSuffixDoc = ".danmaku.json"
)

// SidecarDocument 是 `<视频名>.danmaku.json` 的内容。
// 键序与缩进都按 v1 `danmaku.document()`：`json.dumps(..., ensure_ascii=False, indent=1)`，
// 而 Go 的 `json.Marshal` 默认会转义 HTML 且不缩进——
// 那会让同一份文件在两版之间字节不同（用户手动 diff 或以后再迁移时会撞上），
// 所以这里用 encoder 显式对齐 house style（与 watchstore 落盘同一套：缩进 1 空格、不转义 HTML）。
type SidecarDocument struct {
	EpisodeID string `json:"episodeId"`
	SeriesID  string `json:"seriesId"`
	Total     int64  `json:"total"`
	FetchedAt int64  `json:"fetchedAt"` // epoch 毫秒（v1 int(time.time()*1000)）
	Items     []Item `json:"items"`
}

// MarshalText 按 v1 的字节形状序列化。
func (d SidecarDocument) MarshalText() (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", " ") // v1 indent=1
	enc.SetEscapeHTML(false)
	if d.Items == nil {
		d.Items = []Item{} // v1 list(items or []) → 空数组而不是 null
	}
	if err := enc.Encode(d); err != nil {
		return "", err
	}
	// Encode 会补一个换行；v1 的 json.dumps 不补。
	return strings.TrimRight(buf.String(), "\n"), nil
}

// SidecarPaths 给出某个成品文件对应的两个旁挂路径（v1 os.path.splitext(path)[0]）。
func SidecarPaths(videoPath string) (ass string, doc string) {
	base := strings.TrimSuffix(videoPath, filepath.Ext(videoPath))
	return base + SidecarSuffixAss, base + SidecarSuffixDoc
}

// DetectSidecar 找出某个成品文件对应的旁挂 .ass 与它的弹幕条数（v1 main.py:1880-1895
// `_attach_sidecar_danmaku` 的"找文件 + 数条数"那两步）。
//
// 三条与 v1 一致的细节，都不是可有可无：
//   · `.ass` 必须**存在且非空**才返回 ok —— 空文件挂上去 mpv 会当字幕轨处理但什么都没有，
//     而且会让"这一集有弹幕"的显示变成假话；
//   · `.danmaku.json` **读不到不算失败**（v1 用 try/except 把 OSError/ValueError/TypeError
//     全吞掉，count 保持 0）：条数只是显示用的旁证，弹幕本体在 .ass 里；
//   · 条数优先取 `items` 的长度，其次 `total`（v1 只数 items；核心写的 total 可能更大，
//     但用户看到的"有多少条"一直是本地已有条数，所以 items 优先）。
func DetectSidecar(videoPath string) (ass string, count int64, ok bool) {
	assPath, docPath := SidecarPaths(videoPath)
	st, err := os.Stat(assPath)
	if err != nil || st.Size() <= 0 {
		return assPath, 0, false
	}
	raw, err := os.ReadFile(docPath)
	if err != nil {
		return assPath, 0, true // v1：json 读不到照样挂 .ass
	}
	var doc SidecarDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return assPath, 0, true
	}
	if len(doc.Items) > 0 {
		return assPath, int64(len(doc.Items)), true
	}
	return assPath, doc.Total, true
}

// SidecarReady 判定"这一集已经存过了"：两个文件都在、且 .ass 非空。
// 照 v1 library.py:421-422（.ass 存在且 size>0 **并且** .danmaku.json 存在）——
// 只查 .ass 的话，一次写坏一半就永远不会再补，弹幕数也就丢了。
func SidecarReady(videoPath string) bool {
	ass, doc := SidecarPaths(videoPath)
	st, err := os.Stat(ass)
	if err != nil || st.Size() <= 0 {
		return false
	}
	if _, err := os.Stat(doc); err != nil {
		return false
	}
	return true
}

// WriteTextAtomic 原子写文本文件（v1 danmaku.py:89-101：
// 建目录 → 写 path+".tmp" → flush → fsync → os.replace）。
// 不原子会怎样：这些文件就在成品旁边，播放器/归档扫描随时可能读到半截 .ass。
func WriteTextAtomic(text, path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("danmaku: 写入路径为空")
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("danmaku: 创建目录失败: %w", err)
		}
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("danmaku: 建临时文件失败: %w", err)
	}
	if _, err := f.WriteString(text); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("danmaku: 写入失败: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("danmaku: fsync 失败: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("danmaku: 关闭失败: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("danmaku: 替换失败: %w", err)
	}
	return nil
}

// SidecarRequest 是"给这一集存弹幕"需要的东西。
// 注意没有 drama/chapter：那两个字段只在接线侧（native.Resolve）有用，
// 纯函数这层只关心时长与画布，方便单测。
type SidecarRequest struct {
	VideoPath  string // 成品文件路径，旁挂文件就写在它旁边
	DurationMs int64  // <=0 直接报错（v1：缺少分集时长，无法抓弹幕）
	Width      int    // ASS 画布，0 走 BuildAss 的默认
	Height     int
	SeriesID   string // v1 drama.sourceId
}

// SidecarDeps 把要打核心的两步注入进来。
type SidecarDeps struct {
	// 这三步在 v1 里直接打核心；这里做成字段而不是包级变量，
	// 是为了让"怎么写"这半段能在无网络的 CI 上被真测到（见文件头）。
	// Resolve 拿播放会话与弹幕主键
	// （v1：core.call("resolve", …, quality=0, force=True, session="danmaku-collect")
	//   → plan.session / plan.danmakuId）。
	Resolve func() (session string, episodeID string, err error)
	// FetchAll 整集弹幕（v1 danmaku.fetch_all → 核心 danmakuAll）。
	FetchAll func(session string) (items []Item, total int64, err error)
	// Now epoch 毫秒；nil → time.Now。测试注入固定值才能断 fetchedAt。
	Now func() int64
}

// SidecarResult 说清这次到底做了什么，调用方据此给 UI 文案
// （v1 的三条：本集没有弹幕 / 抓到了 / 已经存过所以跳过）。
type SidecarResult struct {
	Skipped  bool
	Written  bool
	Empty    bool
	Items    int
	AssPath  string
	DocPath  string
}

// CollectSidecar 抓一集的弹幕并写成两个旁挂文件。
// 错误文案逐字照 v1（缺时长 / 没有拿到播放会话），因为这些句子会直接进用户可见的下载状态。
func CollectSidecar(req SidecarRequest, deps SidecarDeps) (SidecarResult, error) {
	var out SidecarResult
	if strings.TrimSpace(req.VideoPath) == "" {
		return out, errors.New("缺少成品文件路径，无法保存弹幕")
	}
	if deps.Resolve == nil || deps.FetchAll == nil {
		return out, errors.New("danmaku: 未注入 resolve/fetch，无法抓弹幕")
	}
	assPath, docPath := SidecarPaths(req.VideoPath)
	out.AssPath, out.DocPath = assPath, docPath

	// v1 的跳过条件在最前面（_queue_danmaku 里就判了），不浪费一次网络往返。
	if SidecarReady(req.VideoPath) {
		out.Skipped = true
		return out, nil
	}
	if req.DurationMs <= 0 {
		return out, errors.New("缺少分集时长，无法抓弹幕")
	}

	session, episodeID, err := deps.Resolve()
	if err != nil {
		return out, err
	}
	if session == "" {
		return out, errors.New("没有拿到播放会话")
	}
	items, total, err := deps.FetchAll(session)
	if err != nil {
		return out, err
	}
	if len(items) == 0 {
		// v1：on_status "%s：本集没有弹幕" 后直接 return，**不写空文件**。
		// 写了就会让 SidecarReady 变真，以后再也补不上。
		out.Empty = true
		return out, nil
	}
	// v1 fetch_all 里排过序（按 timeMs）；这里也排，
	// 否则 ASS 的事件行顺序会跟着核心的分页顺序漂。
	sort.SliceStable(items, func(i, j int) bool { return items[i].TimeMs < items[j].TimeMs })

	text := BuildAss(items, req.DurationMs, AssOptions{Width: req.Width, Height: req.Height})
	if err := WriteTextAtomic(text, assPath); err != nil {
		return out, err
	}
	// fetchedAt 与 .ass 写在同一段里，保持"一次采集一份快照"的语义。
	now := deps.Now
	if now == nil {
		now = func() int64 { return time.Now().UnixMilli() }
	}
	// total 照 v1 `int(reply.get("total") or 0)`：核心不报总数时用条数兜底。
	totalOut := total
	if totalOut <= 0 {
		totalOut = int64(len(items))
	}
	doc, err := SidecarDocument{
		EpisodeID: episodeID,
		SeriesID:  req.SeriesID,
		Total:     totalOut,
		FetchedAt: now(),
		Items:     items,
	}.MarshalText()
	if err != nil {
		return out, err
	}
	if err := WriteTextAtomic(doc, docPath); err != nil {
		// ⚠ v1 在这里**不做回滚**：.ass 已经落盘，json 失败就留下半套。
		// 我沿用 v1（不自作聪明加清理），但把风险写明白：
		// 半套不会让 SidecarReady 变真（它要求两个都在），
		// 所以下一次采集会整对重写；代价只是中间那段时间本地播放没有弹幕。
		// 真要原子成对，得改成"两个 tmp 都写完再一起 rename"——那是独立一件事。
		return out, err
	}
	out.Written = true
	out.Items = len(items)
	return out, nil
}
