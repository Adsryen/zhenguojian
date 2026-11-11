package updater

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// rangedFile 支持 Range 的静态文件服务器；calls 计数请求数。
func rangedFile(t *testing.T, content []byte) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if rng := r.Header.Get("Range"); rng != "" {
			n, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(rng, "bytes="), "-"), 10, 64)
			if err != nil || n >= int64(len(content)) {
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", n, len(content)-1, len(content)))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(content[n:])
			return
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Write(content)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func newTestDownloader(progress *[]Progress) *Downloader {
	return &Downloader{OnProgress: func(p Progress) {
		if progress != nil {
			*progress = append(*progress, p)
		}
	}}
}

func TestDownloadFresh(t *testing.T) {
	content := bytes.Repeat([]byte("abcdefghij"), 4096) // 40KB
	srv, calls := rangedFile(t, content)
	dir := t.TempDir()
	dest := filepath.Join(dir, "zhenGuoJian-v2.exe")
	var prog []Progress
	d := newTestDownloader(&prog)
	if err := d.Fetch(context.Background(), []string{srv.URL + "/x.exe"}, dest, int64(len(content))); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("content mismatch err=%v len=%d", err, len(got))
	}
	if *calls != 1 {
		t.Errorf("want 1 request, got %d", *calls)
	}
	if len(prog) == 0 || prog[len(prog)-1].State != StateDone || prog[len(prog)-1].Pct != 100 {
		t.Errorf("final progress event missing: %+v", prog)
	}
}

func TestDownloadResume206(t *testing.T) {
	content := bytes.Repeat([]byte("0123456789"), 2048) // 20KB
	half := content[:len(content)/2]
	srv, calls := rangedFile(t, content)
	dir := t.TempDir()
	dest := filepath.Join(dir, "app.zip")
	part := dest + ".part"
	if err := os.WriteFile(part, half, 0o600); err != nil {
		t.Fatal(err)
	}
	d := newTestDownloader(nil)
	if err := d.Fetch(context.Background(), []string{srv.URL}, dest, int64(len(content))); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, content) {
		t.Fatalf("resumed content wrong: got %d bytes want %d", len(got), len(content))
	}
	if *calls != 1 {
		t.Errorf("resume should take exactly 1 ranged request, got %d", *calls)
	}
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Error(".part should be gone after rename")
	}
}

// 416：.part 不小于服务器文件 → 删 .part 同候选无 Range 重试一次成功。
func TestDownload416Retry(t *testing.T) {
	content := []byte("hello-update-bytes")
	srv, calls := rangedFile(t, content)
	dir := t.TempDir()
	dest := filepath.Join(dir, "zhenGuoJian-v2.exe")
	part := dest + ".part"
	if err := os.WriteFile(part, content, 0o600); err != nil { // 恰等长 → Range bytes=18- → 416
		t.Fatal(err)
	}
	d := newTestDownloader(nil)
	if err := d.Fetch(context.Background(), []string{srv.URL}, dest, int64(len(content))); err != nil {
		t.Fatal(err)
	}
	if *calls != 2 {
		t.Errorf("want 416 + retry = 2 calls, got %d", *calls)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != string(content) {
		t.Errorf("content wrong %q", got)
	}
}

// 带 Range 却回 200（不支持续传的源）：截断重下，不得追加损坏。
func TestDownload200WithRangeTruncates(t *testing.T) {
	content := []byte("FULL-FRESH-CONTENT")
	var mu sync.Mutex
	rangeSeen := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if r.Header.Get("Range") != "" {
			rangeSeen = true
		}
		mu.Unlock()
		w.Write(content) // 永远 200 整文件
	}))
	defer srv.Close()
	dir := t.TempDir()
	dest := filepath.Join(dir, "zhenGuoJian-v2.exe")
	if err := os.WriteFile(dest+".part", []byte("STALE"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := newTestDownloader(nil)
	if err := d.Fetch(context.Background(), []string{srv.URL}, dest, int64(len(content))); err != nil {
		t.Fatal(err)
	}
	if !rangeSeen {
		t.Error("test setup: server should have seen a Range request")
	}
	got, _ := os.ReadFile(dest)
	if string(got) != string(content) {
		t.Errorf("must truncate & restart, got %q", got)
	}
}

func TestDownloadSizeMismatch(t *testing.T) {
	content := []byte("short")
	srv, _ := rangedFile(t, content)
	dir := t.TempDir()
	dest := filepath.Join(dir, "zhenGuoJian-v2.exe")
	var prog []Progress
	d := newTestDownloader(&prog)
	err := d.Fetch(context.Background(), []string{srv.URL}, dest, 9999)
	if err == nil || !strings.Contains(err.Error(), "大小校验失败") {
		t.Fatalf("want size mismatch error, got %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("dest must not exist")
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Error("bad .part must be deleted")
	}
	last := prog[len(prog)-1]
	if last.State != StateError || last.Error == "" {
		t.Errorf("error progress missing: %+v", last)
	}
}

func TestDownloadIdempotentSkip(t *testing.T) {
	content := []byte("already-here")
	srv, calls := rangedFile(t, content)
	dir := t.TempDir()
	dest := filepath.Join(dir, "zhenGuoJian-v2.exe")
	if err := os.WriteFile(dest, content, 0o600); err != nil {
		t.Fatal(err)
	}
	d := newTestDownloader(nil)
	if err := d.Fetch(context.Background(), []string{srv.URL}, dest, int64(len(content))); err != nil {
		t.Fatal(err)
	}
	if *calls != 0 {
		t.Errorf("completed file must skip network, calls=%d", *calls)
	}
}

func TestDownloadCancelKeepsPart(t *testing.T) {
	content := bytes.Repeat([]byte("x"), 1<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		flusher := w.(http.Flusher)
		for i := 0; i < len(content); i += 64 * 1024 {
			end := i + 64*1024
			if end > len(content) {
				end = len(content)
			}
			w.Write(content[i:end])
			flusher.Flush()
			time.Sleep(60 * time.Millisecond)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	dest := filepath.Join(dir, "zhenGuoJian-v2.exe")
	ctx, cancel := context.WithCancel(context.Background())
	d := &Downloader{OnProgress: func(p Progress) {
		if p.State == StateDownloading && p.Done > 0 {
			cancel() // 首个进度事件即取消
		}
	}}
	err := d.Fetch(ctx, []string{srv.URL}, dest, int64(len(content)))
	if err != ErrCanceled {
		t.Fatalf("want ErrCanceled, got %v", err)
	}
	st, serr := os.Stat(dest + ".part")
	if serr != nil || st.Size() == 0 || st.Size() >= int64(len(content)) {
		t.Errorf(".part must be kept partial: size=%v err=%v", st, serr)
	}
}

func TestDownloadCandidateFallback(t *testing.T) {
	content := []byte("via-mirror")
	srv, _ := rangedFile(t, content)
	dead := httptest.NewServer(nil)
	deadURL := dead.URL
	dead.Close()
	dir := t.TempDir()
	dest := filepath.Join(dir, "zhenGuoJian-v2.exe")
	d := newTestDownloader(nil)
	err := d.Fetch(context.Background(), []string{deadURL, srv.URL}, dest, int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != string(content) {
		t.Errorf("fallback content wrong: %q", got)
	}
}

func TestPctOf(t *testing.T) {
	if p := pctOf(1000, 333); p != 33.3 {
		t.Errorf("pctOf=%v want 33.3", p)
	}
	if p := pctOf(0, 10); p != 0 {
		t.Errorf("unknown total should be 0, got %v", p)
	}
	if p := pctOf(100, 250); p != 100 {
		t.Errorf("pct must cap at 100, got %v", p)
	}
}
