// 归档目录扫描（长尾补齐·交付 B）。
//
// 背景：v2 DownloadArchive 只对索引记录打 archived 旗标（service.go:510-555），
// 从不枚举磁盘产物 —— v1（downloads.py:2529-2577）归档区是文件系统扫描，
// v2 索引丢了记录但文件还在就「隐身」。ScanArchiveRoot 按 v2 真实布局补齐：
//   - 分集成品 <root>/<folder>/media.mp4，folder=job.folder（核心为
//     source/jobId 两级，merge.go resolveEpisodeSource :224-274 同源解析）；
//   - 合并产物平铺 <root>/合并/<safeTitle> 全NNN-NNN集[（未完整校验）].mkv
//     （merge.go :98/:195-205），按文件名剧名前缀归组；
//   - 排除 合并/、.cache/、隐藏目录；深度 ≤2（source/jobId）。
//
// 与索引记录合并（父修正第 4 条：文件系统为准）：folder 命中 completed/归档
// 记录 → 剧名/集归属与 archived 旗标取记录，否则孤儿目录以目录名成组
// （indexed=false）。点开路径 openPath：有合并产物 → 最新合并文件；否则
// 最新分集目录（前端拼 <directory>/<openPath> 走 OpenDownloadPath 现有链路）。

package downloadsvc

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// mergeOutDirName 合并成品目录（与 merge.go runMerge :98 同源约定）。
	mergeOutDirName = "合并"
	// archiveCacheDirName ffmpeg 中间产物根（不计归档）。
	archiveCacheDirName = ".cache"
	// archiveScanMaxDepth 分集目录相对根的最大层数（source/jobId=2）。
	archiveScanMaxDepth = 2
	// unverifiedSuffix 未完整校验文件名标记（merge.go :197）。
	unverifiedSuffix = "（未完整校验）"
	// mergeNameInfix 合并产物名剧名/集号分隔（merge.go :195 fmt）。
	mergeNameInfix = " 全"
)

// ArchiveGroup 扫描分组条目（前端 types.ArchiveGroupRow 对齐）。
type ArchiveGroup struct {
	Name        string   `json:"name"`      // 剧名（索引）或目录/文件名（孤儿）
	Episodes    int      `json:"episodes"`  // *.mp4/*.mkv 分集产物计数
	Bytes       int64    `json:"bytes"`     // 分集产物总大小
	Mtime       int64    `json:"mtime"`     // 最近产物修改时间（unix ms）
	HasMerged   bool     `json:"hasMerged"` // 含合并徽标数据源
	MergedCount int      `json:"mergedCount,omitempty"`
	MergedBytes int64    `json:"mergedBytes,omitempty"`
	Archived    bool     `json:"archived"` // 索引记录里有 archived 标记的分集
	Indexed     bool     `json:"indexed"`  // 剧名来自索引记录（否则文件系统孤儿）
	Folders     []string `json:"folders,omitempty"`
	OpenPath    string   `json:"openPath"` // 点开目标（相对 directory，slash 分隔）
}

type archiveEpisodeDir struct {
	rel   string
	files int
	bytes int64
	mtime time.Time
}

type archiveAgg struct {
	g            *ArchiveGroup
	newestEpRel  string
	newestEpTime time.Time
	newestMgRel  string
	newestMgTime time.Time
}

// ScanArchiveRoot 扫描下载目录归档产物。错误语义：核心未接线/目录取不到/
// 目录不存在 → {directory, exists:false, groups:[]}（非 error），前端区分
// 「目录不存在」空态；refresh 失败不致命（用既有索引快照富化）。
func (s *Service) ScanArchiveRoot() (map[string]any, error) {
	root := ""
	if s.call != nil {
		if data, err := s.call("downloadDirectory", nil); err == nil {
			root, _ = data["directory"].(string)
		}
	}
	out := map[string]any{"directory": root, "exists": false, "groups": []ArchiveGroup{}}
	if root == "" {
		return out, nil
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return out, nil
	}

	_, _ = s.refresh(false) // 索引富化（失败容忍）
	s.mu.Lock()
	jobs := append([]map[string]any(nil), s.jobs...)
	s.mu.Unlock()

	// folder → 索引记录（剧归属/archived 旗标）；同 folder 后者胜。
	type rec struct {
		dramaID  string
		title    string
		archived bool
	}
	byFolder := map[string]rec{}
	for _, job := range jobs {
		f, _ := job["folder"].(string)
		f = strings.Trim(filepath.ToSlash(strings.TrimSpace(f)), "/")
		if f == "" {
			if id, _ := job["id"].(string); id != "" {
				f = id // resolveEpisodeSource 同款兜底（folder 缺省=jobId）
			}
		}
		if f == "" {
			continue
		}
		r := byFolder[f]
		if id := jobDramaID(job); id != "" {
			r.dramaID, r.title = id, jobTitle(job)
		}
		if a, _ := job["archived"].(bool); a {
			r.archived = true
		}
		byFolder[f] = r
	}

	aggs := map[string]*archiveAgg{}
	byTitle := map[string]string{} // 剧名（原文与 safe 形）→ groupKey，合并文件回接用
	getAgg := func(key, name string) *archiveAgg {
		if a := aggs[key]; a != nil {
			return a
		}
		a := &archiveAgg{g: &ArchiveGroup{Name: name}}
		aggs[key] = a
		return a
	}

	for _, d := range collectArchiveDirs(root) {
		r := byFolder[d.rel]
		key, name := "f:"+d.rel, filepath.Base(d.rel)
		if r.dramaID != "" && r.title != "" {
			key, name = "d:"+r.dramaID, r.title
			if _, ok := byTitle[name]; !ok {
				byTitle[name] = key
			}
			safeKey := "d:" + r.dramaID // 合并文件名是 safeMergeTitle 形
			safeName := safeMergeTitle(r.title)
			if _, ok := byTitle[safeName]; !ok {
				byTitle[safeName] = safeKey
			}
		}
		a := getAgg(key, name)
		a.g.Episodes += d.files
		a.g.Bytes += d.bytes
		a.g.Folders = append(a.g.Folders, d.rel)
		if d.mtime.UnixMilli() > a.g.Mtime {
			a.g.Mtime = d.mtime.UnixMilli()
		}
		if d.mtime.After(a.newestEpTime) {
			a.newestEpTime, a.newestEpRel = d.mtime, d.rel
		}
		if r.dramaID != "" && r.title != "" {
			a.g.Indexed = true
			if r.archived {
				a.g.Archived = true
			}
		}
	}

	// 合并产物归组：按名前缀回接剧集组；回接不上自成「仅合并」组。
	if entries, err := os.ReadDir(filepath.Join(root, mergeOutDirName)); err == nil {
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if !archiveMediaExt(ext) {
				continue
			}
			info, ierr := e.Info()
			if ierr != nil || info.Size() == 0 {
				continue
			}
			stem := strings.TrimSuffix(strings.TrimSuffix(e.Name(), ext), unverifiedSuffix)
			prefix := stem
			if i := strings.LastIndex(stem, mergeNameInfix); i > 0 {
				prefix = stem[:i]
			}
			key, ok := byTitle[prefix]
			if !ok {
				key = "m:" + prefix
			}
			a := getAgg(key, prefix)
			a.g.HasMerged = true
			a.g.MergedCount++
			a.g.MergedBytes += info.Size()
			rel := mergeOutDirName + "/" + e.Name()
			if info.ModTime().After(a.newestMgTime) {
				a.newestMgTime, a.newestMgRel = info.ModTime(), rel
			}
			if mt := info.ModTime().UnixMilli(); mt > a.g.Mtime {
				a.g.Mtime = mt
			}
		}
	}

	list := make([]ArchiveGroup, 0, len(aggs))
	for _, a := range aggs {
		switch {
		case a.newestMgRel != "" && !a.newestMgTime.Before(a.newestEpTime):
			a.g.OpenPath = a.newestMgRel
		default:
			a.g.OpenPath = a.newestEpRel
		}
		if a.g.OpenPath == "" {
			a.g.OpenPath = "." // 兜底：开目录本身
		}
		list = append(list, *a.g)
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Mtime > list[j].Mtime })
	out["exists"] = true
	out["groups"] = list
	return out, nil
}

// collectArchiveDirs 收集分集目录（目录内含 ≥1 个媒体成品即算一集目录，
// 不再下钻；容器目录在深度内继续下探）。
func collectArchiveDirs(root string) []archiveEpisodeDir {
	var out []archiveEpisodeDir
	var walk func(dir, rel string, depth int)
	walk = func(dir, rel string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		var files int
		var bytes int64
		var newest time.Time
		var subDirs []os.DirEntry
		for _, e := range entries {
			if e.IsDir() {
				subDirs = append(subDirs, e)
				continue
			}
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			if !archiveMediaExt(strings.ToLower(filepath.Ext(name))) {
				continue
			}
			info, ierr := e.Info()
			if ierr != nil || info.Size() == 0 {
				continue
			}
			files++
			bytes += info.Size()
			if info.ModTime().After(newest) {
				newest = info.ModTime()
			}
		}
		if files > 0 {
			out = append(out, archiveEpisodeDir{rel: rel, files: files, bytes: bytes, mtime: newest})
			return
		}
		if depth >= archiveScanMaxDepth {
			return
		}
		for _, e := range subDirs {
			if !archiveScanWantedDir(e.Name()) {
				continue
			}
			walk(filepath.Join(dir, e.Name()), rel+"/"+e.Name(), depth+1)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !e.IsDir() || !archiveScanWantedDir(e.Name()) {
			continue
		}
		walk(filepath.Join(root, e.Name()), e.Name(), 1)
	}
	return out
}

func archiveScanWantedDir(name string) bool {
	return name != mergeOutDirName && name != archiveCacheDirName && !strings.HasPrefix(name, ".")
}

func archiveMediaExt(ext string) bool {
	return ext == ".mp4" || ext == ".mkv"
}
