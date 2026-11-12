package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/debug"

	"zhenguojian/client-v2/internal/native"
)

// ---------- V2-4 设置绑定 ----------

// GetResourceSettings → JSON: 网络与下载设置快照（核心权威值）。
func (a *App) GetResourceSettings() (string, error) {
	data, err := native.ResourceSettings()
	if err != nil {
		return "", err
	}
	return marshalJSON(data)
}

// SaveResourceSettings settingsJSON = 仅含显式字段的对象；核心校验并即时生效。
func (a *App) SaveResourceSettings(settingsJSON string) (string, error) {
	settings, err := unmarshalObject(settingsJSON)
	if err != nil {
		return "", err
	}
	data, err := native.SaveResourceSettings(settings)
	if err != nil {
		return "", err
	}
	return marshalJSON(data)
}

// GetBuildInfo 打包指纹与版本：构建期注入的 buildVersion 永远置顶（防同级残留
// 旧 build-info.json 把版本号/指纹拉回旧值），其余字段取 exe 同级 build-info.json
// （发布流程写入），再退构建期 VCS 戳，都缺失时返回 unknown。
func (a *App) GetBuildInfo() string {
	out := map[string]any{}
	if exe, err := os.Executable(); err == nil {
		if body, err := os.ReadFile(filepath.Join(filepath.Dir(exe), "build-info.json")); err == nil {
			var file map[string]any
			if json.Unmarshal(body, &file) == nil {
				for k, v := range file {
					out[k] = v
				}
			}
		}
	}
	if len(out) == 0 {
		if bi := readBuildVCS(); bi != nil {
			for k, v := range bi {
				out[k] = v
			}
		}
	}
	if buildVersion != "" {
		out["version"] = buildVersion
	}
	if len(out) == 0 {
		return `{"revision":"unknown"}`
	}
	if s, err := marshalJSON(out); err == nil {
		return s
	}
	return `{"revision":"unknown"}`
}

// readBuildVCS 读取 go build 默认注入的 VCS 戳（仅构建环境位于 git 工作树
// 内时存在）；无戳返回 nil。
func readBuildVCS() map[string]any {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}
	out := map[string]any{}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			out["revision"] = s.Value
		case "vcs.time":
			if len(s.Value) >= 10 {
				out["commitTime"] = s.Value[:10]
			}
		case "vcs.modified":
			out["modified"] = s.Value == "true"
		}
	}
	if _, ok := out["revision"]; !ok {
		return nil
	}
	return out
}
