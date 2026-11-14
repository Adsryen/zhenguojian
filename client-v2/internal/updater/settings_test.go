package updater

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSettingsDefaultOnMissing(t *testing.T) {
	dir := t.TempDir()
	s := NewSettingsStore(filepath.Join(dir, "update-settings.json"))
	got := s.Get()
	if !got.Autocheck || got.Prerelease || got.MirrorPrefix != "" || got.IgnoredTag != "" || got.LastCheckAt != 0 {
		t.Errorf("default wrong: %+v", got)
	}
}

func TestSettingsSaveReloadAndMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "update-settings.json")
	s := NewSettingsStore(path)
	want := Settings{Autocheck: false, Prerelease: true, MirrorPrefix: "https://m/", IgnoredTag: "v9.9.9", LastCheckAt: 1730000000}
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// ⚠ 第五轮真机反馈连带修正：这条用例此前只在 Linux 上跑过，
	//    windows job 加上 internal 测试后当场红 9 条。逐条核过都是
	//    **测试自己假设 POSIX**，不是产品在 Windows 上不对；
	//    但"测试假设错"与"产品真错"必须分开核过才能这么说，
	//    每条的判据写在各自注释里。
	// Windows 上没有 POSIX 权限位（CI 实测 -rw-rw-rw-），
	// 0600 这个意图只能靠"文件位于当前用户 profile 目录内"来近似保证。
	// 所以这里在 Windows 上跳过 mode 检查，但**不假装验证过私密性**：
	// 真正的访问控制是 ACL，不在本用例范围内，已另记一条待办。
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("mode=%v want 0600", st.Mode().Perm())
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("tmp leftover after atomic rename")
	}
	s2 := NewSettingsStore(path)
	if got := s2.Get(); got != want {
		t.Errorf("reload mismatch: %+v want %+v", got, want)
	}
}

func TestSettingsCorruptFallsBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "update-settings.json")
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := NewSettingsStore(path).Get()
	if !got.Autocheck {
		t.Errorf("corrupt file should fall back to defaults, got %+v", got)
	}
}

func TestSettingsMergePatch(t *testing.T) {
	dir := t.TempDir()
	s := NewSettingsStore(filepath.Join(dir, "s.json"))
	base := s.Get()
	got, err := s.MergePatch(map[string]any{"autocheck": false, "prerelease": true, "unknownKey": 1})
	if err != nil {
		t.Fatal(err)
	}
	if got.Autocheck || !got.Prerelease {
		t.Errorf("patch wrong: %+v", got)
	}
	if got.IgnoredTag != base.IgnoredTag {
		t.Error("unmentioned keys must be kept")
	}
	// 前缀归一 + 类型校验
	got, err = s.MergePatch(map[string]any{"mirrorPrefix": "x.example"})
	if err != nil || got.MirrorPrefix != "https://x.example/" {
		t.Fatalf("prefix normalize: %+v err=%v", got, err)
	}
	if _, err := s.MergePatch(map[string]any{"autocheck": "yes"}); err == nil {
		t.Error("type mismatch should error")
	}
	if _, err := s.MergePatch(map[string]any{"lastCheckAt": "abc"}); err == nil {
		t.Error("non-numeric lastCheckAt should error")
	}
	// 落盘一致性
	var raw map[string]any
	data, _ := os.ReadFile(filepath.Join(dir, "s.json"))
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["mirrorPrefix"] != "https://x.example/" || raw["prerelease"] != true {
		t.Errorf("persisted content wrong: %v", raw)
	}
}

func TestLoadLocalVersion(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "真果鉴.exe")
	os.WriteFile(exe, []byte("MZ"), 0o600)
	// 无 build-info.json → dev
	if lv := LoadLocalVersion(exe); !lv.Dev {
		t.Errorf("missing build-info should be dev: %+v", lv)
	}
	// 有 version 字段
	write := func(content string) {
		os.WriteFile(filepath.Join(dir, "build-info.json"), []byte(content), 0o600)
	}
	write(`{"version":"v2.0.0-alpha.3","revision":"abc"}`)
	if lv := LoadLocalVersion(exe); lv.Dev || lv.Version != "v2.0.0-alpha.3" {
		t.Errorf("want parsed version, got %+v", lv)
	}
	// version 缺失/非法 → dev
	write(`{"revision":"abc"}`)
	if lv := LoadLocalVersion(exe); !lv.Dev {
		t.Errorf("no version field should be dev: %+v", lv)
	}
	write(`{"version":"garbage"}`)
	if lv := LoadLocalVersion(exe); !lv.Dev {
		t.Errorf("invalid version should be dev: %+v", lv)
	}
	// CI dev 注入值 v0.0.0-dev：合法 semver-lite → 非 dev
	write(`{"version":"v0.0.0-dev"}`)
	if lv := LoadLocalVersion(exe); lv.Dev || lv.Version != "v0.0.0-dev" {
		t.Errorf("v0.0.0-dev should count as parsable current: %+v", lv)
	}
}
