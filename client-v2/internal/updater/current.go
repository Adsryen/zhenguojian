package updater

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// EmbeddedVersion 构建期注入的发布版本号（main.buildVersion 转发）；空=开发构建。
// 由 main() 在启动时赋值，测试可直接置值。
var EmbeddedVersion string

// LocalVersion 本地当前版本判定结果。
type LocalVersion struct {
	Version string `json:"version"` // build-info.json 的 version 原样（可能空）
	Dev     bool   `json:"dev"`     // 无合法版本号 → 开发构建，任何发布版都算更新
}

// LoadLocalVersion 判定本地版本。读取顺序：构建期注入的 EmbeddedVersion 最权威
// （随二进制走，单文件更新后即刻生效，防同级残留旧 build-info.json 污染），其次
// exe 同级 build-info.json 的 version 字段（旧产物兼容）；都拿不到或形态非法记
// Dev=true，UI 标注「本地为开发构建」。
func LoadLocalVersion(exePath string) LocalVersion {
	if IsValidVersion(EmbeddedVersion) {
		return LocalVersion{Version: EmbeddedVersion, Dev: false}
	}
	lv := LocalVersion{Dev: true}
	if exePath == "" {
		return lv
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(exePath), "build-info.json"))
	if err != nil {
		return lv
	}
	var info struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(data, &info) != nil || info.Version == "" {
		return lv
	}
	if !IsValidVersion(info.Version) {
		return lv
	}
	lv.Version = info.Version
	lv.Dev = false
	return lv
}

// LocalVersionFromExecutable 供绑定层使用：os.Executable() + LoadLocalVersion。
func LocalVersionFromExecutable() LocalVersion {
	exe, err := os.Executable()
	if err != nil {
		return LocalVersion{Dev: true}
	}
	return LoadLocalVersion(exe)
}
