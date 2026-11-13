# v2 真机待验清单（本次会话的增量）

> 对应范围：`a1a1c89`（工作起点）→ 当前 `master`，共 **106 笔提交**（这个数字是本轮用 `git rev-list --count a1a1c89..HEAD` 现测的；
> 上一版这里写 50，是二十多轮前留下的，已经过期。）。
> 这里只收**必须在打包后的 Windows 上才能验**的条目。
> 本机已经验过的部分列在第 6 节，**不必重复劳动**。
>
> 逐项按「✔ / ✘ + 一句现象」记录即可；✘ 的条目请按第 5 节抓现场，比截图更有用的是文本。

## 0. 前置

- [ ] 用 `build-v2` 的一次 workflow_dispatch 产物即可（不发 Release）：Actions → build-v2 → Run workflow →
      下载 artifact `v2-windows-x64`，解压后 `runtime\`（mpv/ffmpeg/ffprobe）与 `client-v2.exe` 同级
- [ ] `设置 → 关于`：版本号应显示 `v0.0.0-dev`（dispatch 构建的既定值，不是 bug）；打包提交非「未知」
- [ ] 记录一句 WebView2 Runtime 版本（`设置 → 关于` 或 `mrm.exe`/注册表均可）——
      第 1 节的保存弹框行为依赖它

## 1. 详情页：复制简介 / 保存海报（R5a、R5b）

- [ ] 详情页点「复制简介」→ 提示成功，粘贴板内容 = 简介原文（长文不截断、换行保留）
- [ ] 若提示「复制失败，请手动选择」→ 属降级路径（`App.ClipboardWrite` 也失败），记下现象即可
- [ ] 点「↓ 高清海报」（详情页按钮原文，`detail.savePoster`）：
  - [ ] 应弹**系统另存为**对话框（`showSaveFilePicker`）；文件名预填 `<剧名> 海报.jpg`
  - [ ] 选目录后确实落盘，文件可打开；体积明显大于列表小图（走的是 `~tplv-shrink:1600:0.image` 原图）
  - [ ] 取消对话框 → 应出现「已取消保存海报」（`detail.posterCancelled`），
        **不能**出现「海报已保存（…）」——那是把取消当成成功的老毛病
  - [ ] 成功提示原文是「海报已保存（{size}）」（`detail.posterSaved`），size 要带真实大小
- [ ] 二次验证 `<a download>` 回落：用 Ctrl 方式或临时禁用 picker（可在 DevTools 里
      `Object.defineProperty(window,'showSaveFilePicker',{value:undefined})`）后再点保存，
      确认文件落到**默认下载目录**且文件名正确
- [ ] 失败态：断网后点保存，应出现「读取海报失败（HTTP …）」而不是静默

## 2. 播放页：弹幕透明度（R4）

- [ ] 控制条上弹幕开关旁出现「透明 100%」按钮（默认不透明）
      ⚠ 数字是**不透明度**（v1 历史口径：100=完全不透明，往上更实、往下更透）；
      觉得文案反直觉是 v1 遗留问题，改动要单独决策，不要当 bug 顺手改
- [ ] 点开是竖向小浮层 + 滑条（20–100）。**竖向是否在真机成立要看这里**：
      无头 Chromium 验过值与落盘，样式方向依赖 WebView2 的 `writing-mode` 支持
- [ ] 拖到 40% → 弹幕视觉上更透（不需要精确值，能看出变化即可）
- [ ] 关掉播放页重开：按钮仍显示上次的百分比（`localStorage.zgj.player.danmakuOpacity`）
- [ ] 关弹幕再开：透明度设置**仍然生效**（这条走的是「开启时回灌壳层」的新代码路径，本机 mock 验过逻辑）

## 3. 季末下一季入口（R6）

- [ ] 找一部有第二季的剧，看到**本季最后一集播完**（不是第 30 集那种中间集）
- [ ] 播完后控制条出现「去看 《剧名》 第二季 ▸」（不是自动跳播）
- [ ] 点它 → 进的是第二季详情页（另一部剧的 id）
- [ ] 追剧列表：该剧（旧季）条目此时应带「有新一季」类标记；双击/「去观看」直接进下一季
- [ ] 未追剧的剧播完：仍给入口，但**不会**在追剧表里凭空多出一行（v1 语义，本机已断言）

## 4. `client-v2.exe --selftest` 首跑（R7）

命令行执行，别双击：

```
client-v2.exe --selftest
```

预期输出结构（**首次跑，第 7/9 步请预期可能红**——它们在 CI 里只编译过、从没执行过）：

- [ ] 逐行 `[selftest] <步骤名> OK|FAIL|SKIP …`，顺序固定为
      `resources library discover search detail preload download decrypt merge`
- [ ] `decrypt` 必须是 `SKIP`，理由里带「不适用」——这是架构差异，不是欠账
- [ ] 末行有 `[selftest] PENDING 1 step(s) not implemented yet: decrypt`
- [ ] 有 `[selftest] report -> <exe 目录>\selftest-report.json`
- [ ] 退出码：全 OK → `0`；有 FAIL → `1`；沙箱建不起来 → `2`（`echo %errorlevel%`）
- [ ] **沙箱隔离验证**（最重要的一条）：跑之前记下你 `下载\` 与库目录的文件数/修改时间，
      跑完之后它们**不应有变化**。自检的数据全在临时 `ZHENGUO_DATA` 里
- [ ] 全绿时临时目录自动删除；**任何 FAIL 时打印 `[selftest] 沙箱保留供排查: <路径>`**
      且该目录里至少能看到 `merges.state.json`（合并表，路径见
      `internal/downloadsvc/merge_state.go:73`）与 `downloads.state.json`
      （`service.go:11`）；**下载根目录的确切子目录名由核心 `downloadDirectory` 动作决定**
      （Go 侧不硬编码），自检报告 `download` 步的 `cache_dir` 字段会把实际路径报出来
- [ ] `resources` 步里 `sidecar` 应显示 `in-process`（v2 无独立 sidecar 进程，属正常）
- [ ] 若 `search` / `detail` 红：先跑 `--selftest` 前确认网络与站点可达（这几步真连核心）
- [ ] 若 `download` 红：detail 里 `jobs` 摘要是 `id/state/detail`，把那一行原文贴回来
- [ ] 若 `merge` 红：区分三种原因——`合并未成功：state=…`（ffmpeg 侧）、
      `合并产物文件不存在`（路径/落盘）、`合并产物为空文件`（解码/写入）

## 5. 失败时请抓这些（比截图有用）

1. `selftest-report.json` 全文（很小）
2. 屏幕上那几行 `[selftest] …` 原文（含 detail 摘要）
3. 涉及第 1/2 节时：如果这个构建能开 DevTools（右键「检查」或 F12；
   仓里没有一处 Go 代码显式启用它，**开不了不算失败**，跳过这条即可），
   附 Console 报错行；再附 `localStorage` 里三个键的值：
   `zgj.player.danmakuOpacity`、`zgj.follows`、`zgj.locale`
   （DevTools 开不了时，第 2 节的文本摘要 + 第 1 条报告已经够定位）
4. WebView2 Runtime 版本

## 6. 本机已验，不用重复

- **前端全链**：类型检查、构建、以及 28 个 Playwright 验收脚本（`run_all.py` 全量 28/28、
  0 失败、0 端口泄漏），含：复制简介走桥回落、海报保存（含文件名契约与原图 URL 改写）、
  透明度控件的值/落盘/钳位/重启恢复、下一季入口回写与跳转、en 态整页无中文残留。
- **Go 侧**：`build-v2` 的 `vet + build`（Windows）与 `go test ./internal/...`（ubuntu，9 包全绿），
  其中含弹幕透明度重建 ASS 的 alpha 断言、`MarkNextSeason`、`selftest` 包 12 组纯逻辑单测。
- **i18n**：`.vue`/`.ts` 界面中文硬编码双零 + 46 条豁免的可达性探针（三态 × 8 路由 DOM 实测）。

也就是说：第 1–4 节要验的是**真实 WebView2 的行为、真实落盘位置、真实 ffmpeg/网络产物**，
不是逻辑对错。

## 7. 反馈格式（建议）

```
环境：Win11 23H2 / WebView2 1.7.xx.xxxx
1.1 ✔  1.3 ✔（文件名对，落到 D:\posters）  1.4 ✘ 取消后仍提示已保存
2.1 ✔  2.2 ✘ 滑条是横的
4.x 退出码 1；report 摘要：download FAIL「2 集里只有 0 集下载完成」jobs=[...]
```

---

# 补录（第四十六轮）：第 1–4 节之后新增、且只能在真机验的条目

第 1–4 节写于 R4–R7 时期（约 `106` 笔提交里的前 20 轮）。
第 22 轮之后又做了不少**只能在真实 Windows + 真实核心 + 真实文件系统的东西**，
本机最多验到"编译过 + 假核心逻辑对"，所以下面这些请一并打 ✔/✘。

## 8. 本地成品播放（R22a；对应 v1 `main.py:2609 open_local`）

- [ ] 历史页里**带本地成品的行**点进去 → 播放器标题应是「本地播放 <文件名>」，
      且**不渲染选集侧栏**、上下集按钮 disabled（v1 是清空选集条 + 关掉上下集）。
- [ ] 播放的应该是磁盘上那个文件本身：拖动进度/暂停/倍速都要正常
      （mpv 的 `demuxer-lavf-o` 在本地与加密原件两种情况下的参数不同，见 `internal/mpv/player.go` 的 `lavfOptions`）。
- [ ] 把那个文件手动改名或删掉再点 → 应提示「本地文件已不存在：<文件名>」，
      **不要**去走线上解析、也不要回落演示模式。
- [ ] 「合并」页签里**双击**一条已完成的合并 → 同样进本地播放（v1 也是双击，
      `downloads.py:1139`）；未完成的行双击应什么都不做。
- [ ] 归档页签里**只有含合并产物那一组**有「播放」按钮，另两组（分集目录）没有；
      点播放 → 「本地播放 <合并文件名>」。

## 9. 下载时保存弹幕（R22a③；对应 v1 `library.py:413-475`）

- [ ] 下载页「并发设置」面板里勾上「下载时保存弹幕」（默认关）→ 刷新后**仍是勾着的**
      （真值在 `downloads.state.json`，不在 localStorage）。
- [ ] 之后每完成一集，成品旁边应出现同名 `.ass` + `.danmaku.json`
      （v1 tooltip 原话就是这两样）。两个文件缺一个，下次会整对重写（不算 bug）。
- [ ] 这一集**没有弹幕**时不应该出现两个空文件（v1 也是直接 return 不写；
      写了会让"已存过"判定变真，那一集永远补不上）。
- [ ] 勾上开关后本地播放该成品 → 应挂上那份 `.ass`：字幕轨可见、
      弹幕条数 = `.danmaku.json` 里 items 的条数（不是核心 total）。
- [ ] 关掉弹幕再打开 → 应该**重挂同一支 .ass**，而不是跑去线上重抓。
- [ ] ⚠ 与 v1 的一处已知不同：v1 解析时带 `force=True, session="danmaku-collect"`，
      **v2 核心契约没有这两个入参**，所以这里拿到的可能是核心缓存里的既有会话。
      真机上如果发现"明明有新弹幕却没存进来"，先怀疑这条，再怀疑别的。

## 10. 后台追剧检查（R23；对应 v1 `main.py:714-726 / 776-860`）

- [ ] 挂机 60 秒后应跑第一轮（v1 也是启动 60s 首跑），之后每 30 分钟一轮；
      调试可用 localStorage `zgj.follow.interval` 覆盖（下限 60 秒）。
- [ ] 追剧/预约列表里若有剧更新了，一轮检查后应：
      ① 该行 `known`/`newEpisodes`/`newSeason`/`arrived` 旗标变化，
      ② 追更页顶部出现一句话「追剧检查：N 部有更新」（v1 打在状态栏）。
- [ ] 预约的是"下一季"的剧：检查应去搜同系列并把入口记回旧季行（v1 `mark_next_season`）。
      真机上重点看这条，因为它是整条预约提醒链的终点。
- [ ] 某一部解析失败时**不应该打断整轮**（v1 一条坏剧只计入 failed），
      结论那句应是「追剧检查：N 部暂时取不到更新」。
- [ ] 关掉应用期间不会有提醒（本机定时器的边界，v1 同样如此）。

## 11. 按季合并选中（R22b；对应 v1 `downloads.py:1940-1974`）

- [ ] 「合并入口」那行每颗分组 chip 前有复选，勾 ≥2 部才出现「按季合并选中（2 部）」；
      勾 1 部不出现（v1 门槛是不成批）。
- [ ] 点下去 → 每季各一个成品，名字是「剧名 S01E01.mkv」这种形状（v1 命名口径），
      季序**从小到大**，不是勾选顺序。
- [ ] 某一季正在合并时再点整批：那一季应**跳过**而不是整批报错；
      其余季照样排进去（v1 `skip_running=True` 的语义）。
- [ ] 只有一部可合并时点这批 → 提示「请先勾选至少两部要合并的剧集（每部至少完成 2 集）」。
- [ ] 真机特别看一条：合并产物的**输出目录**与 v1 是否一致
      （v2 走 `merge.go` 的命名与 `<root>/合并/`，v1 是同目录不同拼装）。

- [ ] 按季合并跑完之后，去**归档页签**看那两季成品：应归在**剧名那一组**里
      （带「含合并」徽标、合并计数 +2），**不该**自己另成一组
      （v2 曾经只认 `剧名 全NNN-NNN集` 这种中缀，SxxExx 命名的成品会被甩成独立组；
      第 47 轮改成照 v1 解析三种形状，真机上确认这条真的生效）。
- [ ] 归档组「播放」按钮点下去播的应该是**最新那份**合并产物
      （`openPath` 取 mtime 最新的），标题里的文件名与磁盘上看到的一致。
- [ ] 故意把某一季的成品文件名改成带路径分隔符的样子不会发生（用户改不了），
      但如果真机上看到成品落到 `下载/合并/` **之外**，立刻停下报我 ——
      那说明 basename 守卫没生效。
## 12. 弹幕不透明度（R4 的真机那一半）

第 2 节写的是"控件值/落盘/重启恢复"。R4 后来把值真的接到了 ASS 渲染上，需要补验：
- [ ] 拖动透明度滑条后，**已显示的弹幕**要按新透明度重绘（Go 侧 `BuildAss` 会重算 alpha，
      真机上要看到的是字幕实际变淡/变浓，而不是只改了设置值）。
- [ ] 刷新页面后滑条与弹幕实际观感要一致（`zgj.player.danmakuOpacity` → `SetDanmakuOpacity` → 重渲染）。

## 14. 核心动作覆盖对账（第 53 轮由 `core_action_audit.py` 现算，不再手写）

跑法：`python3 client-v2/frontend/verification/core_action_audit.py`
（也每次全量回归自动打一行；D 方向与"判据被 mpv 污染"硬拦）。
本轮实测：**生产用到 26 个核心动作，假核心覆盖 11 个，剩下 15 个只有真实核心才会第一次执行**：

```
browseFiltered  browseFilters  calendar  catalog  creatorProfile  creatorWorks
danmaku  detail  dramaCreators  initialize  listSources  preload
setSourceEnabled  soraniAccount  suggestions
```

这 15 个里包含几个本轮才看清的东西（**别当成"已验证过逻辑所以没问题"**）：
上新日历、同系列 browseFilters、创作者/演职员、搜索建议 preload、
Sorani 账号、以及核心的 `initialize` —— 它们只在本机的 mock 面与 Playwright 里绿过。

## 15. 一条需要你决定的真缺口：v2 从不归还核心会话（`release` / `cancelRead`）

追第 53 轮对账时撞出来的。`internal/native/engine.go:64-67` 那张
"不带 session 的豁免名单"里，`cancelRead`、`release`、`cancelPlayback`、
`controlDownloadBatch` 四个动作**在 v2 生产代码里没有任何调用点**
（本轮 grep 过：除了那张表本身，一处都没有）。

前两个不是死条目 —— **v1 真的在用**：

| 动作 | v1 调用点 | 用在什么时候 |
|---|---|---|
| `release` | `core_client.py:499`（包装成 `release(session)`），调用方 5 处：`main.py:2120/2379/2490`、`detail_episodes.py:586`、`library.py:483` | 播放拆除、选集池关闭、**弹幕保存 finally 里**（就是我第 41-44 轮移植的那条 sidecar 链） |
| `cancelRead` | `core_client.py:530`（`cancel(scope, sequence)`） | 取消同一 session 的更早读 |

而 v2 这边 `engine.go:148-150` 会**自动给每个非豁免动作补上 `session = owner + ":" + action`**，
也就是说 v2 确实在核心侧注册了会话，只是从来没有一次 `release`。

后果我现在**无法判定**，因为本机没有真实核心：
- 乐观情况：session 按动作固定，核心"同 session 的更早读会被取消"
  （`engine.go:54` 那条注释描述的正是这个语义），所以占用是有界的、会被覆盖的；
- 悲观情况：核心为每个 session 持有 reader/文件句柄，
  v2 长时间运行会累积，或者播放拆除后核心侧仍以为在读。

所以我没有直接照 v1 补一次 `release`：在没跑过真实核心的机器上
往拆除路径加清理调用，属于"看起来对齐、实际可能引入新 bug"的那类改动。
**需要你一句话**（或等真机跑一轮再定）：
1. 先按 v1 补 `native.Release(session)` 并在与 v1 相同的 5 个位置调用（错误吞掉，v1 就是 `cleanup only`）；
2. 还是等真机观察"长时间运行/反复播放"后核心侧表现再决定。

另外 `cancelPlayback` / `controlDownloadBatch` 连 v1 也只是写在豁免名单里
（`core_client.py:240-241`），全仓没有调用点 —— **这两个是 v1 自己的死条目**，
我照搬进表里是对的，不用补实现。

## 13. 一句提醒

上面这些的**逻辑**都在 CI（ubuntu + windows）与本机 Playwright 里绿过：
28 个验收脚本 28/28、`go test ./internal/...` 9 个包全绿（danmaku/downloadsvc/mpv/native/
player/selftest/tray/updater/watchstore）。**但它们全都没有碰过真实核心、真实网络与真实磁盘**：
- 核心侧我只验到"编译 + 契约存在"，`decrypt`/`resolve`/`danmakuAll` 的真实返回一行都没跑过
  （`--selftest` 那一步在真机上是第一次）；
- 弹幕保存/挂载涉及真实文件读写，只有真机能证明文件名口径和 v1 一致。
所以第 8–12 节哪怕本机全绿，也要按"没验过"对待。
