package updater

import (
	"fmt"
	"strings"
)

// ApplySpec 是生成 Windows 更新重启脚本所需的全部信息。
type ApplySpec struct {
	PID       int    // 当前进程 PID
	NewExe    string // 已下载的新 exe（<dataDir>/updates/<tag>/zhenGuoJian-v2.exe）
	CurExe    string // os.Executable() 的当前 exe 路径
	UpdateDir string // <dataDir>/updates/<tag>，替换成功后整目录删除
}

// BuildApplyBat 生成 %TEMP%\zgj-update-<pid>.bat 的完整文本（CRLF 行尾，UTF-8 无 BOM）。
// 流程（决策约定）：tasklist 轮询等待旧进程退出（≤60s，ping 计秒）
// → move /Y 新 exe 覆盖当前 exe（失败重试 5 次后 copy /Y 兜底 5 次）
// → rmdir 更新目录 → start 当前 exe → del 自身。
// 中文路径兼容：chcp 65001（配 UTF-8 无 BOM 写盘）。
// 任何失败路径都会走到 startApp：宁可旧版继续跑，不可让用户无法启动。
func BuildApplyBat(spec ApplySpec) string {
	lines := []string{
		"@echo off",
		"chcp 65001 >nul",
		"setlocal enableextensions",
		fmt.Sprintf(`set "PID=%d"`, spec.PID),
		fmt.Sprintf(`set "NEW=%s"`, batPath(spec.NewExe)),
		fmt.Sprintf(`set "CUR=%s"`, batPath(spec.CurExe)),
		fmt.Sprintf(`set "UPD=%s"`, batPath(spec.UpdateDir)),
		"set /a N=0",
		":waitloop",
		`tasklist /nh /fo list /fi "PID eq %PID%" | findstr /r /c:"^PID:[ ]*%PID%$" >nul`,
		"if errorlevel 1 goto doMove",
		"set /a N+=1",
		"if %N% geq 60 goto doMove",
		"ping -n 2 127.0.0.1 >nul",
		"goto waitloop",
		":doMove",
		"set /a M=0",
		":moveTry",
		`move /Y "%NEW%" "%CUR%" >nul 2>&1`,
		"if not errorlevel 1 goto moved",
		"set /a M+=1",
		"if %M% geq 5 goto copyTry",
		"ping -n 2 127.0.0.1 >nul",
		"goto moveTry",
		":copyTry",
		"set /a C=0",
		":copyAgain",
		`copy /Y "%NEW%" "%CUR%" >nul 2>&1`,
		"if not errorlevel 1 goto moved",
		"set /a C+=1",
		"if %C% geq 5 goto startApp",
		"ping -n 2 127.0.0.1 >nul",
		"goto copyAgain",
		":moved",
		`rmdir /s /q "%UPD%"`,
		":startApp",
		`start "" "%CUR%"`,
		`del "%~f0" >nul 2>&1`,
		"exit /b 0",
	}
	return strings.Join(lines, "\r\n") + "\r\n"
}

// batPath 去掉双引号（cmd set 的引号包裹会破坏含引号路径，Windows 路径不允许 "）。
func batPath(p string) string {
	return strings.ReplaceAll(p, `"`, "")
}

// ApplySpecZip 是 Electron 产物「主程序包」应用脚本所需信息（分离打包方案，
// 2026-11-18 用户诉求 m01534：依赖=NSIS 首装一次，之后只换易变三件
// app.asar+sidecar.exe+frontend/dist，下载量 475MB→~25MB）。
type ApplySpecZip struct {
	SidecarPID  int    // 当前进程（sidecar）PID：bat 先等它退出
	ElectronPID int    // Electron 主进程 PID（os.Getppid()）：taskkill /T 整树收掉，含自拉起的新 sidecar
	Zip         string // 已下载的 app.zip（<dataDir>/updates/<tag>/zhenGuoJian-v2-app.zip）
	UpdateDir   string // <dataDir>/updates/<tag>，成功后整目录删除
	ExtractDir  string // <UpdateDir>\x，zip 解出目录（相对路径=resources 层级）
	ResDir      string // 安装目录的 resources（替换目标）
	ElectronExe string // 安装目录的 zgj-v2.exe（重启对象）
}

// BuildApplyBatZip 生成 Electron 主程序包应用 bat（CRLF，UTF-8 无 BOM）。
// 流程：等旧 sidecar 退出（≤60s）→ taskkill /T Electron 整树（≤20s 确认，
// 覆盖「Electron 自拉起新 sidecar 又锁住 exe」的窗口；本 bat 的 PPID 链经
// 已死的旧 sidecar/cmd 断开，不在 /T 枚举内——LaunchDetached 的 start 秒退
// 保证 cmd 先死）→ tar.exe（Win10 1803+ 内置 bsdtar）解压 → 结构断言 →
// robocopy /E 覆盖 resources（exit code ≥8 才算失败）→ 清理 → 重启主程序。
// 失败路径同样 start 旧版：宁可旧版继续跑，不可让用户无法启动。
func BuildApplyBatZip(spec ApplySpecZip) string {
	lines := []string{
		"@echo off",
		"chcp 65001 >nul",
		"setlocal enableextensions",
		fmt.Sprintf(`set "PID=%d"`, spec.SidecarPID),
		fmt.Sprintf(`set "EPID=%d"`, spec.ElectronPID),
		fmt.Sprintf(`set "ZIP=%s"`, batPath(spec.Zip)),
		fmt.Sprintf(`set "UPD=%s"`, batPath(spec.UpdateDir)),
		fmt.Sprintf(`set "X=%s"`, batPath(spec.ExtractDir)),
		fmt.Sprintf(`set "RES=%s"`, batPath(spec.ResDir)),
		fmt.Sprintf(`set "EXE=%s"`, batPath(spec.ElectronExe)),
		"set /a N=0",
		":waitPid",
		`tasklist /nh /fo list /fi "PID eq %PID%" | findstr /r /c:"^PID:[ ]*%PID%$" >nul`,
		"if errorlevel 1 goto killShell",
		"set /a N+=1",
		"if %N% geq 60 goto killShell",
		"ping -n 2 127.0.0.1 >nul",
		"goto waitPid",
		":killShell",
		"taskkill /f /t /pid %EPID% >nul 2>&1",
		"set /a N=0",
		":waitEpid",
		`tasklist /nh /fo list /fi "PID eq %EPID%" | findstr /r /c:"^PID:[ ]*%EPID%$" >nul`,
		"if errorlevel 1 goto extract",
		"set /a N+=1",
		"if %N% geq 20 goto fail",
		"ping -n 2 127.0.0.1 >nul",
		"goto waitEpid",
		":extract",
		`rmdir /s /q "%X%" >nul 2>&1`,
		`mkdir "%X%" >nul 2>&1`,
		`tar -xf "%ZIP%" -C "%X%"`,
		"if errorlevel 1 goto fail",
		`if not exist "%X%\sidecar.exe" goto fail`,
		`if not exist "%X%\app.asar" goto fail`,
		`robocopy "%X%" "%RES%" /E /NFL /NDL /NJH /NJS /R:3 /W:2`,
		"if %ERRORLEVEL% geq 8 goto fail",
		`rmdir /s /q "%UPD%" >nul 2>&1`,
		`start "" "%EXE%"`,
		`del "%~f0" >nul 2>&1`,
		"exit /b 0",
		":fail",
		`if exist "%EXE%" start "" "%EXE%"`,
		`del "%~f0" >nul 2>&1`,
		"exit /b 1",
	}
	return strings.Join(lines, "\r\n") + "\r\n"
}
