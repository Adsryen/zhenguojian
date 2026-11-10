package main

// 封面管线绑定（PosterGrid / Detail / 日历 / 预约 首屏海报取数）。
//
// 背景：红果等站源封面是签名 HEIC —— webview 直接 <img src> 必挂（不支持解码），
// 把 URL 扩展名改成 .jpeg 又会 403（签名校验）；v1 PyQt 在 media.py:171-203
// （media.export_cover）用 ffmpeg 把 HEIC 转 JPEG 再显示。核心已有 `cover` action
// （app_runtime.go:526 → engine.loadCover）负责下载并返回
// {"path": 本地缓存绝对路径, "heic": bool}，但**不解码**（HEIC 在
// nativeIsCoverImage 里就是合法封面，app_covers.go:279）。
// v2 壳层补齐转码段，整链路：
//
//	dramaJSON → native.Cover → {path, heic}
//	  heic==true → ffmpeg 单帧转 JPEG（参数逐字对齐 v1 media.export_cover），
//	               产物落 TempDir/zhenguojian-covers/<sha1(源path+".v1")>.jpg，
//	               新鲜缓存直用（存在 && size>0 && mtime 不早于源文件），
//	               原子写：<dst>.<pid>.tmp → rename。
//	  读取文件（≤4MB）按 magic 定 mime → 返回 "data:<mime>;base64,…"。
//
// ffmpeg 查找链与 internal/downloadsvc/executor.go commandExecutor.Look 同序
// （ZHENGUO_FFMPEG env → exe 同级 runtime/ffmpeg/ffmpeg(.exe) → exe 同级
// ffmpeg(.exe) → PATH），但自带小实现不 import downloadsvc（下载域禁碰）。
// 风格照 bindings_calendar.go：方法挂 *App，错误中文原文走 JS reject；
// Wails 经 main.go Bind 整结构体绑定导出方法，无需 app.go/main.go diff。

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"zhenguojian/client-v2/internal/native"
)

const (
	// coverDirName 转码缓存目录名（TempDir 下）。
	coverDirName = "zhenguojian-covers"
	// coverTranscodeTimeout 单张海报转码限时。v1 media.export_cover 整图 timeout=120s；
	// 海报只取 1 帧（-frames:v 1），从紧到 60s。
	coverTranscodeTimeout = 60 * time.Second
	// coverMaxBytes data URL 读入上限：>4MB 拒绝（正常海报 JPEG 远低于该值）。
	coverMaxBytes = 4 << 20
	// coverStderrTailBytes ffmpeg stderr 尾巴截断长度（进错误消息）。
	coverStderrTailBytes = 200
)

// Cover 返回封面 data URL（"data:<mime>;base64,…"）。force=true 让核心回源重下。
// 前端 resolveCover 失败兜底 ”（首字占位），这里错误一律中文原文透出。
func (a *App) Cover(dramaJSON string, force bool) (string, error) {
	drama, err := unmarshalObject(dramaJSON)
	if err != nil {
		return "", err
	}
	data, err := native.Cover(drama, force)
	if err != nil {
		return "", err
	}
	src, _ := data["path"].(string)
	if src == "" {
		return "", errors.New("核心未返回封面文件路径")
	}
	heic, _ := data["heic"].(bool)
	display := src
	if heic {
		if display, err = transcodeCover(src); err != nil {
			return "", err
		}
	}
	mime, body, err := readCoverImage(display)
	if err != nil {
		return "", err
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(body), nil
}

// coverCachePath 转码产物路径：TempDir/zhenguojian-covers/<sha1hex(源path+".v1")>.jpg。
// 键 = 核心缓存文件的绝对路径（核心 cover_cache 落盘文件名自带内容散列：换封面 =
// 换路径 = 换键，缓存天然失效；".v1" 为管线版本盐）。纯函数便于表驱动测试。
func coverCachePath(src string) string {
	sum := sha1.Sum([]byte(src + ".v1"))
	return filepath.Join(os.TempDir(), coverDirName, hex.EncodeToString(sum[:])+".jpg")
}

// coverFFmpegArgs 逐字对齐 v1 media.py:194-197 export_cover .jpg 分支：
//
//	ffmpeg -y -loglevel error -nostdin -i SRC -frames:v 1 -q:v 2 DEST.jpg
func coverFFmpegArgs(src, dst string) []string {
	return []string{"-y", "-loglevel", "error", "-nostdin",
		"-i", src, "-frames:v", "1", "-q:v", "2", dst}
}

// transcodeCover 把 HEIC 源转为缓存 JPEG，返回产物路径。
// 命中缓存条件（三者同时）：产物存在、size>0、mtime 不早于源文件 mtime。
// 找不到 ffmpeg → 「缺少 ffmpeg 组件，无法显示海报」；转码失败 → 「海报转码失败」。
func transcodeCover(src string) (string, error) {
	dst := coverCachePath(src)
	srcSt, srcErr := os.Stat(src)
	dstSt, dstErr := os.Stat(dst)
	if srcErr == nil && dstErr == nil && dstSt.Size() > 0 &&
		!dstSt.ModTime().Before(srcSt.ModTime()) {
		return dst, nil // 新鲜缓存直用
	}
	ffmpeg, err := lookupFFmpegFromDisk()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", fmt.Errorf("海报转码失败：%w", err)
	}
	tmp := dst + "." + strconv.Itoa(os.Getpid()) + ".tmp"
	ctx, cancel := context.WithTimeout(context.Background(), coverTranscodeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, coverFFmpegArgs(src, tmp)...)
	cmd.SysProcAttr = hideWindowAttr() // GUI 主程序 spawn ffmpeg 不闪控制台窗
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = io.Discard
	runErr := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("海报转码失败（超时 %s）", coverTranscodeTimeout)
	}
	if runErr != nil {
		_ = os.Remove(tmp)
		tail := strings.TrimSpace(stderr.String())
		if len(tail) > coverStderrTailBytes {
			tail = "…" + tail[len(tail)-coverStderrTailBytes:]
		}
		if tail != "" {
			return "", fmt.Errorf("海报转码失败：%s", tail)
		}
		return "", errors.New("海报转码失败")
	}
	st, err := os.Stat(tmp)
	if err != nil || st.Size() == 0 {
		_ = os.Remove(tmp)
		return "", errors.New("海报转码失败")
	}
	if err := os.Rename(tmp, dst); err != nil { // 原子写：先 .tmp 再 rename
		_ = os.Remove(tmp)
		return "", fmt.Errorf("海报转码失败：%w", err)
	}
	return dst, nil
}

// lookupFFmpeg 按候选顺序找 ffmpeg：ZHENGUO_FFMPEG env →
// exeDir/runtime/ffmpeg/ffmpeg(.exe) → exeDir/ffmpeg(.exe) → PATH。
// 顺序与 internal/downloadsvc/executor.go Look 一致（该域禁 import，此处独立小实现）；
// getenv/exeDir/lookPath 参数注入，表驱动测试覆盖优先级。
func lookupFFmpeg(getenv func(string) string, exeDir string, lookPath func(string) (string, error)) (string, error) {
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	var candidates []string
	if v := getenv("ZHENGUO_FFMPEG"); v != "" {
		candidates = append(candidates, v)
	}
	if exeDir != "" {
		candidates = append(candidates,
			filepath.Join(exeDir, "runtime", "ffmpeg", "ffmpeg"+suffix),
			filepath.Join(exeDir, "ffmpeg"+suffix))
	}
	for _, cand := range candidates {
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return cand, nil
		}
	}
	if lookPath != nil {
		if p, err := lookPath("ffmpeg"); err == nil {
			return p, nil
		}
	}
	return "", errors.New("缺少 ffmpeg 组件，无法显示海报")
}

// lookupFFmpegFromDisk 生产入口：exeDir 取自身可执行文件目录，env/PATH 走系统面。
func lookupFFmpegFromDisk() (string, error) {
	exeDir := ""
	if self, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(self)
	}
	return lookupFFmpeg(os.Getenv, exeDir, exec.LookPath)
}

// coverMime 按 magic 判 mime；返回 "" 表示不识别（含未转成功的 HEIC → 上层报错）。
// GIF magic 与 v1/nativeIsCoverImage 同用 6 字节前缀（GIF87a/GIF89a）；
// avif 认 ftyp 后 brand = avif/avis（app_covers.go:279 同款分支，heic 系品牌
// 不在此列——它们必须已被转码，走到这里说明转码没发生）。
func coverMime(data []byte) string {
	switch {
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return "image/jpeg"
	case len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}):
		return "image/png"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	case len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a"):
		return "image/gif"
	case len(data) >= 12 && string(data[4:8]) == "ftyp" &&
		(string(data[8:12]) == "avif" || string(data[8:12]) == "avis"):
		return "image/avif"
	}
	return ""
}

// readCoverImage 读封面文件（≤coverMaxBytes），返回 mime + 字节。
// 空文件/超限/magic 不识别 → error（不返回半截数据）。
func readCoverImage(path string) (string, []byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil, fmt.Errorf("海报文件读取失败：%w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, coverMaxBytes+1))
	if err != nil {
		return "", nil, fmt.Errorf("海报文件读取失败：%w", err)
	}
	if len(data) == 0 {
		return "", nil, errors.New("海报文件为空")
	}
	if len(data) > coverMaxBytes {
		return "", nil, fmt.Errorf("海报文件超过 %dMB 上限", coverMaxBytes>>20)
	}
	mime := coverMime(data)
	if mime == "" {
		return "", nil, errors.New("无法识别海报格式（HEIC 转码未生效）")
	}
	return mime, data, nil
}
