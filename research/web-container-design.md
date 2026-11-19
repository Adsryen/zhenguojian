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

### P4 落地状态（2026-11-19）
- **CI 门禁**（.github/workflows/v3-docker-image.yml）：push 到 docker 配置/
  sidecar web 前端/core overlay 触发；干净 ubuntu-latest 上 docker build 三阶段镜像
  →起容器→`client-v2/docker/ci-acceptance.js` 14 项判据（零外网依赖：HTTP 鉴权面
  6 项、WS 桥面 3 项含 __methods 必含 40 核心绑定子集的漂移检测、只读真调用 5 项含
  GetBuildInfo 版本=构建参数 -ldflags 注入实锤）。CI 版本 ci-<run> 经 compose 同款
  --build-arg 注入。本地容器实跑 14/14 绿后提交。
- **使用文档**（client-v2/docker/README.md）：部署/更新（compose pull 语义=
  up -d --build）/凭据轮换（内存 session 重启失效=设计内）/端口网络/排障表/
  web vs 桌面形态对照。
- **健康检查真 bug（发现+修复）**：首版 HEALTHCHECK 用 bash 的 /dev/tcp，但
  debian:bookworm-slim 的 /bin/sh 是 dash → `cannot create /dev/tcp/127.0.0.1/8080:
  Directory nonexistent`，容器恒 unhealthy（FailingStreak 344）而服务其实好的。
  CI 首跑（run 44524390911）没抓到——ci-acceptance 探 HTTP 端点不探 docker 健康态
  →补 `docker inspect State.Health.Status==healthy` 断言防回归。修复=**静态 Go
  健康探针** client-v2/docker/healthcheck/（独立 go module、stdlib-only，go 阶段
  同编→COPY /opt/zgj/healthcheck→HEALTHCHECK 指向它，零新 apt 包零外网依赖）。
  中途方案「往运行时加 curl」弃用：废 stage-3 apt 层缓存+探针引外网依赖，
  115 deb 源不稳时构建挂 25 分钟。验收：探针二进制对活服务 exit 0/坏端口 exit 1；
  容器重建后 docker-health 5s 内转 healthy。
- **实机使用面两真 bug（已修复，player.ts）**：
  ① **本地路径分流**：下载过整集后再播该集，核心 Resolve 直接回**本地文件路径**
  （/data/appdata/downloads/<jobId>/media.mp4）而非 loopback 流 URL——web
  openEpisode 若盲目 /video/seg?url= 会 403（loopback 白名单认不出绝对路径）。
  修=按形状分流 `plan.url.startsWith('/')` → `/file?p=`（下载根白名单+Range，与
  openLocal 同一端点），http(s) → `/video/seg?url=`。桌面 mpv 两种都吃故无感。
  实网验收：下载过那集浏览器播 src=/file?p=%2Fdata%2F...、rs=4、dur=96.9、360px。
  ② **play/pause 状态漂移**：自动播放被浏览器策略拦时（新会话无用户手势），
  openEpisode 的 el.play() reject 但元素保持暂停——store.paused 不回灌则 store
  认为在播、用户再点一次反而走 pause 分支空转。修=setWebVideo 挂
  `play`/`pause` 元素事件回灌 store.paused（pause 用 el.ended 挡掉 ended 前那次）。
  实网验收：新会话登录后点视频 t=30.7 paused=false 正常播。
- P0-P4 全部交付，CI 首跑绿（44524390911），健康检查真 bug 修复绿（44537851948）。

### P3 落地状态（2026-11-19，审计全绿）
P3 无新代码（长尾/设置/契约都是"把已平迁的 WS 面证到证据级"），产出是三条审计：
- **执行级可达性 89/89**（sweep2.js，零副作用）：反射解码 arity 检查先于逐参
  json.Unmarshal（sidecar_main.go:112-114）→ 送 N+1 参必在参数解码前报
  「需要 N 个参数，收到 N+1」，方法体不执行。全表 89 名逐一探针 + 16 只读方法
  （Ping/GetBuildInfo/DataPath/ListSources/RankingBoards/DownloadList/DownloadSettings/
  GetResourceSettings/MergeList/HistoryList/Watchlist/SeenList/DownloadDirectory/
  UpdateStatus/UpdateSettingsGet/PlayerState）真调用全回真数据。
- **契约三向对账**（go/ast dump 单一真源 /tmp/dump_table.go）：源码导出 90 中
  OpenDownloadPath 双定义（_other.go `//go:build !windows` + _windows.go）→ unique 89
  = WS 活表 __methods 89 精确相等；前端 contract.ts GO_BINDINGS 引用 77 unique App.*
  全在表内；表内未前端引用 12 个=桌面形态专属（PlayerAttach/SetBounds/HitBands/
  ForceWindow/Detach/EmbedDiag/Clipboard*/BrowseFiltered/DanmakuWindow/MergeList/
  TrayStatus），WS 面照样可达（容器里前端不调而已）。
- **evidence_audit 方向 A/S/G 全 PASS**（bridge 面 89、contract_not_in_sidecar 0、
  内嵌签名 5 条一致）；方向 B 只登记不判红：BrowseFiltered/MergeList/TrayStatus。
- **设置 parity 回环**：资源/下载/更新三组 get→set→get 值全部不变（容器真数据面）。
- **下载→容器内播真链**（P2 验收尾）：WS DownloadStart 1 集→completed（6425563B
  media.mp4 落盘 downloads/<jobId>/）→/file 流回（md5 e5cc2178… 与源逐字节一致、
  206+Content-Range）→浏览器 #/player?lp= 播放（dur=96.9、360px、readyState=4）。
- **审计事故留痕**：首版 sweep 用"类型对撞"探针，对 string 首参送了可解码 JSON
  字符串→7 方法带假 id "1" 真实执行（ToggleSeen/RemoveFollow/RemoveHistory/
  MergeCancel/CancelReservation/ClipboardWrite/Suggest）。已清理唯一真污染
  （seen-library.json 假 "1" 键已删；其余 6 个 no-op 或只读）。教训：执行级探针
  必须用 arity 不匹配（解码前拦截），类型对撞在"形参能吞下该类型"时照样执行。
  重设计版 sweep2.js 已钉住零副作用口径。

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

## 8. MCP 接入面（2026-11-19，用户后续 AI 接入需求）

> 用户诉求（m04436）：「准备 mcp，后续接入 AI，搜索视频、下载、合并。然后将合并的资源，
> 可以通过另外的 mcp 或方式，上传到别的地方去。」→ 拆成两半：**本 server 只做
> 搜索→下载→合并→取成品交接地址**；上传侧是**另一个**独立 MCP/脚本，消费交接地址，
> 不在本 server 职责内（本 server 不碰上传）。

### 8.1 形态：stdio 单二进制 + WS 桥

- `client-v2/mcp/main.go`（独立 `main` package，stdlib-only，CGO_ENABLED=0 → 9.3MB 纯静态，
  `client-v2/build/bin/zgj-mcp`，build/bin 已 gitignore）。
- **stdio JSON-RPC 2.0**（MCP 协议，newline-delimited）；stdout 只发协议帧，日志 stderr。
- 后端复用容器 sidecar 的 **`/rpc` WebSocket**（与浏览器前端同一桥面、同一鉴权、同一
  89 方法）：POST `/login` 拿 `zgj_session` cookie → WS `ws://<host>/rpc`，信封
  `{"id","method","args":[raw...]}`，参数编码与 sidecar 反射解码严格对齐（P3 已执行级
  验证 89/89）。
- **断连自愈**：容器重启作废内存 cookie → 自动重登录+重连+重放一次调用（MCP 调用可
  安全重试，因为 sidecar 侧是无状态桥转发）。
- 协议协商：initialize 回显客户端 protocolVersion（兼容 2024-11-05 / 2026-07-28，实测
  DSH 选 2026-07-28 正常握手）。

### 8.2 工具面（11 个，`mcp__zgj__*`）

zgj_sources / zgj_search / zgj_rankings / zgj_detail / zgj_download / zgj_downloads /
zgj_wait_downloads / zgj_merge / zgj_wait_merges / zgj_outputs / zgj_cancel。
**只暴露受控 11 工具**（不转发任意桥方法），写面仅 Download*/Merge*/Cancel 一族，受
容器下载管线自身约束（≤500 集/次、磁盘检查、白名单端点）。wait 类有超时上限
（下载 28min / 合并 55min），不无限挂起。

### 8.3 关键设计决策（实现中实锤）

1. **drama 对象原样透传，不裁剪**：搜索/榜单结果直接喂 Detail/DownloadStart/MergeStart。
   裁剪（brief）会丢 sourceId/metadataSchema 等 core 管线可能读的字段 → 部分源静默失败。
   宁可输出大，也要保证「搜索结果直接下一步」零摩擦。
2. **交接主形态 = 容器 `/file` URL（fileUrl），hostPath 降级为辅**：容器以 **root** 跑，
   合并产物 **root 属主**、数据树 **0700**（data/appdata 与 downloads 均 drwx------）→
   host 上的 AI（ryen uid 1001）直读 hostPath 会 **Permission denied**（真实链实测撞点）。
   `/file` 端点由容器内 root 进程出流（下载根白名单+原生 Range，P2 已验证）→ 天然跨
   属主边界，是可靠交接面。fileUrl 需先 POST /login 拿 cookie 再 GET（支持 Range）。
   `/file` 已实测：合并 mkv 206 Partial Content + `Content-Range: bytes 0-15/19087993` +
   `video/x-matroska` + 整包 19087993B + MKV magic 1A45DFA3。
3. **job 终态 = `completed`**（不是 done）；merge 终态 = `done`；merges 空时 MergeList
   回 raw `"null"`（解析要容忍）。

### 8.4 实网验收（2026-11-19）

- stdio 会话：initialize（2026-07-28 回显 + capabilities.tools）→ tools/list=11 →
  sources/search/rankings/detail 真数据。
- **全链真实跑通**（~9min）：search「穿越八零」→ hongguo:7705824595416190014（317 集）→
  detail → download [2,3]（added:2，ep1 已在前次跑过）→ wait（3 job 全 completed：
  ep1 6425563B / ep2 5887709B / ep3 6830634B）→ merge（queued:1）→ wait（state=done，
  19087993B）→ outputs（fileUrl + hostPath + 3 集分片）。
- 推荐链路写进 initialize 的 server instructions，AI 拿到即用。

### 8.5 注册（DSH profile）

`/home/ryen/.dsh/profiles/web/cordis.patch.yml` insert 段加 stdio 条目
（serverName=zgj，command 指 build/bin/zgj-mcp，env 注入 ZGJ_WEB_URL/USER/PASS，
toolCallTimeoutMs=3600000 覆盖 55min 合并等待，failOnStartupError=false）。重启 DSH
会话后工具以 `mcp__zgj__*` 出现。其他 MCP 客户端（Claude/Codex）同构：stdio + command + env。
详见 `client-v2/mcp/README.md`。
