package updater

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// manualCooldown 手动检查冷却（照抄参照 guoapp app_update.go 语义 30s）。
const manualCooldown = 30 * time.Second

// Emiter 事件出口（绑定层注入 runtime.EventsEmit 包装；测试注入 channel 收集器）。
type Emiter func(event string, data map[string]any)

// CheckResult 一次检查的结果快照，也是绑定层 UpdateCheck/UpdateStatus 的 JSON 面。
type CheckResult struct {
	Available  bool     `json:"available"`
	Current    string   `json:"current"`
	Dev        bool     `json:"dev"`
	Channel    string   `json:"channel"` // stable | prerelease
	SkippedTag string   `json:"skippedTag,omitempty"`
	Cooldown   bool     `json:"cooldown,omitempty"`
	CheckedAt  int64    `json:"checkedAt"`
	Error      string   `json:"error,omitempty"`
	Release    *Release `json:"release,omitempty"`
}

// DownloadState 当前/最近一次下载任务状态。
type DownloadState struct {
	Tag      string  `json:"tag"`
	Asset    string  `json:"asset"`
	Dest     string  `json:"dest"`
	State    string  `json:"state"` // idle | downloading | done | error | canceled
	Total    int64   `json:"total"`
	Done     int64   `json:"done"`
	Pct      float64 `json:"pct"`
	SpeedBps float64 `json:"speedBps"`
	Error    string  `json:"error,omitempty"`
}

// Service 把 semver/chain/download/settings/bat 串成绑定层可直接调用的门面。
type Service struct {
	DataDir  string
	Local    LocalVersion
	Settings *SettingsStore

	checkClient *http.Client
	dlClient    *http.Client
	emit        Emiter

	mu         sync.Mutex
	lastCheck  *CheckResult
	releases   []Release
	checking   bool
	lastManual time.Time

	dlMu     sync.Mutex
	dl       *DownloadState
	dlCancel context.CancelFunc

	startupOnce  sync.Once
	StartupDelay time.Duration // 决策：3s；测试缩短
}

// NewService 构造服务。emit 可为 nil（纯测试）。
func NewService(dataDir string, local LocalVersion, settingsPath string, emit Emiter) *Service {
	return &Service{
		DataDir:      dataDir,
		Local:        local,
		Settings:     NewSettingsStore(settingsPath),
		checkClient:  &http.Client{Timeout: perCandidateTimeout},
		dlClient:     NewDownloadClient(),
		emit:         emit,
		StartupDelay: 3 * time.Second,
	}
}

func (s *Service) fireEvent(ev string, data map[string]any) {
	if s.emit != nil {
		s.emit(ev, data)
	}
}

// Channel 当前通道名（UI 展示）。
func (s *Service) Channel() string {
	if s.Settings.Get().Prerelease {
		return "prerelease"
	}
	return "stable"
}

// Check 执行一次检查；force=false 时套用 30s 手动冷却（参照语义）。
// 成功/失败都会更新 lastCheck 与 settings.lastCheckAt。
func (s *Service) Check(ctx context.Context, force bool) (*CheckResult, error) {
	s.mu.Lock()
	if s.checking {
		res := s.lastCheck
		s.mu.Unlock()
		if res != nil {
			return res, nil
		}
		return nil, errors.New("检查进行中，请稍候")
	}
	set := s.Settings.Get()
	if !force && s.lastCheck != nil && time.Since(s.lastManual) < manualCooldown {
		cp := *s.lastCheck
		cp.Cooldown = true
		s.mu.Unlock()
		return &cp, nil
	}
	s.checking = true
	s.lastManual = time.Now()
	s.mu.Unlock()

	res := s.runCheck(ctx, set)

	s.mu.Lock()
	s.checking = false
	s.lastCheck = res
	if res.CheckedAt > 0 {
		_ = s.Settings.Save(withLastCheckAt(set, res.CheckedAt))
	}
	s.mu.Unlock()

	if res.Error != "" {
		return res, fmt.Errorf("%s", res.Error)
	}
	return res, nil
}

func withLastCheckAt(set Settings, ts int64) Settings {
	set.LastCheckAt = ts
	return set
}

func (s *Service) runCheck(ctx context.Context, set Settings) *CheckResult {
	channel := "stable"
	if set.Prerelease {
		channel = "prerelease"
	}
	res := &CheckResult{Current: s.Local.Version, Dev: s.Local.Dev, Channel: channel, CheckedAt: time.Now().Unix()}
	urls := ChainCandidates(ReleasesAPIURL, set.MirrorPrefix)
	data, err := FetchJSON(ctx, s.checkClient, urls, 4<<20)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	releases, err := ParseReleases(data)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	rel := PickLatest(releases, set.Prerelease)
	if rel == nil {
		res.Error = "未找到 v2 发布版本"
		return res
	}
	s.mu.Lock()
	s.releases = releases
	s.mu.Unlock()

	if set.IgnoredTag != "" && rel.Tag == set.IgnoredTag {
		res.SkippedTag = rel.Tag
		res.Release = rel
		return res // available=false，UI 提示「已跳过 vX」
	}
	if s.Local.Dev {
		res.Available = true // 决策：开发构建时任何发布版都算更新
		res.Release = rel
		return res
	}
	newer, err := IsNewer(rel.Tag, s.Local.Version)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Available = newer
	res.Release = rel
	return res
}

// Status 给 UpdateStatus 绑定：当前版本 + 最近检查结果 + 下载状态 + 设置摘要。
// downloadSnapshot 返回当前下载状态的结构副本（无任务=nil）。
// 下载 goroutine 在 dlMu 下持续改 s.dl 字段：外部（Status/PrepareApply/测试）必须拿副本，避免数据竞争。
func (s *Service) downloadSnapshot() *DownloadState {
	s.dlMu.Lock()
	defer s.dlMu.Unlock()
	if s.dl == nil {
		return nil
	}
	c := *s.dl
	return &c
}

func (s *Service) Status() map[string]any {
	s.mu.Lock()
	last := s.lastCheck
	checking := s.checking
	s.mu.Unlock()
	dl := s.downloadSnapshot()
	set := s.Settings.Get()
	out := map[string]any{
		"current":  s.Local.Version,
		"dev":      s.Local.Dev,
		"channel":  s.Channel(),
		"checking": checking,
		"settings": settingsMap(set),
	}
	if last != nil {
		out["lastCheck"] = last
	}
	if dl != nil {
		out["download"] = dl
	}
	return out
}

func settingsMap(set Settings) map[string]any {
	return map[string]any{
		"autocheck":    set.Autocheck,
		"prerelease":   set.Prerelease,
		"mirrorPrefix": set.MirrorPrefix,
		"ignoredTag":   set.IgnoredTag,
		"lastCheckAt":  set.LastCheckAt,
	}
}

// ReleaseByTag 从最近一次解析的发布列表按 tag 查找（下载用）。
func (s *Service) ReleaseByTag(tag string) (*Release, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.releases {
		if strings.EqualFold(s.releases[i].Tag, tag) {
			return &s.releases[i], true
		}
	}
	return nil, false
}

// UpdateDir 某发布对应的本地更新目录 <dataDir>/updates/<tag>。
func (s *Service) UpdateDir(tag string) string {
	return filepath.Join(s.DataDir, "updates", sanitizeTag(tag))
}

func sanitizeTag(tag string) string {
	return strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", `"`, "_", "<", "_", ">", "_", "|", "_").Replace(tag)
}

// Download 启动（或续传）一次资产下载；异步执行，进度走 update:progress 事件。
// 已有进行中任务时返回错误（单任务并发，取消后再来）。
func (s *Service) Download(tag, asset string) error {
	rel, ok := s.ReleaseByTag(tag)
	if !ok {
		return fmt.Errorf("请先检查更新或重新检查后再下载 %s", tag)
	}
	a, err := rel.FindAsset(asset)
	if err != nil {
		return err
	}
	set := s.Settings.Get()
	destDir := s.UpdateDir(tag)
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return err
	}
	dest := filepath.Join(destDir, filepath.Base(a.Name))

	s.dlMu.Lock()
	if s.dl != nil && s.dl.State == StateDownloading {
		s.dlMu.Unlock()
		return errors.New("已有下载任务进行中，请先取消或等待完成")
	}
	ctx, cancel := context.WithCancel(context.Background())
	st := &DownloadState{Tag: tag, Asset: a.Name, Dest: dest, State: StateDownloading, Total: a.Size}
	s.dl = st
	s.dlCancel = cancel
	s.dlMu.Unlock()

	go func() {
		defer cancel()
		d := &Downloader{
			Client: s.dlClient,
			OnProgress: func(p Progress) {
				s.dlMu.Lock()
				s.dl.Total, s.dl.Done, s.dl.Pct, s.dl.SpeedBps = p.Total, p.Done, p.Pct, p.SpeedBps
				s.dlMu.Unlock()
				s.fireEvent("update:progress", map[string]any{
					"tag": tag, "asset": p.Asset, "total": p.Total, "done": p.Done,
					"pct": p.Pct, "speedBps": p.SpeedBps, "state": p.State, "error": p.Error,
				})
			},
		}
		urls := ChainCandidates(a.URL, set.MirrorPrefix)
		err := d.Fetch(ctx, urls, dest, a.Size)
		s.dlMu.Lock()
		switch {
		case err == nil:
			s.dl.State = StateDone
			s.dl.Pct, s.dl.Done = 100, a.Size
			s.fireEvent("update:progress", map[string]any{"tag": tag, "asset": a.Name, "state": StateDone, "pct": 100, "done": a.Size, "total": a.Size})
		case errors.Is(err, ErrCanceled):
			s.dl.State = "canceled"
			s.fireEvent("update:progress", map[string]any{"tag": tag, "asset": a.Name, "state": "canceled", "error": "canceled"})
		default:
			s.dl.State = StateError
			s.dl.Error = err.Error()
		}
		s.dlMu.Unlock()
		_ = err
	}()
	return nil
}

// CancelDownload 取消进行中的下载；.part 保留以便续传。
func (s *Service) CancelDownload() error {
	s.dlMu.Lock()
	defer s.dlMu.Unlock()
	if s.dlCancel == nil || s.dl == nil || s.dl.State != StateDownloading {
		return errors.New("当前没有进行中的下载")
	}
	s.dlCancel()
	return nil
}

// PrepareApplyResult 应用更新的计划（绑定层执行 bat/Quit 或手动解压提示）。
type PrepareApplyResult struct {
	Kind      string `json:"kind"`      // restart | manual
	BatPath   string `json:"batPath"`   // restart：待写入的 bat 路径
	BatText   string `json:"batText"`   // restart：bat 内容
	UpdateDir string `json:"updateDir"` // manual：zip 所在目录
	Hint      string `json:"hint"`      // manual：给用户的说明
}

// PrepareApply 依据当前已完成的下载生成应用计划。
// exe 资产 + Windows → restart 计划（bat 文本）；zip 资产 → manual 提示；其他 → 错误。
func (s *Service) PrepareApply(tempDir string, pid int) (*PrepareApplyResult, error) {
	dl := s.downloadSnapshot()
	if dl == nil || dl.State != StateDone {
		return nil, errors.New("尚未完成下载更新包")
	}
	if st, err := os.Stat(dl.Dest); err != nil || st.Size() == 0 {
		return nil, fmt.Errorf("更新包文件缺失：%s", dl.Dest)
	}
	low := strings.ToLower(dl.Asset)
	if strings.HasSuffix(low, ".exe") {
		if !ApplySupported {
			return nil, errors.New("仅 Windows 支持自动更新")
		}
		curExe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("无法定位当前程序：%w", err)
		}
		spec := ApplySpec{PID: pid, NewExe: dl.Dest, CurExe: curExe, UpdateDir: s.UpdateDir(dl.Tag)}
		batPath := filepath.Join(tempDir, fmt.Sprintf("zgj-update-%d.bat", pid))
		return &PrepareApplyResult{Kind: "restart", BatPath: batPath, BatText: BuildApplyBat(spec), UpdateDir: spec.UpdateDir}, nil
	}
	if strings.HasSuffix(low, ".zip") {
		return &PrepareApplyResult{
			Kind:      "manual",
			UpdateDir: filepath.Dir(dl.Dest),
			Hint:      fmt.Sprintf("已下载 %s 到 %s ，请关闭程序后手动解压覆盖安装目录，再重新启动。", dl.Asset, filepath.Dir(dl.Dest)),
		}, nil
	}
	return nil, fmt.Errorf("资产 %s 不支持自动应用", dl.Asset)
}

// Startup 懒触发启动自动检查（决策：前端 mount 调 updateStartup，绑定内首次调用起 goroutine）。
// 3s 延迟给壳启动让路；settings.autocheck 关闭或被跳过版本命中时不发事件。
func (s *Service) Startup() {
	s.startupOnce.Do(func() {
		go func() {
			time.Sleep(s.StartupDelay)
			set := s.Settings.Get()
			if !set.Autocheck {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			res := s.runCheck(ctx, set)
			s.mu.Lock()
			s.lastCheck = res
			if res.CheckedAt > 0 {
				_ = s.Settings.Save(withLastCheckAt(set, res.CheckedAt))
			}
			s.mu.Unlock()
			if res.Available && res.Release != nil {
				s.fireEvent("update:available", map[string]any{
					"tag": res.Release.Tag, "version": res.Release.Version, "publishedAt": res.Release.PublishedAt,
					"body": res.Release.Body, "current": res.Current, "dev": res.Dev, "channel": res.Channel,
					"assets": res.Release.SelectableAssets(),
				})
			}
		}()
	})
}
