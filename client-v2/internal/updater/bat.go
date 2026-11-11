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
