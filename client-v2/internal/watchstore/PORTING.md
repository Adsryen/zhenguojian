# watchstore v1(Python) → v2(Go) 移植说明

源：`zhenguojian/app/zhenguojian/watchstore.py`（643 行）+ 调用方 `watch.py` / `main.py` / `tools.py`。
目标包：`client-v2/internal/watchstore`（`store.go`、`season.go`、`store_test.go`，本文件）。

## 0. 数据归属结论（重要）

追剧（follow）数据**全部**在 `watchstore.py` 内：`follows` 与 `history` 同存于
`<data_dir>/watch-library.json` 一个文件、一把 `threading.Lock`、一次原子写。
`library.py` 经 grep 确认没有任何 follow 状态；v1 UI 的「有更新置顶标集数」纯在
`watch.py`（`_visible_rows` / `_update_state`）计算，不在存储层。本次移植把
watchstore.py 1:1 移植进 `Store`，并把 watch.py 的展示层计算（has_update/badge、
今天/昨天/更早分段）以**纯函数形式**一并移植（`FollowList()` 预计算字段 +
`Segments/Today/Yesterday/Earlier`），因为 v2 没有 PyQt 视图层可依赖。
`follows()` 行上附带的 `sourceGroup` 依赖 `media.SOURCE_GROUPS`（媒体子系统），
属 v2-3 接入点，不在存储包内复刻。

## 1. 文件与路径

| 项 | v1 | v2 |
| --- | --- | --- |
| 存储文件 | `tools.data_dir()/watch-library.json` | `Open(dataDir)` + `FileName = "watch-library.json"` |
| data_dir | `ZHENGUO_DATA` 环境变量→abspath；win32→`%LOCALAPPDATA%/ZhenguoJian`（缺→`~`+ZhenguoJian）；其它→`~/.zhenguojian` | `DefaultDataDir()` 同三段逻辑（`runtime.GOOS == "windows"`，免 build tag；LOCALAPPDATA 空时回退 UserHomeDir） |
| 原子写 | 同目录 `tempfile.mkstemp(".watch-", ".tmp")` + `os.replace` | `os.CreateTemp(dir, ".watch-*.tmp")` + `os.Rename`；失败删 tmp、保留 dirty、记 `lastErr` |
| 损坏容错 | `JSONDecodeError`/`UnicodeDecodeError` → `tools.backup_broken`：rename 成 `<file>.corrupt-%Y%m%d-%H%M%S` 后从空开始；文件缺失→空且不备份；顶层非 dict→空且**不**备份；行非 dict→跳过 | `load()` 同梯度（`!utf8.Valid` ≈ UnicodeDecodeError；解码后用 `dec.InputOffset()` 之后的残留字节判 Extra data——Go `dec.More()` 会漏掉 `{}\n}garbage` 这类尾垃圾，故不用它；非对象顶层不备份）；`backupBroken` 同名后缀格式 |

## 2. 方法映射表（Python → Go）

| v1（watchstore.py） | v2（store.go） | 说明 |
| --- | --- | --- |
| `WatchStore(tools)` / `_load()` | `Open(dataDir string) *Store` | v1 构造即加载、永不抛错；v2 同（错误只进 `lastErr`，`Flush()` 返回） |
| `tools.data_dir()` | `DefaultDataDir() string` | 见 §1 |
| `save_watch(drama, episode, position, duration, finished)` | `SaveWatch(dramaID, title, cover string, episode int, position, duration float64, finished bool)` | 签名摊平；`drama={id,title,cover}` 语义保持（`_source_from_id` 冒号前缀推导 source 同移植）；去重（同集且 Δposition、Δduration 均 <0.5 且 !finished → 静默返回、不 dirty、不发信号）、duration<=0 沿用旧值、300 条按 updated 升序驱逐、`SAVE_INTERVAL=30s` 节流、finished 立即落盘——全部 1:1 |
| `save_local_watch(drama_id, title, path, position, duration, finished)` | `SaveLocalWatch(...)`（同参 + `filepath.Abs`） | 见差异 D8 |
| `history()` | `History() []HistoryEntry`（updated 倒序） | v1 `-float(updated or 0)`：v2 转 `HistoryEntry.Updated float64`，**类型上防住字符串崩溃**（差异 D1） |
| `follows()` | `FollowList() []FollowEntry` | 含 watch.py `_update_state` 预计算的 `HasUpdate/Badge` 与 `HasWatched/SeenEpisode`（int(episode or 1) 口径） |
| `watched(drama_id)` | `Watched(id) (HistoryEntry, bool)` | 深拷贝行 → 类型视图 |
| `clear_history()` / `remove_history(id)` | `ClearHistory()` / `RemoveHistory(id)` | 同：删除仅在存在时 touch，emit 无条件（D5） |
| `is_followed(id)` | `IsFollowed(id)` | `getBoolDefault(row,"followed",true)`：**键缺失=在追**，v1 原样（D6） |
| `toggle_follow(drama)` | `ToggleFollow(Drama) (added bool)`；`Follow/Unfollow` 为幂等包装 | 三分支 + 建行 schema（21 键，含 `sourceTime=onlineDate`、`nextDrama=nil`）逐键移植；`Unfollow` 只走「取消关注」分支，已删/不存在的行不再反向加回（v1 无此调用形态，见 D7） |
| `remove_follow(id)` | `RemoveFollow(id)` | 无条件 emit 保留（D5） |
| `reserve_drama(drama)` | `Reserve(Drama)` | 建行 19 键 + `reserveSeason = season+1 当 releaseStatus=="finished" 否则 season`、`merged drama`（stored∪drama）逐行 1:1 |
| `cancel_reservation(id)` | `CancelReservation(id) bool` | 未预约→false 无 emit；`!followed`→整行删除；预约中→想看；同 1:1 |
| `is_reserved` / `unseen_arrivals` / `mark_arrivals_seen` | `IsReserved` / `UnseenArrivals` / `MarkArrivalsSeen` | 同 1:1（mark_arrivals_seen 不发信号） |
| `observe_drama(drama)` | `ObserveDrama(Drama)` | **更新检测引擎** 348–426 行全量移植：merged-drama 比较、reserved→arrived 三条件、季切换（known=episodes, read=0, newSeason, nextDrama/nextSeason, updateAt, newEpisodes=0）、episodes>known 分支（newEpisodes=max(old,unread)、sourceTime 回退 onlineDate、已看且 !marked 且 unread>0 → 重开为在看）、changed→`_touch`+emit |
| `mark_next_season(id, next, season)` | `MarkNextSeason(id, nextDrama, season int) bool` | 428–454 行 1:1 |
| `mark_watched(id)` | `MarkWatched(id)` | 行不存在→静默无 emit；status=已看, read=known, marked=true, arrived=false/arrivedSeen=true, newEpisodes=0, updateSeen=true, reserved 时 reserveAtKnown=known——1:1 |
| `flush()` | `Flush() error` | v1「空 dirty 也落盘」保留（D4）；错误不吞（D3） |
| `season_number` / `_chinese_number` | `season.go: SeasonNumber / chineseNumber` | 正则 `第\s*([0-9０-９零〇一二两兩三四五六七八九十百]+)\s*[季部]`、NFKC 折叠、百/十进位、clamp 1..200 全同（差异 D9 记录 Go \s 语义） |
| `changed = pyqtSignal(str)` | `OnChanged(func(dramaID string))` 回调注册表 | emit 点与 v1 **逐一对齐**（含「去重/无 id 早退不发」「remove_history/clear/remove_follow 无条件发」）；回调在锁外调用，与 v1 emit-outside-lock 等价 |
| `watch.py _day_bucket`（87–99 行） | `dayBucket`（非导出）+ `Segments/Today/Yesterday/Earlier` | 本地时区日历日差：`<=0`→今天（**未来时间戳也算今天**，v1 原样）、`==1`→昨天、否则/异常/`0 或 None`→更早（epoch 1970） |
| `watch.py _visible_rows/_update_state`（313–338 行） | `FollowEntry.HasUpdate/Badge`（`followUpdateState`） | `new = newEpisodes>0 ? newEpisodes : max(0, known-read)`；`has_update = arrived || new>0`；badge 仅 has_update 且非 reserved 时给数；「有更新置顶」= FollowList 调用方按 `(HasUpdate 优先, -UpdateAt||-Added)` 排，同 watch.py:319-325 口径（本包保留 added 倒序为基序，置顶排序留给 UI 层，见 D10） |

存储行内部仍保留 `map[string]any`（`obj`）原样读写，所以 v1 的行 schema、键序、
数值文本在「加载→不变更→保存」路径上逐字节还原（`json.Number` 不转 float64）。

## 3. 有意差异清单（每条给理由）

- **D1 float 崩溃防御**：v1 `history()`/`follows()` 排序 `-float(r.get("updated") or 0)`
  对字符串型 updated（如 `"1762900000.5"` 或 `"abc"`）直接抛 ValueError——`"abc"`
  会崩 UI。v2 `toFloat` 统一转 float64，非数字串按 Python `float("...")` 失败→0 处理，
  **排序永不崩**；数字串（`"1762900000.5"`）在文件里仍原样保留为字符串
  （往返兼容，见测试断言 `"updated": "1762900000.5"` 文本不变）。
- **D2 Open 无 error 返回**：对齐 v1「构造永不抛错」；损坏文件已备份、缺失按空库，
  真正的写错误延迟到 `Flush()`。
- **D3 提交错误不静默**：v1 `_commit` 吞 `OSError`（dirty 保留）。v2 同样保留 dirty
  并允许重试，但把错误记入 `lastErr` 由 `Flush()` 返回——磁盘写失败在 GUI-less 的
  v2 里必须可观测。
- **D4 flush 无条件落盘**：v1 `flush()` 直接 `_touch`（即使不 dirty 也写一次）；保留，
  理由：兼容 main.py 停播即 flush 的行为，不改写频率语义。
- **D5 无条件 emit 保留**：`remove_history`/`clear_history`/`remove_follow` 即使没删掉
  东西也发信号。这是 v1 怪癖，但 main.py 的界面刷新依赖它，照抄。
- **D6 `followed` 键缺失默认为 true**：v1 `is_followed` 用 `row.get("followed", True)`；
  历史遗留行没有该键时 v2 `getBoolDefault(..., true)` 同语义（`reserved` 无默认 true
  的对应键，缺失=false）。
- **D7 `Follow/Unfollow` 幂等包装**：v1 只有 `toggle_follow`，UI 自己判断态。v2 提供
  `Follow(drama)`（已 followed 则 no-op）与 `Unfollow(id)`（仅取消关注，未跟则 no-op），
  避免 v2 调用方重蹈「toggle 语义误用」；`ToggleFollow` 原样保留供需要时使用。
- **D8 `save_local_watch` 的 `_resolve_local` 依赖未移植**：v1 会去
  `download_dir()/episodes.json` 库索引里反查真实 drama 行与集数。那是 library/下载
  子系统，不属于本包职责。v2 `SaveLocalWatch` 接收调用方已解析的
  `(dramaID, title, path)`，写行时固定 `episode=1 + local_path`（v1 解析不到时的
  fallback 形态），去重逻辑（同 local_path 且 Δ<0.5 静默）1:1。**v2-3 接入待办**：
  由上层解析后传入真实集数。
- **D9 正则 `\s` 语义**：Python `\s`（str 模式，Unicode）匹配 NBSP/全角空格 U+3000；
  Go RE2 的 `\s` 仅 ASCII。`season.go` 用 `[\s\v\u00A0\u3000]` 显式补齐，
  保证「第\u3000二季」这类标题两版同结果；NFKC 折叠用等价字符类实现（无 x/text）。
- **D10 置顶排序留在 UI 层**：v1 `watch.py._visible_rows` 还混入 sourceGroup 过滤、
  分组搜索等视图逻辑；本包只把判定所需的字段（HasUpdate/Badge/UpdateAt/Added）算好
  放进 `FollowEntry`，不复制排序，避免存储包长出视图依赖。
- **D11 分段函数在存储包内提供**：理由同 §0（v2 暂无视图层）；口径严格对齐
  `watch.py._day_bucket`，包括「未来时间戳归今天」和异常归更早。
- **D12 行对象并发语义**：`History()/FollowList()/Watched()` 返回类型化快照值
  （v1 返回 dict 的浅拷贝、内部行仍共享）——v2 不共享可变内部行，杜绝
  main.py 曾依赖不到的撕裂读。
- **D13 整数值浮点的 JSON 文本**：Go `encoding/json` 把 `float64(0)` 写成 `0`，
  Python `json.dump` 写成 `0.0`——Go **新写入**的字段（如建行 `updateAt`、
  `position/duration` 为整数值时）少一个 `.0` 后缀。JSON 数值语义相同，v1
  `float()` 读 `0` 得 `0.0`，v2 `toFloat` 读两种文本同值；从文件**读入且未改写**的
  数字仍以 `json.Number` 原文透传（fixture 里的 `0.0`/`1729000000.0` 原样写回）。
  不做字符串级补齐（收益不抵复杂度）。

## 4. v1 怪癖备忘（移植时原样保留，测试锁定）

1. `flush()` 不判 dirty 直接写（`watchstore.py:159` `flush` → `_touch`）。
2. `remove_history` 不存在也 emit；`remove_follow` 无条件 emit。
3. `is_followed` 对缺 `followed` 键的旧行返回 True。
4. `observe_drama` 每次 `now()` 现取，同一次调用内 `updateAt/lastChecked` 可能差微秒。
5. `save_watch` 去重命中时**完全不写不发**，连 dirty 都不置。
6. `toggle_follow` 取消关注遇 reserved 行：只置 `followed=false` + 状态回退
   （`known<=0→"预约中"` 否则 `"想看"`），不删行。
7. 顶层非 dict 的坏文件**不**产生 `.corrupt-*` 备份（只有解码/编码错误才备份）。
8. 未知键原样透传保存（v1 dict 整体写回；v2 `map[string]any` 同理）——hidden 字段
   如 history 行的 `local_path` 不会丢。
9. `main.py` 恢复播放时以 `episode=position+1, position=resume, duration=0.0,
   finished=False` 调 `save_watch`（main.py:2269-2270）；配合 `save_watch` 的
   「duration<=0 沿用旧值」规则。
10. `mark_next_season` 仅在 `is_followed || is_reserved` 时被调用（main.py:2596）。

## 5. 兼容性验收（硬门槛）证据

`store_test.go`：

- `TestV1FixtureRoundTrip`：内嵌按 v1 写盘形态手工拼的 fixture（含 legacy 行缺
  `followed`/缺 `drama.source`、字符串型 `updated`、`local_path`、`nextDrama` 非空、
  reserve-only 行）。`Open→Flush` 后重读文件与 fixture 语义零漂移；唯一允许差异 =
  v1 `_normalize_row` 本就会做的 `drama.source` 回填；再 Save 一次，文件字节级幂等。
- `TestFloatCrashQuirk`（D1 的崩溃向量）、`TestCorruptFileTolerance` +
  `TestInvalidUTF8Backup` + `TestMissingFileNoBackup` + `TestNonDictRowsSkipped`
  （备份名后缀 / 顶层非 dict 不备份 / 缺文件静默 / 坏行跳过）、
  `TestAtomicWriteNoLeftovers`（目录无 `.watch-*.tmp` 残留）+
  `TestWriteFailureKeepsOldFileAndDirty`（写失败保旧文件、dirty 留存可重试）、
  `TestDayBucketBoundaries`/`TestSegments`（本地时区跨日、未来戳、epoch 戳）、
  `TestObserveDrama` 表（更新置顶标集数全链路：季切换/集数增长/预约到货/已看重开/
  不重复 bump）、`TestConcurrentSafety`（-race 下的锁语义）。
- 格式细节：indent=1（一个空格）、`ensure_ascii` 关闭（Go 侧
  `SetEscapeHTML(false)` + `SetIndent("", " ")`）、**无尾随换行**（TrimRight）。
  `go test` 断言这三点。
- 本包 `~/.local/go/bin/go vet ./internal/watchstore/` +
  `go test -count=1 ./internal/watchstore/`（含 `-race`）在 Linux 全绿，
  `gofmt -l` 无输出、全部文件 LF。模块级 `./...` 的通过与否取决于并行会话的
  根包骨架状态（见 §7）。
- 交叉验证：临时 Go 测试（已删除，不入库）把「fixture Load→Save 产物」和「带变更
  操作产物」写到 /tmp，用 **v1 自己的 `_normalize_row`（从源码 AST 抽出）**+ 复刻的
  `_load` 排序读取：语义零漂移、`updated` 倒序一致、字符串 updated 原样保留且仍被
  v1 `float()` 正常解析、Go 新写的整数值浮点按 D13 写为 `0`（v1 读回 0.0 等价）、
  `nextDrama` 为 `null` 不缺席——证明同一份 watch-library.json 两版互读。

## 6. v2-3 接入时需要的东西

1. **事件通知口子**（已有最小实现，建议演进为）：
   - 现状：`OnChanged(func(dramaID string))` 多播，锁外调用，语义=v1 `changed` 信号
     （含「去重不发 / 删除无条件发」）。
   - 建议：升级为带**事件类型**的载荷 `{Kind, DramaID}`，Kind ∈
     {history, follow, reserve, arrival-seen}，让 main.py 式分面板刷新
     （history/follow/reservation/online-update 四类）不用反解 id；并保持现签名作
     Kind 未知时的兼容路径。回调须**异步投递或允许重入安全**（v1 是 Qt 信号跨线程）。
   - 另需一个 `Watched(id)` 变更细化信号：v1 `save_watch` 每次落盘都发同一信号，
     播放进度 tick（5s UI 节流 + 30s 存储节流双层）与「看完一集」不可分——v2-3 若做
     追剧角标，建议 `finished=true` 单独发 `Kind=history-done`。
2. **本地播放解析**：D8 所述 `episodes.json` 反查需由上层（下载/library 模块）提供
   `ResolveLocal(dramaID,title,path) (episode int, drama Drama, ok bool)` 后传入
   `SaveLocalWatch`。
3. **`sourceGroup`**：`FollowEntry` 未带 `sourceGroup`（依赖 `media.SOURCE_GROUPS`）；
   v2-3 的分组视图在媒体侧移植后再富化。
4. **Windows 路径验收**：`DefaultDataDir` 的 `%LOCALAPPDATA%/ZhenguoJian` 分支在
   Windows 构建下补一条单测（当前 Linux 覆盖 env + home 分支）。
5. 若 v2 采用 Wails/JS 前端：本包无 cgo/平台依赖，可直接被 `bind`；注意
   `History()/FollowList()` 返回快照值类型，绑定层每调一次就是一次拷贝（与 v1
   每次重建 dict 列表同阶，300 上限下无压力）。

## 7. 并发工作注记

汇报时 `client-v2/` 根包（`main.go/app.go/wails.json`）是另一会话正在搭的 Wails 骨架，
模块级 `go ./...` 会连带其 `frontend/dist` embed 报错；本包验收命令以
`go vet ./internal/watchstore/ && go test ./internal/watchstore/` 为准（全绿，含
`-race`），根包状态以最终汇报为准。
