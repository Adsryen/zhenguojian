# internal/player ← v1 移植对照表

v1 = `zhenguojian/app/zhenguojian/`（PySide6 + QThread mpv IPC），v2 = 本包（Go，编排
`internal/mpv` 内核 + `internal/danmaku` 弹幕管线）。行号为撰写时 v1 现状。

## 生命周期

| v1 落点 | v2 落点 | 说明 |
| --- | --- | --- |
| `player.py:1562 _load_in_session` | `Service.Open` → `applySourceLocked` | 换集复用进程（loadfile replace）；换集先 `SubRemove` 防旧集弹幕串画，同 v1 |
| `player.py:1560-1595` 播放参数下发（speed/volume/pause 意图） | `spawnLocked`（load 前应用）+ `wantSpeed/wantVolume/wantPaused` | 意图字段常驻，重建后回放到新实例 |
| `player.py:1685-1714 _on_event` file-loaded → resume `["seek",s,"absolute"]`、pause 意图重放 | `handleEventLocked` `EventFileLoaded` | 续播只在 file-loaded 后发（不在 loadfile 后立即 seek）；`pendingResumeMs>1000ms` 才 seek，同 v1 阈值 |
| `player.py:1715-1730 end-file` reason=eof → `finished` 一次（`_eof` 去重）；reason=error → `failed.emit("播放失败：%s")` | `handleEventLocked` `EventEndFile` → `markEndedLocked` / `player:error` | `endedFired` 每集去重；reason=stop 等其他原因静默（loadfile replace 自带），同 v1 |
| `player.py` eof-reached 属性兜底（keep-open 下 end-file 不一定发） | `tick()`/`handleEventLocked` 的 `State.EofReached` 分支 → `markEndedLocked` | 与 end-file 双通道，共用 `endedFired` 去重 |
| v1 无断线自动重建（进程死亡=本集失败，用户重开） | `rebuildLocked` + `MaxRebuilds`（默认 3） | **v2 新增**：disconnected/exit 事件→重 spawn + 新 file-loaded 后 `AbsSeekSmooth` 续播；超限发 `player:error` 放弃。任务卡引用的 `player.py:2131-2139` 实为弹幕 tip 行，v1 该处无重建逻辑 |

## 弹幕管线

| v1 落点 | v2 落点 | 说明 |
| --- | --- | --- |
| `main.py:698-701` `_danmaku_timer` 1000ms | `Service.loop` `TickInterval` 默认 500ms | **有意偏差**：v2 同一 tick 还负责发 `player:tick` 快照（前端零轮询），取两者节奏的较小值 |
| `main.py:1928-1945 _queue_danmaku`（换集清零、span=0 等时长） | `Open` → `fetcher.SetEpisode(Episode{Session,DurationMs,Key})` + `durMs` 兜底链 | 时长：`plan.DurationSec` → mpv `duration` 属性（`syncEpisodeLocked` 拿到后补 SetEpisode） |
| `main.py:1965-1985 _await_danmaku_duration`（≤40 次轮询，超时 note「没拿到时长，弹幕没加载」） | `tick()` 拿不到时长不抓取（`durMs<=0` 门槛） | v2 靠属性事件驱动无需轮询；note 简化为「无提示不抓取」，时长最终取不到随 `player:error`/静音处理 |
| `main.py:1987-2005 _pump_danmaku`（span && enabled && is_playing 门槛；窗口对齐 `pos//W*W`） | `tick()` 抓取门槛（enabled && session && FileLoaded && !Paused && !Eof）→ `Fetcher.Tick(posMs)` | 对齐逻辑下沉 `internal/danmaku.Fetcher`（同 v1 公式） |
| `main.py:2007-2085 _fetch_danmaku_window`（按 id 去重、期望条数抽样 `bust++` 绕 5min 缓存、失败退避 10s/30s、≥3 次放弃本集 + note） | `internal/danmaku.Fetcher`（RetryDelays 默认 [10s,30s]、MaxWindowFails=3、MaxSampleRetries=2） | 语义逐条对应；note 文案在 fetcher 内（「本集弹幕加载失败」等） |
| `main.py:2087-2110 _refresh_danmaku_track`（build_ass→write_ass key=`剧ID-集号`→已挂则 reload 否则 attach） | `refreshTrackLocked`（`BuildAss`→`WriteAss`→`SubReload`/`SubAdd(path,"弹幕")`） | ASS key `dramaID-index`（index 1 起始），同 v1 |
| `player.py:2107-2125 attach/reload/reset_danmaku` | `SubAdd`/`SubReload`/`Close`+换集 `SubRemove` | v1 tip「本集 %d 条」→ `danmaku:stats{count,note}` 事件 |
| `main.py:1873-1874 _on_danmaku_toggled` + `player.py:2075-2097 _apply_danmaku_visibility`（v1 只切 `sub-visibility` yes/no，抓取门槛在 main 侧） | `SetDanmakuEnabled` | **有意偏差**（按任务卡）：关=`SubRemove`（数据保留在 fetcher，重开不回滚重拉已抓窗口）+ 停止抓取；开=下 tick 恢复抓取并用已保留数据 `BuildAss`+`SubAdd` 重渲染。v1 关仅隐藏字幕仍显示旧画面 |

## 播放控制

| v1 落点 | v2 落点 | 说明 |
| --- | --- | --- |
| `player.py` play/pause/倍速/音量按钮 | `Play/Pause/SeekTo/SetSpeed/SetVolume` | 全部先记意图再下发；`SeekTo` 用 `AbsSeekSmooth`（绝对、允许关键帧取整） |
| — | `State()` | v1 靠 Qt 信号；v2 给 app.go 一个拉取快照的口子，事件仍是主通道 |

## 命名偏差备忘

- `SeekTo(posMs int64)`：任务卡写作 `Seek(pos)`。Go `go vet` 对 `Seek(int64) error`
  形态报 io.Seeker 签名告警，为保 vet 全绿改名 `SeekTo`，语义不变。
- `Resolver.Resolve(ctx, Request)`：任务卡写作 `Resolve(drama, chapter)`。以
  `Request{DramaID,Title,Index,Drama,Chapter}` 收参（换集需要 1 起始集号做 ASS key，
  需要剧 ID 做去重键），ctx 供 app.go 侧取消慢解析。
- 任务卡引用的 `main.py:2161-2168`（danmaku_enabled 开关、count 事件）与
  `player.py:2131-2139`（断线续播）在当前 v1 文件对不上（分别是全屏样式与弹幕 tip
  代码）。实际落点分别为 `main.py:1873`/`player.py:2075`（开关）与 v1 不存在
  （断线重建为 v2 新增），已按实际语义对齐。

## 接线备忘（app.go）

- `Plan` 的 JSON tag 已对齐核心 `nativePlan`（`session` 是会话键；
  `playbackSession` 只是 danmaku 动作入参名），适配器可直接
  `json.Marshal(native.Resolve 结果)` → `Unmarshal(Plan)`，回归测试
  `TestPlanDecodesNativeShape` 守护。
- `Emit` 契约是非阻塞（事件在 Service 锁内发出）：`runtime.EventsEmit`
  满足；若包了慢逻辑需自行派发。
