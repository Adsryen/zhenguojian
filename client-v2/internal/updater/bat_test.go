package updater

import (
	"strings"
	"testing"
)

func TestBuildApplyBatGolden(t *testing.T) {
	spec := ApplySpec{
		PID:       4321,
		NewExe:    `C:\Users\r\AppData\Local\ZhenguoJian\updates\v2.0.0\zhenGuoJian-v2.exe`,
		CurExe:    `D:\Apps\真果鉴\真果鉴.exe`,
		UpdateDir: `C:\Users\r\AppData\Local\ZhenguoJian\updates\v2.0.0`,
	}
	wantLines := []string{
		"@echo off",
		"chcp 65001 >nul",
		"setlocal enableextensions",
		`set "PID=4321"`,
		`set "NEW=C:\Users\r\AppData\Local\ZhenguoJian\updates\v2.0.0\zhenGuoJian-v2.exe"`,
		`set "CUR=D:\Apps\真果鉴\真果鉴.exe"`,
		`set "UPD=C:\Users\r\AppData\Local\ZhenguoJian\updates\v2.0.0"`,
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
	want := strings.Join(wantLines, "\r\n") + "\r\n"
	got := BuildApplyBat(spec)
	if got != want {
		t.Errorf("bat golden mismatch.\ngot:\n%s\nwant:\n%s", got, want)
	}
	// 行尾必须 CRLF（Windows cmd 对 LF-only bat 兼容性差）
	if !strings.Contains(got, "\r\n") || strings.Count(got, "\n") != len(wantLines) {
		t.Error("line ending problem")
	}
	// 决策点存在性断言
	for _, probe := range []string{"chcp 65001", "tasklist", "PID eq %PID%", "move /Y", "copy /Y", "rmdir /s /q", "start ", "del ", "60", "真果鉴.exe"} {
		if !strings.Contains(got, probe) {
			t.Errorf("bat missing %q", probe)
		}
	}
}

func TestBatPathStripsQuotes(t *testing.T) {
	got := batPath(`C:\a"b\c.exe`)
	if strings.Contains(got, `"`) {
		t.Errorf("quotes must be stripped: %q", got)
	}
}
