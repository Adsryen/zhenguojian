package main

// 下载与合并壳层绑定（V2-3）。事件契约：downloadsvc 轮询变化时
// EventsEmit("downloads:update", payloadJSON)，payload =
// {jobs:[{id,drama,chapter,index(集号,1-based),state,archived,bytes,total,
//   progress(0..1),quality,actualQuality,speed,eta,error,…}],
//  merges:[{id,dramaId,title,episodes(1-based集号),count,state,stage,progress,detail,output,bytes}],
//  status:string, mergeVerify:bool}——字段与前端 types.DownloadSnapshot 对齐。

import (
	"encoding/json"
	"errors"
	"os"

	"zhenguojian/client-v2/internal/downloadsvc"
	"zhenguojian/client-v2/internal/native"
)

func (a *App) getDownloadSvc() (*downloadsvc.Service, error) {
	if a.downloadSvc == nil {
		return nil, errors.New("下载服务未初始化")
	}
	return a.downloadSvc, nil
}

// DownloadList → JSON {jobs, merges, status, mergeVerify}（强制刷新一次核心快照）。
func (a *App) DownloadList() (string, error) {
	svc, err := a.getDownloadSvc()
	if err != nil {
		return "", err
	}
	snap, err := svc.Snapshot()
	if err != nil {
		return "", err
	}
	return marshalJSON(snap)
}

// DownloadStart 选集入队：entriesJSON = [{chapter, index}]（前端由 detail chapters
// 勾选拼装，index=真实集号，1-based；核心 enqueueDownloads 要求 ≥1，否则整批
// 报「下载集数无效」）→ 核心 enqueueDownloads。→ JSON {added}
func (a *App) DownloadStart(dramaJSON, entriesJSON string, quality float64) (string, error) {
	svc, err := a.getDownloadSvc()
	if err != nil {
		return "", err
	}
	drama, err := unmarshalObject(dramaJSON)
	if err != nil {
		return "", err
	}
	entries, err := decodeEpisodeEntries(entriesJSON)
	if err != nil {
		return "", err
	}
	out, err := svc.Enqueue(drama, entries, int(quality))
	if err != nil {
		return "", err
	}
	return marshalJSON(out)
}

func decodeEpisodeEntries(s string) ([]downloadsvc.EpisodeEntry, error) {
	if s == "" {
		return nil, errors.New("请先选择要下载的分集")
	}
	var raw []struct {
		Chapter map[string]any `json:"chapter"`
		Index   float64        `json:"index"`
	}
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return nil, err
	}
	entries := make([]downloadsvc.EpisodeEntry, 0, len(raw))
	for _, e := range raw {
		entries = append(entries, downloadsvc.EpisodeEntry{Chapter: e.Chapter, Index: int(e.Index)})
	}
	return entries, nil
}

// DownloadPause / DownloadResume / DownloadRetry / DownloadCancel：
// 单任务控制（Retry=resume 映射，核心无独立重试命令；Cancel=remove 删记录+删文件）。
func (a *App) DownloadPause(jobID string) (string, error) {
	return a.downloadControl(jobID, "pause")
}

func (a *App) DownloadResume(jobID string) (string, error) {
	return a.downloadControl(jobID, "resume")
}

func (a *App) DownloadRetry(jobID string) (string, error) {
	return a.downloadControl(jobID, "resume")
}

func (a *App) DownloadCancel(jobID string) (string, error) {
	return a.downloadControl(jobID, "remove")
}

// DownloadArchive / DownloadRestore：归档视图配套（核心 archive/restore）。
func (a *App) DownloadArchive(jobID string) (string, error) {
	return a.downloadControl(jobID, "archive")
}

func (a *App) DownloadRestore(jobID string) (string, error) {
	return a.downloadControl(jobID, "restore")
}

func (a *App) downloadControl(jobID, command string) (string, error) {
	svc, err := a.getDownloadSvc()
	if err != nil {
		return "", err
	}
	out, err := svc.Control(jobID, command)
	if err != nil {
		return "", err
	}
	return marshalJSON(out)
}

// DownloadControlAll 全部暂停/继续（"pauseAll"|"resumeAll"）。
func (a *App) DownloadControlAll(command string) (string, error) {
	svc, err := a.getDownloadSvc()
	if err != nil {
		return "", err
	}
	out, err := svc.ControlAll(command)
	if err != nil {
		return "", err
	}
	return marshalJSON(out)
}

// DownloadRemoveBatch 批量删除 → JSON {completed, failures}。
func (a *App) DownloadRemoveBatch(jobIDsJSON string) (string, error) {
	svc, err := a.getDownloadSvc()
	if err != nil {
		return "", err
	}
	ids, err := decodeStringArray(jobIDsJSON)
	if err != nil {
		return "", err
	}
	out, err := svc.RemoveBatch(ids)
	if err != nil {
		return "", err
	}
	return marshalJSON(out)
}

// DownloadSetDanmakuDownload 客户端开关「下载时保存弹幕」（v1 downloads.py:1095-1099 那个勾选框，
// 真值在 tools.danmaku_download_enabled，默认关）。
//
// 为什么开关落在 Go 而不是 localStorage：抓弹幕发生在**下载完成的那一刻**，
// 那是 Go 的轮询循环，页面关掉也得能跑；开关放前端就等于把真值放在没人读的地方。
// 落盘与 mergeVerify 同一个文件（downloads.state.json），两个键一起写，
// 详见 internal/downloadsvc/sidecar.go 的 loadClientFlags/saveClientFlags。
func (a *App) DownloadSetDanmakuDownload(on bool) (string, error) {
	svc, err := a.getDownloadSvc()
	if err != nil {
		return "", err
	}
	if err := svc.SetDanmakuDownload(on); err != nil {
		return "", err
	}
	return marshalJSON(map[string]any{"danmakuDownload": svc.DanmakuDownload()})
}

// DownloadSettings → JSON（资源设置 + mergeVerify + danmakuDownload 合并视图）。
func (a *App) DownloadSettings() (string, error) {
	svc, err := a.getDownloadSvc()
	if err != nil {
		return "", err
	}
	out, err := svc.Settings()
	if err != nil {
		return "", err
	}
	return marshalJSON(out)
}

// SaveDownloadSettings 读-改-写保存下载并发/分源并发/合并校验开关 → JSON 最新设置。
func (a *App) SaveDownloadSettings(concurrency float64, bySource, mergeVerify bool) (string, error) {
	svc, err := a.getDownloadSvc()
	if err != nil {
		return "", err
	}
	out, err := svc.SaveSettings(int(concurrency), bySource, mergeVerify)
	if err != nil {
		return "", err
	}
	return marshalJSON(out)
}

// MergeStart 触发合并：mode="season"（dramaJSON 全剧完成分集）或
// "selected"（jobIDsJSON 显式勾选，须同一部剧且全部完成）。→ JSON {queued, replaced, episodes}
func (a *App) MergeStart(mode, dramaJSON, jobIDsJSON string) (string, error) {
	svc, err := a.getDownloadSvc()
	if err != nil {
		return "", err
	}
	var drama map[string]any
	if dramaJSON != "" {
		var derr error
		drama, derr = unmarshalObject(dramaJSON)
		if derr != nil {
			return "", derr
		}
	}
	ids, err := decodeStringArray(jobIDsJSON)
	if err != nil {
		return "", err
	}
	out, err := svc.MergeStart(mode, drama, ids)
	if err != nil {
		return "", err
	}
	return marshalJSON(out)
}

// MergeCancel 取消合并（""=全部运行中+排队）。
func (a *App) MergeCancel(mergeID string) (string, error) {
	svc, err := a.getDownloadSvc()
	if err != nil {
		return "", err
	}
	out, err := svc.MergeCancel(mergeID)
	if err != nil {
		return "", err
	}
	return marshalJSON(out)
}

// MergeList → JSON 数组（同 DownloadList.merges，供单独刷新）。
func (a *App) MergeList() (string, error) {
	svc, err := a.getDownloadSvc()
	if err != nil {
		return "", err
	}
	return marshalJSON(svc.MergeList())
}

// ArchiveScan → JSON {directory, exists, groups[]}：下载目录归档产物扫描
// （交付 B，downloadsvc.ScanArchiveRoot）。文件系统为准 + 索引记录富化；
// 目录不存在回 {exists:false, groups:[]} 非 error，供归档页签空态展示。
func (a *App) ArchiveScan() (string, error) {
	svc, err := a.getDownloadSvc()
	if err != nil {
		return "", err
	}
	out, err := svc.ScanArchiveRoot()
	if err != nil {
		return "", err
	}
	return marshalJSON(out)
}

// DownloadDirectory → JSON {directory, exists}：核心下载根目录（分集成品在
// <root>/<jobId>/media.mp4，合并成品在 <root>/合并/），供归档页签自证扫描位置，
// 并区分「目录不存在」与「目录存在但暂无归档」。
func (a *App) DownloadDirectory() (string, error) {
	data, err := native.Call("downloadDirectory", nil)
	if err != nil {
		return "", err
	}
	dir, _ := data["directory"].(string)
	exists := false
	if dir != "" {
		if st, serr := os.Stat(dir); serr == nil && st.IsDir() {
			exists = true
		}
	}
	return marshalJSON(map[string]any{"directory": dir, "exists": exists})
}

func decodeStringArray(s string) ([]string, error) {
	if s == "" || s == "[]" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, err
	}
	return out, nil
}
