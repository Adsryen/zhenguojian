package updater

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadLocalVersionEmbeddedWins 锁定判定顺序：构建期注入版本 > 同级 build-info.json，
// 真机「单文件更新后仍提示有更新」的回归即源于旧文件抢先/缺 version。
func TestLoadLocalVersionEmbeddedWins(t *testing.T) {
	old := EmbeddedVersion
	defer func() { EmbeddedVersion = old }()

	dir := t.TempDir()
	body := []byte(`{"revision":"a6d9a11f2808","version":"v2.0.0-alpha.8","modified":false}`)
	if err := os.WriteFile(filepath.Join(dir, "build-info.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "真果鉴.exe")
	if err := os.WriteFile(exe, []byte("MZ"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1) 注入新值：压过同级旧文件
	EmbeddedVersion = "v2.0.0-alpha.24"
	if lv := LoadLocalVersion(exe); lv.Version != "v2.0.0-alpha.24" || lv.Dev {
		t.Fatalf("embedded should win: %+v", lv)
	}

	// 2) 无注入：回落文件 version（旧产物兼容）
	EmbeddedVersion = ""
	if lv := LoadLocalVersion(exe); lv.Version != "v2.0.0-alpha.8" || lv.Dev {
		t.Fatalf("file fallback: %+v", lv)
	}

	// 3) 无注入且文件缺 version：dev
	if err := os.WriteFile(filepath.Join(dir, "build-info.json"), []byte(`{"revision":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if lv := LoadLocalVersion(exe); !lv.Dev || lv.Version != "" {
		t.Fatalf("missing version → dev: %+v", lv)
	}

	// 4) 注入值形态非法：忽略，继续走文件链
	EmbeddedVersion = "not-a-version"
	if err := os.WriteFile(filepath.Join(dir, "build-info.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if lv := LoadLocalVersion(exe); lv.Version != "v2.0.0-alpha.8" || lv.Dev {
		t.Fatalf("invalid embedded ignored → file: %+v", lv)
	}
}
