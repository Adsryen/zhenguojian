package core

// 黑桃取流：GET /index.php/vod/player/id/{id}/nid/{n}/sid/{s}.html（需年龄墙
// Cookie，否则服务端不输出 player_aaaa），页内 player_aaaa={...} 为大括号扫描
// 提取的 JSON，url 按 encrypt 三态解密：0 原样 / 1 URL 反转义 / 2 base64 后再
// URL 反转义。移植自 reference/guoapp/native/core/provider_heitao.go:368-523。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type heitaoPlayerEntry struct {
	URL     string `json:"url"`
	Encrypt any    `json:"encrypt"`
}

func heitaoSeparateEpisode(key string) (int, int, bool) {
	parts := strings.Split(key, ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	sid, sidErr := strconv.Atoi(parts[0])
	nid, nidErr := strconv.Atoi(parts[1])
	if sidErr != nil || nidErr != nil || sid < 1 || nid < 1 {
		return 0, 0, false
	}
	return sid, nid, true
}

func heitaoPlayerID(raw string) (int, int, bool) {
	match := reHeitaoPlayerRoute.FindStringSubmatch(raw)
	if len(match) < 3 {
		return 0, 0, false
	}
	nid, nidErr := strconv.Atoi(match[1])
	sid, sidErr := strconv.Atoi(match[2])
	if nidErr != nil || sidErr != nil || sid < 1 || nid < 1 {
		return 0, 0, false
	}
	return sid, nid, true
}

// heitaoReadPlayerConfig 从播放器页正文里大括号扫描提取 player_aaaa JSON。
func heitaoReadPlayerConfig(body string) (heitaoPlayerEntry, error) {
	var entry heitaoPlayerEntry
	index := strings.Index(body, "player_aaaa")
	if index < 0 {
		return entry, errors.New("黑桃播放器未返回配置")
	}
	text := body[index:]
	open := strings.Index(text, "{")
	if open < 0 {
		return entry, errors.New("黑桃播放器配置无效")
	}
	depth, inString, escaped, end := 0, false, false, -1
scan:
	for i := open; i < len(text); i++ {
		value := text[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if value == '\\' {
				escaped = true
				continue
			}
			if value == '"' {
				inString = false
			}
			continue
		}
		switch value {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
				break scan
			}
		}
	}
	if end < 0 {
		return entry, errors.New("黑桃播放器配置不完整")
	}
	if err := json.Unmarshal([]byte(text[open:end+1]), &entry); err != nil {
		return entry, fmt.Errorf("黑桃播放器配置解析失败：%w", err)
	}
	if strings.TrimSpace(entry.URL) == "" {
		return entry, errors.New("黑桃该集暂无可用播放地址")
	}
	return entry, nil
}

// heitaoDecryptMediaURL 三态解密：0 原样 / 1 URL 反转义 / 2 base64 后反转义。
func heitaoDecryptMediaURL(raw string, encrypt int) (string, error) {
	raw = strings.TrimSpace(raw)
	switch encrypt {
	case 0:
		return raw, nil
	case 1:
		unescaped, err := url.PathUnescape(raw)
		if err != nil {
			return "", errors.New("黑桃播放地址解码失败")
		}
		return unescaped, nil
	case 2:
		decoded, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			decoded, err = base64.RawStdEncoding.DecodeString(raw)
		}
		if err != nil {
			return "", errors.New("黑桃播放地址解码失败")
		}
		unescaped, err := url.PathUnescape(string(decoded))
		if err != nil {
			return "", errors.New("黑桃播放地址解码失败")
		}
		return unescaped, nil
	default:
		return "", errors.New("黑桃播放地址加密方式暂不支持")
	}
}

func heitaoPlayerEncrypt(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case string:
		number, _ := strconv.Atoi(strings.TrimSpace(typed))
		return number
	case bool:
		if typed {
			return 1
		}
	}
	return 0
}

// resolveHeitaoMedia 取流入口：校验分集 → 抓播放器页（带年龄墙 Cookie）→
// 解析并解密 player_aaaa.url → 带 Referer 走 m3u8 播放列表准备。
func (d *Downloader) resolveHeitaoMedia(ctx context.Context, task Task) (providerMedia, error) {
	source, sourceID, valid := splitProviderDramaID(task.DramaID)
	prefix := providerChapterID(sourceHeitao, sourceID, "")
	if !valid || source != sourceHeitao || !webProviderNumericID.MatchString(sourceID) || !strings.HasPrefix(task.Chapter.ID, prefix) {
		return providerMedia{}, errors.New("黑桃播放分集信息无效，请刷新详情")
	}
	sid, nid, ok := heitaoSeparateEpisode(strings.TrimPrefix(task.Chapter.ID, prefix))
	if !ok {
		sid, nid, ok = heitaoPlayerID(task.Chapter.PageURL)
	}
	if !ok {
		return providerMedia{}, errors.New("黑桃播放分集信息无效，请刷新详情")
	}
	site := heitaoSite()
	playerURL := fmt.Sprintf("%s/index.php/vod/player/id/%s/nid/%d/sid/%d.html", site, sourceID, nid, sid)
	playURL := fmt.Sprintf("%s/index.php/vod/play/id/%s/sid/%d/nid/%d.html", site, sourceID, sid, nid)
	body, pageURL, err := d.heitaoFetchText(ctx, playerURL, playURL)
	if err != nil {
		return providerMedia{}, err
	}
	entry, err := heitaoReadPlayerConfig(body)
	if err != nil {
		return providerMedia{}, err
	}
	address, err := heitaoDecryptMediaURL(entry.URL, heitaoPlayerEncrypt(entry.Encrypt))
	if err != nil {
		return providerMedia{}, err
	}
	address = resolveProviderURL(pageURL, address)
	if !isProviderHTTPMediaURL(address) {
		return providerMedia{}, errors.New("黑桃播放地址无效")
	}
	referer := site + "/"
	return d.prepareWebProviderMedia(ctx, providerMedia{
		URL:     address,
		Referer: referer,
		credentials: &providerMediaCredentials{
			userAgent: heitaoUserAgent,
			referer:   referer,
		},
	}, "黑桃")
}
