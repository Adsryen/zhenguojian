package updater

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// LocalVersion 本地当前版本判定结果。
type LocalVersion struct {
	Version string `json:"version"` // build-info.json 的 version 原样（可能空）
	Dev     bool   `json:"dev"`     // 无合法版本号 → 开发构建，任何发布版都算更新
}

// LoadLocalVersion 读 exe 同级 build-info.json 的 version 字段（决策约定读取顺序：
// 文件缺失、JSON 无 version、版本形态非法，任一情况都记 Dev=true，UI 标注「本地为开发构建」）。
func LoadLocalVersion(exePath string) LocalVersion {
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
