package updater

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Progress 是下载进度快照，即事件 update:progress 的 payload。
type Progress struct {
	Asset    string  `json:"asset"`
	Total    int64   `json:"total"`    // 期望字节数（未知时为 0）
	Done     int64   `json:"done"`     // 已落盘字节（含续传前存量）
	Pct      float64 `json:"pct"`      // 0-100，一位小数
	SpeedBps float64 `json:"speedBps"` // 最近节流窗口的瞬时速率
	State    string  `json:"state"`    // downloading | done | error
	Error    string  `json:"error,omitempty"`
}

const (
	StateDownloading = "downloading"
	StateDone        = "done"
	StateError       = "error"
)

// progressThrottle 进度事件节流间隔（决策约定 ~400ms）。
const progressThrottle = 400 * time.Millisecond

// ErrCanceled 下载被上层取消。
var ErrCanceled = errors.New("canceled")

// NewDownloadClient 下载专用 http.Client：拨号 15s 超时，但整体不设 Timeout
// （大文件持续读会误杀），取消依赖 ctx。
func NewDownloadClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext:     (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
			MaxIdleConns:    4,
			IdleConnTimeout: 30 * time.Second,
		},
	}
}

// Downloader 沿候选 URL 链把首个可用源下载到 dest，支持 .part 断点续传。
// OnProgress 可为 nil；Service 内接 runtime.EventsEmit。
type Downloader struct {
	Client     *http.Client
	OnProgress func(Progress)
}

func (d *Downloader) emit(p Progress) {
	if d.OnProgress != nil {
		d.OnProgress(p)
	}
}

// Fetch 依次尝试 candidates。dest 已存在且大小与 size 相符直接跳过（幂等）。
// size>0 时完成后强校验字节数；ctx 取消时保留 .part 以便续传；
// 416 时删 .part 并同候选无 Range 重试一次；请求 Range 却收 200 时截断重下。
func (d *Downloader) Fetch(ctx context.Context, candidates []string, dest string, size int64) error {
	if len(candidates) == 0 {
		return fmt.Errorf("下载候选链为空")
	}
	part := dest + ".part"
	base := filepath.Base(dest)
	if st, err := os.Stat(dest); err == nil && size > 0 && st.Size() == size {
		d.emit(Progress{Asset: base, Total: size, Done: size, Pct: 100, State: StateDone})
		return nil
	}
	client := d.Client
	if client == nil {
		client = NewDownloadClient()
	}
	var lastErr error
	for _, u := range candidates {
		err := d.fetchOne(ctx, client, u, part, dest, size)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, ErrCanceled) || ctx.Err() != nil:
			d.emit(Progress{Asset: base, Total: size, Done: partSize(part), Pct: pctOf(size, partSize(part)), State: StateError, Error: "canceled"})
			return ErrCanceled
		default:
			lastErr = err
		}
	}
	done := partSize(part)
	d.emit(Progress{Asset: base, Total: size, Done: done, Pct: pctOf(size, done), State: StateError, Error: lastErr.Error()})
	return fmt.Errorf("下载失败（全部候选）：%v", lastErr)
}

// fetchOne 单候选下载；已有 .part 则带 Range 续传。
func (d *Downloader) fetchOne(ctx context.Context, client *http.Client, url, part, dest string, size int64) error {
	status, resumed, err := d.pump(ctx, client, url, part, dest, partSize(part), size)
	if err != nil {
		if isContextCancel(err) {
			return ErrCanceled
		}
		return err
	}
	if status == http.StatusRequestedRangeNotSatisfiable {
		// 决策：416 → 删 .part 同候选无 Range 重试一次
		_ = os.Remove(part)
		status, resumed, err = d.pump(ctx, client, url, part, dest, 0, size)
		if err != nil {
			if isContextCancel(err) {
				return ErrCanceled
			}
			return err
		}
	}
	if status/100 != 2 {
		return fmt.Errorf("HTTP %d", status)
	}
	_ = resumed
	return d.verifyRename(part, dest, size)
}

// pump 发一次 GET 并流式写盘。offset>0 时带 Range：
//
//	206 → 追加写；200 → 源未按 Range 应答，截断重头写。
//
// 返回应答状态码与是否走了续传（206）。非 2xx 不落数据。
func (d *Downloader) pump(ctx context.Context, client *http.Client, url, part, dest string, offset, size int64) (status int, resumed bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, false, err
	}
	req.Header.Set("User-Agent", "zhenguojian-v2-updater")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, false, err
	}
	defer resp.Body.Close()
	status = resp.StatusCode
	if status/100 != 2 {
		return status, false, nil
	}
	resumed = status == http.StatusPartialContent && offset > 0
	base := int64(0)
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if resumed {
		base = offset
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	f, err := os.OpenFile(part, flags, 0o600)
	if err != nil {
		return status, resumed, err
	}
	defer f.Close()

	total := size
	if total <= 0 && resp.ContentLength > 0 {
		if resumed {
			total = offset + resp.ContentLength
		} else {
			total = resp.ContentLength
		}
	}
	start := time.Now()
	lastEmit := start
	windowBytes := int64(0)
	done := base
	buf := make([]byte, 64*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return status, resumed, werr
			}
			done += int64(n)
			windowBytes += int64(n)
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) || errors.Is(rerr, io.ErrUnexpectedEOF) {
				break
			}
			if isContextCancel(rerr) || ctx.Err() != nil {
				_ = f.Sync()
				return status, resumed, ErrCanceled
			}
			return status, resumed, rerr
		}
		if el := time.Since(lastEmit); el >= progressThrottle {
			d.emit(Progress{
				Asset: filepath.Base(dest), Total: total, Done: done,
				Pct: pctOf(total, done), SpeedBps: float64(windowBytes) / el.Seconds(),
				State: StateDownloading,
			})
			lastEmit = time.Now()
			windowBytes = 0
		}
	}
	if err := f.Sync(); err != nil {
		return status, resumed, err
	}
	return status, resumed, nil
}

// verifyRename 校验大小（size>0 时强校验）并原子改名 part→dest。
func (d *Downloader) verifyRename(part, dest string, size int64) error {
	st, err := os.Stat(part)
	if err != nil {
		return err
	}
	if size > 0 && st.Size() != size {
		_ = os.Remove(part)
		return fmt.Errorf("大小校验失败：期望 %d 字节，实际 %d", size, st.Size())
	}
	if err := os.Rename(part, dest); err != nil {
		return err
	}
	d.emit(Progress{Asset: filepath.Base(dest), Total: size, Done: st.Size(), Pct: 100, State: StateDone})
	return nil
}

func partSize(part string) int64 {
	if st, err := os.Stat(part); err == nil {
		return st.Size()
	}
	return 0
}

func pctOf(total, done int64) float64 {
	if total <= 0 {
		return 0
	}
	p := float64(done) / float64(total) * 1000
	if p > 1000 {
		p = 1000
	}
	if p < 0 {
		p = 0
	}
	return float64(int64(p)) / 10 // 一位小数
}

func isContextCancel(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, ErrCanceled)
}
