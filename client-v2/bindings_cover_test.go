package main

// bindings_cover.go 表驱动单测：
//   - coverMime：各 magic → mime、HEIC/垃圾字节 → ""；
//   - coverCachePath：稳定性（同 src 同路径）、盐敏（".v1"）、.jpg 后缀、目录位；
//   - coverFFmpegArgs：逐字对齐 v1 media.export_cover（media.py:194-197）；
//   - lookupFFmpeg：ZHENGUO_FFMPEG env → exeDir/runtime → exeDir → lookPath 优先级；
//   - transcodeCover：ZHENGUO_FFMPEG 指向 sh stub（写假 JPEG 字节）走 happy path +
//     新鲜缓存命中不重跑 + 源变新重转 + stub 失败路径（错误文案、无残留 .tmp）。

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCoverMime(t *testing.T) {
	heic := append([]byte{0, 0, 0, 0}, []byte("ftypheic")...)
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 'J', 'F', 'I', 'F'}
	png := append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}, 1, 2, 3)
	webp := append(append([]byte("RIFF"), 0, 0, 0, 0), []byte("WEBPVP")...)
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"jpeg", jpeg, "image/jpeg"},
		{"jpeg 短到 3 字节", jpeg[:3], "image/jpeg"},
		{"png", png, "image/png"},
		{"webp", webp, "image/webp"},
		{"gif87a", []byte("GIF87a...."), "image/gif"},
		{"gif89a", []byte("GIF89a...."), "image/gif"},
		{"avif", append([]byte{0, 0, 0, 0}, []byte("ftypavif")...), "image/avif"},
		{"avis", append([]byte{0, 0, 0, 0}, []byte("ftypavis")...), "image/avif"},
		{"heic 未转码必须报错", heic, ""},
		{"heix 品牌同样拒", append([]byte{0, 0, 0, 0}, []byte("ftypheix")...), ""},
		{"空文件", nil, ""},
		{"截断的 jpeg 头", []byte{0xFF, 0xD8}, ""},
		{"垃圾字节", []byte("not an image at all"), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := coverMime(c.data); got != c.want {
				t.Fatalf("coverMime=%q want=%q", got, c.want)
			}
		})
	}
}

func TestCoverCachePath(t *testing.T) {
	const src = "/data/zhenguo/cover_cache/abc123.heic"
	got := coverCachePath(src)
	wantSum := sha1.Sum([]byte(src + ".v1"))
	wantDir := filepath.Join(os.TempDir(), coverDirName)
	want := filepath.Join(wantDir, hex.EncodeToString(wantSum[:])+".jpg")
	if got != want {
		t.Fatalf("coverCachePath=%q want=%q", got, want)
	}
	if coverCachePath(src) != got {
		t.Fatal("同 src 两次调用路径必须一致（缓存键稳定性）")
	}
	if coverCachePath(src+".v1") == got {
		t.Fatal(`".v1" 盐参与散列输入：src+".v1" 与 "…v1.v1" 不得同键`)
	}
	if coverCachePath("/data/zhenguo/cover_cache/xyz789.heic") == got {
		t.Fatal("不同 src 必须不同键")
	}
	if filepath.Base(got) != hex.EncodeToString(wantSum[:])+".jpg" || len(filepath.Base(got)) != 44 {
		t.Fatalf("产物名应为 40 位 sha1hex + .jpg: %q", filepath.Base(got))
	}
	if filepath.Dir(got) != wantDir {
		t.Fatalf("目录应为 TempDir/%s: %q", coverDirName, filepath.Dir(got))
	}
}

func TestCoverFFmpegArgs(t *testing.T) {
	// 逐字对齐 v1 media.py:194-197：ffmpeg -y -loglevel error -nostdin -i SRC
	// -frames:v 1 -q:v 2 DEST.jpg
	got := coverFFmpegArgs("/in/poster.heic", "/out/poster.jpg")
	want := []string{"-y", "-loglevel", "error", "-nostdin",
		"-i", "/in/poster.heic", "-frames:v", "1", "-q:v", "2", "/out/poster.jpg"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args=%v want=%v", got, want)
	}
}

func TestLookupFFmpeg(t *testing.T) {
	dir := t.TempDir()
	mk := func(rel string) string {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	envBin := mk("envbin/ffmpeg")
	runtimeBin := mk("appdir/runtime/ffmpeg/ffmpeg")
	appDir := filepath.Join(dir, "appdir")
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	noLookPath := func(string) (string, error) { return "", fmt.Errorf("not found") }
	okLookPath := func(name string) (string, error) { return "/usr/bin/" + name, nil }

	t.Run("env 优先级最高", func(t *testing.T) {
		got, err := lookupFFmpeg(env(map[string]string{"ZHENGUO_FFMPEG": envBin}), appDir, noLookPath)
		if err != nil || got != envBin {
			t.Fatalf("got=%q err=%v want env bin %q", got, err, envBin)
		}
	})
	t.Run("env 未设 → exeDir/runtime/ffmpeg", func(t *testing.T) {
		got, err := lookupFFmpeg(env(nil), appDir, noLookPath)
		if err != nil || got != runtimeBin {
			t.Fatalf("got=%q err=%v want %q", got, err, runtimeBin)
		}
	})
	t.Run("runtime 缺失 → exeDir/ffmpeg", func(t *testing.T) {
		sub := filepath.Join(dir, "app2")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(sub, "ffmpeg")
		if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
		got, err := lookupFFmpeg(env(nil), sub, noLookPath)
		if err != nil || got != p {
			t.Fatalf("got=%q err=%v want %q", got, err, p)
		}
	})
	t.Run("候选文件都不存在 → PATH lookPath", func(t *testing.T) {
		got, err := lookupFFmpeg(env(map[string]string{"ZHENGUO_FFMPEG": filepath.Join(dir, "ghost")}),
			filepath.Join(dir, "empty-dir"), okLookPath)
		if err != nil || got != "/usr/bin/ffmpeg" {
			t.Fatalf("got=%q err=%v want PATH 命中", got, err)
		}
	})
	t.Run("全失败 → 固定文案", func(t *testing.T) {
		got, err := lookupFFmpeg(env(nil), filepath.Join(dir, "empty-dir"), noLookPath)
		if err == nil || err.Error() != "缺少 ffmpeg 组件，无法显示海报" || got != "" {
			t.Fatalf("got=%q err=%v want 缺少 ffmpeg 文案", got, err)
		}
	})
}

// ---- transcodeCover 主流程（stub ffmpeg）----

// writeFFmpegStub 生成一个「假 ffmpeg」：把运行记录 append 到 runs，
// 取最后一个参数当输出路径，写入 JPEG magic + 定长填充。fail=true 时改
// 写 stderr 并退出 1（模拟转码失败）。
func writeFFmpegStub(t *testing.T, dir, runs string, fail bool) string {
	t.Helper()
	body := "#!/bin/sh\n"
	if fail {
		body += `echo "stub: cannot decode" >&2` + "\nexit 1\n"
	} else {
		body += "dst=\"\"\nfor a in \"$@\"; do dst=\"$a\"; done\n"
		body += "printf '\\377\\330\\377STUBJPG' > \"$dst\"\n"
	}
	body += fmt.Sprintf("echo run >> %q\n", runs)
	path := filepath.Join(dir, fmt.Sprintf("ffmpeg-stub-%d.sh", 0xFa11+boolByte(fail)))
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func boolByte(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestTranscodeCover(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub 为 sh 脚本；Windows CI 跳过（质量门在 linux 本机执行）")
	}
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir) // coverCachePath 的 os.TempDir 落点
	t.Setenv("ZHENGUO_FFMPEG", "")
	src := filepath.Join(dir, "poster.heic")
	if err := os.WriteFile(src, []byte{0, 0, 0, 0, 'f', 't', 'y', 'p', 'h', 'e', 'i', 'c'}, 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("happy path：stub 转码 + 缓存命中 + 源变新重转", func(t *testing.T) {
		runs := filepath.Join(dir, "runs.txt")
		stub := writeFFmpegStub(t, dir, runs, false)
		t.Setenv("ZHENGUO_FFMPEG", stub)

		dst, err := transcodeCover(src)
		if err != nil {
			t.Fatalf("转码失败: %v", err)
		}
		if want := coverCachePath(src); dst != want {
			t.Fatalf("dst=%q want=%q", dst, want)
		}
		data, err := os.ReadFile(dst)
		if err != nil || len(data) < 3 || data[0] != 0xFF || data[1] != 0xD8 || data[2] != 0xFF {
			t.Fatalf("产物应为 stub 写的 JPEG magic，data=%x err=%v", data, err)
		}
		if _, err := os.Stat(dst + "." + fmt.Sprint(os.Getpid()) + ".tmp"); err == nil {
			t.Fatal("rename 后不应残留 .tmp")
		}
		if n := countLines(t, runs); n != 1 {
			t.Fatalf("stub 应跑 1 次，实际 %d", n)
		}

		// 二次调用：新鲜缓存命中，不再跑 ffmpeg
		if _, err := transcodeCover(src); err != nil {
			t.Fatal(err)
		}
		if n := countLines(t, runs); n != 1 {
			t.Fatalf("缓存命中不应重跑，实际 %d 次", n)
		}

		// 源 mtime 变新 → 缓存过期重转
		future := dstInfo(t, dst).ModTime().Add(2 * 1e9) // +2s，纳秒制
		if err := os.Chtimes(src, future, future); err != nil {
			t.Fatal(err)
		}
		if _, err := transcodeCover(src); err != nil {
			t.Fatal(err)
		}
		if n := countLines(t, runs); n != 2 {
			t.Fatalf("源变新应重跑，实际 %d 次", n)
		}
	})

	t.Run("stub 失败路径：错误文案 + 无残留", func(t *testing.T) {
		runs := filepath.Join(dir, "runs-fail.txt")
		stub := writeFFmpegStub(t, dir, runs, true)
		t.Setenv("ZHENGUO_FFMPEG", stub)
		src2 := filepath.Join(dir, "poster2.heic")
		if err := os.WriteFile(src2, []byte("fake heic"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := transcodeCover(src2)
		if err == nil || !strings.Contains(err.Error(), "海报转码失败") {
			t.Fatalf("want 海报转码失败, got=%v", err)
		}
		if !strings.Contains(err.Error(), "cannot decode") {
			t.Logf("stderr 尾巴未透传（不强制）: %v", err)
		}
		entries, _ := os.ReadDir(filepath.Dir(coverCachePath(src2)))
		for _, e := range entries {
			if strings.Contains(e.Name(), ".tmp") {
				t.Fatalf("失败后不应残留 .tmp: %v", entries)
			}
		}
		if _, err := os.Stat(coverCachePath(src2)); err == nil {
			t.Fatal("失败不应产出目标文件")
		}
	})

	t.Run("找不到 ffmpeg → 固定文案", func(t *testing.T) {
		t.Setenv("ZHENGUO_FFMPEG", filepath.Join(dir, "ghost-ffmpeg"))
		_, err := transcodeCover(src)
		// 本机 PATH 可能真有 ffmpeg，只断言不是 panic 且文案二选一
		if err != nil && !strings.Contains(err.Error(), "缺少 ffmpeg 组件") &&
			!strings.Contains(err.Error(), "海报转码失败") {
			t.Fatalf("unexpected err: %v", err)
		}
	})
}

func dstInfo(t *testing.T, path string) os.FileInfo {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatal(err)
	}
	return strings.Count(string(data), "run")
}

// ---- readCoverImage ----

func TestReadCoverImage(t *testing.T) {
	dir := t.TempDir()
	jpg := filepath.Join(dir, "a.jpg")
	body := append([]byte{0xFF, 0xD8, 0xFF}, bytesRepeat('x', 100)...)
	if err := os.WriteFile(jpg, body, 0o644); err != nil {
		t.Fatal(err)
	}
	mime, data, err := readCoverImage(jpg)
	if err != nil || mime != "image/jpeg" || len(data) != len(body) {
		t.Fatalf("mime=%q len=%d err=%v", mime, len(data), err)
	}

	empty := filepath.Join(dir, "empty.jpg")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readCoverImage(empty); err == nil || !strings.Contains(err.Error(), "为空") {
		t.Fatalf("want 空文件错误, got=%v", err)
	}

	heic := filepath.Join(dir, "b.heic")
	if err := os.WriteFile(heic, []byte{0, 0, 0, 0, 'f', 't', 'y', 'p', 'h', 'e', 'i', 'c'}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readCoverImage(heic); err == nil || !strings.Contains(err.Error(), "无法识别海报格式") {
		t.Fatalf("want magic 不识别错误, got=%v", err)
	}

	if _, _, err := readCoverImage(filepath.Join(dir, "ghost")); err == nil {
		t.Fatal("want 读取失败错误")
	}
}

func bytesRepeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}
