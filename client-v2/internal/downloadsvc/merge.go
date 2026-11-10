// merge.go —— v1 media.py MergeTask 的 Go 移植（管线骨架逐段对齐）：
// 解密分集(0–8%) → 解析媒体信息(8–15%) → 统一分集格式(15–45%) →
// 合并视频(45–55%) → 完整解码校验(55–95%，可关) → 保存成品(97–100%)。
// 外部进程经 Executor 注入，测试注假核心即可全链路断言。
package downloadsvc

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// mediaSig 单集 ffprobe 摘要（v1 media_signature 同构）。
type mediaSig struct {
	Path     string
	Duration float64
	Bytes    int64
	Video    map[string]any
	Audio    map[string]any
	VideoSig string
	AudioSig string
}

// partPlan 逐分集规范化方案（v1 build_plan 产物）。
type partPlan struct {
	Transport  bool   // 统一走 mpegts 流拷贝
	Encoder    string // "" 表示视频流拷贝；libx264/libx265
	VideoCodec string // 目标 h264/hevc
	Bitrate    float64
	Width      int
	Height     int
	PixFmt     string
	Aspect     string
	MajorVideo string
	MajorAudio string
	ForceAudio bool // 多数音频非 LC-AAC：全部重编音频
	AnyChange  bool // 至少一集需要重编码
}

type episodeSource struct {
	path string
	key  string
}

// runMerge 执行一条合并任务，成功返回成品信息。
func (s *Service) runMerge(ctx context.Context, entry *MergeEntry) (map[string]any, error) {
	jobs := append([]map[string]any(nil), entry.jobs...)
	if len(jobs) < 2 {
		return nil, errors.New("至少需要 2 集已完成的分集才能合并")
	}
	episodes := make([]int, 0, len(jobs))
	for _, job := range jobs {
		episodes = append(episodes, episodeOfJob(job))
	}
	first, last := episodes[0], episodes[len(episodes)-1]
	for i, ep := range episodes {
		if ep != first+i {
			return nil, fmt.Errorf("分集不连续，缺少第 %d 集，请补齐后合并", first+i)
		}
	}
	if s.call == nil {
		return nil, errors.New("下载服务未接线（缺少核心门面）")
	}
	rootData, err := s.call("downloadDirectory", nil)
	if err != nil {
		return nil, fmt.Errorf("读取下载目录失败：%w", err)
	}
	root, _ := rootData["directory"].(string)
	if root == "" {
		return nil, errors.New("下载目录未初始化，请先在设置里指定下载位置")
	}
	s.setStage(ctx, entry, "准备中", 0)

	// ── 源解析：localPlayback（缓存文件+密钥）→ folder 兜底 ──
	sources := make([]episodeSource, 0, len(jobs))
	var totalBytes int64
	for i, job := range jobs {
		src, err := s.resolveEpisodeSource(job, root)
		if err != nil {
			return nil, fmt.Errorf("第 %d 集：%w", episodes[i], err)
		}
		if st, statErr := os.Stat(src.path); statErr == nil {
			totalBytes += st.Size()
		}
		sources = append(sources, src)
	}

	mergeOut := filepath.Join(root, "合并")
	if free := diskFree(filepath.Dir(mergeOut)); free > 0 {
		need := totalBytes*2 + 512*1024*1024
		if free < need {
			return nil, fmt.Errorf("合并需要约 %s，目标磁盘只剩 %s，请清理空间或换一个下载位置", humanSize(need), humanSize(free))
		}
	}

	// ── 工作目录（v1：md5(dramaID|first-last) 前 12 位，陈旧目录 >2h 清理）──
	workRoot := filepath.Join(root, ".cache", "merge")
	cleanupStale(workRoot, 2*time.Hour)
	workDir := filepath.Join(workRoot, fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("%s|%03d-%03d", entry.DramaID, first, last))))[:12])
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return nil, err
	}
	defer os.RemoveAll(workDir)

	ffmpeg, err := s.exec.Look("ffmpeg")
	if err != nil {
		return nil, err
	}
	ffprobe, err := s.exec.Look("ffprobe")
	if err != nil {
		return nil, err
	}

	// ── 1. 解密分集（0–8%，无密钥直用源文件）──
	files := make([]string, len(sources))
	decryptAny := false
	for i, src := range sources {
		if err := ctxErr(ctx); err != nil {
			return nil, err
		}
		s.setStage(ctx, entry, "解密分集", 0.08*float64(i)/float64(len(sources)))
		if src.key == "" {
			files[i] = src.path
			continue
		}
		decryptAny = true
		out := filepath.Join(workDir, fmt.Sprintf("dec-%03d.mp4", episodes[i]))
		args := []string{"-y", "-loglevel", "error", "-decryption_key", src.key, "-i", src.path,
			"-map", "0:v:0", "-map", "0:a:0?", "-c", "copy", "-avoid_negative_ts", "make_zero", out}
		if err := s.runProc(ctx, ffmpeg, args, nil); err != nil {
			return nil, fmt.Errorf("第 %d 集解密失败：%w", episodes[i], err)
		}
		files[i] = out
	}

	// ── 2. 解析媒体信息（8–15%）──
	s.setStage(ctx, entry, "解析媒体信息", 0.08)
	sigs := make([]*mediaSig, len(files))
	var totalDuration float64
	for i, f := range files {
		sig, err := s.probe(ctx, ffprobe, f)
		if err != nil {
			return nil, fmt.Errorf("第 %d 集媒体信息解析失败：%w", episodes[i], err)
		}
		sigs[i] = sig
		totalDuration += sig.Duration
	}
	if totalDuration <= 0 {
		return nil, errors.New("分集时长解析异常，无法合并")
	}

	// ── 3. 统一分集格式（15–45%）──
	s.setStage(ctx, entry, "统一分集格式", 0.15)
	plan := buildPlan(sigs)
	parts, err := s.normalizeAll(ctx, entry, ffmpeg, ffprobe, files, sigs, plan, workDir, episodes)
	if err != nil {
		return nil, err
	}

	// ── 4. 合并视频（45–55%）──
	s.setStage(ctx, entry, "合并视频", 0.45)
	full := filepath.Join(workDir, "full.mkv")
	if err := s.concat(ctx, ffmpeg, parts, full, totalDuration, entry); err != nil {
		return nil, fmt.Errorf("合并视频失败：%w", err)
	}

	// ── 5. 完整解码校验（55–95%，可关；失败保留产物并标注）──
	unverified := false
	s.mu.Lock()
	verify := s.mergeVerifyLocked()
	s.mu.Unlock()
	if verify {
		s.setStage(ctx, entry, "完整解码校验", 0.55)
		if err := s.verifyFull(ctx, entry, ffmpeg, ffprobe, full, sigs, plan, totalDuration); err != nil {
			if ctxErr(ctx) != nil {
				return nil, ctxErr(ctx)
			}
			unverified = true
			s.setDetail(entry, err.Error())
		}
	}

	// ── 6. 保存成品（97–100%）──
	s.setStage(ctx, entry, "保存成品", 0.97)
	name := fmt.Sprintf("%s 全%03d-%03d集.mkv", safeMergeTitle(entry.Title), first, last)
	if unverified {
		name = strings.TrimSuffix(name, ".mkv") + "（未完整校验）.mkv"
	}
	if err := os.MkdirAll(mergeOut, 0o755); err != nil {
		return nil, err
	}
	target := filepath.Join(mergeOut, name)
	if err := moveFile(full, target); err != nil {
		return nil, fmt.Errorf("保存成品失败：%w", err)
	}
	var outBytes int64
	if st, statErr := os.Stat(target); statErr == nil {
		outBytes = st.Size()
	}
	s.setStage(ctx, entry, "保存成品", 1)
	return map[string]any{
		"path":       target,
		"duration":   totalDuration,
		"bytes":      outBytes,
		"episodes":   len(episodes),
		"first":      first,
		"last":       last,
		"transcoded": plan.AnyChange || decryptAny,
		"unverified": unverified,
	}, nil
}

// resolveEpisodeSource v1 _local_file 顺序：localPlayback → folder/media.mp4 → folder 内最大 mp4。
func (s *Service) resolveEpisodeSource(job map[string]any, root string) (episodeSource, error) {
	dramaID := jobDramaID(job)
	index := jobIndex(job)
	plan, err := s.call("localPlayback", map[string]any{
		"drama": map[string]any{"id": dramaID},
		"index": index,
	})
	if err == nil {
		if local, _ := plan["local"].(bool); local {
			url, _ := plan["url"].(string)
			if url != "" {
				key, _ := plan["decryptionKey"].(string)
				if _, statErr := os.Stat(url); statErr == nil {
					return episodeSource{path: url, key: key}, nil
				}
			}
		}
	}
	folder, _ := job["folder"].(string)
	if folder == "" {
		folder = stringField(job["id"])
	}
	dir := filepath.Join(root, filepath.FromSlash(folder))
	candidates := []string{filepath.Join(dir, "media.mp4")}
	if entries, readErr := os.ReadDir(dir); readErr == nil {
		var best string
		var bestSize int64
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if ext != ".mp4" && ext != ".mkv" && ext != ".m4s" {
				continue
			}
			if st, statErr := e.Info(); statErr == nil && st.Size() > bestSize {
				best = filepath.Join(dir, e.Name())
				bestSize = st.Size()
			}
		}
		if best != "" {
			candidates = append(candidates, best)
		}
	}
	for _, cand := range candidates {
		if st, statErr := os.Stat(cand); statErr == nil && st.Size() > 0 {
			return episodeSource{path: cand}, nil
		}
	}
	return episodeSource{}, errors.New("本地文件缺失或不完整，请重新下载")
}

// probe ffprobe JSON → mediaSig（v1 _ffprobe_json + media_signature）。
func (s *Service) probe(ctx context.Context, ffprobe, path string) (*mediaSig, error) {
	var lines []string
	args := []string{"-v", "error", "-print_format", "json", "-show_format", "-show_streams", "-show_data_hash", "sha256", path}
	if err := s.runProc(ctx, ffprobe, args, func(line string) { lines = append(lines, line) }); err != nil {
		return nil, err
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(strings.Join(lines, "\n")), &data); err != nil {
		return nil, errors.New("无法解析媒体信息")
	}
	return mediaSignature(path, data), nil
}

func mediaSignature(path string, data map[string]any) *mediaSig {
	sig := &mediaSig{Path: path}
	if format, ok := data["format"].(map[string]any); ok {
		sig.Duration = jsonFloat(format["duration"])
		sig.Bytes = int64(jsonFloat(format["size"]))
	}
	streams, _ := data["streams"].([]any)
	if sig.Duration <= 0 {
		for _, item := range streams {
			if st, ok := item.(map[string]any); ok {
				if d := jsonFloat(st["duration"]); d > sig.Duration {
					sig.Duration = d
				}
			}
		}
	}
	if sig.Bytes == 0 {
		if st, err := os.Stat(path); err == nil {
			sig.Bytes = st.Size()
		}
	}
	for _, item := range streams {
		st, ok := item.(map[string]any)
		if !ok {
			continue
		}
		switch st["codec_type"] {
		case "video":
			if sig.Video == nil {
				sig.Video = st
			}
		case "audio":
			if sig.Audio == nil {
				sig.Audio = st
			}
		}
	}
	sig.VideoSig = videoSignature(sig.Video)
	sig.AudioSig = audioSignature(sig.Audio)
	return sig
}

// videoSignature v1：codec|宽|高|pix_fmt|SAR|色彩转移|色彩基。
func videoSignature(stream map[string]any) string {
	if stream == nil {
		return ""
	}
	parts := []string{
		stringField(stream["codec_name"]),
		strconv.Itoa(int(jsonFloat(stream["width"]))),
		strconv.Itoa(int(jsonFloat(stream["height"]))),
		stringField(stream["pix_fmt"]),
		orDefault(stringField(stream["sample_aspect_ratio"]), "1:1"),
		orDefault(stringField(stream["color_transfer"]), "unknown"),
		orDefault(stringField(stream["color_primaries"]), "unknown"),
	}
	return strings.Join(parts, "|")
}

// audioSignature v1：codec|profile|采样率|声道数|声道布局|extradata 哈希；无声="none"。
func audioSignature(stream map[string]any) string {
	if stream == nil {
		return "none"
	}
	parts := []string{
		stringField(stream["codec_name"]),
		orDefault(stringField(stream["profile"]), "unknown"),
		stringField(stream["sample_rate"]),
		strconv.Itoa(int(jsonFloat(stream["channels"]))),
		stringField(stream["channel_layout"]),
		orDefault(stringField(stream["extradata_hash"]), "unknown"),
	}
	return strings.Join(parts, "|")
}

// videoBitrate v1：流 bit_rate，缺失则 bytes*8/duration。
func videoBitrate(sig *mediaSig) float64 {
	if sig == nil {
		return 0
	}
	if sig.Video != nil {
		if rate := jsonFloat(sig.Video["bit_rate"]); rate > 0 {
			return rate
		}
	}
	if sig.Duration > 0 {
		return float64(sig.Bytes) * 8 / sig.Duration
	}
	return 0
}

// majority v1 _majority：出现次数优先，平票取总时长更长的组。
func majority(sigs []*mediaSig, key func(*mediaSig) string) *mediaSig {
	groups := map[string][]*mediaSig{}
	order := []string{}
	for _, sig := range sigs {
		k := key(sig)
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], sig)
	}
	best := order[0]
	var bestRank [2]float64
	for _, k := range order {
		group := groups[k]
		var dur float64
		for _, sig := range group {
			dur += sig.Duration
		}
		rank := [2]float64{float64(len(group)), dur}
		if best == "" || rank[0] > bestRank[0] || (rank[0] == bestRank[0] && rank[1] > bestRank[1]) {
			best, bestRank = k, rank
		}
	}
	return groups[best][0]
}

var transportVideo = map[string]bool{"h264": true, "hevc": true}
var transportAudio = map[string]bool{"aac": true, "ac3": true, "eac3": true, "mp2": true}

// buildPlan v1 build_plan：以多数签名统一；transport 一致走流拷贝进 mpegts，
// 差异分集重编码；多数音频为非 LC 的 aac 时全部重编（canonical_aac）。
func buildPlan(sigs []*mediaSig) *partPlan {
	plan := &partPlan{}
	major := majority(sigs, func(x *mediaSig) string { return x.VideoSig })
	plan.MajorVideo = major.VideoSig
	plan.MajorAudio = major.AudioSig
	if major.Video != nil {
		plan.VideoCodec = stringField(major.Video["codec_name"])
		plan.Width = int(jsonFloat(major.Video["width"]))
		plan.Height = int(jsonFloat(major.Video["height"]))
		plan.PixFmt = stringField(major.Video["pix_fmt"])
	}
	plan.Bitrate = videoBitrate(major)
	plan.Transport = transportVideo[plan.VideoCodec]
	if plan.Transport && major.Audio != nil {
		plan.Transport = transportAudio[stringField(major.Audio["codec_name"])]
	}
	switch plan.VideoCodec {
	case "hevc":
		plan.Encoder = "libx265"
	case "h264":
		plan.Encoder = "libx264"
	}
	if major.Audio != nil && stringField(major.Audio["codec_name"]) == "aac" &&
		stringField(major.Audio["profile"]) != "LC" {
		plan.ForceAudio = true
	}
	for _, sig := range sigs {
		if sig.VideoSig != plan.MajorVideo || plan.ForceAudio || sig.AudioSig != plan.MajorAudio {
			plan.AnyChange = true
			break
		}
	}
	return plan
}

// normalizeAll v1 _normalize_all：worker 池（2..6），逐分集规范化为统一容器。
func (s *Service) normalizeAll(ctx context.Context, entry *MergeEntry, ffmpeg, ffprobe string,
	files []string, sigs []*mediaSig, plan *partPlan, workDir string, episodes []int) ([]string, error) {

	ext := ".mkv"
	if plan.Transport {
		ext = ".ts"
	}
	parts := make([]string, len(files))
	type task struct {
		idx int
	}
	work := make(chan task, len(files))
	for i := range files {
		work <- task{i}
	}
	close(work)
	workers := min(6, max(2, cpuCount()-1))
	var done int64
	var firstErr error
	var errMu sync.Mutex
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range work {
				if ctxErr(ctx) != nil {
					return
				}
				part, err := s.normalizeOne(ctx, ffmpeg, ffprobe, files[t.idx], sigs[t.idx], plan,
					filepath.Join(workDir, fmt.Sprintf("part-%03d%s", episodes[t.idx], ext)), episodes[t.idx])
				if err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					errMu.Unlock()
					return
				}
				parts[t.idx] = part
				n := atomic.AddInt64(&done, 1)
				s.setStage(ctx, entry, "统一分集格式", 0.15+0.30*float64(n)/float64(len(files)))
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	return parts, nil
}

// normalizeOne 单分集 → 统一容器/编码（v1 _normalize_one + _part_audio_ok 128k 重试）。
func (s *Service) normalizeOne(ctx context.Context, ffmpeg, ffprobe, src string, sig *mediaSig,
	plan *partPlan, out string, episode int) (string, error) {

	needVideo := sig.VideoSig != plan.MajorVideo
	needAudio := plan.ForceAudio || sig.AudioSig != plan.MajorAudio
	args := []string{"-y", "-loglevel", "error", "-i", src,
		"-map", "0:v:0", "-map", "0:a:0?", "-map_metadata", "-1", "-avoid_negative_ts", "make_zero"}
	audioTranscoded := false
	switch {
	case !needVideo && !needAudio:
		args = append(args, "-c", "copy")
	default:
		if needVideo && plan.Encoder != "" {
			args = append(args, "-c:v", plan.Encoder, "-preset", "veryfast")
			if plan.Bitrate > 0 {
				args = append(args,
					"-b:v", strconv.FormatInt(int64(plan.Bitrate), 10),
					"-maxrate", strconv.FormatInt(int64(plan.Bitrate*1.4), 10),
					"-bufsize", strconv.FormatInt(int64(plan.Bitrate*2.8), 10))
			} else {
				args = append(args, "-crf", "18")
			}
			if vf := buildVF(plan); vf != "" {
				args = append(args, "-vf", vf)
			}
		} else {
			args = append(args, "-c:v", "copy")
		}
		if needAudio {
			audioTranscoded = true
			args = append(args, "-c:a", "aac", "-aac_coder", "fast", "-b:a", "192k")
		} else if sig.Audio != nil {
			args = append(args, "-c:a", "copy")
		}
	}
	effectiveVideoCodec := plan.VideoCodec
	if !needVideo && sig.Video != nil {
		effectiveVideoCodec = stringField(sig.Video["codec_name"])
	}
	if plan.Transport {
		switch effectiveVideoCodec {
		case "h264":
			args = append(args, "-bsf:v", "h264_mp4toannexb")
		case "hevc":
			args = append(args, "-bsf:v", "hevc_mp4toannexb")
		}
		args = append(args, "-f", "mpegts")
	}
	args = append(args, out)
	if err := s.runProc(ctx, ffmpeg, args, nil); err != nil {
		return "", fmt.Errorf("第 %d 集格式统一失败：%w", episode, err)
	}
	if st, err := os.Stat(out); err != nil || st.Size() == 0 {
		return "", fmt.Errorf("第 %d 集格式统一失败：产物为空", episode)
	}
	if audioTranscoded {
		if err := s.partAudioOK(ctx, ffmpeg, out); err != nil {
			// v1：严格解码失败 → 降 128k 重编一次
			retry := filepath.Join(filepath.Dir(out), fmt.Sprintf("retry-%03d%s", episode, filepath.Ext(out)))
			rebuilt, rbErr := s.normalizeAudioRetry(ctx, ffmpeg, src, plan, needVideo, retry, episode)
			if rbErr != nil {
				return "", fmt.Errorf("第 %d 集音频规范化失败：%v", episode, err)
			}
			if err2 := s.partAudioOK(ctx, ffmpeg, rebuilt); err2 != nil {
				return "", fmt.Errorf("第 %d 集音频规范化失败：%v", episode, err2)
			}
			return rebuilt, nil
		}
	}
	return out, nil
}

func (s *Service) normalizeAudioRetry(ctx context.Context, ffmpeg, src string, plan *partPlan,
	needVideo bool, out string, episode int) (string, error) {

	args := []string{"-y", "-loglevel", "error", "-i", src,
		"-map", "0:v:0", "-map", "0:a:0?", "-map_metadata", "-1", "-avoid_negative_ts", "make_zero"}
	if needVideo && plan.Encoder != "" {
		args = append(args, "-c:v", plan.Encoder, "-preset", "veryfast", "-crf", "18")
	} else {
		args = append(args, "-c:v", "copy")
	}
	args = append(args, "-c:a", "aac", "-aac_coder", "fast", "-b:a", "128k")
	if plan.Transport {
		args = append(args, "-f", "mpegts")
	}
	args = append(args, out)
	if err := s.runProc(ctx, ffmpeg, args, nil); err != nil {
		return "", err
	}
	return out, nil
}

// partAudioOK v1 _part_audio_ok：严格解码音轨。
func (s *Service) partAudioOK(ctx context.Context, ffmpeg, part string) error {
	args := []string{"-nostdin", "-xerror", "-err_detect", "explode", "-loglevel", "error",
		"-i", part, "-map", "0:a:0", "-vn", "-f", "null", os.DevNull}
	return s.runProc(ctx, ffmpeg, args, nil)
}

func buildVF(plan *partPlan) string {
	var filters []string
	if plan.Width > 0 && plan.Height > 0 {
		filters = append(filters,
			fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease", plan.Width, plan.Height),
			fmt.Sprintf("pad=%d:%d:(ow-iw)/2:(oh-ih)/2", plan.Width, plan.Height))
	}
	filters = append(filters, "setsar=1")
	if plan.PixFmt != "" {
		filters = append(filters, "format="+plan.PixFmt)
	}
	return strings.Join(filters, ",")
}

// concat v1 合并视频段：ffconcat 列表 + 流拷贝。
func (s *Service) concat(ctx context.Context, ffmpeg string, parts []string, out string, totalDuration float64, entry *MergeEntry) error {
	listing := out + ".list.txt"
	var sb strings.Builder
	sb.WriteString("ffconcat version 1.0\n")
	for _, part := range parts {
		sb.WriteString("file '" + strings.ReplaceAll(part, "'", `'\''`) + "'\n")
	}
	if err := os.WriteFile(listing, []byte(sb.String()), 0o644); err != nil {
		return err
	}
	args := []string{"-y", "-loglevel", "error", "-f", "concat", "-safe", "0", "-i", listing,
		"-map", "0:v:0", "-map", "0:a:0?", "-c", "copy", "-avoid_negative_ts", "make_zero",
		"-progress", "pipe:1", out}
	onLine := progressTicker(s, ctx, entry, "合并视频", 0.45, 0.10, totalDuration)
	return s.runProc(ctx, ffmpeg, args, onLine)
}

// verifyFull v1 完整解码校验 + 输出流/时长一致性；失败由调用方降级为「未完整校验」。
func (s *Service) verifyFull(ctx context.Context, entry *MergeEntry, ffmpeg, ffprobe, full string,
	sigs []*mediaSig, plan *partPlan, expectedDuration float64) error {

	args := []string{"-nostdin", "-loglevel", "error", "-xerror", "-err_detect", "explode",
		"-progress", "pipe:1", "-i", full, "-map", "0:v:0", "-map", "0:a:0?", "-sn", "-dn",
		"-f", "null", os.DevNull}
	onLine := progressTicker(s, ctx, entry, "完整解码校验", 0.55, 0.40, expectedDuration)
	if err := s.runProc(ctx, ffmpeg, args, onLine); err != nil {
		if ctxErr(ctx) != nil {
			return ctxErr(ctx)
		}
		return errors.New("完整解码校验未通过（存在坏帧或解码中断）")
	}
	out, err := s.probe(ctx, ffprobe, full)
	if err != nil {
		return errors.New("成品复检失败")
	}
	if out.VideoSig != plan.MajorVideo {
		return errors.New("成品视频流与源分集不一致")
	}
	if out.AudioSig != plan.MajorAudio {
		return errors.New("成品音频流与源分集不一致")
	}
	if expectedDuration > 0 && math.Abs(out.Duration-expectedDuration) > math.Max(1.5, expectedDuration*0.02) {
		return fmt.Errorf("成品时长校验失败（期望 %.1fs，实际 %.1fs）", expectedDuration, out.Duration)
	}
	return nil
}

// progressTicker 解析 ffmpeg -progress 的 out_time_ms/out_time_us → 阶段内线性进度。
func progressTicker(sv *Service, ctx context.Context, entry *MergeEntry, stage string, from, span, total float64) func(string) {
	return func(line string) {
		var us float64
		switch {
		case strings.HasPrefix(line, "out_time_us="):
			us, _ = strconv.ParseFloat(strings.TrimPrefix(line, "out_time_us="), 64)
		case strings.HasPrefix(line, "out_time_ms="):
			ms, _ := strconv.ParseFloat(strings.TrimPrefix(line, "out_time_ms="), 64)
			us = ms * 1000
		default:
			return
		}
		if total <= 0 || ctxErr(ctx) != nil {
			return
		}
		ratio := us / 1e6 / total
		if ratio > 1 {
			ratio = 1
		}
		sv.setStageQuiet(entry, stage, from+span*ratio)
	}
}

// setStage 阶段推进（含取消传播检查）。
func (s *Service) setStage(ctx context.Context, entry *MergeEntry, stage string, progress float64) {
	_ = ctxErr(ctx)
	s.setStageQuiet(entry, stage, progress)
}

func (s *Service) setStageQuiet(entry *MergeEntry, stage string, progress float64) {
	s.mu.Lock()
	entry.Stage = stage
	if progress > entry.Progress {
		entry.Progress = math.Round(progress*1000) / 1000
	}
	s.mu.Unlock()
}

func (s *Service) setDetail(entry *MergeEntry, detail string) {
	s.mu.Lock()
	entry.Detail = detail
	s.mu.Unlock()
}

func (s *Service) runProc(ctx context.Context, bin string, args []string, onLine func(string)) error {
	return s.exec.Run(ctx, bin, args, onLine)
}

// ---------- helpers ----------

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func stringField(v any) string {
	s, _ := v.(string)
	return s
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func jsonFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

// safeMergeTitle v1：非法文件名字符→下划线，截 60 字。
func safeMergeTitle(title string) string {
	replacer := strings.NewReplacer(`\`, "_", `/`, "_", `:`, "_", `*`, "_", `?`, "_", `"`, "_", `<`, "_", `>`, "_", `|`, "_")
	clean := replacer.Replace(strings.TrimSpace(orDefault(title, "合并成品")))
	runes := []rune(clean)
	if len(runes) > 60 {
		clean = string(runes[:60])
	}
	return clean
}

// moveFile 同盘 rename，跨盘退化为 .part 复制 + 原子替换。
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	tmp := dst + ".part"
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := copyAll(in, out); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func cleanupStale(root string, olderThan time.Duration) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	now := time.Now()
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) < olderThan {
			continue
		}
		_ = os.RemoveAll(filepath.Join(root, e.Name()))
	}
}

func cpuCount() int {
	if n := parallelism(); n > 0 {
		return n
	}
	return 2
}
