package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"zhenguojian/client-v2/internal/native"
	"zhenguojian/client-v2/internal/updater"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ---------- V-UPD 程序内更新绑定 ----------
// 所有权说明：禁改 app.go，更新服务在此文件内做包级懒加载单例（全程序仅一个 App，
// 无多实例风险）；事件出口闭包在触发时刻读取 a.ctx，避免 OnStartup 前空指针。

var (
	updateSvcOnce sync.Once
	updateSvc     *updater.Service
)

func (a *App) updateService() *updater.Service {
	updateSvcOnce.Do(func() {
		dir, err := native.DataDir()
		if err != nil || dir == "" {
			dir = filepath.Join(os.TempDir(), "ZhenguoJian-update")
		}
		emit := func(ev string, data map[string]any) {
			ctx := a.ctx
			if ctx == nil {
				return
			}
			runtime.EventsEmit(ctx, ev, data)
		}
		updateSvc = updater.NewService(dir, updater.LocalVersionFromExecutable(),
			filepath.Join(dir, "update-settings.json"), emit)
	})
	return updateSvc
}

// UpdateStartup 前端 App.vue mount 调用：懒触发启动自动检查（首次调用起 goroutine，
// 内部 sleep 3s 让壳启动，settings.autocheck 开且发现更新时发 update:available）。
func (a *App) UpdateStartup() string {
	a.updateService().Startup()
	return `{"ok":true}`
}

// UpdateCheck 手动检查更新；force=false 时 30s 冷却（返回缓存结果并标 cooldown）。
// 永远返回 CheckResult JSON（网络失败在 error 字段内），前端无需分支解析。
func (a *App) UpdateCheck(force bool) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	res, _ := a.updateService().Check(ctx, force)
	return marshalJSON(res)
}

// UpdateStatus 当前版本/通道/最近检查/下载状态/设置摘要。
func (a *App) UpdateStatus() (string, error) {
	return marshalJSON(a.updateService().Status())
}

// UpdateDownload 下载（或续传）指定 tag 的资产；asset 空=默认 zhenGuoJian-v2.exe。
// 异步执行，进度经 update:progress 事件。
func (a *App) UpdateDownload(tag, asset string) (string, error) {
	if err := a.updateService().Download(tag, asset); err != nil {
		return "", err
	}
	return `{"ok":true}`, nil
}

// UpdateCancel 取消进行中的下载（.part 保留，可续传）。
func (a *App) UpdateCancel() (string, error) {
	if err := a.updateService().CancelDownload(); err != nil {
		return "", err
	}
	return `{"ok":true}`, nil
}

// UpdateApply 应用已下载的更新：exe → 写 %TEMP%\zgj-update-<pid>.bat 分离启动后退出本程序；
// zip → 返回手动解压指引；非 Windows → 错误「仅 Windows 支持自动更新」。
func (a *App) UpdateApply() (string, error) {
	svc := a.updateService()
	plan, err := svc.PrepareApply(os.TempDir(), os.Getpid())
	if err != nil {
		return "", err
	}
	if plan.Kind != "restart" {
		return marshalJSON(plan)
	}
	if err := os.WriteFile(plan.BatPath, []byte(plan.BatText), 0o600); err != nil {
		return "", err
	}
	if err := updater.LaunchDetached(plan.BatPath); err != nil {
		return "", err
	}
	go func() {
		time.Sleep(300 * time.Millisecond) // 让 bat 进入轮询，再走 Wails 正常退出
		a.allowQuit()                      // V-TRAY：放行 beforeClose（否则 X-拦截逻辑会只隐藏不退出，bat 永远等不到进程结束）
		runtime.Quit(a.ctx)
	}()
	return marshalJSON(map[string]any{"kind": "restart", "restarting": true})
}

// UpdateSettingsGet 更新设置全量 JSON（autocheck/prerelease/mirrorPrefix/ignoredTag/lastCheckAt）。
func (a *App) UpdateSettingsGet() (string, error) {
	svc := a.updateService()
	return marshalJSON(svc.Settings.Get())
}

// UpdateSettingsSet 合并补丁式保存（仅覆盖出现的键），落盘 Go 侧 update-settings.json。
func (a *App) UpdateSettingsSet(patchJSON string) (string, error) {
	patch, err := unmarshalObject(patchJSON)
	if err != nil {
		return "", err
	}
	set, err := a.updateService().Settings.MergePatch(patch)
	if err != nil {
		return "", err
	}
	return marshalJSON(set)
}
