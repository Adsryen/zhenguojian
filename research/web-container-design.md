# 真果鉴 v3 容器化设计（纯 Web 形态）

> 状态：已批（2026-11-19，用户逐段确认 §1–§5 后指示「你先做吧，用起来了就知道哪儿有问题了」）。
> 背景：Windows 黑屏问题在桌面壳路线（Wails→Electron）两次未果，用户拍板放弃 Windows，
> 转向 Docker 容器 + 纯浏览器访问。定调（用户原话）：「v1 版本的代码都是能正常播放的，
> v2 和现在 v3 其实都是转译而已」——**平迁不是重写**。

## 0. 决策记录（问→答）

| 问题 | 决策 |
|---|---|
| 使用形态 | 纯 Web，多设备浏览器访问（拒绝 VNC 远程桌面方案） |
| 功能面 | **B 全量平迁**（~98 桥方法一次过，分批评交） |
| 鉴权 | 账号密码，**compose 环境变量**注入（`ZGJ_WEB_USER`/`ZGJ_WEB_PASS`） |
| 桥面传输 | WebSocket RPC，**复用现有反射方法表**（stdio JSON 信封原样过 WS） |
| 视频播放 | **后端分段代理 + 浏览器 MSE**（拒绝 mpv-rtc / ffmpeg-HLS） |
| spec 位置 | 本文件（仓库惯例 research/，不用 skill 默认路径） |

## 1. 容器架构（§1 已批）

一个容器、一个进程（sidecar web 模式）、一个端口。sidecar 二进制加 web 模式
（env `ZGJ_WEB=1`）：

| 端点 | 内容 |
|---|---|
| `/` | 前端静态（Vite build 产物，`go:embed` 进二进制） |
| `/rpc`（WS） | 桥面：现有 stdio 信封 + 反射表原样过 WS，98 方法零改写 |
| `/video/seg` | bilibili 分段代理（Referer 透传，白名单防 SSRF，支持 Range） |
| `/file` | 本地下载成片播放/下载出口（同白名单机制，限 /data 下） |

- 数据持久化：`./data` bind mount → `/data`（`ZHENGUO_DATA=/data`：登录态/下载/设置/日志）
- 端口：compose `9320:8080`
- 镜像：sidecar + ffmpeg（下载合并）+ 前端静态；**无 mpv、无 X11**
- 构建：多阶段 Dockerfile（go + node → 运行时），`docker compose build` 一键
- 调试同一性：`docker exec` = 用户浏览器 = 同一容器同一份数据
- Windows/Electron 代码**冻结留仓**（不删、不再投资、CI 线停派工）

## 2. 鉴权（§2 已批）

- `ZGJ_WEB_USER`/`ZGJ_WEB_PASS` 缺失 → 容器拒绝启动（防裸奔）
- `POST /login {user,pass}` → 成功发 session cookie（HttpOnly、随机 32B token、30 天、
  内存态 map）；未登录任意端点 401 → 前端登录页
- WS `/rpc` 握手校验同一 cookie

## 3. 前端平迁策略（§3 已批）

- 只换传输层：`bridge.ts` 的 `callGo` 从 `window.go` 换 WS send/recv；
  函数签名、`contract.ts`、全部调用点不动
- 保留 `window.go` 探测（桌面形态不坏）：window.go 在=桌面桥，否则=WS 桥
- 纯数据页（榜单/库/历史/下载）开箱即通
- 播放页 = 唯一大改（§4）；设置页「更新」项 web 模式隐藏（容器更新=compose pull && up -d）

## 4. 视频播放管线（§4 已批；**2026-11-19 实施侦察后按实际媒体面修正**）

原假设「bilibili DASH 双 m4s」被代码事实推翻：core 的 provider 面输出
**HLS m3u8（47 处）+ 直连 mp4（10 处），无 m4s**；`nativePlan` 实际字段
`{URL, Headers(map), Key(decryptionKey), Quality, Qualities, ...}`。
修正后路线（更简单、更标准）：

- **不再自研双 SourceBuffer**。两种播放器形态，按 resolve 结果自动分流：
  - m3u8 → **hls.js**（标准 MSE 库，Chrome 无原生 HLS 的唯一正解；
    自带 ABR 档选择=天然编码降级、AES-128 解密密钥支持、拖动/缓冲标准行为）
  - mp4 → 原生 `<video src>` 指向代理端点（Referer 由服务端补，浏览器同源无 CORS 问题）
- Go 新增少量绑定：`PlayerWebInfo(drama,chapter,index,quality)` → 复用 core resolve，
  返回 `{url, headers, key, quality, qualities, local}`（即 nativePlan 的 web 投影）；
  现有 PlayerOpen/Seek/Pause 语义保留（web 模式 PlayerOpen 只管解析+状态记录，
  不 spawn mpv——容器里没有 mpv）
- `/video/seg?url=<encoded>` 代理：白名单=provider CDN 域 + /data 本地路径（防 SSRF）；
  透传 plan.Headers（Referer 等）；Range 支持（m3u8 分片/拖动都靠它）；
  本地成片走同端点 `url=data/<path>`
- 编码降级=**hls.js 的 ABR + qualities 列表按 isTypeSupported 过滤**取代原
  「每档 codec 手动过滤」设计（m3u8 的码率档由 demuxer 自读，无需前端猜 codec）
- 风险登记：`nativePlan.Key` 非空的直连 mp4（部分源走解密）需 WebCrypto 分块解密，
  P1 先跑通无 Key 源（jinguo 主线），有 Key 源按实测决定是否做 WebCrypto 路径
- 弹幕：mpv 原生弹幕退役 → 前端 canvas 覆盖层，数据走现有 `DanmakuAll` 绑定，
  速度/密度/区域读现有设置（不变）
- 断点续播=seek（不变）；`PlayerOpenLocal` 语义保留

## 5. 分批评迁 + 验收（§5 已批）

| 批 | 内容 | 验收 |
|---|---|---|
| P0 | sidecar web 模式（静态+WS RPC+鉴权）、Dockerfile+compose、前端传输层切换 | 局域网浏览器登录→首页/榜单/库/历史真实数据流通 |
| P1 | 视频（代理+MSE+编码降级+弹幕覆盖层） | 实机完整看一集：拖动/切档/弹幕/音画同步 |
| P2 | 下载与合并、本地播放、/file | 下载整番→合并→容器内直放+浏览器下载 |
| P3 | 长尾方法、设置 parity、契约覆盖审计（evidence_audit 方向 A/S 指向 WS 面：98 名全可达、方法表单源） | 全 98 可达 + 既有门禁全绿 |
| P4 | 镜像进 CI（Linux 线扩展）、使用文档（更新=compose pull && up -d） | CI 绿 + 文档 |

纪律：每批独立提交、代码+验收同批；既有验证链保留适配（run_all 前端 mock E2E、
sidecar_protocol_test.go、go test）。

### P2 落地状态（2026-11-19，实网验收绿）
- **Go /file 端点**（sidecar_web.go `newLocalFileHandler`）：cookie 鉴权、
  `?p=<容器内绝对路径>` 只放行下载根**之下**的成品（`checkLocalPlayable` 与
  PlayerOpenLocal 同一安全口径，含 `..` 上跳拒）、`http.ServeFile` 原生 Range/
  Content-Type。TestLocalFileEndpoint 钉死 401/400/403(含..)/404/200/206+字节一致。
- **前端 openLocal web 分支**（player.ts）：`/file?p=` 喂同一个 <video>+pending seek，
  不走 playerOpenLocal（会 spawn mpv 失败）；本地标题走 `player.localTitle`。
- 下载/合并/归档/设置面=纯 WS 绑定已天然可用（P0 平迁覆盖）。
- **实网验收**（容器内造 3s testsrc 成片）：curl 全量 200+video/mp4、Range 206+
  Content-Range、**md5 与源文件逐字节一致**（f427ac9e…）、越界 403、无 cookie 401；
  浏览器 `#/player?lp=` 本地成片播放（title=本地播放、dur=3、320px 帧、播完停）。
- 观察项：容器重启会失效内存登录态（webSession 不落盘，设计内强度=内部工具级）。

### P1 落地状态（2026-11-19，实网验收绿）
- **代理**：sidecar_web.go `/video/seg`（cookie 鉴权、loopback-only 防 SSRF、
  Range/If-Range 透传、m3u8 清单分段地址改写）。实网抓出并修掉两个真 bug：
  ①WriteHeader 早于 Set 头 → Content-Range/Length 静默丢失（浏览器时长/seek 全废）；
  ②清单改写后字节数变大但照抄上游 Content-Length → Go 丢弃整包（200+空体）。
  回归测试 TestVideoSegProxy 双断言钉死。
- **前端**：player.ts web 分支（openEpisode 走 Resolve 不 spawn mpv、togglePlay/
  seek/setSpeed/applyVolume 委托 <video>、setWebVideo 元素事件链、bridgeAvailable
  认 WS 桥防演示回落）；PlayerView 挂 <video>+hls.js（m3u8）+DanmakuCanvas 覆盖层
  （DanmakuAll 数据、B 站滚动车道、顶/底固定淡入淡出）；toggleDanmaku/
  setDanmakuOpacity web 分支不下发 mpv ASS。
- **实网验收**（容器 192.168.0.115:9320，hongguo 源直 mp4）：播放 t 24→69 推进、
  dur=96.9 已知、360x640 真实帧、弹幕 canvas 挂载、无演示回落。拖动/切档/音画
  同步留待用户实机反馈（「用起来了就知道哪儿有问题了」）。
- 观察项（非 bug）：hongguo 弹幕上游抽样稀疏（DanmakuAll total=342 但 items=4，
  与桌面 mpv 同源同数据，canvas 忠实渲染）。

## 6. 协议面事实（2026-11-19 读实，实现依据）

- `sidecar_main.go`：`protoRequest{ID *json.RawMessage, Method string, Args []json.RawMessage}`；
  响应行 `{"id":…,"result":string}` / `{"id":…,"error":string}`；事件下行
  `{"event":name,"data":…}`；`__methods` 返回方法名 JSON 数组；`__shutdown` 收尾
- `runStdioSidecar(in io.Reader, rawOut io.Writer) int` 是**可注入管道的纯函数**：
  32 并发槽、respond/事件 sink 全在其内接线 → WS 适配=抽公共 runtime（app+table+appRef+
  slots+fan-out 事件 sink），stdio 与 WS 两个 serve loop 复用
- 反射表：`buildDispatchTable()` 按签名形状分 kind0/1/2（(string,error)/(string)/(error)），
  `protoMethod.call(appRef, args []json.RawMessage)` 单入口
- 版本转发：`updater.EmbeddedVersion = buildVersion` 在 main（web 入口同款需要）
- 前端 `bridge.ts:48`：`window.go?.main?.App` 探测已有降级 local 面——WS 桥插入
  探测链中间（window.go 在=桌面；否则 WS 可连=容器；否则 local mock）
- 依赖：新增 `github.com/gorilla/websocket`（小依赖；仓库依赖树已含 wails/tls-client 全家）

## 7. 风险与已知边界

- MSE 编码覆盖：H.264 保底（bilibili 必给档）；HEVC 依赖浏览器硬件，检测不支持即降级
- 内存态 session：容器重启=全员重登（可接受；v2 桌面形态本来也随进程）
- 代理带宽：单用户局域网场景，不做限流/缓存（YAGNI）
- 登录安全：口令走 env 明文（compose 文件权限 600；文档提醒）——强度=内部工具级
- 既有 30 个 *_verify.py 门禁针对桌面桥；WS 桥的契约审计放 P3（evidence_audit 改指向）
