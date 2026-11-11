package updater

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Settings 是更新功能的用户偏好，落盘 <dataDir>/update-settings.json
// （决策：Go 侧存储，不进 localStorage；0600 + 原子替换）。
type Settings struct {
	Autocheck    bool   `json:"autocheck"`    // 启动自动检查（默认开）
	Prerelease   bool   `json:"prerelease"`   // 测试版通道（含 prerelease）
	MirrorPrefix string `json:"mirrorPrefix"` // 自定义加速前缀，空=默认链
	IgnoredTag   string `json:"ignoredTag"`   // 「跳过此版本」的 tag
	LastCheckAt  int64  `json:"lastCheckAt"`  // 上次检查（epoch 秒），30s 冷却依据
}

func defaultSettings() Settings {
	return Settings{Autocheck: true}
}

// SettingsStore 带内存镜像的读写器（绑定层与 goroutine 并发访问）。
type SettingsStore struct {
	mu     sync.Mutex
	path   string
	cache  Settings
	loaded bool
}

// NewSettingsStore path 一般为 filepath.Join(native.DataDir(), "update-settings.json")。
func NewSettingsStore(path string) *SettingsStore {
	return &SettingsStore{path: path}
}

// Get 返回当前设置（首次读取文件，缺失/损坏回落默认值——损坏不覆盖，等待 Save）。
func (s *SettingsStore) Get() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getLocked()
}

// getLocked 无锁读取（调用方须持有 s.mu；sync.Mutex 不可重入，MergePatch 等持锁路径必须用它）。
func (s *SettingsStore) getLocked() Settings {
	if !s.loaded {
		s.cache = defaultSettings()
		if data, err := os.ReadFile(s.path); err == nil {
			var got Settings
			if json.Unmarshal(data, &got) == nil {
				s.cache = got
			}
		}
		s.loaded = true
	}
	return s.cache
}

// Save 全量落盘（tmp+rename 原子替换，0600）。同步刷新内存镜像，
// 否则 loaded 后的 Get 会一直返回旧 cache（历史 bug：Startup 读到过期 Autocheck）。
func (s *SettingsStore) Save(set Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.saveLocked(set); err != nil {
		return err
	}
	s.cache = set
	s.loaded = true
	return nil
}

// MergePatch 按前端 UpdateSettingsSet(json) 的 map 语义合并：
// 仅覆盖出现的键；未知键忽略；值类型不符报错。返回合并后的设置。
func (s *SettingsStore) MergePatch(patch map[string]any) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.getLocked()
	next := cur
	for k, v := range patch {
		switch k {
		case "autocheck":
			b, err := castBool(v)
			if err != nil {
				return cur, fmt.Errorf("autocheck: %w", err)
			}
			next.Autocheck = b
		case "prerelease":
			b, err := castBool(v)
			if err != nil {
				return cur, fmt.Errorf("prerelease: %w", err)
			}
			next.Prerelease = b
		case "mirrorPrefix":
			str, ok := v.(string)
			if !ok {
				return cur, fmt.Errorf("mirrorPrefix 需为字符串")
			}
			next.MirrorPrefix = NormalizePrefix(str)
		case "ignoredTag":
			str, ok := v.(string)
			if !ok {
				return cur, fmt.Errorf("ignoredTag 需为字符串")
			}
			next.IgnoredTag = str
		case "lastCheckAt":
			switch n := v.(type) {
			case float64:
				next.LastCheckAt = int64(n)
			case int64:
				next.LastCheckAt = n
			default:
				return cur, fmt.Errorf("lastCheckAt 需为数字")
			}
		}
	}
	if err := s.saveLocked(next); err != nil {
		return cur, err
	}
	s.cache = next
	return next, nil
}

func (s *SettingsStore) saveLocked(set Settings) error {
	if dir := filepath.Dir(s.path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func castBool(v any) (bool, error) {
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("需为布尔值")
	}
	return b, nil
}
