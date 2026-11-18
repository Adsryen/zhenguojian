package updater

import (
	"os"
	"path/filepath"
	"sync"
)

// electronInstallProbe 指向 os.Executable 便于单测注入自身路径；
// 生产行为不变。
var electronInstallProbe = os.Executable

// IsElectronInstall 判定当前 sidecar 运行在 Electron 安装形态：
// ①壳身份 env（electron/main.js spawn 时注入 ZHENGUO_SHELL=electron）；
// ②自身 exe 位于 <install>\resources\ 且同目录有 app.asar、上一级有
// zgj-v2.exe——结构断言防「dev 态 build/bin 借了 env」误判。
// 结果缓存（进程生命周期内目录结构不变；测试用 ResetElectronInstallCache）。
func IsElectronInstall() bool {
	electronInstallOnce.Do(func() {
		if os.Getenv("ZHENGUO_SHELL") != "electron" {
			return
		}
		exe, err := electronInstallProbe()
		if err != nil {
			return
		}
		root := filepath.Dir(filepath.Dir(exe))
		if _, err := os.Stat(filepath.Join(root, "resources", "app.asar")); err != nil {
			return
		}
		if _, err := os.Stat(filepath.Join(root, "zgj-v2.exe")); err != nil {
			return
		}
		electronInstallCached = true
	})
	return electronInstallCached
}

var (
	electronInstallOnce   sync.Once
	electronInstallCached bool
)

// ResetElectronInstallCache 仅供测试：清缓存让下次判定重算。
func ResetElectronInstallCache() {
	electronInstallOnce = sync.Once{}
	electronInstallCached = false
}
