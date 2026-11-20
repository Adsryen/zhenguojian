# zgj-mcp —— 真果鉴 v3 容器 MCP server（AI 接入面）

让 AI（DSH / Claude / Codex 等任何 MCP 客户端）驱动 115 上跑的 `zgj-v3` 容器完成
**搜剧 → 看分集 → 下载 → 等完成 → 合并 → 等完成 → 拿成品交接地址** 全链。
上传侧（另一个 MCP 或脚本）消费 `zgj_outputs` 给的地址；本 server 不碰上传。

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

## 工具（11 个，命名空间 `mcp__zgj__*`）

| 工具 | 作用 |
|---|---|
| `zgj_sources` | 列出视频源（slug/名称/搜索形态/启用态） |
| `zgj_search` | 关键词搜剧，返回 drama 对象列表（**原样透传未裁剪**，可直接喂下一步） |
| `zgj_rankings` | 榜单找剧（默认 hongguo-hot） |
| `zgj_detail` | 查一部剧的分集列表（集号 1 基） |
| `zgj_download` | 下载指定集（episodes=集号数组，quality 默认 360） |
| `zgj_downloads` | 下载/合并任务快照（不等待） |
| `zgj_wait_downloads` | 阻塞等所有下载任务到终态（默认 10min，超时返回可续等） |
| `zgj_merge` | 合并已完成分集成整剧（1 部=按季；多部=批量按季） |
| `zgj_wait_merges` | 阻塞等合并到终态（可按 dramaId 过滤） |
| `zgj_outputs` | **交接面**：成品 fileUrl（主）+ hostPath（辅）+ 字节数 |
| `zgj_cancel` | 取消下载/合并任务 |

## 推荐链路（server instructions 里也写了）

```
zgj_search "穿越八零"          → items[0] = drama 对象
zgj_detail  {drama}           → 分集号
zgj_download {drama, episodes:[1,2,3]}
zgj_wait_downloads
zgj_merge {dramas:[drama]}
zgj_wait_merges
zgj_outputs                   → merged[].fileUrl 交给上传侧
```

## 交接契约（给上传侧）

`zgj_outputs` 对每个成品给三种形态，**推荐用 fileUrl**：

- `fileUrl`（主）：`http://<容器>/file?p=<容器内路径>`（URL 编码）。需先
  `POST /login {user,pass}` 拿 `zgj_session` cookie，再带 cookie GET；支持
  Range/断点续传。**为什么主形态是 URL**：容器以 root 跑，产物 root 属主、
  数据树 0700——host 上的 AI（ryen uid）直接读 hostPath 会被 Permission denied
  （2026-11-19 真实链实测撞到的交接断点）。fileUrl 由容器内 root 进程出流
  （下载根白名单+原生 Range，P2 已验证），天然跨属主边界。
- `hostPath`（辅）：host 挂载点下的真实路径，仅 root/sudo 上下文可直读。
- `containerPath`：容器内路径（调试/排障用）。

上传侧示例（Node ≥18 内置 fetch）：

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
- 写面暴露登记：`zgj_download / zgj_merge / zgj_cancel` 经公网网关透出，
  受容器下载管线自身约束（集数上限 500、磁盘检查、白名单端点）；如需收敛，
  在 mcphub 条目加 `tools` 覆写（`enabled:false`）或改用限权 key/组端点。

### 方式 B：stdio 直连（任意 MCP 客户端）

`command` 指向上面构建的二进制，注入 stdio 那组 env（无需 LISTEN/TOKEN）。
工具以 `zgj_*` 原名出现。

## 安全边界

- **HTTP 模式鉴权**：`Authorization: Bearer <ZGJ_MCP_TOKEN>`，恒定时间比较；缺失/不符=401。
  `/healthz` 是唯一免鉴权端点（仅探活，不返回敏感信息）。token 只存 `.env`（0600，gitignore）。
- 凭据=容器 Web 面同一套（内存 session，容器重启失效）；server 本身不落盘任何状态。
- 本 server **只暴露 11 个受控工具**（不转发任意桥方法）——写面只有
  Download*/Merge*/Cancel 这一族，全部受容器下载管线自身的约束（集数上限 500、
  磁盘检查、白名单端点）。
- wait 类工具有超时上限（下载 28min / 合并 55min），不会无限挂起 MCP 调用。

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
