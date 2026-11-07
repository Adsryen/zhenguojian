package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"
)

var errNativeLocalFile = errors.New("本地文件缺失或不完整，请重新下载或选择在线播放")

const (
	// 单次入队上限：一次解析 500 集，更长的季由界面分批提交。
	nativeDownloadBatchLimit = 500
	// 活动任务上限（已归档的不算）。一条记录约 1.5 KB，2 万集约 30 MB；
	// 按现在动辄上千集的剧，这个额度够同时保留 20 部左右。
	nativeDownloadCountLimit = 20000
	// index.json 上限：2 万条约 30 MB，留出约 4 倍余量给长简介和后续扩容。
	nativeDownloadIndexLimit = 128 << 20
)

type nativeDownloadEpisode struct {
	Chapter Chapter `json:"chapter"`
	Index   int     `json:"index"`
}

type nativeDownloadJob struct {
	ID            string      `json:"id"`
	Drama         nativeDrama `json:"drama"`
	Chapter       Chapter     `json:"chapter"`
	Index         int         `json:"index"`
	Quality       int         `json:"quality"`
	ActualQuality int         `json:"actualQuality"`
	Bitrate       int         `json:"bitrate,omitempty"`
	State         string      `json:"state"`
	Archived      bool        `json:"archived,omitempty"`
	Folder        string      `json:"folder,omitempty"`
	Bytes         int64       `json:"bytes"`
	Total         int64       `json:"total"`
	Progress      float64     `json:"progress"`
	Error         string      `json:"error,omitempty"`
	Created       int64       `json:"created"`
	Revision      int64       `json:"revision,omitempty"`
	// RetryAt 是“排队但先别开始”的时间点（毫秒）。风控静默拒绝是临时状态，
	// 失败后隔一段时间自动重排，效果等同用户手动点“继续”。
	RetryAt        int64 `json:"retryAt,omitempty"`
	ResolveRetries int   `json:"resolveRetries,omitempty"`
}

type nativeDownloadRecord struct {
	nativeDownloadJob
	File string `json:"file,omitempty"`
	Key  string `json:"key,omitempty"`
}

type nativeDownloadResult struct {
	file    string
	key     string
	quality int
	bitrate int
}

type nativeDownloads struct {
	mu          sync.Mutex
	root        string
	engine      *nativeEngine
	jobs        map[string]*nativeDownloadRecord
	active      map[string]context.CancelFunc
	closed      bool
	moving      bool
	mediaBusy   bool
	loadErr     error
	workers     sync.WaitGroup
	lastSaved   time.Time
	concurrency int
	bySource    bool
	resolve     func(context.Context, nativeDownloadJob) (providerMedia, error)
	retryDelay  func(int) time.Duration
	wake        *time.Timer
	wakeAt      int64
}

func nativeDownloadID(drama string, episode int) string {
	hash := sha256.Sum256([]byte(drama + "\x00" + strconv.Itoa(episode)))
	return hex.EncodeToString(hash[:16])
}

func nativeDownloadVersion(job nativeDownloadJob) string {
	version := fmt.Sprintf("%d-%d-%d", job.Created, job.Bytes, job.ActualQuality)
	if job.Revision > 0 {
		version += "-" + strconv.FormatInt(job.Revision, 10)
	}
	return version
}

func (manager *nativeDownloads) unarchivedCountLocked() int {
	count := 0
	for _, job := range manager.jobs {
		if !job.Archived {
			count++
		}
	}
	return count
}

// downloadResolveKey marks the context of a background download resolve.  The
// App playback API silently rejects some requests with an empty 200, and a
// download can afford to retry that for several seconds where interactive
// playback cannot.
type downloadResolveKey struct{}

func newNativeDownloads(engine *nativeEngine) *nativeDownloads {
	manager := &nativeDownloads{root: nativeDownloadLocation(engine.directory), engine: engine,
		jobs: map[string]*nativeDownloadRecord{}, active: map[string]context.CancelFunc{}}
	settings := engine.resourceSettings()
	manager.concurrency, manager.bySource = settings.DownloadConcurrency, settings.DownloadBySource
	manager.retryDelay = downloadResolveRetryDelay
	manager.resolve = func(ctx context.Context, job nativeDownloadJob) (providerMedia, error) {
		ctx = context.WithValue(ctx, downloadResolveKey{}, true)
		// 先把同一部剧排队中的分集合成一批取流（≤15 集/次），下载任务随后
		// 命中缓存。逐集请求取流接口既慢，也更容易被风控回 200 空包，最后
		// 掉到网页 360P 兜底。
		if hongguoVideoIDOf(job.Chapter) != "" {
			if ids := manager.pendingHongguoVideoIDs(job, hongguoAppBatchLimit); len(ids) > 0 {
				if stored := engine.downloader.prefetchHongguoAppMedia(ctx, ids); stored > 0 {
					if _, seriesID, ok := splitProviderDramaID(job.Drama.ID); ok {
						engine.downloader.markHongguoAppSeriesWorks(seriesID)
					}
				}
			}
		}
		return engine.downloader.resolveProviderMedia(ctx, Task{
			DramaID: job.Drama.ID, DramaTitle: job.Drama.Title, Chapter: job.Chapter, Index: job.Index})
	}
	manager.loadErr = manager.load()
	return manager
}

func (manager *nativeDownloads) load() error {
	if err := os.MkdirAll(manager.root, 0700); err != nil {
		return errors.New("无法创建下载目录，请检查存储空间和权限")
	}
	path := filepath.Join(manager.root, "index.json")
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return errors.New("无法读取下载记录，原文件已保留")
	}
	if !info.Mode().IsRegular() || info.Size() > nativeDownloadIndexLimit {
		// Move the unusable index aside and start clean: a damaged record
		// used to disable the whole download page with no way to recover.
		_ = os.Rename(path, fmt.Sprintf("%s.corrupt-%d", path, time.Now().Unix()))
		return nil
	}
	data, err := os.ReadFile(path)
	var records []*nativeDownloadRecord
	if err != nil || json.Unmarshal(data, &records) != nil || records == nil {
		_ = os.Rename(path, fmt.Sprintf("%s.corrupt-%d", path, time.Now().Unix()))
		return nil
	}
	seen := map[string]bool{}
	valid := make([]*nativeDownloadRecord, 0, len(records))
	for _, record := range records {
		// Skip individual bad entries instead of rejecting the whole file.
		if record == nil || record.Index < 1 || record.ID != nativeDownloadID(record.Drama.ID, record.Index) || seen[record.ID] {
			continue
		}
		seen[record.ID] = true
		if record.Folder != "" && record.Folder != record.Drama.Source+"/"+record.ID {
			continue
		}
		if record.File != "" && record.File != "media.mp4" && record.File != "index.m3u8" {
			continue
		}
		switch record.State {
		case "removing", "downloading", "queued", "paused", "failed", "completed":
			valid = append(valid, record)
		}
	}
	for _, record := range valid {
		record.Drama = migrateNativeDrama(record.Drama)
		if !nativeDownloadAvailable(record.nativeDownloadJob) {
			manager.jobs[record.ID] = record
			continue
		}
		switch record.State {
		case "removing":
			if err := os.RemoveAll(manager.jobDirectory(record.nativeDownloadJob)); err != nil {
				// A locked directory must not disable the page; keep a
				// failed row so the user can retry the removal.
				record.State = "failed"
				record.Error = "删除未完成，文件可能正被占用，请重试"
			} else {
				continue
			}
		case "downloading", "queued":
			record.State = "paused"
			record.Error = "上次下载已暂停，点击继续"
		case "paused", "failed", "completed":
		default:
			continue
		}
		manager.jobs[record.ID] = record
	}
	return nil
}

func nativeDownloadWrite(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".download-write-")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(temporary, path)
}

func (manager *nativeDownloads) saveLocked() error {
	if manager.loadErr != nil {
		return manager.loadErr
	}
	records := make([]nativeDownloadRecord, 0, len(manager.jobs))
	for _, job := range manager.jobs {
		records = append(records, *job)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Created < records[j].Created })
	data, err := json.Marshal(records)
	if err != nil {
		return err
	}
	if len(data) > nativeDownloadIndexLimit {
		return errors.New("下载记录超过保存上限，原索引已保留")
	}
	if err = nativeDownloadWrite(filepath.Join(manager.root, "index.json"), data); err != nil {
		return errors.New("保存下载记录失败，请检查剩余存储空间")
	}
	manager.lastSaved = time.Now()
	return nil
}

func (manager *nativeDownloads) snapshot() ([]nativeDownloadJob, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.loadErr != nil {
		return nil, manager.loadErr
	}
	jobs := make([]nativeDownloadJob, 0, len(manager.jobs))
	for _, record := range manager.jobs {
		if nativeDownloadAvailable(record.nativeDownloadJob) {
			jobs = append(jobs, record.nativeDownloadJob)
		}
	}
	// 按队列顺序（先入的先跑）排，和 scheduleLocked 取下一个任务的规则一致：
	// 这样界面上的行序就是实际下载顺序。以前这里是 Created 倒序，而 Created 是
	// 毫秒时间戳、在入队循环里逐个赋值，于是"下载全剧"入队 1..N 之后显示成了倒
	// 着的块（同一毫秒内的那批内部升序）——看着就是几百集排在最前面。
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].Created == jobs[j].Created {
			return jobs[i].Index < jobs[j].Index
		}
		return jobs[i].Created < jobs[j].Created
	})
	return jobs, nil
}

func (manager *nativeDownloads) enqueue(input nativeInput) (int, error) {
	return manager.enqueueContext(context.Background(), input)
}

func (manager *nativeDownloads) enqueueContext(ctx context.Context, input nativeInput) (int, error) {
	if !nativeDramaAvailable(input.Drama) {
		return 0, errNativeBuildSource
	}
	if _, _, valid := splitProviderDramaID(input.Drama.ID); !valid {
		return 0, errors.New("剧集信息无效")
	}
	if len(input.Entries) == 0 || len(input.Entries) > nativeDownloadBatchLimit {
		return 0, errors.New("请选择 1 至 500 集加入下载")
	}
	for _, entry := range input.Entries {
		if !nativeChapterAvailable(input.Drama, entry.Chapter) {
			return 0, errNativeBuildSource
		}
		if entry.Index < 1 || entry.Index > 100000 {
			return 0, errors.New("下载集数无效")
		}
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed || manager.moving {
		return 0, errors.New("下载目录正在迁移或队列已关闭，请稍后重试")
	}
	if manager.loadErr != nil {
		return 0, manager.loadErr
	}
	added := []string{}
	previous := map[string]nativeDownloadRecord{}
	repaired := 0
	tasks := manager.unarchivedCountLocked()
	rollback := func() {
		for id, old := range previous {
			*manager.jobs[id] = old
		}
		for _, id := range added {
			delete(manager.jobs, id)
		}
	}
	for _, entry := range input.Entries {
		if err := ctx.Err(); err != nil {
			rollback()
			return 0, err
		}
		id := nativeDownloadID(input.Drama.ID, entry.Index)
		if existing := manager.jobs[id]; existing != nil {
			if input.Force {
				if _, changed := previous[id]; !changed {
					previous[id] = *existing
				}
				existing.Drama = mergeNativeDrama(existing.Drama, input.Drama)
			}
			if input.Force && (existing.State == "failed" || existing.State == "completed" && !manager.localFilesValid(*existing)) {
				if existing.Archived {
					if tasks >= nativeDownloadCountLimit {
						rollback()
						return 0, errors.New("下载任务已满，请先清理已完成任务")
					}
					tasks++
				}
				repaired++
				existing.Chapter, existing.Quality = entry.Chapter, input.Quality
				existing.State, existing.Error, existing.Archived = "queued", "", false
				existing.RetryAt, existing.ResolveRetries = 0, 0
			}
			continue
		}
		if tasks >= nativeDownloadCountLimit {
			rollback()
			return 0, errors.New("下载记录已满，请清理不再需要的任务")
		}
		manager.jobs[id] = &nativeDownloadRecord{nativeDownloadJob: nativeDownloadJob{
			ID: id, Drama: input.Drama, Chapter: entry.Chapter, Index: entry.Index,
			Quality: input.Quality, State: "queued", Created: time.Now().UnixMilli()}}
		if manager.bySource {
			manager.jobs[id].Folder = input.Drama.Source + "/" + id
		}
		added = append(added, id)
		tasks++
	}
	if err := manager.saveLocked(); err != nil {
		rollback()
		return 0, err
	}
	manager.scheduleLocked()
	return len(added) + repaired, nil
}

func (manager *nativeDownloads) control(id, action string) error {
	return manager.controlExpected(id, action, "")
}

func (manager *nativeDownloads) controlExpected(id, action, version string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.loadErr != nil {
		return manager.loadErr
	}
	if manager.closed || manager.moving {
		return errors.New("下载目录正在迁移或队列已关闭，请稍后重试")
	}
	if action == "remove" && manager.mediaBusy {
		return errors.New("请等待合并或导出完成后再删除分集")
	}
	if version != "" {
		job := manager.jobs[id]
		if job == nil {
			return nil
		}
		if action != "remove" || job.State != "completed" || nativeDownloadVersion(job.nativeDownloadJob) != version {
			return errors.New("原分集已改变，已保留文件")
		}
	}
	previous := map[string]nativeDownloadRecord{}
	cancelIDs := []string{}
	remember := func(job *nativeDownloadRecord) { previous[job.ID] = *job }
	switch action {
	case "pauseAll", "resumeAll":
		for _, job := range manager.jobs {
			if !nativeDownloadAvailable(job.nativeDownloadJob) {
				continue
			}
			if action == "pauseAll" && (job.State == "queued" || job.State == "downloading") {
				remember(job)
				job.State = "paused"
				cancelIDs = append(cancelIDs, job.ID)
			}
			if action == "resumeAll" && (job.State == "paused" || job.State == "failed" || job.State == "queued" && job.RetryAt > 0) {
				remember(job)
				job.State, job.Error, job.Archived = "queued", "", false
				job.RetryAt, job.ResolveRetries = 0, 0
			}
		}
	case "pause", "resume", "remove", "archive", "restore":
		job := manager.jobs[id]
		if job == nil {
			return errors.New("下载任务不存在，请刷新列表")
		}
		if !nativeDownloadAvailable(job.nativeDownloadJob) {
			return errNativeBuildSource
		}
		remember(job)
		switch action {
		case "archive", "restore":
			if job.State != "completed" {
				return errors.New("只能清理已完成的下载任务，未完成的分集请先暂停或取消")
			}
			if action == "restore" && job.Archived && manager.unarchivedCountLocked() >= nativeDownloadCountLimit {
				return errors.New("下载任务已满，请先清理其他已完成任务；视频仍保留")
			}
			job.Archived = action == "archive"
		case "pause":
			if job.State == "queued" || job.State == "downloading" {
				job.State = "paused"
				cancelIDs = append(cancelIDs, id)
			}
		case "resume":
			if job.State == "paused" || job.State == "failed" || job.State == "queued" && job.RetryAt > 0 {
				job.State, job.Error, job.Archived = "queued", "", false
				job.RetryAt, job.ResolveRetries = 0, 0
			}
		case "remove":
			job.State, job.Error = "removing", ""
			cancelIDs = append(cancelIDs, id)
		}
	default:
		return errors.New("不支持的下载操作")
	}
	if err := manager.saveLocked(); err != nil {
		for key, value := range previous {
			*manager.jobs[key] = value
		}
		return err
	}
	for _, key := range cancelIDs {
		if cancel := manager.active[key]; cancel != nil {
			cancel()
		}
	}
	if action == "remove" && manager.active[id] == nil {
		job := manager.jobs[id]
		if err := os.RemoveAll(manager.jobDirectory(job.nativeDownloadJob)); err != nil {
			job.Error = "文件正在使用或无法删除，请退出播放后重试"
			_ = manager.saveLocked()
			return errors.New(job.Error)
		}
		delete(manager.jobs, id)
		if err := manager.saveLocked(); err != nil {
			job.Error = "文件已删除，记录尚未清理，请重试"
			manager.jobs[id] = job
			return err
		}
	}
	manager.scheduleLocked()
	return nil
}

// removeBatch 一次删掉多集。每集的 os.RemoveAll 很便宜，贵的是每删一集就
// 重写整份索引（2 万条约 30 MB，实测 234 ms/集）；整批只在最后落盘一次，
// 删一部一千集的剧从几分钟降到几秒。
func (manager *nativeDownloads) removeBatch(ctx context.Context, ids []string) (nativeDownloadControlResult, error) {
	result := nativeDownloadControlResult{Completed: []string{}, Failures: map[string]string{}}
	type pendingRemoval struct {
		id     string
		dir    string
		record nativeDownloadRecord
	}
	manager.mu.Lock()
	if manager.loadErr != nil {
		err := manager.loadErr
		manager.mu.Unlock()
		return result, err
	}
	if manager.closed || manager.moving {
		manager.mu.Unlock()
		return result, errors.New("下载目录正在迁移或队列已关闭，请稍后重试")
	}
	if manager.mediaBusy {
		manager.mu.Unlock()
		return result, errors.New("请等待合并或导出完成后再删除分集")
	}
	// 第一阶段只改内存状态并落盘一次：把任务标成 removing，真正的文件删除
	// 放到锁外。以前整批 500 集都在锁内 RemoveAll，界面轮询和合并请求会被
	// 一起堵住，上千集时就像软件卡死。
	previous := map[string]nativeDownloadRecord{}
	var pending []pendingRemoval
	changed := false
	for _, id := range ids {
		if ctx.Err() != nil {
			result.Failures[id] = "操作已停止，尚未处理此任务"
			continue
		}
		job := manager.jobs[id]
		if job == nil {
			// 已经删掉了：算完成，界面不会把它当成删除失败再放回来。
			result.Completed = append(result.Completed, id)
			continue
		}
		if !nativeDownloadAvailable(job.nativeDownloadJob) {
			result.Failures[id] = errNativeBuildSource.Error()
			continue
		}
		previous[id] = *job
		record := *job
		if manager.active[id] != nil {
			// 正在下载：取消后由 run() 清理目录，这一批不等它。
			job.State, job.Error = "removing", ""
			if cancel := manager.active[id]; cancel != nil {
				cancel()
			}
			changed = true
			result.Completed = append(result.Completed, id)
			continue
		}
		job.State, job.Error = "removing", ""
		pending = append(pending, pendingRemoval{id: id, dir: manager.jobDirectory(job.nativeDownloadJob), record: record})
		changed = true
	}
	if changed {
		if err := manager.saveLocked(); err != nil {
			for key, value := range previous {
				restored := value
				manager.jobs[key] = &restored
			}
			manager.mu.Unlock()
			return result, err
		}
	}
	manager.mu.Unlock()

	// 第二阶段在锁外删文件：慢磁盘/杀毒软件扫描时只影响这一批，不会拖住
	// 下载进度、界面轮询和合并。
	for _, item := range pending {
		if err := os.RemoveAll(item.dir); err != nil {
			result.Failures[item.id] = "文件正在使用或无法删除，请退出播放后重试"
			continue
		}
		result.Completed = append(result.Completed, item.id)
	}

	// 第三阶段回收记录：删成功的从队列移除，删失败的恢复原状态并带上错误，
	// 让界面把那一行重新显示出来。
	manager.mu.Lock()
	changed = false
	for _, item := range pending {
		if message, failed := result.Failures[item.id]; failed {
			if job := manager.jobs[item.id]; job != nil {
				*job = item.record
				job.Error = message
				changed = true
			}
			continue
		}
		if _, ok := manager.jobs[item.id]; ok {
			delete(manager.jobs, item.id)
			changed = true
		}
	}
	var saveErr error
	if changed {
		saveErr = manager.saveLocked()
	}
	manager.scheduleLocked()
	manager.mu.Unlock()
	if saveErr != nil {
		return result, saveErr
	}
	return result, nil
}

func (manager *nativeDownloads) scheduleLocked() {
	if manager.closed || manager.moving {
		return
	}
	for len(manager.active) < max(1, manager.concurrency) {
		now := time.Now().UnixMilli()
		var next *nativeDownloadRecord
		var earliest int64
		for _, candidate := range manager.jobs {
			if candidate.State != "queued" || manager.active[candidate.ID] != nil || !nativeDownloadAvailable(candidate.nativeDownloadJob) {
				continue
			}
			if candidate.RetryAt > now {
				if earliest == 0 || candidate.RetryAt < earliest {
					earliest = candidate.RetryAt
				}
				continue
			}
			if next == nil || candidate.Created < next.Created ||
				(candidate.Created == next.Created && candidate.Index < next.Index) {
				next = candidate
			}
		}
		if next == nil {
			if earliest > 0 {
				manager.armWakeLocked(earliest)
			}
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		next.State, next.Error = "downloading", ""
		next.RetryAt = 0
		manager.active[next.ID] = cancel
		if err := manager.saveLocked(); err != nil {
			next.State, next.Error = "failed", err.Error()
			delete(manager.active, next.ID)
			cancel()
			return
		}
		copy := next.nativeDownloadJob
		manager.workers.Add(1)
		go manager.run(ctx, copy)
	}
}

// armWakeLocked makes sure the queue is looked at again when the earliest
// delayed retry becomes due.  A single timer is enough: it always points at
// the nearest RetryAt, and re-arming with a later time is a no-op.
func (manager *nativeDownloads) armWakeLocked(at int64) {
	if manager.wakeAt > 0 && manager.wakeAt <= at {
		return
	}
	if manager.wake != nil {
		manager.wake.Stop()
	}
	manager.wakeAt = at
	delay := time.Until(time.UnixMilli(at))
	if delay < 0 {
		delay = 0
	}
	manager.wake = time.AfterFunc(delay, func() {
		manager.mu.Lock()
		manager.wake, manager.wakeAt = nil, 0
		manager.scheduleLocked()
		manager.mu.Unlock()
	})
}

func (manager *nativeDownloads) progress(id string, downloaded, total int64, progress float64) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	job := manager.jobs[id]
	if job == nil || job.State != "downloading" {
		return
	}
	job.Bytes, job.Total, job.Progress = downloaded, total, max(0, min(progress, 1))
	// 整份索引重写：2 万条时约 30 MB，1 秒一次会白写几十 MB/s。进度只是
	// 崩溃恢复用的参考值，5 秒粒度足够；状态变更仍会立刻落盘。
	if time.Since(manager.lastSaved) >= 5*time.Second {
		_ = manager.saveLocked()
	}
}

// noteQuality 取到流之后立刻把画质/码率写进任务：卡片的画质列不用等下载完才亮。
func (manager *nativeDownloads) noteQuality(id string, media providerMedia) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	job := manager.jobs[id]
	if job == nil {
		return
	}
	if job.ActualQuality != media.Quality || job.Bitrate != media.Bitrate {
		job.ActualQuality, job.Bitrate = media.Quality, media.Bitrate
		job.Revision++
	}
}

func (manager *nativeDownloads) run(ctx context.Context, job nativeDownloadJob) {
	defer manager.workers.Done()
	result, err := manager.transfer(ctx, job)
	interrupted := ctx.Err() != nil
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if cancel := manager.active[job.ID]; cancel != nil {
		cancel()
	}
	delete(manager.active, job.ID)
	record := manager.jobs[job.ID]
	if record == nil {
		return
	}
	switch {
	case record.State == "removing":
		if removeErr := os.RemoveAll(manager.jobDirectory(job)); removeErr == nil {
			delete(manager.jobs, job.ID)
		} else {
			record.State, record.Error = "failed", "文件无法删除，请稍后重试"
		}
	case err == nil:
		record.State, record.Progress = "completed", 1
		record.File, record.Key, record.ActualQuality = result.file, result.key, result.quality
		record.Bitrate = result.bitrate
		record.Revision++
		record.Error = ""
		record.RetryAt, record.ResolveRetries = 0, 0
	case record.State == "downloading" && !interrupted && errors.Is(err, errHongguoAppEmptyBody) && record.ResolveRetries < 3:
		// 站源风控的静默拒绝会持续几十秒到几分钟。先排回队列，隔一会儿
		// 自动再试，用户不用盯着列表手动点“继续”。
		record.ResolveRetries++
		record.State = "queued"
		record.RetryAt = time.Now().Add(manager.retryDelay(record.ResolveRetries)).UnixMilli()
		record.Error = "站源暂时拒绝请求，已排队自动重试"
	case record.State == "downloading":
		record.State = "failed"
		if interrupted {
			record.Error = "下载已中断，点击重试可继续"
		} else {
			record.Error = publicError(err).Error()
		}
	}
	if saveErr := manager.saveLocked(); saveErr != nil && manager.jobs[job.ID] != nil {
		record.State, record.Error = "failed", saveErr.Error()
	}
	manager.scheduleLocked()
}

func downloadResolveRetryDelay(attempt int) time.Duration {
	switch attempt {
	case 1:
		return 45 * time.Second
	case 2:
		return 90 * time.Second
	default:
		return 180 * time.Second
	}
}

func (manager *nativeDownloads) transfer(ctx context.Context, job nativeDownloadJob) (nativeDownloadResult, error) {
	media, err := manager.resolve(ctx, job)
	if err != nil {
		return nativeDownloadResult{}, err
	}
	choices := nativePlaybackChoices(media, job.Quality).media
	if len(choices) == 0 {
		return nativeDownloadResult{}, errors.New("该集没有可用的下载地址")
	}
	manager.noteQuality(job.ID, choices[0])
	var result nativeDownloadResult
	for _, option := range choices[:min(3, len(choices))] {
		if err = ctx.Err(); err != nil {
			break
		}
		result, err = manager.transferMedia(ctx, job, option)
		if err == nil {
			return result, nil
		}
	}
	return nativeDownloadResult{}, err
}

func (manager *nativeDownloads) localPlan(drama string, index int) (nativePlan, bool, error) {
	if !nativeDramaAvailable(nativeDrama{ID: drama}) {
		return nativePlan{}, false, errNativeBuildSource
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	record := manager.jobs[nativeDownloadID(drama, index)]
	if record == nil {
		return nativePlan{}, false, nil
	}
	if !nativeDownloadAvailable(record.nativeDownloadJob) {
		return nativePlan{}, false, errNativeBuildSource
	}
	if record.State != "completed" {
		if record.File != "" {
			return nativePlan{}, true, errNativeLocalFile
		}
		return nativePlan{}, false, nil
	}
	path := filepath.Join(manager.jobDirectory(record.nativeDownloadJob), record.File)
	if !manager.localFilesValid(*record) {
		record.State, record.Error = "failed", "本地文件缺失，请重新下载"
		record.Archived = false
		_ = manager.saveLocked()
		return nativePlan{}, true, errNativeLocalFile
	}
	return nativePlan{URL: path, Key: record.Key, Local: true, Quality: record.ActualQuality,
		Qualities: []int{}, Headers: map[string]string{}, RouteCount: 1}, true, nil
}

func (manager *nativeDownloads) close() {
	manager.mu.Lock()
	manager.closed = true
	if manager.wake != nil {
		manager.wake.Stop()
		manager.wake, manager.wakeAt = nil, 0
	}
	for id, cancel := range manager.active {
		if job := manager.jobs[id]; job != nil && job.State == "downloading" {
			job.State = "paused"
		}
		cancel()
	}
	_ = manager.saveLocked()
	manager.mu.Unlock()
	manager.workers.Wait()
}

func nativeDownloadError(status int) error {
	return fmt.Errorf("媒体服务器返回 HTTP %d，请稍后重试", status)
}
