//go:build zgj_sidecar

package main

// Web 形态专属文件交接面（research/web-container-design.md §9 起）：
//
//	FileSign                一次性签名 URL（zgj_publish 的 signedFileUrl 兜底形态）
//	DownloadPurgeOutputs    合并成品幂等清理（同剧「全NNN-MMM集」区段被更大区段覆盖即删）
//
// 为什么落 zgj_sidecar 标签：这两个绑定只服务容器 web 形态（MCP 发布链），
// 桌面 Electron 形态不需要，打标签保证桌面构建零牵连。
// 与 /file 的关系：/file 要 zgj_session cookie（设计内强度=重启失效）；
// FileSign 面向**外部**上传器（openlist 等）——它们没法也不该登录本系统，
// 故签发一次性 token URL（短 TTL、单次有效、白名单校验同 /file）。

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------- FileSign

type fileSignEntry struct {
	path    string
	expires time.Time
}

type fileSignStore struct {
	mu     sync.Mutex
	tokens map[string]fileSignEntry
}

// 单例（web 模式下唯一进程；测试可换）。
var webFileSigns = &fileSignStore{tokens: map[string]fileSignEntry{}}

func (s *fileSignStore) add(path string, ttl time.Duration) string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand 失败=系统熵源异常，签名 URL 直接拒发比降级安全强。
		panic("FileSign: crypto/rand 不可用: " + err.Error())
	}
	tok := hex.EncodeToString(buf)
	s.mu.Lock()
	defer s.mu.Unlock()
	// 顺带 GC 过期项（懒清理，量小无压力）。
	now := time.Now()
	for k, e := range s.tokens {
		if now.After(e.expires) {
			delete(s.tokens, k)
		}
	}
	s.tokens[tok] = fileSignEntry{path: path, expires: now.Add(ttl)}
	return tok
}

func (s *fileSignStore) take(tok string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.tokens[tok]
	if !ok {
		return "", false
	}
	delete(s.tokens, tok) // 单次有效：取一次即失效
	if time.Now().After(e.expires) {
		return "", false
	}
	return e.path, true
}

// FileSign → JSON {path, token, ttlSec, url}：为容器内绝对路径签发一次性
// 签名 URL。url 的 host 部分是容器内回环（127.0.0.1:8080）——MCP 层会按
// 自己的 ZGJ_WEB_URL（对外可达视角）重写 host 再交给调用方；path+token
// 两个字段也让调用方自行拼 host 时有确定性口径。
// ttlSec 缺省 1800（30 分钟够大文件分片上传窗口），上限 3600。
// 安全边界与 /file 完全一致：checkLocalPlayable 白名单（下载根内、.. 上跳拒）。
func (a *App) FileSign(path string, ttlSec int) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path 必填（容器内绝对路径）")
	}
	root, err := a.downloadRootDir()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if err := checkLocalPlayable(root, abs); err != nil {
		return "", err
	}
	if st, err := os.Stat(abs); err != nil || st.IsDir() {
		return "", fmt.Errorf("文件不存在或不是文件：%s", filepath.Base(abs))
	}
	if ttlSec <= 0 {
		ttlSec = 1800
	}
	if ttlSec > 3600 {
		ttlSec = 3600
	}
	tok := webFileSigns.add(abs, time.Duration(ttlSec)*time.Second)
	return marshalJSON(map[string]any{
		"path":   abs,
		"token":  tok,
		"ttlSec": ttlSec,
		"once":   true,
		"url":    "http://127.0.0.1:8080/file-signed?token=" + tok,
		"supports": "Range（GET 带 Range 头可分片）",
	})
}

// FileDelete 删除下载根内一个文件（zgj_publish deleteLocalAfterSuccess 用）。
// 白名单同 FileSign：checkLocalPlayable（.. 上跳拒）+ 必须是普通文件（不删目录，
// 清分集缓存走 DownloadRemoveBatch 的核心口径，不在此处旁路）。
func (a *App) FileDelete(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path 必填（容器内绝对路径）")
	}
	root, err := a.downloadRootDir()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if err := checkLocalPlayable(root, abs); err != nil {
		return "", err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("文件不存在：%s", filepath.Base(abs))
	}
	if st.IsDir() {
		return "", fmt.Errorf("拒绝删除目录（用 DownloadRemoveBatch 走核心口径）：%s", filepath.Base(abs))
	}
	if err := os.Remove(abs); err != nil {
		return "", err
	}
	zgjLogf("filedelete 删文件：%s (%d B)", abs, st.Size())
	return marshalJSON(map[string]any{"deleted": abs, "bytes": st.Size()})
}

// newFileSignedHandler /file-signed?token=：一次性 token 出流。
// http.ServeFile 托管 Range/Content-Type（与 /file 同口径）。
func newFileSignedHandler(rt *sidecarRuntime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := r.URL.Query().Get("token")
		path, ok := webFileSigns.take(tok)
		if !ok {
			http.Error(w, "invalid or expired token", http.StatusUnauthorized)
			return
		}
		root, err := rt.app.downloadRootDir()
		if err != nil {
			http.Error(w, "no download root: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		if err := checkLocalPlayable(root, path); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		if st, err := os.Stat(path); err != nil || st.IsDir() {
			http.Error(w, "file not found", http.StatusNotFound)
			return
		}
		http.ServeFile(w, r, path)
	}
}

// ---------------------------------------------------------------- 合并成品幂等清理

// 为什么需要：merge.go 的幂等只覆盖**同名**（同区段重合并 moveFile rename 覆盖）；
// 扩区段（001-050 → 001-060）是**新名字**新文件，旧区段成品会永久留在
// 「合并」目录里累积（用户 m05712 ④ 明确点名「全001-003」「全001-004」累积）。
// 规则：同剧（标题前缀相同）下，文件 A 的集数区间 [fa,la] 被另一文件 B
// [fb,lb] **严格包含**（fb<=fa 且 la<=lb 且区间不完全相等）→ A 是旧区段
// 产物，删。相等区间不可能有两文件（同名=同一文件）。
var mergeOutputRe = regexp.MustCompile(`^(.+) 全(\d{3,})-(\d{3,})集\.mkv$`)

type mergeOutputFile struct {
	name    string
	full    string
	title   string
	first   int
	last    int
	modTime time.Time
	size    int64
}

// DownloadPurgeOutputs 清理「合并」目录下被更大区段覆盖的旧成品。
// root 缺省=下载根（与归档扫描同源）。返回 {mergeDir, scanned, removed, kept}。
// 只动匹配「<剧名> 全NNN-MMM集.mkv」命名约定的文件，手动放进的其它文件不碰。
// 容器内 root 属主进程执行，天然跨属主（host 上 ryen 直删会被 0700 拒——
// 这正是 cleanup 走 WS 的原因，同 DownloadRemoveBatch 的通道选择）。
func (a *App) DownloadPurgeOutputs(root string) (string, error) {
	dir := strings.TrimSpace(root)
	if dir == "" {
		d, err := a.downloadRootDir()
		if err != nil {
			return "", err
		}
		dir = d
	}
	mergeDir := filepath.Join(dir, "合并")
	entries, err := os.ReadDir(mergeDir)
	if err != nil {
		if os.IsNotExist(err) {
			return marshalJSON(map[string]any{"mergeDir": mergeDir, "scanned": 0, "removed": []string{}, "kept": []string{}})
		}
		return "", err
	}
	var files []mergeOutputFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := mergeOutputRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		var first, last int
		fmt.Sscanf(m[2], "%d", &first)
		fmt.Sscanf(m[3], "%d", &last)
		files = append(files, mergeOutputFile{
			name:    e.Name(),
			full:    filepath.Join(mergeDir, e.Name()),
			title:   m[1],
			first:   first,
			last:    last,
			modTime: info.ModTime(),
			size:    info.Size(),
		})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].title != files[j].title {
			return files[i].title < files[j].title
		}
		return files[i].first < files[j].first
	})
	var removed, kept []string
	for i, f := range files {
		subsumedBy := ""
		for j, g := range files {
			if i == j || g.title != f.title {
				continue
			}
			if g.first <= f.first && f.last <= g.last && (g.first < f.first || g.last > f.last) {
				subsumedBy = g.name
				break
			}
		}
		if subsumedBy != "" {
			if err := os.Remove(f.full); err == nil {
				zgjLogf("purgoutputs 删旧成品：%s（被 %s 覆盖）", f.name, subsumedBy)
				removed = append(removed, f.name)
				continue
			}
		}
		kept = append(kept, f.name)
	}
	return marshalJSON(map[string]any{
		"mergeDir": mergeDir,
		"scanned":  len(files),
		"removed":  removed,
		"kept":     kept,
	})
}
