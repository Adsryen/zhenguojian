# zgj-mcp —— 真果鉴 v3 容器 MCP server（AI 接入面）

让 AI（DSH / Claude / Codex 等任何 MCP 客户端）驱动 115 上跑的 `zgj-v3` 容器完成
**搜剧 → 看分集 → 下载 → 等完成 → 分块合并 → 等完成 → 自动推送 115 网盘** 全程无人值守。
上传通道（115）由本 server 内部经 openlist 网关（`ZGJ_OL_*`）解决，调用方**不需要**
自己拿 cookie/登录、也不需要自己 POST 文件——`zgj_publish` 一次搞定。
`zgj_outputs` 仍是人工/脚本交接面（fileUrl + hostPath）。

## 架构

```
AI (MCP client)                本 server (stdio JSON-RPC)              zgj-v3 容器
─────────────  stdio  ────────>  client-v2/mcp (Go 静态二进制)  ──WS──>  /rpc 桥面(89 方法)
                                   登录 /login 拿 cookie              （与 Web 前端同一面）
```

- **两种传输**（同一协议核心 `dispatch()`，行为一致）：
  - **stdio**：JSON-RPC 2.0 newline-delimited。stdout 只发协议帧，日志走 stderr。
  - **HTTP（streamable-http）**：`ZGJ_MCP_LISTEN` 非空即启用（现网 `0.0.0.0:9330`），
    `Authorization: Bearer <ZGJ_MCP_TOKEN>` 恒定时间鉴权（缺失/不符=401）。
    `POST /mcp` 无状态单端点：响应跟随 `Accept`（json→JSON；含 event-stream→SSE 单事件）；
    通知=202；GET=405；`GET /healthz` 探活（无需 token）；不设 WriteTimeout（wait 最长 55min）。
- **后端**：容器 sidecar 的 `/rpc` WebSocket（与浏览器前端同一桥面、同一鉴权、同一 89 方法），
  参数编码与 sidecar 反射解码侧严格对齐（string 形参=JSON 字符串，文档型参数双重编码，
  int/bool=字面量——P3 已执行级验证过 89/89）。
- **断连自愈**：容器重启会作废内存 cookie → server 自动重登录、重连、重放一次调用。

## 构建

```bash
cd /home/ryen/Git/zhenguojian/client-v2    # 模块根在 client-v2/（仓库根无 go.mod）
CGO_ENABLED=0 go build -o build/bin/zgj-mcp ./mcp
```

（`client-v2/build/bin/` 已 gitignore；产物 ~9.9MB 纯静态，无 cgo 依赖。）

## 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `ZGJ_WEB_URL` | `http://127.0.0.1:9320` | 容器 HTTP 地址（MCP server 与容器同机时可默认） |
| `ZGJ_WEB_USER` | —（必填） | 登录账号（= 容器 `.env` 的 ZGJ_WEB_USER） |
| `ZGJ_WEB_PASS` | —（必填） | 登录密码（= 容器 `.env` 的 ZGJ_WEB_PASS） |
| `ZGJ_DATA_HOST_DIR` | `/home/ryen/Git/zhenguojian/client-v2/data` | 容器 `/data` 在 host 的挂载点（hostPath 换算用） |
| `ZGJ_MCP_POLL_MS` | `3000` | wait 类工具轮询间隔 |
| `ZGJ_MCP_LISTEN` | —（空=stdio 模式） | 非空即启用 HTTP 模式并监听该地址，如 `0.0.0.0:9330` |
| `ZGJ_MCP_TOKEN` | —（HTTP 模式必填） | Bearer 鉴权令牌；缺失时 HTTP 模式启动即 fatal |
| `ZGJ_OL_BASE` | — | openlist（115 推送通道）地址，如 `https://alist.prlrr.com`；**三个 OL 变量齐备才启用直推**，否则 `zgj_publish` 回一次性签名 URL 供上游自传 |
| `ZGJ_OL_USER` / `ZGJ_OL_PASS` | — | openlist 登录账号（`POST /api/auth/login`，token 48h 自动续期） |
| `ZGJ_PUBLIC_URL` | 回落 `ZGJ_WEB_URL` | 对外可达的容器基地址（签名 URL 兜底给外部调用方时用；`127.0.0.1` 时调用方需自行改 host） |

## 工具（14 个，命名空间 `mcp__zgj__*`）

| 工具 | 作用 |
|---|---|
| `zgj_sources` | 列出视频源（slug/名称/搜索形态/启用态） |
| `zgj_search` | 关键词搜剧，返回 drama 对象列表（**原样透传未裁剪**，可直接喂下一步） |
| `zgj_rankings` | 榜单找剧（默认 hongguo-hot） |
| `zgj_detail` | 查一部剧的分集列表（集号 1 基） |
| `zgj_download` | 下载指定集（episodes=集号数组，quality 默认 360；drama 接受完整对象**或 id-only 字符串**如 `hongguo:7675932141238111256`，服务端自查标题/分集，空标题拒绝入队） |
| `zgj_downloads` | 下载/合并任务快照（不等待）。**默认摘要**：total + stateCounts + id 清单 + 前 5 条（防网关截断）；`full:true` 全量；`dramaId`/`jobIds` 精过滤 |
| `zgj_wait_downloads` | 增量等待（**默认 45s**、上限 120s 硬限——mcphub 阻塞 ~60s 掐线）。`dramaId`/`jobIds` 过滤；超时回 `timedOut:true` + 同口径快照，调用方循环再调 |
| `zgj_merge` | 合并已完成分集。单部=按季；`chunkSize`（如 50）=**分块合并**，每块一个成品（超长剧整部 mkv >10GB 难传难播）。drama 同样接受 id-only |
| `zgj_wait_merges` | 增量等待合并（默认 45s/上限 120s），`dramaId` 过滤 |
| `zgj_outputs` | **交接面**：成品 fileUrl（主）+ hostPath（辅）+ 字节数。`dramaId`/`jobIds` 过滤 + 同路径去重；`includeEpisodes:true` 附分集成品 |
| `zgj_cancel` | 取消下载/合并任务 |
| `zgj_publish` | **推送 115（无人值守终点）**：`files` 缺省=该剧最新合并成品，`targetDir`+`fileNameTemplate`（默认「{title} 合并版 {E起}-{E止}.{ext}」，`{ext}` 不带点）+ `deleteLocalAfterSuccess`。openlist 齐备=server 内部流式直推（115 root 出流→PUT openlist→回读 size 校验），**全程无需调用方登录**；否则回一次性签名 URL（单次有效+Range+sha1+bytes）。115 重授权失效时返回带指引的死信错误 |
| `zgj_cleanup` | 按剧清理：`episodes:true` 删分集缓存（DownloadRemoveBatch）；缺省=清旧合并成品（同剧被新区段**严格包含**的旧区间文件，幂等覆盖产生的累积垃圾） |
| `zgj_retry` | 复活 failed 下载（per-job `DownloadRetry`；限流类失败等窗口后重试，**禁止 blanket**） |

## 推荐链路（无人值守，server instructions 里也写了）

```
zgj_search "逆江湖"              → items[0] = drama 对象（或直接用 id-only 字符串）
zgj_detail  {drama}             → 分集号
zgj_download {drama, episodes:[1..N], quality:1080}
zgj_wait_downloads {dramaId, timeoutSec:45}   → 循环调直到 timedOut=false
zgj_merge      {dramas:[dramaId], chunkSize:50}
zgj_wait_merges {dramaId, timeoutSec:45}      → 循环调
zgj_publish    {dramaId, targetDir:"115/逆江湖之苍生为局"}
              → 115 文件清单（remote/size/sha1），推送完 zgj_cleanup 清分集缓存
```

**质量自查**：`quality` 请求值未必存在——源没有该档位时核心取最高流，
`zgj_downloads` 的 `actualQuality` 才是真实流画质（2026-11-20 实锤：hongguo
《逆江湖》请求 1080 → actual 360，成品 ffprobe 360p，job/记录/成品三处一致，
=源限制非透传 bug）。

## 推送通道（zgj_publish）

- **直推（openlist 齐备）**：server 内部 `FileSign` 一次性签名 URL → 容器 root 取流
  → `PUT /api/fs/put` 流式上传（4h 超时、不落盘）→ `fs/list` 回读 size 校验。
  调用方拿不到也不需要任何凭据。
- **兜底（未配 openlist）**：回 `signedUrl`（`/file-signed?token=`，单次有效、
  TTL≤1h、Range 支持、基址取 `ZGJ_PUBLIC_URL`）+ 目标名 + bytes，由上游 agent 自传。
- **115 重授权失效**（openlist `/115` 挂 refresh_token）：直推返回 isError 死信 +
  「到 https://alist.prlrr.com 登录 → 存储 → 115 重新授权」指引（token 型凭证无法
  程序化续期）。工作存储（/local、/Onedrive-*、/又拍云…）不受影响，可先推过去。

## 交接契约（给上传侧，zgj_outputs）

`zgj_outputs` 对每个成品给三种形态，**推荐用 fileUrl**：

- `fileUrl`（主）：`http://<容器>/file?p=<容器内路径>`（URL 编码）。需先
  `POST /login {user,pass}` 拿 `zgj_session` cookie，再带 cookie GET；支持
  Range/断点续传。**为什么主形态是 URL**：容器以 root 跑，产物 root 属主、
  数据树 0700——host 上的 AI（ryen uid）直接读 hostPath 会被 Permission denied
  （2026-11-19 真实链实测撞到的交接断点）。fileUrl 由容器内 root 进程出流
  （下载根白名单+原生 Range，P2 已验证），天然跨属主边界。
- `hostPath`（辅）：host 挂载点下的真实路径，仅 root/sudo 上下文可直读。
- `containerPath`：容器内路径（调试/排障用）。

上传侧示例（Node ≥18 内置 fetch，仅在不用 `zgj_publish` 直推时）：

```js
const jar = await fetch('http://127.0.0.1:9320/login', {method:'POST',
  headers:{'content-type':'application/json'},
  body: JSON.stringify({user:'ryen', pass:'zgj-web-115'})})
  .then(r => r.headers.getSetCookie()[0].split(';')[0])
  .then(c => c.split('=')[1]);
const file = await fetch(fileUrl, {headers:{cookie:'zgj_session='+jar}})
  .then(r => r.arrayBuffer()); // 19MB 整包，或带 Range 分片
```

## 接入方式

### 方式 A：挂到 mcphub 网关（现网已接入，2026-11-20）

zgj-mcp 以 HTTP 模式常驻 115（systemd user 服务 `zgj-mcp.service`，
EnvironmentFile=`client-v2/mcp/.env`，0600），mcphub（224）作为上游挂载：

- 条目（`/opt/mcphub/data/mcp_settings.json` 的 `mcpServers.zgj`）：
  `streamable-http` + `http://192.168.0.115:9330/mcp` + `Authorization: Bearer <token>`
  （token 与 `.env` 的 `ZGJ_MCP_TOKEN` 同一值，48 位 hex）。
- SSRF 闸：`systemConfig.network.allowedCidrs` 已追加 `192.168.0.115/32`
  （wg 隧道可达，容器内实测 healthz 200）。
- 下游通路：`https://mcphub.prlrr.com/mcp`（ops-client key，现值在 224
  `client_key.once`），工具以 **`zgj-zgj_*`**（网关 `<serverName>-<tool>` 前缀）出现；
  DSH 侧经现有 `mcp-mcphub` 条目即见，无需单独注册。
- 写面暴露登记：`zgj_download / zgj_merge / zgj_cancel / zgj_publish / zgj_cleanup`
  经公网网关透出。download/merge/cancel 受容器下载管线自身约束（集数上限 500、
  磁盘检查、白名单端点）；**publish/cleanup 是 115 网盘写面**（openlist 直推+
  本地删除），openlist 凭据只在 server 内部（`ZGJ_OL_*` 0600），调用方拿不到；
  如需收敛，在 mcphub 条目加 `tools` 覆写（`enabled:false`）或改用限权 key/组端点。

### 方式 B：stdio 直连（任意 MCP 客户端）

`command` 指向上面构建的二进制，注入 stdio 那组 env（无需 LISTEN/TOKEN）。
工具以 `zgj_*` 原名出现。

## 安全边界

- **HTTP 模式鉴权**：`Authorization: Bearer <ZGJ_MCP_TOKEN>`，恒定时间比较；缺失/不符=401。
  `/healthz` 是唯一免鉴权端点（仅探活，不返回敏感信息）。token 只存 `.env`（0600，gitignore）。
- 凭据=容器 Web 面同一套（内存 session，容器重启失效）；server 本身不落盘任何状态。
- 本 server **只暴露 14 个受控工具**（不转发任意桥方法）——写面=Download*/Merge*/
  Cancel/Purge/Cleanup 一族 + openlist 直推，全部受容器下载管线自身约束（集数上限
  500、磁盘检查、白名单端点）+ openlist 凭据内部隔离。
- wait 类工具有超时上限（**下载/合并默认 45s、上限 120s** 硬限——mcphub 阻塞 ~60s
  掐线），不会无限挂起 MCP 调用。
- 大列表默认摘要（total + stateCounts + id 清单 + 前 5 条），全量 `full:true`——
  防 mcphub 网关对 >250KB 响应截断/压缩。

## 验收记录（2026-11-20 无人值守 115 推送链，m05712 七项）

- **14 工具全链实网**：`tools/list`=14；③ id-only（`dramas:["hongguo:7675932141238111256"]`
  直接入队）；② 过滤+增量（`zgj_downloads {dramaId}` 只回本剧、`zgj_wait_downloads
  {timeoutSec:8}` 实测 9.1s 回 `timedOut:true`+快照、`zgj_wait_merges {dramaId}` 快照
  不再混入别的剧、`zgj_outputs {dramaId}` 过滤+同路径去重）；⑤ chunkSize（5 集
  chunk=2 → 两块 [1,2][3,4,5]，**尾组 <2 集并入上一组**——核心 runMerge 要求每组
  ≥2 连续集，首版尾组单集被拒是真 bug，已修）；⑦ 摘要形态（head 5 条+total+
  stateCounts+ids）。
- **① publish 全链**：→ `/local/zgj-pub-test/pub-test.mp4` channel=openlist
  size=12464168 与源一致 + openlist 独立登录回读落盘确认；→ `/115` 死信 isError +
  「refresh_token 已失效…到 alist.prlrr.com 重授权」指引（**115 重授权是唯一外部
  阻塞**，224 openlist x_storages id=24 的 refresh_token 死，token 型无法程序化续期）。
- **④ 幂等+清理**：同区段重合并=覆盖同名成品（moveFile 同盘 rename）；
  `zgj_cleanup` 实弹删被新区段严格包含的旧文件（「逆江湖 全003-004」被 003-005 包含
  → 删，保留 001-002/003-005），穿越八零旧成品（001-003 被 001-004 包含）同样清掉。
- **⑥ 质量三处一致**：请求 1080 → job.actualQuality=360 → 容器 ffprobe 成品 360p →
  job 记录 360。结论=hongguo 源只给 360p（nativePlaybackChoices 未命中取最高流），
  非透传 bug。源限流（hongguoduanju.com 403「N 秒后重试」）→ downloadConcurrency=1
  + `zgj_retry` 复活 failed。
- **openlist API 真坑（源码 /tmp/olx v4.2.9 实锤）**：① Authorization 头要**裸 token**
  （无 `Bearer ` 前缀——v4 中间件拿整头当内存缓存 key，带前缀必 401「token is
  invalidated」假象）；② 上传=`PUT /api/fs/put`（File-Path/X-File-Size 头+原始字节流，
  不是 multipart `/api/fs/upload`——不存在的路由落 SPA HTML 200）；③ 删除=
  `POST /api/fs/remove {dir,names}`（不是 paths）；④ mkdir 非递归逐级建。
- 部署坑：`cp build/bin/zgj-mcp` 报 Text file busy（服务在跑）→ `systemctl --user
  stop`→cp→start；115 的 /tmp 反复被清（脚本/二进制丢了需重 build）。

## 验收记录（2026-11-20 HTTP 模式 + mcphub 接入）

- **本机 HTTP 鉴权矩阵**：`/healthz` 200 `{ok,mode:http}`；`POST /mcp` 无 token→401、
  错 token→401、GET→405（Allow: POST, OPTIONS）、通知→202；带 token initialize
  （协议回声 2024-11-05）/tools/list=11/zgj_sources 真数据 15 源；Accept 含 SSE→
  `event: message` 单事件；HTTP 模式缺 `ZGJ_MCP_TOKEN` env→启动即 fatal。
- **mcphub 上游连上**：`Successfully connected client for server: zgj` +
  `Successfully listed 11 tools for server: zgj`。
- **公网网关全链**：`https://mcphub.prlrr.com/mcp`（ops-client key）→ initialize
  （serverInfo mcphub 1.2.4）→ tools/list 含 11 个 `zgj-zgj_*` → 实调
  `zgj-zgj_sources`（15 源）/`zgj-zgj_search "穿越"`（15 条真数据）全绿。
- **排障留痕（两真 bug）**：①网关 401「version negotiation failed」——根因是
  224 `mcp_settings.json` 里的 zgj Bearer 被截成 44 位（真值 48 位），加临时
  请求头日志（`auth_len=51`）定位后按 48 位重填即连上，日志随后移除；
  ②公网 `tools/list` 400「No valid session ID」——mcphub 网关 streamable-http
  有状态，须先 `initialize` 取 `mcp-session-id` 头、发 `notifications/initialized`，
  后续请求带该头。
- **现网漂移（已修）**：profile `mcp-mcphub` 的 Bearer key 被 224 轮换
  （mcphub_0913…→mcphub_1a677…，与 `client_key.once` 的 sha256 吻合），
  已把 profile 刷成现值（备份 `cordis.patch.yml.bak-mcphubkey-*`）。

## 验收记录（2026-11-19 实网）

- stdio 会话：initialize（协议回声 2024-11-05 + capabilities.tools）→ tools/list=11 →
  zgj_sources / zgj_search("穿越") 真数据。
- 全链真实跑通：search→detail(317 集)→download(2,3 集, added:2)→wait(3 集全 completed,
  5.9/6.8MB)→merge(queued:1)→wait(state=done, 19087993B)→outputs(fileUrl+hostPath)。
- 交接实锤：fileUrl GET 合并 mkv → 206 Partial Content + `Content-Range: bytes 0-15/19087993`
  + `video/x-matroska` + 整包 19087993B + MKV magic 1A45DFA3。
- 发现的交接断点（已设计规避）：hostPath 直读 Permission denied（root 属主 0700 树）
  → 主交接形态改 fileUrl，hostPath 降级为 root/sudo 便捷通道。
