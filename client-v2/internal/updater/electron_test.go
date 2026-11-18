package updater

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildApplyBatZipGolden 断言 Electron 主程序包应用 bat 的关键结构与
// 失败兜底（golden 风格对齐 TestBuildApplyBatGolden）。
func TestBuildApplyBatZipGolden(t *testing.T) {
	spec := ApplySpecZip{
		SidecarPID: 4321, ElectronPID: 987,
		Zip:         `C:\U\r\AppData\Local\ZhenguoJian\updates\v2.0.0-alpha.9\zhenGuoJian-v2-app.zip`,
		UpdateDir:   `C:\U\r\AppData\Local\ZhenguoJian\updates\v2.0.0-alpha.9`,
		ExtractDir:  `C:\U\r\AppData\Local\ZhenguoJian\updates\v2.0.0-alpha.9\x`,
		ResDir:      `C:\U\r\AppData\Local\Programs\zgj-v2\resources`,
		ElectronExe: `C:\U\r\AppData\Local\Programs\zgj-v2\zgj-v2.exe`,
	}
	got := BuildApplyBatZip(spec)
	if !strings.HasSuffix(got, "\r\n") || strings.Contains(got, "\n\n") {
		t.Fatalf("行尾必须 CRLF 且无空行：%q", got[:60])
	}
	wants := []string{
		`set "PID=4321"`,
		`set "EPID=987"`,
		`taskkill /f /t /pid %EPID% >nul 2>&1`,
		`tar -xf "%ZIP%" -C "%X%"`,
		`if not exist "%X%\sidecar.exe" goto fail`,
		`if not exist "%X%\app.asar" goto fail`,
		`robocopy "%X%" "%RES%" /E /NFL /NDL /NJH /NJS /R:3 /W:2`,
		"if %ERRORLEVEL% geq 8 goto fail", // robocopy 0-7 皆成功，≥8 才是真失败
		`start "" "%EXE%"`,
		`if exist "%EXE%" start "" "%EXE%"`, // fail 路径也要能把旧版拉起来
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("bat 缺关键行：%s", w)
		}
	}
	// 引号剥离（batPath 语义）：路径含 " 会被 cmd set 破坏
	q := BuildApplyBatZip(ApplySpecZip{Zip: `A"B`})
	if strings.Contains(q, `set "ZIP=A"B`) {
		t.Error("batPath 未剥引号")
	}
}

// TestIsElectronInstallEnvGate 无壳身份 env 时必须为 false（Wails/dev 不误判）。
func TestIsElectronInstallEnvGate(t *testing.T) {
	t.Setenv("ZHENGUO_SHELL", "")
	ResetElectronInstallCache()
	defer ResetElectronInstallCache()
	if IsElectronInstall() {
		t.Fatal("无 ZHENGUO_SHELL=electron 却判为 Electron 安装形态")
	}
}

// TestElectronZipApplyPlanFallsBackOffWindows Linux 上 ApplySupported=false，
// zip 计划必须回落 ok=false → 调用方出 manual 提示（旧语义不变）。
func TestElectronZipApplyPlanFallsBackOffWindows(t *testing.T) {
	if ApplySupported {
		t.Skip("本用例只对非 Windows 有意义")
	}
	ResetElectronInstallCache()
	electronInstallCached = true // 强制 env 判定为真，只看平台闸
	electronInstallOnce.Do(func() {})
	defer ResetElectronInstallCache()
	s := NewService(t.TempDir(), LocalVersion{}, filepath.Join(t.TempDir(), "s.json"), nil)
	dl := &DownloadState{Tag: "v2.0.0-alpha.9", Asset: DefaultAppZipAsset, State: StateDone, Dest: filepath.Join(t.TempDir(), "a.zip")}
	if _, ok := s.electronZipApplyPlan(dl, t.TempDir(), os.Getpid()); ok {
		t.Fatal("非 Windows 不应生成 bat 计划")
	}
}

// TestStatusRecommendedAssetElectron Electron 安装形态下 Status 必须带
// recommendedAsset=app.zip（Settings 对话框默认选中项的数据源）；
// 判定不满足时该键不得出现（Wails/dev 零影响）。
func TestStatusRecommendedAssetElectron(t *testing.T) {
	install := t.TempDir()
	if err := os.MkdirAll(filepath.Join(install, "resources"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{
		filepath.Join(install, "resources", "app.asar"),
		filepath.Join(install, "zgj-v2.exe"),
	} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("ZHENGUO_SHELL", "electron")
	ResetElectronInstallCache()
	orig := electronInstallProbe
	electronInstallProbe = func() (string, error) {
		return filepath.Join(install, "resources", "sidecar.exe"), nil
	}
	defer func() {
		electronInstallProbe = orig
		ResetElectronInstallCache()
	}()
	s := NewService(t.TempDir(), LocalVersion{}, filepath.Join(t.TempDir(), "s.json"), nil)
	if got := s.Status()["recommendedAsset"]; got != DefaultAppZipAsset {
		t.Fatalf("recommendedAsset=%v want %q", got, DefaultAppZipAsset)
	}
	// Wails 形态（无 env）：键必须缺席。env 清空 + 重算缓存。
	t.Setenv("ZHENGUO_SHELL", "")
	ResetElectronInstallCache()
	if _, ok := s.Status()["recommendedAsset"]; ok {
		t.Fatal("非 Electron 形态不应出现 recommendedAsset")
	}
}
