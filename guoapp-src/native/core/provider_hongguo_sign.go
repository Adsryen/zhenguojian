package core

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"duanjuapp/native/core/sign6"
)

func newHongguoDeviceID() string {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return strconv.FormatUint(1_000_000_000_000_000_000+binary.BigEndian.Uint64(random[:])%8_000_000_000_000_000_000, 10)
}

// signHongguoRequest 给红果 App 接口请求加签名。
//
// 2026-10-22 起红果网关从「只判 X-Argus 是否存在」升级为校验真实签名，
// 且要求 X-Gorgon / X-Khronos / X-Ladon / X-Argus / X-Helios / X-Medusa
// 六个头齐全；只带 Gorgon/Khronos 时目录与详情接口会稳定返回
// 「HTTP 200 + 0 字节」。六头算法见子包 sign6（纯计算、无网络）。
//
// 注意两点：
//   - 签名对 query 字节级敏感，必须先写完最终 query 再签；
//   - 服务端按 BranchOf 的三选一分支校验，取到 1 的分支会被拒绝，
//     这里通过微调 _rticket 避开（约 1/3 概率），否则偶发空响应。
func signHongguoRequest(request *http.Request, body []byte, now time.Time) {
	khronos := now.Unix()
	ticketBase := now.UnixMilli()
	ticket := ticketBase
	for offset := 0; offset < 32; offset++ {
		query := request.URL.Query()
		ticket = ticketBase + int64(offset)
		query.Set("_rticket", strconv.FormatInt(ticket, 10))
		request.URL.RawQuery = query.Encode()
		if sign6.BranchOf(request.URL.String(), body, khronos) != 1 {
			break
		}
	}
	for key, value := range sign6.Sign(request.URL.String(), body, khronos) {
		request.Header.Set(key, value)
	}
	// 参考客户端只给播放接口带 Gorgon/Khronos/Ladon/Argus 四个签名头。
	// 多带 Helios/Medusa 时取流接口会间歇性回 200 空包（实测去掉后
	// 同一设备同一集 3/3 稳定返回），所以播放类请求按参考客户端裁剪。
	if isHongguoPlaybackPath(request.URL.Path) {
		request.Header.Del("X-Helios")
		request.Header.Del("X-Medusa")
		useHongguoPlayerSign(request, body, khronos)
	}
	request.Header.Set("X-SS-Req-Ticket", strconv.FormatInt(ticket, 10))
	if body != nil {
		request.Header.Set("X-SS-STUB", fmt.Sprintf("%X", md5.Sum(body)))
	}
}

// useHongguoPlayerSign 用播放接口专用的长版 Argus/Ladon 覆盖通用签名。
//
// 2026-11-02 起播放接口只认这一套：Ladon 为 36 字节 Speck 密文，
// Argus 明文用 SM3(md5(body)) 与 SM3(query) 绑定请求内容，通用签名会被
// 判为无效并回 HTTP 200 + 0 字节。/novel/player/ 下的多视频、旧版单视频与
// preload 三个端点实测都要这套签名。算法见 sign6.PlayerArgus / PlayerLadon。
func useHongguoPlayerSign(request *http.Request, body []byte, khronos int64) {
	if !strings.HasPrefix(request.URL.Path, "/novel/player/") {
		return
	}
	deviceID := request.URL.Query().Get("device_id")
	if deviceID == "" {
		return
	}
	rnd := make([]byte, 20)
	if _, err := rand.Read(rnd); err != nil {
		return
	}
	request.Header.Set("X-Ladon", sign6.PlayerLadon("8662", uint32(khronos), rnd[:4]))
	request.Header.Set("X-Argus", sign6.PlayerArgus(deviceID, uint32(khronos), string(body), request.URL.RawQuery, rnd))
}
