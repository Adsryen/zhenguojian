package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// electronFakeInstall 搭一套假的 Electron 安装布局并注入身份（env+probe+
// 缓存重置），返回 root。清理自动挂回。
func electronFakeInstall(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	must := func(p string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must(filepath.Join(root, "resources", "app.asar"))
	svcExe := filepath.Join(root, "resources", "sidecar.exe")
	must(svcExe)
	must(filepath.Join(root, "zgj-v2.exe"))
	t.Setenv("ZHENGUO_SHELL", "electron")
	oldProbe := electronInstallProbe
	electronInstallProbe = func() (string, error) { return svcExe, nil }
	ResetElectronInstallCache()
	t.Cleanup(func() { electronInstallProbe = oldProbe; ResetElectronInstallCache() })
	return root
}

// TestElectronGateNoAppZip 更新源只有 Wails 单 exe（现网 alpha.42 的旧形状）
// 时，Electron 形态必须报无更新并给诊断原因——这是移除无条件止血短路后的
// 等价保护：不能因为「alpha.42 > alpha.16」就把单 exe 推给用户点更新。
func TestElectronGateNoAppZip(t *testing.T) {
	electronFakeInstall(t)
	// 注意：**不能**复用 newComboServer——它的 lmManifest 已含 appZip（前提
	// 不成立）。这里复刻 alpha.42 之前的 Wails 旧形状：只有 exe。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "latest.json") {
			io.WriteString(w, `{"schema":1,"generatedAt":"2026-11-15T12:00:00Z","tag":"v9.9.9","version":"9.9.9","prerelease":false,`+
				`"notes":"old wails shape","assets":{"exe":{"name":"zhenGuoJian-v2.exe","size":21000000,"sha256":"`+lmSHA+`",`+
				`"url":"https://github.com/Adsryen/zhenguojian/releases/download/v9.9.9/zhenGuoJian-v2.exe"}}}`)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	svc := newTestService(t, srv.URL, LocalVersion{Version: "2.0.0-alpha.16"})
	res, err := svc.Check(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Available {
		t.Fatalf("无 appZip 资产不该提示可用更新: %+v", res)
	}
	if res.UnavailableReason == "" {
		t.Error("门控按下必须留诊断原因")
	}
}

// TestElectronGateWithAppZipFullFlow 更新源带 appZip：Check 放行 →
// Download 空资产在 Electron 形态默认选 app.zip（不是 exe）→ PrepareApply
// 在 Linux（ApplySupported=false）回落 manual 但计划结构已在上层单测钉死。
func TestElectronGateWithAppZipFullFlow(t *testing.T) {
	root := electronFakeInstall(t)
	zipBody := []byte("PK\x03\x04 fake app zip bytes for sha check")
	sum := sha256.Sum256(zipBody)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/latest/download/latest.json"):
			fmt.Fprintf(w, `{"schema":1,"tag":"v9.9.9","version":"9.9.9","prerelease":false,
			  "assets":{"exe":{"name":"zhenGuoJian-v2.exe","url":"%s/old-exe","size":3,"sha256":"%s"},
			            "appZip":{"name":"zhenGuoJian-v2-app.zip","url":"%s/app.zip","size":%d,"sha256":"%s"}}}`,
				"http://"+r.Host, strings.Repeat("0", 64),
				"http://"+r.Host, len(zipBody), hex.EncodeToString(sum[:]))
		case strings.HasSuffix(r.URL.Path, "/app.zip"):
			w.Write(zipBody)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	svc := newTestService(t, srv.URL, LocalVersion{Version: "2.0.0-alpha.16"})
	res, err := svc.Check(context.Background(), true)
	if err != nil || !res.Available {
		t.Fatalf("带 appZip 应放行: %+v err=%v resErr=%s", res, err, res.Error)
	}
	if res.UnavailableReason != "" {
		t.Errorf("放行时不该有门控原因: %q", res.UnavailableReason)
	}
	if err := svc.Download(res.Release.Tag, ""); err != nil {
		t.Fatal(err)
	}
	var st *DownloadState
	for i := 0; i < 200; i++ {
		st = svc.downloadSnapshot()
		if st != nil && (st.State == StateDone || st.State == StateError || st.Error != "") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st == nil || st.State != StateDone {
		t.Fatalf("下载未完成: %+v", st)
	}
	if st.Asset != DefaultAppZipAsset {
		t.Errorf("Electron 形态空资产应默认选 app.zip，实际 %s", st.Asset)
	}
	plan, err := svc.PrepareApply(t.TempDir(), 4242)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != "manual" {
		t.Fatalf("Linux 上应回落 manual，实际 %+v", plan)
	}
	_ = root
}
