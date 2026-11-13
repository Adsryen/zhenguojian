# v1 ↔ v2 播放器 + 弹幕对账（inventory-level audit）

- v1 侧范围：`zhenguojian/app/zhenguojian/player.py`（2354 行，全读）、`danmaku.py`（192 行，全读）、
  `main.py` 播放整合段（1685-2830）、`watchstore.py` save 链、`core_client.py`、`library.py:413-475`、
  `tools.py` danmaku/hwdec 键、`playback_dialog.py`、`widgets_guo.py EpisodeGrid`、`detail_episodes.py`。
- v2 侧范围：`frontend/src/views/PlayerView.vue`、`stores/player.ts`、`components/HotkeyHelp.vue`、
  `components/EpisodeGrid.vue`、`services/api.ts`、`internal/player/service.go`、`internal/mpv/*.go`、
  `internal/danmaku/*.go`、`app.go`、`bindings_watchstore.go`、`bindings_suggest.go`、
  `internal/watchstore/store.go`、`internal/native/actions.go`、`verification/{player,danmaku_toggle,history_local_play}_verify.py`。
- **勘误（任务书 vs 仓库现实）**：任务书点名的 `client-v2/frontend/src/components/DanmakuLayer.vue` **不存在**
  （`frontend/src` 全量 find 无此文件）。v2 弹幕渲染定案 = mpv ASS 字幕轨，DOM 不画弹幕
  （PlayerView.vue:8 头注释 + 代码里无任何弹幕 DOM，属实）。本文按现实对账。
- 结论标签：✅=与 v1 行为一致；⚠=有实现但语义/参数与 v1 不同；❌=缺失或错；➕=v2 新增（v1 没有）。
- 规矩：v1 注释/docstring 不当规格，全部以代码行为为准；两处「注释与代码不符」的坑单独列出。

---

## 表 1 — v1 行为清单（file:line）

### 1.1 播放控制

| 行为 | v1 出处 | 细节（以代码为准） |
|---|---|---|
| 播放/暂停 toggle | player.py:1740-1765 | `_pause_intent` 本地意图位（mpv pause 属性滞后 80ms 会吞连点，1149-1151）；`set_paused` 相同意图直接 no-op 返回 False |
| 停止 | player.py:2254-2292 | `stop()`：弃用在建会话 token、杀 mpv、复位全部状态、`reset_danmaku`、`pauseChanged(False)`；用户主动停且全屏 → 退全屏（2290-2292） |
| 进度条 seek | player.py:1200, 2344-2353 | 拖动 sliderReleased → `["seek", v, "absolute+exact"]`（**精确帧**）；拖动期间只更新时间 label（2347-2348） |
| 键盘 ±5s seek | player.py:1773-1775 + main.py:1766-1767, 1802-1805 | `["seek", ±5, "relative"]`，仅 `is_playing()` 时执行；状态栏「快退/快进 5 秒」2s |
| 倍速档位 | player.py:61 | SPEED_CHOICES=(0.5,0.75,1.0,1.25,1.5,2.0,3.0)；底栏按钮单点循环 cycle_speed（1777-1779），覆盖条弹层选择（1825-1828） |
| 倍速设定 | player.py:1870-1879 | set_speed = 吸附最近档 + 应用到会话 + **变更即写 QSettings player/speed**；默认 1.0、启动恢复（77-93, 1306） |
| 音量 0-100 | player.py:1899-1913 | int(round) 钳 0-100；**不落盘**（_volume=100 每次启动，1308）；音量>0 时自动解除 mute（1909-1911） |
| 音量步进 | player.py:1321-1322 + main.py:1724 | ↑/↓ 每档 ±5；覆盖条滑条 singleStep 5（817） |
| 静音 | player.py:1918-1923 | mpv `mute` 属性，音量值不动；M 键 + 覆盖条「静音/已静音」按钮（808-812） |
| 循环 | — | **v1 没有单集循环**（无 playlist loop、无 A-B）；EOF 由 keep-open 定格 + finished 信号走连播（见 1.4）。v1 也无循环设置项（全仓 grep loop 无播放循环语义） |
| mpv 启动参数 | player.py:374-392 | `--no-config --no-terminal --idle=yes --force-window=no --keep-open=yes --hwdec=(auto-safe|no 读 tools) --video-sync=display-resample --network-timeout=30 --cache=yes --cache-secs=60 --demuxer-lavf-o=(seg_max_retry=5,allowed_extensions=ALL,strict=experimental,protocol_whitelist=[http,https,tcp,tls,crypto,data,file] | 本地时 [file,crypto,data]) --input-ipc-server=<pid+时间戳管道名> --log-file=<data>/logs/mpv.log --wid=<surface.winId> --http-header-fields=<K: V 逗号拼接>`；有 key 时 demuxer-lavf-o 追加 decryption_key |
| 退出保存时机 | main.py:2466-2493 + 2788-2820 | 关播放窗→stop_playback：`_remember_progress()`（存当前集位置）→ player.stop → 清预热 → `watch.flush()` **立刻落盘** → `core.release(session)`；主窗 closeEvent 同款兜底 + `player.stop(blocking=True)` 防孤儿 mpv |
| 去抖 | main.py:2500-2503 | 播放中历史落库节流：**纯时间轴 ≥5s**（位移不参与判定） |
| 窗体几何记忆 | playback_dialog.py:24-25, 79-90 | QSettings player/geometry saveGeometry/restoreGeometry（全屏中不存） |

### 1.2 进度与历史

| 行为 | v1 出处 | 细节 |
|---|---|---|
| 位置上报 | player.py:1336-1339, 2314-2342 | QTimer 500ms（内部 0.2s 下限护栏 2317-2320）；`positionChanged(position, duration)` 每 tick 发 |
| 写 watchstore | main.py:2495-2509 | ≥5s 节流 → `save_watch(drama, pos+1, position, duration, False)`；local_path 会话走 `save_local_watch` |
| save_watch 语义 | watchstore.py:457-512 | 内存恒更新；<0.5s 同集同位置同 **去重跳过**；duration<=0 沿用旧值；HISTORY_CAP=300；follow 行 known/在看/已看状态机；**finished=True 立即 _commit()**，非 finished 每 SAVE_INTERVAL=30s 落盘（22-23） |
| save_local_watch 语义 | watchstore.py:608-637 | 同名 local_path 去抖；**每次都 _touch() 立即落盘**（比 save_watch 更实） |
| 起播即记 | main.py:2266-2270 | `_start_plan` resolve 成功后**立刻** `save_watch(resume, 0.0, False)`（保证「看到第 N 集」即使只看几秒也进历史） |
| 换集前落盘 | main.py:2190-2191 | `play_episode` 开头 `_remember_progress()`：手动切集不丢上一集刚看的几分钟（finished=False） |
| 播完定案 | main.py:2511-2534 | EOF → `_watch_last_save=0`；`save_watch(..., duration, duration, True)`（**finished milestone，立即落盘**）；local 会话 `save_local_watch(...True)` |
| 续播链 | main.py:1767-1774（open_detail pending）/ 2671-2678（_play_from_watch）/ 2609-2636（open_local） | 历史/追剧行 position → `play_episode(resume=)` → `player.play(resume=)` → file-loaded 且 **>1.0s** 才 `["seek", resume, "absolute"]`（**非 exact**，1701-1704） |
| 本地成品续播 | watch.py:636-645 → main.py:2647-2648 | history.local_path 且文件在 → `open_local(path, resume)`；文件不在 → 「本地文件已不存在」不回落线上 |

### 1.3 选集与连播

| 行为 | v1 出处 | 细节 |
|---|---|---|
| 选集 UI 双份 | main.py:1711-1751（左栏 EpisodeList 240px）+ player.py:1280, 1015-1113（右停靠栏 EpisodeDock 300/36px） | 停靠栏：有数据后**首次自动展开**（1066-1068）；点格 = 1-based → 0-based episodeClicked（1983-1990）；`set_current` 切分组+滚到可见（1070-1077） |
| 停靠栏「已看淡化」 | player.py:1061-1065, 1082-1088 + widgets_guo.py:507-518 | ⚠ 注释（1018-1019）声称淡化由 EpisodeGrid 负责，但 dock 喂的是**核心 chapters 原样 dict**、只补 `current`；chapters 里是否带 watched **未核实**（v1 详情页的置浅是 detail_episodes 端自己算的，224 行）。停靠栏的 watched 淡化大概率不生效——别按注释当真 |
| 上下集禁用 | player.py:1767-1771 + main.py:2223 | `set_neighbours(position>0, position+1<len(chapters))`；本地文件 set_neighbours(False,False)+清空选集条（2628-2633） |
| 播完自动下一集 | main.py:2523-2531 | 仅 `nxt < len(chapters)`；`QTimer.singleShot(0)` + `_play_request` 令牌防「停止/切集」竞态；无人为延迟（预热好的 loadfile replace 秒开） |
| 连播开关 | — | **v1 没有连播开关**，播完必自动连播（末集除外）。v2 zgj.player.autoplay 是 ➕ |
| EOF 跨季 | main.py:2532-2607 | 本季播完：标题「本季播放完毕，正在查下一季…」→ catalog lane=series:next、900s 缓存、prompted/busy 幂等 → 找到：追剧/预约中则 `mark_next_season`（2596-2599）+ **QMessageBox「现在去观看吗」Yes→open_detail**；没找到：状态栏 6s「没查到下一季，可在详情页预约」。**绝不自动跨季播** |
| 切集画面过渡 | player.py:1397-1497 + main.py:2192-2194 | 解析下一集**之前** `capture_poster()`（screenshot-to-file 存当前帧）→ 换源期间用 VideoPoster 冻帧 + 转圈 pill（1450-1456）→ 新会话 time-pos 走表 70ms 后换回原生窗（1465-1497），兜底 3s |
| loadfile replace 复用 | player.py:597-620, 1512-1515, 1562-1631 | reuse=True 且会话活着：同一 mpv 进程 `loadfile replace`，换源前清 `time-pos/eof-reached` 观察缓存 + **sub-remove 旧 ASS 防串画**（1597-1602）；keep-open 的暂停不在这里解，等 file-loaded（1603-1606, 1693-1697） |
| 预缓冲下一集 | player.py:1993-2013 + main.py:1854-1866, 2295-2397 | file-loaded 后按集去重钩 preload（1699-1700）；`_preload_next` 走 core `preload`，按 (剧,集,画质) key 缓存 plan，`_take_preload` 消费（本地 plan 查文件在否、会话查 expiresAt/20 分钟）；过期/失配 → `core.release` 归还；同剧同画质最多 PRELOAD_LIMIT 份 |

### 1.4 EOF / 错误

| 行为 | v1 出处 | 细节 |
|---|---|---|
| EOF 三通道 | player.py:485-489, 1713-1717, 1730-1737, 2339-2342 | end-file reason=eof（`_stopping/_eof/_awaiting_file_load` 护栏）、property eof-reached、tick 内 cached(eof-reached) 兜底；`_eof` 每集一次性 |
| loadfile replace 的旧文件 end-file | player.py:1715 | `_awaiting_file_load` 抑制「换文件时旧文件的 end-file eof」误报（reason=stop 不算） |
| 播放中文件错误 | player.py:1718-1729 | end-file reason=error → 撤海报 + `failed("播放失败：<原因>")`，原因取事件 → mpv 日志尾 → 「播放器没有给出原因，可查看 logs\mpv.log」 |
| 起播失败 | player.py:357-363, 409-421, 1633-1642 | 缺 vulkan-1.dll 预检（人话文案）；启动失败拼 `_why_died`（退出码/0xC0000135/stderr 尾/日志尾） |
| 断线重建 | — | **v1 没有 mpv 掉线自动重建**（进程死了就报错回占位）；v2 的 rebuild≤3 是 ➕ |

### 1.5 弹幕

| 行为 | v1 出处 | 细节 |
|---|---|---|
| 开关 | player.py:1208-1227, 2075-2093 + main.py:1873-1878 | 双持久层：QSettings player/danmaku_enabled（**变更即存**）+ tools.app-settings `danmaku`（默认 True，tools.py:271-273）；启动 load_player_prefs 播种/回写同步（player.py:87-92, 1213-1217）。开→`_ensure_danmaku` 补当前集 |
| 开=显隐，非挂撤 | player.py:2095-2097 | `sub-visibility yes/no`，轨留在 mpv 上 |
| 渲染机制 | danmaku.py:1-8 + player.py:2107-2126 | 不走 Qt 覆盖层：窗口抓取 → build_ass → 写固定名 .ass → `sub-add <path> select 弹幕`；增量 = 原地重写 + `sub-reload` |
| 抓取窗口 | main.py:698-701, 1987-2005 + danmaku.py:16,19 | QTimer 1s pump；**门控=tools.danmaku_enabled() 且 session 活着（暂停也照抓**，player.py:1366-1372 的 is_playing=进程活着）；候选=当前 30s 对齐窗 + 下一窗，已抓集合跳过；窗口间限速 MIN_GAP_SECONDS=3.0 |
| 窗口入参 | danmaku.py:47-59 + main.py:2033-2034 | core `danmaku` 动作：`startMs=<窗起点>`，`durationMs=<整集 span + bust 重试偏移>`（**绝对窗尾语义**，核心校验 app_danmaku.go:10 `durationMs>startMs`）；items 按 timeMs 排序；重试换 duration 绕核心 5 分钟缓存 |
| 抽样检测/重试 | main.py:2052-2061 | `expected=min(90, total*30000/span)`，条数<期望一半且 expected≥20 → tries<2 重试，冷却 `last=now+12s` |
| 失败退避 | main.py:2068-2083 | 同窗连错 1→10s、2→30s、≥3 放弃并记窗（note「本集弹幕加载失败」+状态栏 6s）；空窗且全无 item → note「本集没有弹幕」 |
| 等时长 | main.py:1965-1985 | 250ms×40 次等 mpv duration（≥5s 才算拿到），超时 note「没拿到时长，弹幕没加载」；`size = player.video_size()`（**实际视频宽高**，竖屏 1080×1920 用真实画布，player.py:2064-2073 观察 width/height） |
| ASS 观感参数 | danmaku.py:23-29, 131-168 | FONT_RATIO=短边×0.045（下限 24）；AREA=上半 0.5；lanes=min(12,max(6,高×0.5/行高))；滚动 8.0s + LaneGap 0.25s 轨道占用、全占用**丢条**；MAX_RUNES=60 截断；`{\move(W+40,top,-w-40,top,0,8000)}`；start>时长+5s 丢弃 |
| 不透明度 | player.py:2130-2188 + main.py:703-712, 1894-1896, 1954-1963 + tools.py:320-337 + danmaku.py:131-139, 175-184 | 滑条 20-100（Qt.Popup 竖条）；**百分比/100=opacity → alpha=round((1-opacity)*255)** 写进 ASS 颜色 alpha（主/副/描边/阴影四个 alpha 同源）；重建有 120ms 去抖、落盘 500ms 去抖；持久 tools `danmakuOpacity` 默认 100 |
| .ass 临时文件 | danmaku.py:171-192 | 固定名 `<temp>/zhenguojian-danmaku/<key净化>.ass`（key=剧id-集号），原子写，按 mtime 保留 6 份 |
| 条数显示 | player.py:2190-2196 | 弹幕按钮 tooltip「弹幕开关（D） · 本集 N 条 / <note>」 |
| 本地成品旁挂 | main.py:1880-1926, 2286-2288, 2638 | 下载保存过 → 同名 .ass 直挂（`.danmaku.json` 数条数，读失败不拦挂轨）；本地播放查两个候选（plan.url 缓存原件 + library.published_path 成品）；没有旁挂 → **回落到线上逐窗抓**（用 resolve force 建临时弹幕会话，2024-2031，用完 release 2110-2123） |
| 下载时保存弹幕 | library.py:413-475 + tools.py:308-316 | 默认关；跳过条件=.ass 非空且 .danmaku.json 在；resolve(force=True, session="danmaku-collect") → **`danmakuAll` 带 startMs=0&durationMs=时长**（danmaku.py:62-75，核心校验 app_danmaku_all.go:181）→ 空集不写文件 → .ass（entry.width/height 或 1920×1080）+ .danmaku.json（document 形状 episodeId/seriesId/total/fetchedAt/items，indent=1） |
| 发送/屏蔽词 | — | **v1 没有**：danmaku.py 只有抓取，PlayerPane 无输入框（player.py:2021-2023 的 QLineEdit 守卫是防御性注释「若将来出现」——这类注释不是规格）。本地无发送弹幕、无屏蔽词过滤 |
| 快捷键 D | main.py:1769, 1869-1871 | PlaybackDialog 窗口级绑定（**帮助卡 ROWS 874-881 没列 D**，按钮 tooltip 列了）；受 `_player_keys_allowed`（1786-1800：窗可见+焦点窗归属+焦点不在 QLineEdit/QTextEdit/QComboBox/QAbstractSpinBox，**刻意不排除按钮**） |
| ↑↓F M ? | player.py:1317-1329, 2016-2030 | WindowShortcut 带自己的守卫（多排除 QAbstractSlider）；空格/←→ **不在 player 注册**（main 已绑，双重触发防护见 1317-1319 注释+代码分工） |
| 帮助浮层 | player.py:861-1012 | 8s 无操作自动收（**mousemove 续期**，1009-1012）；点遮罩/再按 ?/Esc 关；持焦 + 停用同窗其它 Esc/? 快捷键（Qt ambiguous 双吞实测，939-956） |
| 全屏 | player.py:2198-2214 + main.py:2141-2175 + 1765, 1774-1779 | 双击画面（VideoSurface.mouseDoubleClickEvent 157-164）/ F / 按钮；PlaybackDialog.showFullScreen，隐藏侧栏+停靠栏、黑底 QSS、页边距归零；5s 鼠标空闲收控制条+藏光标（33, 2216-2248），指针悬在条上不收；Esc：全屏中=退全屏，否则=关窗（窗关=停播） |

---

## 表 2 — v2 现状（file:line + 判定）

### 2.1 播放控制

| 维度 | v2 出处 | 判定 |
|---|---|---|
| 播放/暂停 | stores/player.ts:565-587；service.go:529-548；mpv/player.go:301-315 | ✅ set_paused 意图位由 service.wantPaused + mpv 即时状态回写承担；EOF 时 togglePlay → seek(0)+play（v1 无 seek(0) 重播，行为略宽） |
| 停止/关闭 | stores/player.ts:731-752（persistWatch→closeVideo）；app.go:372 PlayerClosePlayer→service.Close 662-694 | ⚠ 语义=关会话（quit mpv），**不 release 核心播放会话**（见 ❌ 差集#6）；无「停止但留在页面」形态 |
| 进度条 seek | PlayerView.vue:127-142 + stores/player.ts:590-606 + service.go:552-566（AbsSeekSmooth= `seek absolute`） | ⚠ **不是 v1 的 absolute+exact**；mpv/player.go:322-325 有 AbsSetPosition（exact 版）但 service.MpvPlayer 接口 77-92 没带它 |
| 键盘 ±5s | PlayerView.vue:192-199（store.seek(position±5) 前端算绝对值） | ⚠ v1 是 mpv relative；v2 基于可能滞后 500ms 的快照位置算绝对 seek（连按/高倍速下有累积差）；且 v2 `e.repeat` 直接忽略（172）——按住连跳在 v2 不生效 |
| 倍速档位 | stores/player.ts:54 SPEED_CHOICES 同七档 | ✅ |
| 倍速持久化 | 无（setSpeed 608-620 不写 localStorage；boot 后 speed=1） | ❌ v1 变更即存+启动恢复（player.py:1878, 77-93） |
| 音量 0-100/±5 | PlayerView.vue:200-207, 456-468；player.ts:622-632 | ✅ 钳位/步进一致；➕ v1 不持久化音量、v2 持久 zgj.player.volume 并在 openEpisode 回灌（301） |
| 静音 | player.ts:634-642（音量 0 近似 + preMuteVolume 恢复，兜底 50）；头注释 18 自认近似 | ⚠ v1=mpv mute 属性音量不动；v2 无 mute 绑定；**控制条无静音按钮**（v1 覆盖条有，player.py:808-812），仅 M 键 |
| 循环 | 无 | ✅ v1 也没有 |
| mpv 启动参数 | mpv/player.go:222-258 | ✅ 逐项对齐 v1；➕ `--no-osc`（任务定案）；⚠ idle 起进程不带 URL、headers 改由 loadfile 前 set_property（等价）；❌ `--log-file` 从不传（app.go:53-68 Config 无 LogFile）→ v1 的 logs/mpv.log 诊断链没有；❌ HWDec 恒 false（Config.HWDec app.go 未设，v1 有 tools.hwdec + 侧栏勾选）；❌ 无 vulkan-1.dll 预检 |
| IPC 形态 | mpv/player.go:1-13 头注释, 534-566 超时重拨 | ➕/⚠ 与 v1「每命令一次性连接」策略不同（v1 是实测 mpv handler 卡死的规避）；真机验证项 §5 |
| 退出保存 | player.ts:731-741（shutdown 前 persistWatch，finished=eof‖pos≥dur-1） | ⚠ 见差集#1/#4/#10：无 EOF milestone、无换集落盘、无 `watch.flush()`（Go Store.Flush 存在但生产零调用，grep 全仓仅测试） |

### 2.2 进度与历史

| 维度 | v2 出处 | 判定 |
|---|---|---|
| 位置上报 | service.go:923-976（500ms loop→player:tick）；player.ts:409-431 事件驱动 + 500ms 轮询兜底 434-455 | ✅ |
| 写历史节流 | player.ts:500-509 maybePersistWatch：**位移≥5s 或时间≥5s 任一**；且 `!running‖paused‖eof` 不写（504） | ⚠ v1 纯时间≥5s、暂停也走 save（由 store 端去重）；v2 大跳 seek 立刻写、暂停不写，语义不等价 |
| Go save_watch | bindings_watchstore.go:296-307 → store.go SaveWatch（去重/duration 回落/CAP300/finished 即 commit/30s 节流） | ✅ 与 watchstore.py:457-512 逐条对齐（drama 改合并式，PORTING 已记） |
| save_local_watch | bindings_watchstore.go:325-341 → store.go:1445+（local_path 去抖 + 每次 touch 落盘） | ✅ 语义在；⚠ 不做 `_resolve_local` 文件名反查（改由调用方传 drama，注释 309-324 已声明） |
| 起播即记 | 无（openEpisode 265-303 不写；maybePersist 要 position>0&&duration>0） | ⚠ v1 resolve 成功就 save（main.py:2268-2270）；v2 看 <5s 就退出的集，靠 shutdown persistWatch（position>0 才写）——半途崩溃仍可能整条不落 |
| 续播链（线上） | bootFromQuery pos → openEpisode(...,startPosSec) → **PlayerOpen 返回后立即 this.seek**（302） | ⚠❌ v1 是 file-loaded 后 seek absolute；v2 前端 seek 与 loadfile 竞态（Go 的 pendingResumeMs 通道 829-840 只服务 rebuild/OpenLocal），真机待验 §5 |
| 续播链（本地成品） | 历史行 localPath → playerRoute lp（HistoryView.vue:81）→ openLocal（512-533）→ PlayerOpenLocal（bindings_watchstore.go:169-200：下载根包含校验 Rel/大小写折叠）→ service.OpenLocal ResumeMs→pendingResumeMs→file-loaded seek | ✅ 与 v1 open_local 语义一致且校验更严（v1 无根校验） |

### 2.3 选集与连播

| 维度 | v2 出处 | 判定 |
|---|---|---|
| 选集面板 | PlayerView.vue:530-551（300/36 折叠，CSS 812-823）+ EpisodeGrid.vue（GROUP 30、current 高亮、VIP） | ✅ 尺寸/分组/高亮对齐；⚠ 面板数据来自**进页再拉一次 detail**（loadChapters 227-243），v1 复用播放会话的冻结 chapters —— 连播中途详情失败则面板空（catch→[]，237） |
| 当前集高亮/落位 | scrollToCurrent 258-261 + EpisodeGrid current watch | ✅ |
| 播完连播 | player.ts:459-478 markEnded（autoplay && !demo && hasNext → playNext）；eofHandledAt 集级幂等 | ➕ 连播开关 zgj.player.autoplay（v1 无开关恒连播）；✅ 默认开≈v1 |
| 跨季 | player.ts:475-477 + nextSeason.ts（900s 缓存/prompted/busy）+ setNextSeason 回写（store 484-498；绑定 bindings_watchstore.go:274-284）+ PlayerView「去看下一季」按钮 404-414 | ✅ 除 v1 用 QMessageBox 打断、v2 用常驻按钮（(c) UI 机制差）；nextSeason.ts:9 注释「绑定待补」**已过期**（绑定与调用都在） |
| EOF「播完」态呈现 | videoShouldShow 88-91：eof → **隐藏原生画面** + DOM 播完层（399-416） | ⚠ v1 keep-open 留最后一帧、v2 收掉画面露 DOM（§5 真机体感项） |
| 切集过渡无冻帧 | 无任何 screenshot-to-file/海报实现（grep v2 Go 仅注释 mpv/player.go:433）；切集 loading 期间 setBounds(0,0,0,0) 隐藏视频（103-110） | ❌ v1 的 VideoPoster 冻帧链（player.py:1397-1497）整体缺席 |
| 预缓冲 | PlayerView.vue:271-288 preloadNext（opened+集号变化触发，preloadedEp 去重）→ PreloadEpisode（bindings_suggest.go:20）；**结果丢弃、无人消费** | ⚠ v1 有 plan 缓存 + 秒开消费（_take_preload）+ 过期释放；v2 只暖核心侧片头，每次 Open 仍全量 resolve；预载会话从不 release（并入 ❌#6） |

### 2.4 弹幕

| 维度 | v2 出处 | 判定 |
|---|---|---|
| 开关持久化 | player.ts:169（恒默认 true）；toggleDanmaku 677-689 **不写盘**；Go 初值 `enabled=DanmakuSource!=nil`（service.go:284） | ❌ v1 双持久（QSettings+tools）；v2 每次进程重启都开 |
| 开=挂撤 vs 显隐 | service.go:593-629：关=SubRemove、开=重渲染/SubAdd；mpv/player.go:460-467 SetSubVisibility **存在但 service 未用** | ⚠ 机制不同（效果大体等价：v1 关时轨留在内存，v2 撤轨重挂） |
| 抓取窗口/限速 | fetcher.go:13,17（30s/3s）；Tick 157-199（当前窗+下一窗、windows 去重、inflight 单飞） | ✅ |
| 抓取门控 | service.go:961-963：`enabled && session!="" && FileLoaded && !Paused && !EofReached` | ⚠ v1 暂停也抓（进程活着即可）；v2 暂停停抓（恢复后自然补，观感差异小） |
| 窗口入参 | service.go:113-138 + native/actions.go:102-108（danmaku 动作 durationMs=绝对窗尾）；fetcher.go:224 `span+bust` | ✅ 与 v1 main.py:2033-2034 同式 |
| 抽样重试/失败退避 | fetcher.go:263-281（expected 公式同款、tries<2、bust++）；229-249（failures 3 次放弃、RetryDelays 10/30s） | ⚠ 抽样冷却：v1 last=now+12s；v2 走 lastFail→retryDelayLocked=**10s**（202-212，failures=0 取[0]）——注释「≈3+9」与实际 10s 亦不符（v2 自己的注释坑） |
| note 文案 | fetcher.go:242,244,288（本集弹幕加载失败/稍后重试/本集没有弹幕） | ✅ 逐字同 v1；❌ 缺「没拿到时长，弹幕没加载」路径（v2 durMs<=0 只静默不抓，service.go:947-952/1021） |
| ASS 观感参数 | ass.go:16-28,108-200（常量/公式逐字对齐 v1 danmaku.py） | ✅（顶部/底部持留、逐条颜色/字号为声明过的 v1 超集 ➕） |
| 画布=实际视频尺寸 | service.go:1028 BuildAss **不传 Width/Height**（恒 1920×1080 默认，ass.go:44-50）；mpv 观察表无 width/height（mpv/player.go:111） | ❌ v1 用 `player.video_size()`（main.py:1984）：竖屏剧 1080×1920 真实画布；v2 竖屏剧弹幕字号/轨道/滚动按横屏画布折算，形变待真机确认 §5 |
| 不透明度→渲染 | PlayerView.vue:316-319（@input 每像素直发）→ api 290 percent/100 → service.SetDanmakuOpacity 643-660 → refreshTrack 重渲染+sub-reload；ass alpha 公式一致（ass.go:226-229） | ✅ 数值语义；⚠ **无 v1 的 120ms 重建去抖 + 500ms 落盘去抖**（main.py:703-712）；拖一下=几十次全量 BuildAss+写盘+sub-reload（都持 service 锁、同步 IPC） |
| 不透明度初始回灌 | Config.DanmakuOpacity 在 app.go:53-68 **未设**；前端只在 toggleDanmaku 开启瞬间回灌（685） | ❌ 进程重启后弹幕默认开 → 用户存值（如 40）直到手动开关一次前都按 1.0 渲染 |
| .ass 文件与保留 | ass.go:292-314（同 key 原子重写、keep 裁剪，默认 6）；目录 AssDir→DataDir/ass（service.go:338-347） | ✅（目录 v2 优先 DataDir/ass，v1 恒 temp；键净化规则逐字同，ass.go:316-330） |
| 条数/说明呈现 | PlayerView badge 377-378；snapshot.count/note（service.go:705-742）；danmaku:stats（1003-1014） | ✅ |
| 本地旁挂 .ass 挂载 | OpenLocal→attachSidecarLocked（511-526）+ DetectSidecar sidecar.go:84-102（非空 .ass、json 缺失不拦、items 优先数条数）；重开补挂 SetDanmakuEnabled 616-623 | ✅ 与 v1 _attach_sidecar_danmaku 同语义 |
| 「下载成品走线上流播放」的旁挂 | Open（353-428）：plan.Local 时 **只清 sidecar 不挂载**（395-397），session=="" 不设 fetcher 窗口（415-421） | ❌ v1 main.py:2286-2288：本地 plan 先试旁挂（双候选路径）、没有则临时 resolve 建会话线上抓；v2 两件事都没做，且 snapshot 弹幕计数回落旧集 fetcher 状态（725-734）——下载过的集在 v2 无弹幕 |
| danmakuAll 契约（下载保存） | internal/native/actions.go:114-120 只发 playbackSession；downloadsvc/sidecar.go:330-345 同；**且 sidecar_test.go:177-183 把「不收 durationMs」钉成断言** | ❌ 核心源码 app_danmaku_all.go:180-182 要求 `DurationMS>0 且 ≤24h` 否则「弹幕时间范围无效」；v1 danmaku.py:71-72 传 startMs=0+durationMs。真核心下整条下载保存弹幕必失败；CI 却钉着反的契约（§4） |
| 发送/屏蔽词 | 无 | ✅ v1 也没有（别造假差异） |
| 多屏/DPI 坐标 | PlayerView reportBounds 103-120（getBoundingClientRect×devicePixelRatio，客户区坐标）→ embed_windows.go:232-250 MoveWindow（父窗客户区坐标）；WM_NCHITTEST ScreenToClient 判定 embed_windows.go:78-110 | ✅ 设计自洽（DPI 物理像素换算有注释链）；多屏/异 DPI 实际表现 §5 真机项 |

### 2.5 画质与线路

| 维度 | v2 出处 | 判定 |
|---|---|---|
| 清晰度档位 | QUALITY_CHOICES auto/1080/720/540（player.ts:43）→ PlayerOpen quality 形参（app.go:326-356 playerQualityCode 310-322）→ Request.Quality→native.Resolve | ✅ 档位与解析入参对齐 v1 |
| 切换语义 | player.ts:644-665：存偏好 + **立刻重开当前集原位续播**；PlayerView.vue:25-27 头注释仍写「PlayerOpen 无 quality 形参」——**注释过期**（绑定早已带参） | ⚠ v1 是「本集不动、下一集生效」（main.py:1835-1846），v2 即时重开——两边都是自洽设计，需定谁为准（差集 §3-b1） |
| 多线路 fallback | 无（routeBox/fallback 全 v2 前端与 Go 皆无；native 无 Fallback 函数；核心 app_runtime.go:536 仍支持 "fallback" 动作；v1 core_client.py:494-495 + main.py:2399-2414） | ❌ 整条「线路 N 下拉 + 切线重开」缺失 |
| headers 传递 | mpv/player.go:275-299 set_property http-header-fields "K: V" 逗号拼接 | ✅ 语义一致；⚠ Go map 遍历序随机 vs v1 dict 插入序（仅在服务端挑头顺序时有差，未核实核心是否敏感） |
| 解析失败提示 | service.go:363-378（60s 硬超时+「上游可能限流…」人话）；app.go:302-304 空地址报错；bootFromQuery catch→error（249-258，bad code 不吞成演示） | ✅/➕ v1 只有状态栏「解析失败：<msg>」8s（main.py:2243-2253）；v2 错误常驻+重试按钮（PlayerView 387-393） |

### 2.6 快捷键

| 键 | v1 | v2 | 判定 |
|---|---|---|---|
| Space | main.py:1768（守卫 1786-1800，**不排除按钮**） | PlayerView.vue:187-191（preventDefault 防聚焦按钮双触发） | ✅ |
| ←/→ ±5 | main.py:1766-1767 | 192-199 | ⚠（绝对 vs relative、repeat 吞、v2 播放窗外也生效——见下守卫） |
| ↑/↓ 音量 | player.py:1321-1322 | 200-207 | ✅ |
| F 全屏 | player.py:1323 | 208-211 | ✅ 键位同，行为面差大（§2.7） |
| M 静音 | player.py:1324 | 212-215 → 音量 0 近似 | ⚠ 同 §2.1 |
| D 弹幕 | main.py:1769 | 216-219 | ✅（v1 帮助卡没列、v2 列了——v2 更诚实） |
| ?/Esc 帮助 | player.py:1325 + 929-1007 | 220-222 + 174-184 + HotkeyHelp.vue | ⚠ v2 8s 固定定时器不随 mousemove 续期（HotkeyHelp.vue:24-28 vs player.py:1009-1012）；Esc 次序：v1 帮助→退全屏→关窗；v2 帮助→退全屏→关窗（含浮层记账 overlays.ts，✅） |
| 守卫 | v1 双套：主窗级 `_player_keys_allowed`（窗可见+焦点窗归属+四类输入）；Pane 级另加 QAbstractSlider | v2 `isTypingTarget` INPUT/TEXTAREA/SELECT/contentEditable（164-169），**Escape 先于守卫处理**（174-185） | ⚠ v2 里进度条/音量条是 `<input type=range>` → 聚焦时所有非 Esc 键全被当「输入中」吞掉（浏览器原生会挪滑条、v2 不 seek/不调音量）；v1 主窗级守卫不含 slider → Space/←→ 在 slider 聚焦时照常生效。差集 §3-b6 |
| Ctrl/Meta/Alt 组合 | v1 未显式排除 | v2:172 排除 | ➕ 无害 |

### 2.7 窗口/全屏

| 维度 | v1 | v2 | 判定 |
|---|---|---|---|
| 载体 | 独立 PlaybackDialog（Qt.Window 非模态，几何记忆，关窗=停播）playback_dialog.py:33-127 | SPA 路由页；关/返回=shutdown+router.back（PlayerView 321-325, 358-367） | (c) 机制差；「关=停播+落盘」两端一致 |
| 全屏实现 | Qt showFullScreen + 沉浸 QSS + 侧栏/停靠栏隐藏 + 控制条 5s 自动收 + 光标收 | `window.runtime.WindowFullscreen`（Wails runtime，**非浏览器 API**；垫片缺失时明示不可用，PlayerView 150-161） | ⚠❌ v2=整窗铺满，DOM 头/底条常亮、选集面板不收、无自动隐、无光标收；isFullscreen 本地布尔，系统级旁路退全屏不跟手（无事件回读） |
| 双击画面全屏 | player.py:157-164 | **无**；单击=togglePlay（v1 单击什么都不做） | ❌/➕ 混：v2 少双击全屏、多单击播停（§3-b5） |
| 失败提示 | 无（Qt 必成） | hint「不可用」8s（156） | ✅ 不假成功 |

---

## 表 3 — 差集（按可修/需决策/不该照搬）

### (a) v2 缺/错，实现者可直接修

| # | 差异 | 证据 | 最小修法 | 钉住它的断言 |
|---|---|---|---|---|
| a1 | **EOF 不落 finished 里程碑**：连播/播完既不 `save_watch(…,True)` 也不即时落盘；「已看」状态只在关页时按当前集补 | v1 main.py:2511-2522 + watchstore.py:504-507；v2 player.ts:459-478（无 persist）、736-738（仅 shutdown） | `markEnded()` 里 `eofHandledAt` 判定通过后先 `await this.persistWatch(true)`（local 会话同路径自然走 saveLocalWatch finished=true），再做连播/查季 | Go 面无钩子→新增前端缝断言：player_verify 扩一条「seek 到片尾触发 ended 后 localStorage 历史行 position≈duration」；更实的钉子是给 watch store 记 finished=true 后断言 `api.saveWatch(...,finished=true)`（`window.__zgj.*` 计数缝） |
| a2 | **换集/关闭不存上一集**：`openEpisode` 前无 persistWatch(prev)（v1 `_remember_progress` 同款） | v1 main.py:2190-2191；v2 player.ts:265-303 | openEpisode/playNext/playPrev 开头对**旧会话**补一次 `persistWatch(finished=a1 已记则跳过)` | 同缝：连播后检查 ep N-1 行 position 已更新；Go 侧无必要 |
| a3 | **倍速不持久化**（v1 变更即存、启动恢复） | v1 player.py:77-93,1878；v2 player.ts:608-620 无 LS 写 | 加 `zgj.player.speed`：setSpeed 写、bootFromQuery/openEpisode 起手 `api.setSpeed(stored)`（放 attach 后） | player_verify：设 2.0→reload→`store.speed===2` 且 mock 快照 speed=2 |
| a4 | **弹幕开关不持久化**（v1 QSettings+tools 双层，默认 True） | v1 player.py:2086-2092；v2 player.ts:169,677-689 | 加 `zgj.player.danmaku`：toggle 写盘；boot 时若为 '0' → `api.setDanmaku(false)`（attach 后、首快照前） | player_verify：关→reload→按钮态 off；Go 面 TestDanmakuToggleOffOn 已有，前端补一条即可 |
| a5 | **不透明度不回灌初始值**：进程级 opacity 恒 1.0 起步，用户存 40 也无效直到手动开关 | v1 player.py:1230 从 tools 读；v2 app.go:53-68 未设 Config.DanmakuOpacity + player.ts 只在开启瞬间推 685 | 两选一：openEpisode 成功后无条件 `api.setDanmakuOpacity(this.danmakuOpacity)`；或 app.go 从 v2 设置存储读初值填 Config（推荐前端一行，Go 少一条存储耦合） | Go 已有 TestDanmakuOpacityRendersAndClamps；前端缝断言 PlayerOpen 后 snapshot.danmaku.opacity===存值/100 |
| a6 | **danmakuAll 契约错**：v2 不发 durationMs，真实核心必报「弹幕时间范围无效」；且 v2 自己的测试把错的钉成对的 | 核心 app_danmaku_all.go:180-182；v1 danmaku.py:62-75（startMs=0,durationMs=时长）；v2 actions.go:114-120、downloadsvc/sidecar.go:330-345、**sidecar_test.go:177-183** | `native.DanmakuAll(session, durationMs)` 带上 `startMs=0、durationMs=时长`（sidecar 已有 req.DurationMs 可传）；同步改 sidecar_test 断言为「durationMs>0 必发」；app.go DanmakuAll 绑定形参加 durationMs | sidecar_test.go:177-183 反向修正即断言本体；再加一条 handler `payload["durationMs"]<=0 → t.Fatal` |
| a7 | **ASS 画布恒 1920×1080**：不观察 width/height、不传实际视频尺寸（竖屏剧观感形变风险） | v1 main.py:1984 + player.py:1665,2064-2073；v2 mpv/player.go:111 观察表 + service.go:1028 | observedProperties 加 "width","height"，State 带上；BuildAss AssOptions{Width,Height} 用实测值（未出画回落默认） | Go：harness fake IPC 注入 width=1080,height=1920 → 断言产出 ASS 头 PlayResX:1080/PlayResY:1920、字号=1080×0.045 |
| a8 | **旁挂 .ass 在「线上流播已下载集」路径缺失** + 该路径弹幕计数显示上一集残值 | v1 main.py:2286-2288,1897-1926；v2 service.go:395-397,415-421,725-734 | Open 里 `plan.Local` 分支补 `attachSidecarLocked(plan.URL)`（v2 只有单候选路径即可，见下 b7 关联）+ 非 sidecar 且 session=="" 时 `fetcher.SetEpisode(Episode{})` 清残值 | Go：local_test 加「Open(Plan{Local:true,url:<带 .ass 的临时文件>}) → SubAdd 记录」；断言 snapshot.danmaku.count 不来自上一集 |
| a9 | **进度条 seek 非精确帧**：absolute 代替 absolute+exact | v1 player.py:2353；v2 service.go:552-566 + mpv/player.go:322-330 | MpvPlayer 接口加 AbsSetPosition；`SeekTo(posMs)`（UI/条语义）走 exact，rebuild/resume 的 pendingResumeMs 保持 AbsSeekSmooth（v1 resume 本就是非 exact） | Go harness：断 SeekTo → 命令面收 `["seek",X,"absolute+exact"]`；重建路径仍 "absolute"（TestDisconnectRebuildResumesAfterFileLoaded 不受扰） |
| a10 | **退出/关闭不 flush watchstore**：非 finished 写按 30s 节流，可能丢 ≤30s（v1 stop 与 closeEvent 都 flush） | v1 main.py:2483, 2818；v2 store.go:730 Flush 生产零调用（grep） | App 生命周期钩子 shutdown（或 PlayerClosePlayer 成功路径）调 `getWatchStore().Flush()` | bindings_watchstore_test.go：SaveWatch 后 Flush → 文件 mtime/内容含新行（无 Flush 时断言旧内容，防假绿） |
| a11 | **无 mpv 日志/失败细节**：不传 --log-file，end-file error 只发「播放失败」裸词 | v1 player.py:384-386,1725-1728,344-355；v2 service.go:852-854 + app.go:53-68 无 LogFile | Config.LogFile=dataDir/logs/mpv.log（mpv 包已支持）；end-file error 拼 message 里带上事件 error 字段 | Go：fake 事件载荷 {reason:"error", error:"403"} → player:error.message 含 "403"；LogFile 传递断言 spawn args |
| a12 | **「没拿到时长，弹幕没加载」note 缺失**：durMs 恒 0 时静默不抓 | v1 main.py:1976-1981；v2 service.go:1021, fetcher.go:162 | Open 后 N 秒（如 10s）durMs 仍 ≤0 且 enabled → fetcher 置 note，emitStats 出去 | fetcher_test 形状：无 duration 的 Episode 超时 → Stats().note=="没拿到时长，弹幕没加载" |
| a13 | **帮助浮层 8s 不随鼠标续期** | v1 player.py:1009-1012；v2 HotkeyHelp.vue:24-28（一次性 setTimeout） | @mousemove="重置计时"（3 行） | player_verify ② 加「拖动鼠标 8s 后仍在、静止 8s 后关」 |

### (b) 需要人类决定（谁为准 / 是否补功能 / 契约取舍）

| # | 议题 | 两侧事实 | 建议 |
|---|---|---|---|
| b1 | **清晰度切换语义**：v1 下一集生效 vs v2 立即重开当前集续播 | v1 main.py:1835-1846；v2 player.ts:644-665（且其注释自称对齐 `_switch_route`，实为把「线路」语义安到了「画质」上） | 二选一并改另一侧或改注释；若保留 v2 即时重开，顺带解决 a9（重开首帧 seek 竞态另计 §5） |
| b2 | **线路（fallback）切换要不要移植** | v1 main.py:1713-1715,2399-2414 + core 支持；v2 全无 | 补：native.Fallback → App binding → 播放页小下拉；不补则记录为有意裁剪 |
| b3 | **hwdec 开关**：v1 侧栏勾选（tools.hwdec，默认关，下集生效）；v2 恒关无 UI | v1 main.py:1723-1726,1817-1821 + player.py:378；v2 app.go:53-68 未设 HWDec | 移植成设置项或播放页勾选；至少先让 Config.HWDec 有来源 |
| b4 | **切集冻帧海报（VideoPoster 链）是否移植** | v1 player.py:1397-1497 整套；v2 只有 DOM「正在打开播放会话…」且把画面藏掉 | 真机看过再定：v2 若不做 screenshot-to-file，至少别在换集时 setBounds(0,0,0,0) 把 keep-open 的残帧也藏掉（现状等于双倍黑） |
| b5 | **画面单击/双击语义**：v1 单击无动作、双击全屏；v2 单击=播停、无双击 | v1 player.py:153-164；v2 PlayerView.vue:385 | 建议 v2 改「单击播停 + 双击全屏（单击去抖 250ms）」或维持 v2 现状但明确记为有意差异 |
| b6 | **range 输入聚焦吞快捷键**：v2 isTypingTarget 把 `<input type=range>` 当输入框 | v1 主窗守卫不排滑条（main.py:1798 四类不含滑条）：进度条聚焦时 Space 照样播停、← →照样 seek；音量条聚焦时 ↑↓ 由滑条本体调音量。v2 聚焦态实测语义：**音量条键盘方向键有效**（native @input→applyVolume），**进度条键盘无效**（拖拽标志未置位，@change 早退 PlayerView.vue:138-142，store 快捷键又整排被 typing 守卫吞），**Space 聚焦进度/音量条时失效**（v1 有效） | 把 `INPUT[type=range]` 排除出 typing 判定（或仅让方向键落到滑条本体、Space 照常播停），并把进度条 @change 改为不依赖 pointerdown |
| b7 | **本地集「没有旁挂 .ass 时回线上抓」**（v1 会 force-resolve 临时会话抓完 release）在 v2 是否补 | v1 main.py:2024-2031,2110-2123；v2 OpenLocal 明文不抓（service.go:440-448 注释） | 补=a8 的一半，代价是核心会话泄漏面（release 一起做，a10/差集 b 关联）；不补则写进 README 的有意差异 |
| b8 | **音量持久化**：v2 ➕（v1 每次 100） | v2 player.ts:625 vs v1 player.py:1308 | 倾向保留 v2；需人类点头算对齐 |
| b9 | **键盘 seek 语义（relative vs 前端算绝对 + e.repeat 禁用）** | v1 main.py:1802-1805；v2 PlayerView 172,192-199 | 建议 v2 加 App.PlayerSeekRelative→mpv relative（或 e.repeat 放行 ±5s） |
| b10 | **暂停中不抓弹幕** | v1 main.py:1992（进程活即抓）；v2 service.go:962-963 | 差在「暂停很久→恢复瞬间没弹幕」；建议对齐 v1（去掉 !Paused 门控只留 !EofReached? v1 连 EOF 后进程活着都抓——过界小） |

### (c) 平台机制差异，不该照搬；v2 的承担物

| v1 机制 | v2 承担同一职责的 | 备注 |
|---|---|---|
| pyqtSignal/槽（positionChanged 等 1120-1134） | `player:tick/ended/error/danmaku:stats` 事件 + pinia store（service.go:4-19 契约 + player.ts attach） | 语义映射完整 |
| QThread/worker + 一次性管道连接防卡死（MpvSession 全类） | goroutine 泵 + 长连接超时重拨（mpv/player.go:1-13,532-566）+ `call` 全部有界 | v1 教训（handler 卡死）是否复发只能真机看 §5 |
| Qt 自绘浮层（VideoPoster/ControlOverlay/HotkeyHelpOverlay/Popup 顶层窗） | DOM 组件 + mpv 子窗显隐/穿透带（embed.go/hitbands） | WM_NCHITTEST 穿透带解决的是 v1 在 Windows 上「原生窗盖住 Qt 叠层」的同题（player.py:778-780 自认遗留），是 v2 优于 v1 的 (c) 替代 |
| QSettings/QMessageBox（偏好/跨季询问，watchstore 文件） | localStorage（zgj.*）+ Go watchstore + 常驻按钮替代模态 | 跨季不自动播的共同底线保住了 |
| 全屏=整窗（含 chrome 隐藏、光标收） | Wails runtime.WindowFullscreen 整窗（DOM chrome 常亮） | 沉浸度差异归 b4/b5 同级讨论，机制本身不照搬 |
| mpv --wid 挂 Qt 原生子窗 | attach→ZgjVideoHost 子窗→MoveWindow 客户区坐标 | 坐标契约（dpr 物理像素）见 §2.4 表 |
| QTimer 500ms/1s/80ms 多定时器节奏 | Go 单一 500ms loop（事件+抓取+快照同拍） | 抓取节奏差异并入 b10 |

---

## 「哪些只能真机验证」（本机 Linux + fake mpv + mock 面永远验不到）

1. **mpv IPC 卡死复发**：v2 双长连接+超时重拨 vs v1「一次性连接」教训（mpv/player.go:6-12 自注「真机若仍卡再降级」）。暂停后连点、拖条、连播高频切集下看命令面是否僵。
2. **画面内嵌与穿透带**：多显示器/混合 DPI 缩放、窗口最大化、WebView2 客户区原点与 host 窗客户区原点错位；覆盖条按钮点击（HTTRANSPARENT 带）与 mpv 原生手势（单击暂停等 `--no-osc` 下的 input 默认绑定）互不互踩。`window.__zgjMockEmbed/__zgjHitBands` 缝只能验前端上报链，验不了 Windows 命中判定。
3. **`?pos=` 续播与画质重开的 seek 竞态**：loadfile replace 未 file-loaded 时前端 PlayerSeek 是否生效（v1 刻意延到 file-loaded）。mock/CI 用假时钟全绿不代表真 mpv 落位。
4. **竖屏短剧弹幕观感（a7）**：1080×1920 视频在 1920×1080 ASS 画布下 libass 实际缩放结果（字被压扁与否）只有肉眼在真 mpv 上看；同样含 keep-open 的「播完定格 vs v2 收画面」观感。
5. **真核心下的弹幕网络面**：抽样限速（3s）、bust 绕缓存、danmakuAll 契约（a6）在真核心/真红果响应下的形状；`danmaku:stats` 的「本集没有弹幕」触发。
6. **Wails runtime 全屏/事件注入**：`window.runtime` 垫片是否注入、`WindowFullscreen` 实际效果、`EventsOn` 断线与重连。
7. **缺 DLL/孤儿进程**：vulkan-1.dll 缺失场景文案（v1 有预检 v2 只有退出码）、退出时 mpv 是否真的被 quit→terminate→kill 收干净（Quit 有界，但 Windows 真进程树只有真机能验）。
8. **硬件解码/解码性能**：hwdec auto-safe 花屏黑屏反馈路径（v1 勾选存在的理由）。
9. **12h/会话过期**：核心侧 playbackSession 过期（app_danmaku.go:12）后的弹幕报错在 v2 note/退避链里的实际表现。
10. **keep-open 下 end-file 缺席**：v2 依赖 property eof-reached 兜底（无 v1 的 tick cached 三通道轮询兜底，v1 player.py:2339-2342）；极端 mpv 版本不发事件时 v2 会漏 ended——真机各 mpv 版本要跑一遍。

**mock 面下成立、别当真机结论的断言清单**（本仓 CI 绿≠行为对）：player_verify ①-⑧ 全部跑在 dev:mock+stub runtime；`__zgjLocalMissing/__zgjLocalOutside` 只是错误码注入缝（api.ts:825-853 自注「mock 没真实目录，不假装实现守卫」）；eof 位是 mock 扩展字段（types 注释 399-401，真实后端不发 snapshot.eof）；danmakuCount=80 是 fixtures 铺底；mockDanmakuAll/演示时钟；`v1 的 finished/commit/flush、抽样、MinGap` 全部只有 Go 单测的假时钟等价物。

---

## 我没查什么、为什么

1. **未逐行读 `internal/mpv/ipc.go`/`proc*.go`/`backend_windows.go` 全实现** —— 只需其对 player.go 承诺的接口面（超时/重拨语义已从 player.go 注释与调用点核实）；细枝是内核层，不是 v1 行为映射。
2. **未对账 mpv 原生鼠标/键盘绑定面**（单击暂停、拖动等）：v1 未禁用（无 input-forced bindings、player 只绑 Qt 侧键），v2 `--no-osc` 也仅关 OSC——原生行为两边都吃 mpv 默认，差异只可能来自内嵌命中带 → 归入真机项 §5-2，不在这抄录默认文档。
3. **未验证核心 chapters 行是否带 `watched/vip` 字段**（停靠栏「已看淡化」是否有米下锅）：需要跑真核心或翻 core 详情组装码；两版大概率同样不显示，未写死结论（表 1.3 标了未核实）。
4. **未对账下载页对 `danmakuDownload` 的开关 UI 两侧细节**：`danmaku_toggle_verify.py` 已覆盖 v2 面、v1 downloads.py:1095-1099 被逐字引用过，属下载面（另一 agent 的面）。
5. **未读 v2 `spike/`、`bindings/smoke`、`PORTING.md` 全文**：spike 是历史原型，产品码以 internal/ 为准。
6. **未跑任何 verify/单测**（任务规矩 1 只读）；文中所有「CI 钉住」的说法取自测试/脚本源码本身。
7. **音量>100（101-130）、A-B 循环、播放列表（playlist-*）、字幕/音轨选择**：v1 不存在这些行为（grep 无锚点），不构成差异。
