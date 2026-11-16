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


---

## 已发布 `v2.0.0-alpha.26`（第五十九轮，供 Windows 真机测试）

- 发布页：https://github.com/Adsryen/zhenguojian/releases/tag/v2.0.0-alpha.26
- 构建提交：`d557157`（代码）／tag 打在 `af004a4`；CI run `43396255620` 两 job ✓
- 资产：`zhenGuoJian-v2.exe` 20.9MB、`zhenGuoJian-v2-app.zip` 8.4MB（内含 真果鉴.exe）、
  `zhenGuoJian-v2-runtime.zip` 138.5MB（mpv/ffmpeg，首次装一次）、`build-info.json`
- 发布前门禁实测：`run_all` 30/30、失败 0、端口泄漏 0；typecheck 0；build ✓；
  CI 的 `go vet` + internal 全量 Go 单测 ✓
- ⚠ 这个 alpha 的**目的就是真机验证**：真核心 / 真 mpv / WebView2 的行为
  在此之前从未在真机执行过。下列每一项都还是"待执行"，不是"已验证"。

### 请优先跑（按能定多少东西排序）

1. **`zhenGuoJian-v2.exe --selftest`** —— 把输出整段贴回来。
   其中 1 步是有意标注的未对齐项（`decrypt`：v2 无独立解密子系统），那不是失败。
2. **下载一集并勾选「保存弹幕」** —— 本轮修了 `danmakuAll` 契约
   （核心要求 `durationMs>0`，v2 以前不发；`app_danmaku_all.go:180-181`）。
   真机上要确认的是：`.ass` 是否旁挂到成品旁、条数是否与实际一致、
   以及 `total` 的口径。修之前是"必然失败"，修之后**未经真机确认**。
3. **播放本地成品文件**（下载页 → 本地成品 → 播放）：旁挂 `.ass` 是否挂载、
   退出后会话是否归还（`release`/`cancelRead` 在 v2 仍**零调用**，见 R26）。
4. **多季剧按季合并 + SxxExx 命名**（第 47/48 轮补的，只在 CI 上验过纯函数与单测）。
5. 设置页「打包提交」应显示哈希（需把 `build-info.json` 放 exe 同级）。
6. 排错用：**Ctrl+Shift+F12** 开 Chromium DevTools（单按 F12 无效）。

跑完把 1、2 的输出贴回来，我按结果决定 R26（要不要补 `release`）和余下的队列。


---

## 真机反馈第一批（三条播放 bug）→ `v2.0.0-alpha.27`

发布页：https://github.com/Adsryen/zhenguojian/releases/tag/v2.0.0-alpha.27
构建提交 `3c71402`，CI run `43619338982` ✓，四个资产齐（exe 21MB / app.zip 8MB /
runtime.zip 139MB / build-info.json）。

### 三条是同一条链，逐条的代码证据

| 用户原话 | 根因（文件:行） | 修法 |
|---|---|---|
| 「关闭播放之后，后面的就再也无法播放了」（截图红字 `player: 服务已关闭`） | `app.go:374` 关闭按钮调 `Service.Close()`；Close 置 `closed=true`，而 `Open()` 一见它就返回 `ErrClosed`（`service.go:355/382`）。这个 Service 是**应用生命周期**的（app.go 只建一次）→ 点一次关闭，本进程内再也播不了 | 新增 `Service.Stop()`（非终态：quit mpv + 清会话状态，不置 closed）；`PlayerClosePlayer` 改调 Stop；应用退出那路（`app.go:157`）仍走 Close |
| 「弹窗播放，关闭又会被二次打开」 | 后半句：`handleEventLocked` 的重建守卫只有 `if s.quitting || s.closed`（`service.go:861`），用户从 mpv 窗点 X 时两者都不是真 → 体面退出被当成崩溃 → `rebuildLocked` 重新 spawn。而 **v1 根本没有死亡重建**（`player.py` grep rebuild/respawn/意外 零命中），这条是 v2 自加的（`service.go:18` 注释写着） | mpv 体面退出会先发 `shutdown` 事件（`mpv/player.go:653`）→ 做成类型化哨兵 `mpv.ErrGracefulShutdown`，服务层 `errors.Is` 分流：关窗不重建并发 `player:stopped`（前端落到"已停止、可再播"），真崩了仍重建；`Open()` 复位 `quitting`，否则上一集的抑制标志会害本集崩了也不重建 |
| 「点击关闭根本关闭不了」 | `stores/player.ts:shutdown()` 里 `await api.closeVideo()` **无上限等待**，IPC 卡住时路由走不到 `router.back()` | 1.2s 上限先回界面（v1 的 `stop(blocking=False)` 就是这个语义，`player.py:728-735` 注释明写 "never freezes the window"）；Go 侧 Stop 的 quit 也改成 goroutine 非阻塞 |

### 前半句「为什么是弹窗」我仍然缺数据，所以把它变成可见

`PlayerAttach` 一直返回 `{embedded, reason}`，但 **reason 从不显示**，
所以我只能猜。现在真机回退时视频区角落显示
「画面未内嵌（独立窗口播放）：{原因}」（i18n `player.embedFallback` 中英成对）。
**请把这句原样抄回来** —— 这是定位内嵌失败的唯一入口。

### 验证边界（不夸大）

- 本机：`run_all` 30/30、失败 0、端口泄漏 0；typecheck 0；i18n 617/617 对称。
- CI：`go vet` + internal 全量 Go 单测 **231/231**；
  `internal/player` 由 alpha.26 那次的 **20/20 变成 23/23** ——
  这 3 条正是本次新增用例，且那道门要求"执行数==声明数、缺哪个点名哪个"，
  所以"跑了"是有日志行号的证据，不是我的推断。
- 我第一版把断言写成 `st.EpIndex` 与 `h.em.has(...)`，**两个名字都是我编的**
  （真实是 `Snapshot.Episode.Index`、`waitEvent/collect`），grep 才发现。
  在"只有 CI 能编译 Go"的处境下，编方法名的代价就是一次构建失败。
- **这三条都还没在真机验证过**：修的是你报的现象，我这边没有真机。

### 请复测这条路径（顺序有意义）

进播放 → **关视频窗**（不点页面关闭）→ 再播下一集 → 点页面「关闭」→ 再播一次。
外加：视频区角落那句「画面未内嵌…」的**原因文本**，以及
`zhenGuoJian-v2.exe --selftest` 的完整输出（alpha.26 那次还没跑）。

---

## 十六、拿到 `[embed]` 徽标后的判读表（写在这之前先核对徽标实际字段）

徽标由 `PlayerView.vue:92 embedDebugText` 组成，字段就是 `mpv.HostDebug`：
`cls`=父窗类名、`hwnd`=子窗句柄、`x/y/w/h`=子窗**屏幕**矩形、`vis`=IsWindowVisible、
`inW/inH`=最后一次上报的 bounds（物理像素）、`outW/outH/outVis`=换算后的结果。
判读按这个顺序走，**每一步都能把可能性砍一半**：

| 现场 | 结论 | 下一步 |
|---|---|---|
| `inW=0` 或 `inH=0` | 前端从没成功上报几何 | 查 `PlayerSetBounds` 调用时机（attach 之后、首帧之前）与 `.pl-stage` 尺寸 |
| `inW>0` 但 `outVis=0` | **61 轮之后**这条只意味着前端把 `show` 报成了 false（`PlaceDecision` = 尺寸有效 **且** `Show`）；旧代码在这里是"换算判成隐藏" | 看有没有「播放失败」/`running` 没翻真；尺寸本身是对的 |
| `outVis=1` 且 `vis=1`、`w/h>0`，但 `x/y` 落在屏幕外或负值 | 坐标基准错（客户区 vs 屏幕） | 换算要减客户区原点 |
| `hwnd=0` | 子窗没建成/已销毁 | 看 `embedReason`（RegisterClassExW 那类 Win32 错误在这里） |
| 以上都正常 | 才是真·airspace/z-order 问题 | 见下面那个二选一判据 |

**最后一行才需要那个关键二选一**，它只靠肉眼就能分：

- **DOM 播放键覆盖层还看得见**（画面区域内是网页在画）⇒ 子窗没压在 WebView2 之上。
  `SetWindowPos(HWND_TOP)` 只在**同一个父窗的兄弟之间**排序；
  若 `cls` 显示我们挂的父窗不是 WebView2 的兄弟（层级差了一层），
  置顶再多次也排不到它上面 —— 修法是把子窗挂到 WebView2 的**同一个父**下，
  或反过来把 WebView2 让出这块矩形。
- **覆盖层被盖住了、但整块是黑的** ⇒ 子窗已经在最上，是 **mpv 没往这个窗画**。
  方向变成 `--wid` 与 spawn 的时序（`SetWid` 是否在 mpv 起进程之前生效）、
  以及 mpv 是否自己另开了顶层窗（看任务栏/窗口列表里有没有第二个 mpv 窗）。

alpha.31 起徽标默认常驻，所以这两条不用猜，用户回传一张图就能定。

### 61 轮实测结果：这张表的第一行把我带偏了一次

alpha.31 的徽标回来了，现场是

```
子窗 wailsWindow · 子窗 0x161706 · 屏幕矩形 1×1 @328,147 · 可见=0 · 最近上报 0×0 → 换算 0×0 (可见=0)
```

按第一行读会得出「前端从没成功上报几何」。**错**：同一张图上还有「播放失败」，
而 `PlayerView.vue` 的 `videoShouldShow()` 是
`store.running && !store.loading && !store.error && !store.eof` ——
播放失败 ⇒ `error` 非空 ⇒ `reportBounds()` **主动**上报 `0×0` 让位给 DOM。
所以 `0×0` 是"播放失败"的**结果**，不是原因。判读表缺了这条反推，补上：

> **看 `inW=0` 之前先看有没有「播放失败」。** 有失败就先查失败，
> 几何为零可能只是同一件事的影子。（徽标自 61 轮起会把 `store.error` 一起带出来。）

顺着这条往下查，挖出两个**必然发生**的顺序缺陷（与是否 z-order 无关）：

1. `embed_windows.go` 的 `ApplyBounds` 在隐藏分支里**只 `ShowWindow(SW_HIDE)`、
   从不 `MoveWindow`** —— 于是子窗永远停在创建时的 1×1；
2. 前端旧约定把"隐藏"编码成 `0×0`，而 `running` 只能在 `PlayerOpen` **成功之后**
   才为真 —— 于是 `PlayerOpen` 之前那次"起手摆位"（`PlayerView.vue` onMounted 里的
   `reportBounds()`）报的永远是 `0×0`，而且那行还是 `void` 不是 `await`，
   与 `PlayerOpen` 赛跑。

合起来的后果：**mpv 每次都是在「1×1 且隐藏」的子窗上 spawn 的**。
`--wid` 指向的 drawable 是 1×1，之后即便 resize 也不保证重建 swapchain ——
这和"attach 成功、子窗已建、拿到 --wid、就是没画面"完全对得上。

修法是把**几何与可见性拆成两件事**：`mpv.Bounds` 新增 `Show`，
`PlaceDecision`（纯函数，Linux 可跑单测）返回 `(rect, visible)`，
`ApplyBounds` 只要量得出尺寸就 `MoveWindow`，`Show=false` 只负责 `SW_HIDE`；
前端 `reportBounds()` 一律照实上报，起手那次改成 `await`。
`PlayerSetBounds` 因此多一个参数（`contract.GO_BINDINGS_EMBED` 同步）。

**这个修法不是我自己发明的，v1 就是这个顺序**（本轮从 tag `v1-final` 现查）：

- `player.py:1535-1543`：起新会话时先 `_show_poster(...)` 铺海报/上一帧
  （注释原文「别让画面直接黑一下」），紧接着
  `wid = int(self.surface.winId())` —— 交给 mpv 的是**已经按布局量好尺寸的
  原生窗口**，Qt 的 `winId()` 在这里只是拿句柄，不是拿 1×1 占位；
- `player.py:388`：`args.append("--wid=%d" % int(wid))`，spawn 前就位；
- `player.py:1536` 注释：「新会话真的出画后 `_swap_in_surface()` 再换回视频窗口」
  —— **可见性切换发生在首帧之后，几何切换发生在 spawn 之前**，两件事在 v1 里
  本来就是分开的。v2 旧代码把它们合成一件事（用 0×0 表示"藏"），才同时踩中两个洞。

顺带补了两件门禁的账：

- `GO_BINDINGS_EMBED` 此前**全仓零引用** —— 一张写着时序契约的表没人核对过。
  现在 `evidence_audit.py` 有方向 G 拿它跟 `app.go` 的真实签名对账（selftest 34/34）。
- `hitbands_verify.py` 的场景 C 一开始只断言 `last`，反向验证（塞回旧行为）
  实测 **C1/C3/C4 全绿、只有 C2 红** —— 因为 running 之后 `last` 本来就是真尺寸。
  已把 mock 的记录加上 `first`，C1 改盯**第一次上报**，重跑反向验证 C1+C2 双红。
  又一次「绿 ≠ 覆盖」。

### 62 轮：判读表最后一格不再留给人眼，并且 v1 反证掉一个假设

**（1）新增"谁压着谁"的直读。** 原表最后一格要人看图判断"DOM 覆盖层还看得见吗"，
这是把判断题留给人。现在直接问系统：取子窗屏幕矩形的**中心点**，
`RealWindowFromPoint` 返回该像素最靠上的窗口，徽标末尾多出

    · 该像素最上层=Chrome_RenderWidgetHostHWND=other     ← 别人压着我们 ⇒ airspace/z-order
    · 该像素最上层=ZgjVideoHost=ours                      ← 我们在最上 ⇒ 该怪 mpv 没往这窗画
    · 该像素最上层=…(selftest!)                           ← 探测点不在返回窗口矩形内，这条读数不可信

第三行的自检位不是装饰：`RealWindowFromPoint` 是**按值传 POINT**（8 字节），x64 下要把
x 放低 32 位、y 放高 32 位；打包写错不会报错，只会返回**另一个窗口的句柄**——
一份看起来很可信的假数据。所以回读"返回窗口自己的矩形是否包含探测点"，
不包含就把这条读数作废。打包函数 `mpv.PackPoint` 是纯算术，
`embed_test.go` 里钉了 6 组含负数/极值的用例（互换 x/y 必须得到不同结果）。

**（2）v1 反证：「mpv 在隐藏窗上 spawn 所以不画」这个假设不成立。**
我本来怀疑必须先可见再 spawn。查 tag `v1-final` 的 `player.py`：

    1450  def _show_poster(self, hint=""):
    1452      self.surface.hide()          ← 交给 mpv 的那个窗，v1 在 spawn 前就是隐藏的
    1455      self.poster.show()
    1456      self.poster.raise_()
    1543      wid = int(self.surface.winId())   ← 紧接着才取句柄交给 mpv

也就是说 v1 同样在**隐藏态**spawn，差别只在它的隐藏窗**有真实尺寸**（Qt 的 hide()
不改几何）。这正好把 61 轮的修法钉死：要修的是几何，不是可见性。
`_swap_in_surface()`（1491）在首帧后才 `setVisible(True)` + `raise_()`（1377/1383），
对应 v2 的 `show=true` 上报里那次 `SW_SHOWNA + SetWindowPos(HWND_TOP)`。

**（3）接线断言。** 这类诊断字段的历史失败模式是"Go 加了、TS 类型加了、前端没接"
（`GO_BINDINGS_EMBED` 就零引用过）。`hitbands_verify.py` 加 C5：mock attach 带回合成
host，断言徽标里出现**具体值** `Chrome_RenderWidgetHostHWND=other` 且没有残留 `{top}`。
反向对照实测：把 `embedHost.value = attach.host ?? null` 改成 `= null` →
`FAIL C5 | badge=''`、脚本 exit 1。

**（4）notes 的"第一优先级从来没生效过"。** alpha.33 的 Windows 构建绿了，
`latest.json` 的 notes 也确实是中文，我一度认为编码修完就成了。
实际取到的是**文件回退**那一行（"首次安装：下载 …"），不是 annotated tag 注释——
`actions/checkout` 默认不取 tag 对象，`git for-each-ref refs/tags/<tag>` 在 CI 里恒空。
从输出上看不出区别，所以这次把来源本身暴露出来：

- `release_notes.py` 拆出 `source_of()` 返回 `(notes, 来源)`，CLI 加 `--explain PATH`
  把来源**写进文件**（stdout 仍只有 notes 那一行）；selftest 加来源断言，17 → **25 通过**
  （含 `explain-file`、`explain-stdout-is-notes-only`）。
  为什么走文件：我本机没有 pwsh，「用数组 -match 筛 stderr」这种 PowerShell 逻辑
  一行都验不了，而 `Get-Content` 读一个文件几乎不会错 —— 可测的部分留在 Python。
- 两个 job 的 checkout 加 `fetch-tags: true`；
- CI 步骤打印 `notes notes-source=… (len=…)`，并加硬门：
  `git cat-file -t refs/tags/<tag>` = `tag`（有注释）而来源不是 annotation → **发布失败**。
  lightweight tag 退回文件是设计内，所以只钉「有注释却没用它」。
  ⚠ 这段 PowerShell 本身仍是**待执行**证据：本机无 pwsh，只有 YAML 解析通过，
  真正生效要到下一次 tag 构建在 CI 里跑出来。

本地实测 `python3 client-v2/scripts/release_notes.py --tag v2.0.0-alpha.33
--explain /tmp/tc/ns.txt` → stdout 是注释首行、文件里是 `notes-source=annotation`，
说明脚本侧优先级本来就对，缺的是 CI 的 tag 对象。
（63 轮把 `--explain` 从「只写 stderr」改成「写文件」：本机没有 pwsh，
「用数组 -match 筛 stderr」这种 PowerShell 逻辑一行都验不了，
而 `Get-Content` 读一个文件几乎不会错 —— 可测的部分留在 Python。）

## 十七、为什么 Qt 和参考项目都没这问题（实测对照，63 轮）

用户问：「难道是前端代码的问题？以前 qt 都没这个问题啊，参考项目的 ui 也没出现这个情况啊！！」
两边都去查了，答案是**三层不同的原因**，不是「前端写得不好」一句能盖住的。

### 17.1 Qt（v1）为什么没有
v1 的 UI 本身就是原生窗口体系里的东西：`player.py:1543` 交给 mpv 的
`wid = int(self.surface.winId())` 是一个 **Qt 布局已经量好尺寸的原生子窗**，
`1452 self.surface.hide()` 只是不可见、几何照旧（Qt 的 hide() 不改 geometry）。
所以 v1 里根本没有「第二个合成器」——UI 和视频窗在同一套 Win32 窗口里排序。

### 17.2 Wails（v2）为什么有
Wails 的界面是 **WebView2（Chromium 合成器）** 画的，它自己也是一个子窗
（类名 `Chrome_RenderWidgetHostHWND`）。我们的 `ZgjVideoHost` 是它的**兄弟**。
兄弟之间 `SetWindowPos(HWND_TOP)` 只在本进程 z-order 里排序，
能不能盖过 Chromium 的合成输出，就是所谓 airspace 问题。
**这一层与前端代码质量无关**，是宿主选型带来的。

### 17.3 参考项目（Electron，同样是 Chromium）为什么也没有
它没有遇到这问题**不是因为架构更好**，而是它把 airspace 规避做成了设计。
`apps/desktop/src/main/nativeVideoSurface.ts`（实测原文）：

```ts
/** An owned, frameless video region. mpv fills this region, never the controls. */
surface = new BaseWindow({
  parent, title: '…', width: 1, height: 1,
  frame: false, show: false, skipTaskbar: true, resizable: false, movable: false,
  backgroundColor: '#000000', useContentSize: true,
})
window.setIgnoreMouseEvents(true, { forward: true })
const layout = () => {
  const bounds = surfaceBoundsInScreen(parent.getContentBounds(), lastBounds,
                                        parent.webContents.getZoomFactor())
  if (!bounds || !parent.isVisible() || parent.isMinimized()) { hide(); return }
  window.setBounds(bounds); window.showInactive(); window.moveTop()
  resolveReady(true)          // ← 「几何就绪」是 promise，不是调用顺序约定
}
```

四条它做了、我们没做的：

| 它的做法 | 我们现在的做法 | 差别 |
|---|---|---|
| **owned 顶层窗**（`BaseWindow({parent})`） | `WS_CHILD` 子窗 | Windows 保证 owned 窗永远排在 owner 之上，不靠手动抢 z-order；WS_CHILD 只能跟 WebView2 当兄弟比 |
| layout 挂在父窗 **9 个事件**（move/resize/show/restore/focus/enter-full-screen/leave-full-screen/hide/minimize） | 只挂 `window.addEventListener('resize')` | 移动窗口、全屏、从托盘恢复都不会重摆位 |
| `surfaceBoundsInScreen` 用 `getZoomFactor()` 换算并**钳进父窗内容区**，量不到有效矩形就 `hide()` | 按 `devicePixelRatio` 换算，不钳父窗 | 越界与零尺寸的边界处理 |
| `ready` promise **第一次成功 layout 才 resolve**，播放等它 | 靠「attach → setBounds → open」的**调用顺序**约定 | 顺序约定会被任何一次提前 return / 竞态打破，promise 不会 |

### 17.4 这条对照怎么用（决策树）
alpha.35 的徽标会给出 `该像素最上层=…`：

- `=other`（多半 `Chrome_RenderWidgetHostHWND`）→ **确实是 airspace**，
  修法照 17.3：换成 owned 顶层窗 + 事件驱动重摆 + ready 门。这是结构性改动，
  要新建窗口类、消息循环、跟随父窗事件，不能顺手改。
- `=ours` 而画面仍黑 → 跟 z-order 无关，该查 mpv 有没有往这窗画
  （`--wid` 是否真的传了、gpu/hwdec/vo 在该窗上的输出），别去动窗口结构。
- `该像素最上层=no-probe`（子窗还是 1×1）→ 摆位仍然没生效，
  看同排的 `上报N次` / `前端量得…` / `上报异常=…` 判断断在哪一侧。

**注意**：`no-probe` 时不要拿这张表下任何 z-order 结论——探测点在一个 1×1 的窗上
没有意义，所以 Go 侧干脆不探（`Debug()` 里 `w>=2&&h>=2` 才探）。

## 十八、两个 tag 白烧的账：「推上 tag」不等于「出了包」（83 轮）

### 18.1 事实
- alpha.34（run 43697722376）与 alpha.35（run 43700529505）**两个 job 都死在 `Run actions/checkout@v4`**：
  ```
  fatal: Cannot fetch both 734922a54d3e183ac1607d137e09b244bdf7bb47 and refs/tags/v2.0.0-alpha.35 to refs/tags/v2.0.0-alpha.35
  ```
  `gh release view` 两个 tag 都是 **release not found**，一个资产都没有。
- 我却已经对用户说过「请测 alpha.34」「alpha.35 构建中」。用户实际在测的是 **alpha.33**
  —— 这正好解释了 63 轮那个「徽标里没有 `该像素最上层=`」的谜：**不是新功能没生效，是那个包不存在**。
- 成因是我 62 轮加的 `fetch-tags: true`：tag 触发时 `github.ref` 本身就是 `refs/tags/<tag>`，
  checkout 把目的地算成 `refs/tags/<tag>`，fetch-tags 再叠一条同名不同来源的 refspec，
  git 拒绝「同一目的地两个来源」。**alpha.33 及以前没有这个选项，所以从没失败过。**

### 18.2 我错在哪（不是「CI 抖了」）
1. 打完 tag 我只核对了「tag 推上去了」，没核对「run 结论」和「资产存在」。
   上一轮我明明起了 `gh run watch` 的后台 watcher，**却没去读它的结果**就写了回复。
2. 复现时我第一次把 refspec 写成 `"$SHA"`（目的地不同）→ rc=0，
   差点得出「本地复现不了，是 CI 环境问题」。目的地相同才是这条错——
   一次不成功的复现不等于复现不了。

### 18.3 修法（两处，都有本地证据）
- 撤掉两个 job 的 `fetch-tags`；windows job 单独加一步
  `git fetch --force --depth=1 origin "refs/tags/$TAG:refs/tags/$TAG"`，
  并且 `git cat-file -t` 不是 `tag` 就直接 exit 1（**取不到注释要让 job 红**，
  否则 release_notes 会静默退回文件那一行，从资产内容上看不出来）。
- `scripts/tag-fetch-repro.sh`：A 段用**两条同目的地 refspec**精确复现 CI（实测 rc=128、
  报错逐字相同）；B/C 段证明修法在浅克隆里能取到 annotated tag 对象，
  且 `release_notes.py --explain` 给出 `notes-source=annotation`。

### 18.4 新门禁：`scripts/release-check.sh <tag> [--wait]`
「我说发布了」之前必须跑它。四段判据，全部实测过两个方向：

| 检查 | alpha.34（坏） | alpha.33（好但 notes 来源错） |
|---|---|---|
| run 结论 | `failure`，并点名 `Run actions/checkout@v4` | `success` |
| release + 5 个资产 | 不执行（上一步已红） | 齐 |
| latest.json 契约 | — | schema/tag/version/sha256/size/url 全过 |
| **notes 是否用了 tag 注释** | — | **FAIL**：注释首行是「v2.0.0-alpha.33 —— 内嵌几何…」，实际是文件回退那一行 |

最后一行就是 18.1 那条隐身错的**发布级**可见化：以后只要 CI 没取到 tag 对象，
`release-check` 会直接指着「注释首行 vs 实际」两串字，而不是等我发现用户测的是旧包。

### 18.5 顺带：linux-smoke 的两条自身缺陷（83 轮实测）
1. 它依赖的 `client-v2-linux` **没有任何脚本产出**，构建命令只活在我的聊天记录里
   —— 第一次跑用的还是 06:36 的旧产物，而前端 09:46 才重建过。
   现在脚本自己判新鲜度（`find` 比 mtime）并重建（前端 → make-overlay → go build 带
   `-X main.buildVersion=$(git describe --tags)`，实测打印 `buildVersion=v2.0.0-alpha.35`）。
2. 播放按钮的 y **不是常量**：标签换几行、「看到第 N 集」在不在，都会推移它几十像素。
   固定点 370 对某个剧命中、换剧就打在按钮下方 → `mpv 进程数=0`，
   看起来像产品回归，其实是我的点击没命中。
   现在沿 x=283 扫 y=295..445 找主按钮的填充色（实测 `srgb(74,131,255)`，
   判据 `b>120 && b-r>60 && b-g>40`）取连续 12 个蓝像素的中点；
   进详情那一步也用「找得到主按钮颜色」当作**已经在详情页**的判据。
   实测三种布局分别命中 y=322 / y=334，`mpv 进程数=1`，离开播放页后归 0。

## 十九、内联 PowerShell 也必须本地能跑：alpha.36 死在语法错（83 轮）

### 19.1 事实
- alpha.36（run 43703147869）：**test-linux success、windows failure**，
  失败 step = `Generate latest.json (更新器的元数据资产)`，真错逐字：
  ```
  ParserError: D:\a\_temp\0faf4236-….ps1:37
     (& python $rs --tag $tag --file client-v2/build/bin/RELEASE-NOTES.md
         --explain $srcFile | Select-Object -First 1)
  Missing closing ')' in expression.
  ```
- 这段是 62 轮我为"notes 来源可见"改的。改完之后它**一次都没被执行过**——
  它排在 checkout 后面，而 checkout 先死了（见十八节）。
  所以"我看过 diff"在这件事上价值为零：本地跑不到、CI 才跑得到，
  中间隔着的代价就是又一个白烧的 tag。

### 19.2 修法分两层（不是"下次小心"）
1. **逻辑搬出 workflow**：新增 `client-v2/scripts/gen_latest_json.ps1`，
   latest.json 的生成判据全在里面（参数化 -Tag/-BinDir/-NotesFile/-RepoRoot，
   带 `-SelfTest`）。workflow 里只剩三行胶水：跑 selftest、跑生成、判退码。
   -SelfTest 11 条实测全过，其中第 11 条是**「annotated tag + notes 来自文件回退 = 拒绝发布」**
   ——用桩脚本冒充 CI 的坏环境，把 alpha.33 那条隐身错做成会响的硬门。
   变异实测：把硬门改成 `if ($false)` → 立刻 `FAIL 11 … 根本没报错`（改前这条**没有判据**，
   门坏了也没人知道）。
2. **剩下的胶水做语法门**：新增 `scripts/pwsh_syntax_gate.py`，
   把 workflow 里每段 `shell: pwsh` 的 run 抽出来，交给 PowerShell 自己的
   `Parser::ParseFile`（不执行）。实测它能在本地复现 CI 的报错：
   ```
   FAIL … Generate latest.json (pwsh)
        36:78 Missing closing ')' in expression.
   ```
   （拿 `git show 4dc0e50:.github/workflows/build-v2.yml` 喂给它，rc=1）

### 19.3 本机 pwsh 从此可用
- 装了 pwsh 7.4.6 到 `~/.local/pwsh`（tarball 解包后要 `chmod +x pwsh`）。
  **"我本机没有 pwsh 所以这段没法验"这个借口从此不成立**。
- 两道门都进了 `run_all.py` 预检（现在共九道）；没有 pwsh 时打印
  `⚠ …**未验证**`，不伪装成通过。

### 19.4 门自己也被抓了两条错（都是我的）
1. 第一版用 `pwsh -Command <脚本> <路径>` 传参，`$args[0]` 拿不到 →
   报 `The file could not be read`。后果很坏：**坏块和好块都因为同一个读文件错而红**，
   S1「坏块被拒绝」看着过了，其实一行源码都没解析。改成走环境变量
   `ZGJ_PS_PARSE_TARGET`，并把 S2 从"有没有报错"改成**盯报错内容**
   （不得含 could not be read / TARGET-MISSING），另加 S5 专测"门自己坏了要报错"。
2. `${{ github.ref_name }}` 原样喂给 Parser 会报
   `Use `{ instead of { in variable names.`——GitHub 在调用 pwsh **之前**做替换，
   PowerShell 从不认识它。现在解析前把 `${{ … }}` 换成占位符（S4 钉住不误报）。
   诚实边界：占位符只消除这一类假错；替换后的**值**带空格/引号会不会破坏写法，
   这道门管不了，靠"插值一律放在双引号里"的约定。
3. 顺带又犯一次**变量遮蔽**：我在 `main()` 里用 `for label, args in …` 把
   CLI 的 `args` 命名空间覆盖成 None，run_all 在脚本列表那行崩
   （`AttributeError: 'NoneType' object has no attribute 'only'`）。
   这次是崩、不是假绿，但同类错（R52 也犯过）已经是第二次。

### 19.5 这轮的门禁实测
- `pwsh_syntax_gate.py --selftest` 6/6；真 workflow 2 段 0 错。
- `gen_latest_json.ps1 -SelfTest` 11/11（变异体 m1/m2/m3/m4 各抓到对应条）。
- `run_all.py` 九道预检全过；全量回归结果见提交信息。
- alpha.37 待用 `scripts/release-check.sh` 核对（四段判据，见 18.4）。

### 19.6 alpha.37 又红一次——但这次红得有价值，而且我拿到了**本地 Windows 替身**
- alpha.37（run 43705834656）test-linux success、windows failure，仍是
  `Generate latest.json`，但失败点变了：**新 selftest 自己在 Windows 上红两条**，
  于是它按设计拒绝了发布（生产路径根本没跑到）：
  ```
  FAIL 9 优先级 | 实际=[这是发布说明的第一段正文。]
  FAIL 11 annotated tag + 文件回退 = 拒绝发布 | 报错内容不对：release_notes 取摘要失败 (exit 1)
  gen_latest_json selftest: 9 通过 / 2 失败
  ```
- 两条都不是"产品逻辑错"，是**夹具在 Windows 上坏掉**：
  1. `git tag -a` 需要提交者身份。我的 Linux 开发机有全局 user.name，
     全新 Windows runner 没有（checkout 只设 safe.directory）→ 建 tag 失败。
     更坏的是我把 git 的 stderr 用 `2>&1 | Out-Null` 吞了，
     于是"设置没成功"表现为"被测行为不对"：case 9 报的是 notes 不对，
     **case 10 甚至假过**（一个 tag 都没建成的仓库，退回文件当然对）。
  2. case 11 的 python 桩打中文到 stdout，cp1252 直接
     `UnicodeEncodeError: 'charmap' codec can't encode characters in position 0-11`。
     这与 82 轮 release_notes.py 在 Windows 上崩是**同一类错，我在新代码里又犯了一遍**。
- 修法：`Run-Git` 不吞 stderr、非 0 就 throw 并带上原文；每条 git 命令后加
  **夹具自证**（`cat-file -t` 必须是 tag / 必须不是 tag）；桩里
  `sys.stdout.reconfigure(encoding="utf-8", errors="replace")`。
- **本轮最值钱的产出：本地 Windows 替身。**
  `HOME=<空目录> PYTHONIOENCODING=cp1252 pwsh -File gen_latest_json.ps1 -SelfTest`
  实测：拿修复前的文件跑，报的两行 FAIL 与 CI 日志**逐字相同**；
  修复后同一命令 11/11。也就是说这类 Windows 专属夹具错，
  以后**不需要再烧一个 tag 才能发现**。已作为第十道预检进 run_all
  （`gen_latest_json 自检（模拟 Windows runner）`，与本机那遍并列跑）。
- 顺带修 `scripts/release-check.sh` 自己的一条假失败：我在推完 tag 几秒后就跑
  `--wait`，那时 GitHub 还没建出 run，脚本直接判"没有任何 build-v2 运行"退出。
  `--wait` 的语义必须包含**等 run 出现**（现在 15s 一次、最长 5 分钟）。

## 二十、alpha.38：资产目录搞混，以及"selftest 全绿而生产路径必炸"是怎么可能的
### 20.1 事实
- alpha.38（run 43707569205）windows failure。selftest **11/11 通过**、
  `notes-source=annotation` 正常，紧接着生产路径抛：
  ```
  Exception: …\client-v2\scripts\gen_latest_json.ps1:51
     latest.json 缺资产源文件: client-v2\build\bin\zhenGuoJian-v2.exe
  ```
- 根因是我 83 轮搬代码时造的错：**四个资产并不在同一个目录**。
  `Assemble runtime zip + single exe` 步用的是相对 cwd（仓库根）的裸文件名
  （`Copy-Item client-v2\build\bin\client-v2.exe zhenGuoJian-v2.exe`、
  `Compress-Archive … -DestinationPath 'zhenGuoJian-v2-app.zip'` 等），
  只有 `build-info.json` 与 `latest.json` 在 `client-v2/build/bin/`。
  `Publish release` 步的 files 列表逐字可证。旧内联版写的是根目录相对路径，
  所以 alpha.33 能过；我把它们统一成 `$BinDir` 等于自己造了个错。

### 20.2 为什么 11 条判据一条都没抓到
因为夹具把四个资产**全塞进同一个 `$bin`**。也就是说 selftest 验的是
"我的假设"（资产同目录），不是 CI 的真实布局。判据越写越多，
只要夹具的布局错，它们就一起绿。**覆盖面不等于保真度。**

### 20.3 修法（三条，都有实测）
1. `New-Manifest` 拆出 `-PackageDir`（exe + 两个 zip）与 `-BinDir`
   （build-info.json、RELEASE-NOTES.md、latest.json 输出）；
   workflow 那句改成 `-PackageDir . -BinDir client-v2/build/bin -RepoRoot .`。
2. 夹具改成**按 CI 真实布局分两个目录**（`pkg/` 与 `bin/`）。
3. 新增 case 12「打包资产不在 BinDir 时必须拒绝」当回归钉：
   把 `-PackageDir` 误写成 `$BinDir` 立刻红。
   写它的时候我自己又踩一个错：续行符用了反斜杠 `\`。
   PowerShell 的续行符是反引号 `` ` ``，`\` 会被当成**一个普通参数**，
   Parser 完全不报错，于是表现为运行期
   `Cannot bind argument to parameter 'Path' because it is an empty string.`
   —— 上面那道 Parser 门对这类错天生无效，因为它语法合法。

### 20.4 因此给 pwsh_syntax_gate.py 补了一道纯文本门
- `continuation_lint(text)`：代码部分（去掉行尾 `#` 注释、跳过 `<# … #>` 块）
  以 ` \` 结尾就点名。覆盖 `client-v2/scripts/*.ps1`、`scripts/*.ps1`
  以及 workflow 内联 pwsh 段；**不依赖 pwsh**，所以放在 pwsh 可用性判断之前跑。
- selftest 9 条（新增 S6 坏写法被点名、S7 反引号/注释不误报、S6b 真脚本干净）。
- 真 workflow 实测：反斜杠续行 0 处，内联 2 段 0 语法错。

### 20.5 本轮实测
- `gen_latest_json -SelfTest`：本机 **12/12**、模拟 Windows runner（空 HOME + cp1252）**12/12**。
- **正向**：假 CI 树（仓库根放 exe+两 zip，client-v2/build/bin 放 build-info.json）
  用新脚本 + 新调用 → rc=0，`latest.json 就绪：tag=v2.0.0-alpha.33 prerelease=True
  notes来源=v2.0.0-alpha.33 —— 内嵌几何起手就摆位`，四个资产 key 齐。
- **反向（先错后对）**：我第一版"反向检查"只把 `-PackageDir` 省略掉，
  结果 rc=0 —— 因为默认值 `.` 在 CI 里本来就是仓库根，**我测的根本不是那个 bug**。
  改成拿 `git show HEAD:` 的旧脚本 + 旧调用跑，才逐字复现出
  `缺资产源文件: client-v2\build\bin\zhenGuoJian-v2.exe`（连行号 51 都一样）。
  教训：**反向检查必须复刻出错的那份代码，不是复刻我以为的出错方式。**
- go-local test internal 9 包全 ok、winvet rc=0、vue-tsc rc=0。


## 二十一、Linux 冒烟的「7 个页面渲染正常」其实是 3 个（83 轮）

### 21.1 事实

`scripts/linux-smoke.sh` 逐页截图后只 print 字节数，**从不检查页面真的换了**；
而它硬编码的导航 y 表整体偏了半行。把每一张截图的「侧栏高亮项中心 y」量出来：

| 脚本里点的 y | 名义页面 | 高亮实际落在 | 真相 |
|---|---|---|---|
| 107 | discover | 109 | ✓ 找剧 |
| 143 | library | 109 | ✗ 没导航（重试后 144 才中） |
| 211 | search | 214 | ✗ 打到「新剧预约」 |
| 283 | reserve | 285 | ✗ 打到「追更预约」 |
| 355 | history | 355 | ✗ 打到「下载与合并」 |
| 427 | downloads | 427 | ✗ 打到「设置」 |
| 499 | settings | 427 | ✗ 打空，页面仍是「设置」 |

真实条目中心（从 `page_discover.png` 逐行量）：
找剧 109 / 剧库推荐 144 / 搜索 178 / 新剧预约 214 / 追更预约 286 / 观看历史 321 /
下载与合并 355 / 设置 428（「我的」标签 256、「系统」标签 398、「收起」772 不是条目）。

⇒ 之前几轮我说过的「7 个页面渲染正常」只有 3 页成立。**搜索页和历史页在冒烟里从没被打开过。**

### 21.2 判据为什么换成「高亮项」

内容 md5 变不变会被加载转圈、封面数量、鼠标位置污染——既可能假变（只有光标动了），
也可能假不变（两页恰好同骨架）。侧栏高亮项不会说谎：活动项文字是主题蓝
（实测 `srgb(74,131,255)`），hover 只是灰底，两者可分；顶部 logo 也是蓝的，所以只看 y>=60。

新脚本 `scripts/smoke_nav.py` 就是这个判据的唯一实现，冒烟脚本按
「高亮中心 y ≈ 我点的 y（±12）」逐页断言，并**同一坐标重试 3 次**。

### 21.3 判据自己的门（5 条）与变异体

`python3 scripts/smoke_nav.py --selftest`：
N1 单行蓝条→检出中心±3；N2 全灰→空；N3 坏文件→空且不抛；
N4 两行→两簇都报出来（调用方据此判失败，不静默取第一个）；N5 顶部 logo 被 y0 排除。
实测 5 通过 / 0 失败。

把颜色阈值 `b>120 && b-r>60 && b-g>40` 改成 `b>0`（等于没判据）后：**1 通过 / 4 失败**
（只有 N3 还过，因为坏文件本来就没输出）。真图上它会把整页读成一个 60..699 的大簇、
中心 379——正好说明「没有这条 selftest，检测器可以完全胡说而冒烟照样绿」。

已接入 `run_all.py` 作第十一道预检（没有 ImageMagick 时打 `SKIP … 不是通过，是没测`，
不允许静默变成"没测"）。反向实测：把坏检测器临时放回原位跑预检 →
`FAIL smoke_nav selftest 非 0（exit=1）`、RC=1。

### 21.4 我造的第二条错：在没有 -e 的脚本里偷偷打开 -e

加固后的冒烟连续两次静默死在「进详情并播放」之后，日志一句 FAIL 都没有，进程也没了。
加了 EXIT 陷阱才看见：

```
EXIT: rc=1 line=1 last=[pkill -x mpv 2> /dev/null]
```

原因是我自己写的那行 `set +e; DIFF=$(( … )); set -e`——本脚本第 22 行只有
`set -uo pipefail`，**从来没有 -e**，我那句"恢复"等于从这行起把 -e 打开了，
于是后面第一条合法返回 1 的命令（`pkill` 没匹配到任何 mpv）就把整只脚本杀掉。
改成 `case "$ACT" in ''|*[!0-9]*) … ;; esac`，不再碰 shell 选项；顺手给
`pkill`/`grep|head` 加 `|| true`。

教训两条：① 「恢复现场」之前要先确认现场本来是什么；② 长脚本必须带 EXIT 陷阱，
否则静默死亡会被读成"没跑到"，而我已经连着两轮把它当成产品问题了。

### 21.5 一次点击丢失（记录，不判产品缺陷）

`library` 首点 y=144 后高亮仍在 109，重试即中；`search` 也丢过一次。
产品侧查过：`components/NavRail.vue` 没有 `pointer-events:none`/disabled 分支，
`App.vue` 没有全局遮罩，所以不是"加载时吞点击"的代码路径。
判为 xdotool 合成输入撞上页面重渲染的时机问题，冒烟用 3 次重试容忍，
但把「重试过」打进日志（`⚠ … 高亮却在「…」，重试`），不让它变成无声的绿。

### 21.6 加固后的实测（/tmp/zgj-smoke83h）

8 个页面全部 `OK`（高亮与点击行一致，其中 library 重试 1 次）；
`详情页已进，主按钮 y=356` → `起播成功（来源=color），新 mpv pid=2696524`；
`playing.png` 348341B；末尾 `PASS 冒烟通过`、EXIT rc=0。

### 21.7 边界

内嵌只在 Windows 实现（`internal/mpv/embed_stub.go` 非 Windows 返回错误），
所以 Linux 冒烟能验「能起 / 逐页真到 / 能拉流出画面」，
**验不了 Windows 的黑屏与 z-order**——那仍然只能靠 alpha.39 真机回传徽标。


## 二十二、「单独绿、套件红」：重负载进程预检（83 轮）

### 22.1 现象

加固冒烟后跑全量回归，`discover_hero_verify.py` 报 5/15 失败：

```
[FAIL] A1 items[0] 直接进网格（不再上墙剔除）  — grid[0]=[] items[0]='一世之尊'
[FAIL] A1 网格非空  — cards=0
[FAIL] A1 墙顶贴齐（contentTop 无 hero 占位，≤8px）  — offsetTop=-1
```

`cards=0` 且 `offsetTop=-1`（元素根本找不到）说明**页面没渲染出来**，不是判据过期。
单独重跑 15/15 全绿；把 `detail_reserve → discover_hero` 两个脚本按顺序手动跑也全绿。

### 22.2 真因

`scripts/linux-smoke.sh` 跑完**从不收尾**：`client-v2-linux` 由 `timeout 600` 包着，
mpv 是它的子进程，冒烟结束时两者都还在拉流。回归正好在那几分钟里跑到第 8 个脚本，
CPU/带宽被抢，固定等待的验收脚本等不到渲染。

⇒ 这不是脚本顺序耦合，是**机器状态耦合**。任何"绿"的判据都默认了独占机器，
这个前提从没被检查过。

### 22.3 修法（两条，都带反向实测）

1. 冒烟脚本的 EXIT trap 里 `pkill -x client-v2-linux` + `pkill -x mpv`。
   ⚠ bash 的 `trap ... EXIT` **后设的覆盖先设的**，不是叠加：我先写成两条独立 trap，
   第二条会把第一条的诊断 echo 悄悄丢掉；合成一条后单元实测——
   trap 期间 mpv 在跑（pid 2768837），脚本退出后 mpv 为空 ✓。
2. `run_all.py` 新增**重负载预检** `heavy_processes()`（读 `/proc/*/comm`，
   名单 `client-v2-linux / client-v2.exe / mpv`，不 shell out）。
   命中就 exit 1 并写明「被抢负载会出现假失败」。
   反向实测：起一个 `mpv --idle` 后跑预检 →
   `FAIL 有重负载进程在跑：mpv`、RC=1；清掉后 → `重负载预检 OK`。
   非 Linux（没有 /proc）时打印 `SKIP（非 Linux，读不到 /proc，这条验不了）`，
   不允许把它读成"通过"。

### 22.4 顺带记着的一条

`grep -n "FAIL" 全量日志` 会先命中表头那行「PASS FAIL 秒」，
所以找失败脚本要用 `grep "exit [^0]"` 或直接看表里 exit 列——
这次我先按 FAIL 关键字查，结果抓到的是表头，差点以为日志坏了。

## 二十三、Linux 内嵌整链的三个断点：宿主选窗 / visual 不匹配 / vo 不认 --wid（93 轮）

提交 21e121d + 1654551。Xvfb + 真流产物实测，三个断点全有对照实验或
真机探针实锤，不靠猜。判据全部机器可查（xwininfo 子窗树 / mpv cmdline /
截图像素多样性 / ApplyBounds 时序日志）。

### 23.1 宿主选窗：Wails 建两个带本进程 pid 的顶层窗

探针（/tmp/xprobe，QueryTree→逐窗读 _NET_WM_PID+MapState）对**活 app**
实跑：`0x200003 "真果鉴" 1280x800 IsViewable`（真主窗）和
`0x200001 10x10 IsUnMapped`（隐形辅助窗）**都**带 app 的 pid。
只按 pid 取第一个命中会选中辅助窗（QueryTree 顺序不保证主窗在前），
子窗建进 10x10 → 画面永不出现。
修法：findHostWindowX 加 `MapState==IsViewable` 判据；无 Viewable 宿主
返回 0 → 上层回退独立窗（宁回退不建进隐形窗）。
回归：测试建 10x10 未 map 同 pid 诱饵窗 + L1b 断言选主窗；
变异验证（判据改恒真）→ 测试立刻红，还原后绿。

### 23.2 子窗 depth/visual 必须取父窗（CreateWindow BadMatch）

Wails 主窗 visual=0x3b7，root 默认 visual=0x21。拿 screen.RootDepth/
RootVisual 建子窗 → `CreateWindow 失败: BadMatch {BadValue: 主窗XID}`。
修法：visual 从父窗 GetWindowAttributes 读；depth 协议上无「按窗问」的
请求（GetWindowAttributesReply 无 Depth 字段），按 visual id 在
Setup() 的 AllowedDepths 表里查。xproto **没有**独立的 GetVisualInfo
请求（首版照 xlib 习惯写了 xproto.GetVisualInfo，编译直接报 undefined）。

### 23.3 mpv 默认 vo 不认 --wid（画面根本不进子窗的核心根因）

mpv 0.34.1 默认 vo 自动选择链 gpu→…→sdl。软渲染/Xvfb 里 gpu 初始化
失败（`x11egl uninit` → `drm` → `Can't open TTY`）后**回退 SDL**，
而 SDL 输出不认 --wid，自建 640x360 顶层 mpv 窗。
对照实验（固定映射父窗 + 真视频 testsrc，`/tmp/xprobe/embedctl`）：
- 默认 vo：root 下出现 `640x360 "testsrc.mp4 - mpv"` 独立窗，父窗截图
  1 种颜色（空）。映射与未映射父窗**都**复现（说明不是时序，是 vo 问题）。
- `--vo=x11`：无独立窗，父窗截图 16767 种颜色（真视频嵌入）。
与真机现象逐字吻合：子窗摆好了但黑 + root 下 640x360 独立 mpv 窗。
修法：player.go buildArgs 内嵌路径（Wid!=0 且 GOOS==linux）追加
`--vo=x11`。独立窗（Wid=0）保持默认 vo 自建窗是期望行为，不加。
注意：mpv 0.34 的 x11 vo 会**自己在宿主 wid 下再建一个渲染子窗**
（class "x11"，如 0x600002），所以「宿主子窗 0 children」不是失败判据，
要看宿主子窗**下面**有没有 mpv 渲染窗。

### 23.4 可见性时序（诊断日志定案，日志已删）

前端起手 show=videoShouldShow() 为假 → ApplyBounds 连报 3 次 show=false
（#apply=2/3/4），running 变真后 watch 触发第 5 次 show=true →
两窗 MapState 均 IsViewable。播放中主窗截图整窗 44759 种颜色、
视频区 43045（同尺寸空白区对照 162）。**Linux 内嵌整链到此打通。**

### 23.5 对 Windows 黑屏（alpha.40）的迁移价值

三断点里只有 23.3 的形态在 Windows 存在（Windows 的 d3d11/wgl 渲染器
认 --wid，不存在「SDL 回退」这种链），但 23.1 的教训**直接适用**：
Windows 版 findHostWindow 用枚举顶层窗打分（类+题=3 > 类=2 > 题=1），
如果 Wails 在 Windows 上也建多个顶层窗（比如一个 1x1 辅助窗），
打分法可能选中错误的窗。alpha.40 数据（applyCount=0 + 前端量得
764x614#3 上报异常=none + v?）仍指向 IPC 挂起主假设（见 b31 块），
但宿主选窗是探针回来之前值得先静态核对的一条。

## 二十四、有 WM 时内嵌整体退化：reparent 后 QueryTree(root) 看不到宿主窗（94 轮）

提交 be9fb57。承接 23 节（无 WM 整链打通）。在 Xvfb + xfwm4（带窗口
装饰的真实桌面形态，用户 xrdp 就是这种）下复测，内嵌**整体退化**：
mpv spawn 没有 --wid、退化成独立窗。

### 24.1 根因（xwininfo 实测，不靠猜）

- WM 把顶层窗 **reparent** 进自己的 frame：主窗的 parent 从 root
  变成 frame（0x207b0a），QueryTree(root) 只能看到 frame 和一堆
  WM 自己的辅助窗（xfwm4 标题栏/边框条），看不到我们的客户端窗。
- 但 reparent 后的客户端窗**仍然带着 _NET_WM_PID**（实测 0x400003
  _NET_WM_PID=app pid、MapState IsViewable）——只是不在 root 直挂。
- 首版 findHostWindowX 只查 QueryTree(root) 的直接子窗 → 返回 0 →
  上层回退独立窗。

### 24.2 修法

候选窗 = EWMH _NET_CLIENT_LIST ∪ QueryTree(root) 的并集，再按
_NET_WM_PID==本进程 且 IsViewable 筛：
- 无 WM：_NET_CLIENT_LIST 不存在 → 自动只剩 root 子窗（原形态不变）；
- 有 WM：WM 维护的 _NET_CLIENT_LIST 恰好列着被 reparent 的客户端窗。

### 24.3 免费的好结果：WM 边框偏移天然自偿

前端 viewport 原点 == 客户端窗内容区原点，子窗的父也是同一个客户端窗，
所以面板/边框的绝对偏移在「子窗相对父窗」坐标系里自动消失——不需要
额外处理。实测：子窗绝对坐标 +200+111 = 相对 200,87 + 面板 24px，
画面与视频区精确重合（截图视频区 76029 种颜色 vs 空白 173）。
**这推翻了 23 节「有 WM 装饰时视口≠客户区原点」的担忧**：那个担忧
的前提（子窗父窗==root）在有 WM 时不成立，父窗本来就是客户端窗。

### 24.4 回归测试 + 变异验证

TestFindHostWindowXUnderWmReparent：手工复刻 WM 两个动作（建 frame
顶层窗 + ReparentWindow 把主窗挂进去 + root 写 _NET_CLIENT_LIST），
先断言前提成立（reparent 后主窗不在 root 直挂），再断言发现链选中
主窗、子窗建在主窗下。变异验证：短路 _NET_CLIENT_LIST 读取（模拟旧
实现）→ 测试立刻红「选中 0x0」，还原后绿。

### 24.5 顺带抓到的基线 flaky

player 包 TestOpenLocalDoesNotFetchOnlineDanmaku 偶发红（5 次 1 次、
10 次 1 次）；基线对照（git stash 我的改动）15 次也失败 4 次 →
**基线就有的偶发，与本轮改动无关**（本轮只动 buildArgs 的 --vo 行，
不碰弹幕路径）。另案处理，未混入本次提交。

## 二十五、update:notify 事件对账定案（95 轮）

此前遗留的「Go 发 update:notify 前端听 update:available」对账，本轮查清：

- **不是名字写错，是两个不同事件**：
  - `update:available`：updater/service.go:457/486（发现新版本）→ App.vue:42
    在听 → 顶部常驻横幅（带「去设置/跳过」按钮）。正路，无问题。
  - `update:notify`：app.go:97-103，托盘「检查更新」→ CheckManual →
    原生信息框（Windows MessageBoxW）**失败时**的回落事件，payload
    {title, text}（Go 侧文案 = manualCheckText：失败原因/发现新版/已跳过/
    已是最新）。
- **漏点精确定位**：app.go:96 注释自己写了「前端未监听 update:notify，
  非 Windows 仅开发机」——「engine 在、wiring 缺」又一次。但触发面比注释
  想的还窄：**Linux 上托盘本身是 no-op**（tray_other.go:12 Register 恒失败，
  CheckManual 只被托盘调用）→ Linux 上该事件永远发不出。真正会触发的是
  **Windows 上 MessageBoxW 失败**（窗口句柄异常/弹窗被禁）→ 事件兜底被
  丢弃（前端没听）。
- **修法**（前端 App.vue）：补监听 `update:notify` + `zgj:update-notify`
  CustomEvent（mock 通道同款双通道），展示为**自动 8s 消失的结果通知条**
  （区别于 update:available 常驻横幅——notify 是「查完了告诉你」不是
  「有新版本等你处理」，无按钮、不抢横幅位置）。
- **mock 侧刻意不派发**：真实 UpdateCheck 绑定（bindings_update.go:53）走
  `Check`（不 Notify），只有托盘路径的 `CheckManual` 才 Notify，mock 无
  托盘；mock 里给 updateCheck 加派发 = 造假行为（把不存在的产品路径测绿）。
  E2E（update_verify N1/N2）直接注入事件测前端监听链路：出现 + 8s 自动消失。
- **诚实边界**：Windows 侧「MessageBoxW 失败 → 事件兜底 → 通知条」这条
  链在 Linux 无法执行级验证（托盘 no-op + MessageBoxW 仅 Windows），
  只有前端监听链路（E2E）+ 代码走读（Go 侧 app.go 路径完整）两层证据。
  Linux 用户「点托盘检查更新看不到结果」的**真实体验缺口不是这条**——
  Linux 上根本没有托盘，手动检查走设置页按钮（updateCheck → Check，
  结果直接渲染在设置页结果行，一直好的）。
