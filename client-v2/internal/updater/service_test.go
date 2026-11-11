package updater

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const exeContent = "0123456789abcdef01" // 18B
const zipContent = "ZIPDATA"            // 7B

// newComboServer 一个服务器同时扮演 releases API 与资产下载源；
// 用 mirrorPrefix 指向它即可全程离线（候选链首选 = 前缀拼接）。
func newComboServer(t *testing.T) (*httptest.Server, string, int64) {
	t.Helper()
	var base string
	exeSize := int64(len(exeContent))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.Contains(p, "api.github.com"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `[{"tag_name":"v9.9.9","prerelease":false,"published_at":"2026-03-01T00:00:00Z","body":"test notes",
"assets":[{"name":"zhenGuoJian-v2.exe","browser_download_url":"%s/dl/x.exe","size":%d},
          {"name":"zhenGuoJian-v2-app.zip","browser_download_url":"%s/dl/app.zip","size":%d}]}]`,
				base, exeSize, base, len(zipContent))
		case strings.Contains(p, "/dl/x.exe"):
			serveBytes(w, r, []byte(exeContent))
		case strings.Contains(p, "/dl/app.zip"):
			serveBytes(w, r, []byte(zipContent))
		default:
			http.NotFound(w, r)
		}
	}))
	base = srv.URL
	t.Cleanup(srv.Close)
	return srv, base, exeSize
}

func serveBytes(w http.ResponseWriter, r *http.Request, content []byte) {
	if rng := r.Header.Get("Range"); rng != "" {
		n, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(rng, "bytes="), "-"), 10, 64)
		if err != nil || n >= int64(len(content)) {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.WriteHeader(http.StatusPartialContent)
		w.Write(content[n:])
		return
	}
	w.Write(content)
}

func newTestService(t *testing.T, base string, local LocalVersion) *Service {
	t.Helper()
	dir := t.TempDir()
	svc := NewService(dir, local, filepath.Join(dir, "update-settings.json"), nil)
	if base != "" {
		if _, err := svc.Settings.MergePatch(map[string]any{"mirrorPrefix": base}); err != nil {
			t.Fatal(err)
		}
	}
	svc.StartupDelay = 10 * time.Millisecond
	return svc
}

func TestServiceCheckAvailableAndUpToDate(t *testing.T) {
	_, base, _ := newComboServer(t)
	// 本地 v2.0.0-alpha.2 < v9.9.9 → 有更新
	svc := newTestService(t, base, LocalVersion{Version: "v2.0.0-alpha.2"})
	res, err := svc.Check(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || res.Release == nil || res.Release.Tag != "v9.9.9" {
		t.Fatalf("expect update available: %+v", res)
	}
	if res.Channel != "stable" || res.Dev {
		t.Errorf("channel/dev wrong: %+v", res)
	}
	// 本地即最新
	svc2 := newTestService(t, base, LocalVersion{Version: "v9.9.9"})
	res2, err := svc2.Check(context.Background(), true)
	if err != nil || res2.Available {
		t.Fatalf("v9.9.9 vs v9.9.9 should be up-to-date (available=%v err=%v)", res2.Available, err)
	}
}

func TestServiceDevAlwaysUpdates(t *testing.T) {
	_, base, _ := newComboServer(t)
	svc := newTestService(t, base, LocalVersion{Dev: true})
	res, err := svc.Check(context.Background(), true)
	if err != nil || !res.Available || !res.Dev {
		t.Fatalf("dev build must see any release as update: %+v err=%v", res, err)
	}
}

// ignoredTag 抑制（含手动检查）；清除后恢复提醒。
func TestServiceIgnoredTag(t *testing.T) {
	_, base, _ := newComboServer(t)
	svc := newTestService(t, base, LocalVersion{Version: "v2.0.0"})
	if _, err := svc.Check(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Settings.MergePatch(map[string]any{"ignoredTag": "v9.9.9"}); err != nil {
		t.Fatal(err)
	}
	res, err := svc.Check(context.Background(), true)
	if err != nil || res.Available || res.SkippedTag != "v9.9.9" {
		t.Fatalf("ignored tag must suppress: %+v err=%v", res, err)
	}
	if _, err := svc.Settings.MergePatch(map[string]any{"ignoredTag": ""}); err != nil {
		t.Fatal(err)
	}
	// force=true：跳过 30s 冷却缓存（缓存里还是 ignored 结果；冷却语义另有专门用例）
	res, err = svc.Check(context.Background(), true)
	if err != nil || !res.Available {
		t.Fatalf("cleared ignore must restore availability: %+v err=%v", res, err)
	}
}

// 30s 手动冷却（参照语义）：第二次非强制检查回缓存 Cooldown=true。
func TestServiceCooldown(t *testing.T) {
	_, base, _ := newComboServer(t)
	svc := newTestService(t, base, LocalVersion{Version: "v2.0.0"})
	first, err := svc.Check(context.Background(), false)
	if err != nil || first.Cooldown {
		t.Fatalf("first manual check should not be cooldown: %+v", first)
	}
	second, err := svc.Check(context.Background(), false)
	if err != nil || !second.Cooldown || !second.Available {
		t.Fatalf("second within 30s should return cached+cooldown: %+v err=%v", second, err)
	}
	third, err := svc.Check(context.Background(), true)
	if err != nil || third.Cooldown {
		t.Fatalf("force=true must bypass cooldown: %+v", third)
	}
}

func TestServiceLastCheckAtPersisted(t *testing.T) {
	_, base, _ := newComboServer(t)
	svc := newTestService(t, base, LocalVersion{Version: "v2.0.0"})
	before := svc.Settings.Get().LastCheckAt
	if _, err := svc.Check(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if got := svc.Settings.Get().LastCheckAt; got <= before {
		t.Errorf("lastCheckAt should advance: before=%d got=%d", before, got)
	}
}

func TestServiceStartupEmitsAvailable(t *testing.T) {
	_, base, _ := newComboServer(t)
	var (
		emu    sync.Mutex
		events []map[string]any
	)
	svc := newTestService(t, base, LocalVersion{Version: "v2.0.0"})
	svc.emit = func(ev string, data map[string]any) {
		if ev == "update:available" {
			emu.Lock()
			events = append(events, data)
			emu.Unlock()
		}
	}
	svc.Startup()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		emu.Lock()
		n := len(events)
		emu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	emu.Lock()
	gotEvents := events
	emu.Unlock()
	if len(gotEvents) == 0 {
		t.Fatal("startup autocheck should emit update:available")
	}
	if gotEvents[0]["tag"] != "v9.9.9" {
		t.Errorf("payload wrong: %+v", gotEvents[0])
	}

	// autocheck=off → 不发事件
	svc2 := newTestService(t, base, LocalVersion{Version: "v2.0.0"})
	if err := svc2.Settings.Save(Settings{Autocheck: false}); err != nil {
		t.Fatal(err)
	}
	var evs2 atomic.Int64
	svc2.emit = func(ev string, data map[string]any) {
		if ev == "update:available" {
			evs2.Add(1)
		}
	}
	svc2.Startup()
	time.Sleep(400 * time.Millisecond)
	if evs2.Load() != 0 {
		t.Errorf("autocheck off must not emit, got %d", evs2.Load())
	}
}

func TestServiceDownloadAndStates(t *testing.T) {
	_, base, size := newComboServer(t)
	svc := newTestService(t, base, LocalVersion{Version: "v2.0.0"})
	if _, err := svc.Check(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if err := svc.Download("v9.9.9", ""); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(svc.UpdateDir("v9.9.9"), DefaultExeAsset)
	st := waitDownloadState(t, svc, StateDone, 5*time.Second)
	if st.Dest != dest || st.Total != size {
		t.Errorf("state mismatch: %+v dest=%s", st, dest)
	}
	data, err := os.ReadFile(dest)
	if err != nil || string(data) != exeContent {
		t.Fatalf("downloaded content wrong: %q err=%v", data, err)
	}
	// 重复下载幂等直接 done
	if err := svc.Download("v9.9.9", ""); err != nil {
		t.Fatal(err)
	}
	waitDownloadState(t, svc, StateDone, 2*time.Second)
	// 未知 tag / 未知资产报错
	if err := svc.Download("v0.0.1", ""); err == nil {
		t.Error("unknown tag should error")
	}
	if err := svc.Download("v9.9.9", "no-such.asset"); err == nil {
		t.Error("unknown asset should error")
	}
}

func waitDownloadState(t *testing.T, svc *Service, want string, d time.Duration) *DownloadState {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		st := svc.Status()
		if dl, ok := st["download"].(*DownloadState); ok && dl.State == want {
			return dl
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("download never reached state %q", want)
	return nil
}

func TestServiceCancelNoActive(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir, LocalVersion{Dev: true}, filepath.Join(dir, "update-settings.json"), nil)
	if err := svc.CancelDownload(); err == nil {
		t.Error("cancel without active download should error")
	}
}

func TestServicePrepareApply(t *testing.T) {
	_, base, _ := newComboServer(t)
	svc := newTestService(t, base, LocalVersion{Version: "v2.0.0"})
	if _, err := svc.Check(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	// 未下载 → 报错
	if _, err := svc.PrepareApply(t.TempDir(), os.Getpid()); err == nil {
		t.Error("apply before download should error")
	}
	// zip → manual 计划（跨平台一致）
	if err := svc.Download("v9.9.9", DefaultAppZipAsset); err != nil {
		t.Fatal(err)
	}
	waitDownloadState(t, svc, StateDone, 5*time.Second)
	plan, err := svc.PrepareApply(t.TempDir(), os.Getpid())
	if err != nil || plan.Kind != "manual" {
		t.Fatalf("zip apply plan wrong: %+v err=%v", plan, err)
	}
	if plan.UpdateDir == "" || !strings.Contains(plan.UpdateDir, "v9.9.9") || !strings.Contains(plan.Hint, DefaultAppZipAsset) {
		t.Errorf("manual plan should point at update dir & name asset: %+v", plan)
	}
	// exe → restart 计划；非 Windows 平台 ApplySupported=false → 错误文案
	pid := os.Getpid()
	if err := svc.Download("v9.9.9", DefaultExeAsset); err != nil {
		t.Fatal(err)
	}
	waitDownloadState(t, svc, StateDone, 5*time.Second)
	plan, err = svc.PrepareApply(t.TempDir(), pid)
	if !ApplySupported {
		if err == nil || !strings.Contains(err.Error(), "仅 Windows 支持自动更新") {
			t.Fatalf("non-windows must reject auto apply, got plan=%+v err=%v", plan, err)
		}
	} else {
		wantBat := fmt.Sprintf("zgj-update-%d.bat", pid)
		if err != nil || plan.Kind != "restart" ||
			!strings.Contains(plan.BatPath, wantBat) || !strings.Contains(plan.BatText, "@echo off") {
			t.Fatalf("windows restart plan wrong: %+v err=%v", plan, err)
		}
	}
}

func TestUpdateDirSanitizesTag(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir, LocalVersion{Dev: true}, filepath.Join(dir, "s.json"), nil)
	got := svc.UpdateDir(`v2.0.0/test:*`)
	if strings.ContainsAny(filepath.Base(got), `/*:`) {
		t.Errorf("tag not sanitized: %s", got)
	}
}
