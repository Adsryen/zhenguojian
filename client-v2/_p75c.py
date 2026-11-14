import io
import os

base = os.path.dirname(os.path.abspath(__file__))


def rw(rel, reps):
    p = os.path.join(base, rel)
    s = io.open(p, encoding='utf-8').read()
    for old, new in reps:
        c = s.count(old)
        assert c == 1, (rel, old[:56], c)
        s = s.replace(old, new)
    io.open(p, 'w', encoding='utf-8').write(s)
    print('OK', rel)


NOTE = ('// ⚠ 第五轮真机反馈连带修正：这条用例此前只在 Linux 上跑过，\n'
        '//    windows job 加上 internal 测试后当场红 9 条。逐条核过都是\n'
        '//    **测试自己假设 POSIX**，不是产品在 Windows 上不对；\n'
        '//    但"测试假设错"与"产品真错"必须分开核过才能这么说，\n'
        '//    每条的判据写在各自注释里。\n')

# ── 1) danmaku：把 1ns 节流换成确定性假时钟 ──
rw('internal/danmaku/fetcher_test.go', [
    ('''func syncFetcher(t *testing.T, src *fakeSource, opt FetcherOptions) *Fetcher {
	t.Helper()
	opt.MinGap = time.Nanosecond
	opt.RetryDelays = []time.Duration{time.Nanosecond, time.Nanosecond}''',
     NOTE + '''// 节流与冷却都改由**假时钟**决定，不再依赖真实时钟精度。
//
// 上一版写的是 `opt.MinGap = time.Nanosecond`，靠"真实时钟必然已经走过 1ns"
// 来解除节流。Windows 单调时钟粒度约 1ms，`Now().Sub(lastReq)` 可能正好是 0，
// 于是 `0 < 1ns` 成立 → 第一个窗口之后全被挡，
// 表现就是 `窗口序列 = [0], want [0 30000 60000 90000]`（四条用例同因）。
// Linux 纳秒精度下永远过得去，所以这 bug 只在目标平台暴露。
//
// 现在：每次取时钟前进 4s（> MinGap 3s，也 > MinGap+RetryDelays[0]=4s 的下限），
// 于是"过了多久"由测试决定，与平台时钟无关。
func syncFetcher(t *testing.T, src *fakeSource, opt FetcherOptions) *Fetcher {
	t.Helper()
	opt.MinGap = 3 * time.Second
	opt.RetryDelays = []time.Duration{time.Second, time.Second}
	var clkMu sync.Mutex
	clk := time.Unix(1700000000, 0)
	opt.Now = func() time.Time {
		clkMu.Lock()
		defer clkMu.Unlock()
		clk = clk.Add(4 * time.Second)
		return clk
	}'''),
])

# ── 2) selftest：桩要带平台可执行后缀 ──
rw('internal/selftest/selftest_test.go', [
    ('''	dir := t.TempDir()
	stub := filepath.Join(dir, "ffprobe")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\\n"), 0o755); err != nil {''',
     NOTE + '''// Windows 上 `exec.LookPath("ffprobe")` 不会匹配一个**没有扩展名**的文件
// （它按 PATHEXT 找 ffprobe.exe），所以原来那个 POSIX 形状的桩
// 在目标平台根本不该被找到——产品报错文案「找不到 ffprobe.exe（放 exe 同级、
// PATH 或 ZHENGUO_FFPROBE 指定）」在 Windows 上恰恰是对的。
// 桩按平台命名，判据也跟着比 Base（去扩展名后）。
	dir := t.TempDir()
	stubName := "ffprobe"
	if runtime.GOOS == "windows" {
		stubName = "ffprobe.exe" // LookPath 在 Windows 只认带扩展名的候选
	}
	stub := filepath.Join(dir, stubName)
	if err := os.WriteFile(stub, []byte("#!/bin/sh\\n"), 0o755); err != nil {'''),
])

# ── 3) watchstore：默认数据目录与"不可写"都要平台自洽 ──
rw('internal/watchstore/store_test.go', [
    ('''	got := DefaultDataDir()
	if got != filepath.Join(homeDir(), ".zhenguojian") {
		t.Fatalf("unix default: %q", got)
	}''',
     NOTE + '''// 默认目录本来就是分平台的：unix 走 ~/.zhenguojian，
// Windows 走 %LOCALAPPDATA%\\ZhenguoJian（CI 实测返回
// `C:\\Users\\runneradmin\\AppData\\Local\\ZhenguoJian`，那是**期望值**，
// 原来这句断言把 unix 分支写死成唯一正确答案）。
	got := DefaultDataDir()
	if runtime.GOOS == "windows" {
		if !filepath.IsAbs(got) || filepath.Base(got) != "ZhenguoJian" {
			t.Fatalf("windows default 应落在 %LOCALAPPDATA%\\\\ZhenguoJian，实得 %q", got)
		}
	} else if got != filepath.Join(homeDir(), ".zhenguojian") {
		t.Fatalf("unix default: %q", got)
	}'''),
    ('''	// Make the directory unwritable so CreateTemp fails (run as non-root;
	// skip when root, which ignores mode bits).
	if os.Geteuid() == 0 {
		t.Skip("running as root; file modes are not enforced")
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })''',
     '''	// 要造的是"写不进去"这个**结果**，方法必须跨平台。
	// 原来用 `chmod(dir, 0500)`：Windows 上 chmod 基本是 no-op
	// （CI 实测写照样成功 → `expected write error surfaced`），
	// 而且 root 下也无效，所以还多了一条 Skip。
	// 换成把"目录"变成一个**同名普通文件**：在它下面建临时文件
	// 在 unix 与 Windows 上都必然失败（ENOTDIR），判据回到平台无关。
	if err := os.Remove(dir); err != nil {
		t.Fatalf("腾出路径: %v", err)
	}
	f, err := os.Create(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()'''),
])

# ── 4) 权限位断言：Windows 没有 POSIX mode ──
rw('internal/updater/settings_test.go', [
    ('''	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode=%v want 0600", st.Mode().Perm())
	}''',
     NOTE + '''// Windows 上没有 POSIX 权限位（CI 实测 -rw-rw-rw-），
// 0600 这个意图只能靠"文件位于当前用户 profile 目录内"来近似保证。
// 所以这里在 Windows 上跳过 mode 检查，但**不假装验证过私密性**：
// 真正的访问控制是 ACL，不在本用例范围内，已另记一条待办。
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("mode=%v want 0600", st.Mode().Perm())
	}'''),
])
rw('internal/downloadsvc/state_test.go', [
    ('''	if fi.Mode().Perm() != 0o600 {''',
     NOTE + '''// 同上：Windows 无 POSIX mode 位，跳过而不是放宽判据。
	if fi.Mode().Perm() != 0o600 && runtime.GOOS != "windows" {'''),
])

# ── 5) 补 `runtime` 导入（上面几条都要按 GOOS 分支）──
for rel, anchor in [
    ('internal/danmaku/fetcher_test.go', '\t"sync"\n'),
    ('internal/watchstore/store_test.go', '\t"strings"\n'),
    ('internal/updater/settings_test.go', '\t"os"\n'),
    ('internal/downloadsvc/state_test.go', '\t"os"\n'),
]:
    pp = os.path.join(base, rel)
    t = io.open(pp, encoding='utf-8').read()
    if '"runtime"' in t:
        print('  已有 runtime，跳过', rel)
        continue
    assert t.count(anchor) >= 1, (rel, anchor)
    t = t.replace(anchor, anchor + '\t"runtime"\n', 1)
    io.open(pp, 'w', encoding='utf-8').write(t)
    print('OK 导入 runtime:', rel)
