package updater

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	if st.Mode().Perm() != 0o600 {
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
