# selftest —— v1 `--selftest` 的移植状态

v1 出处：`zhenguojian/app/zhenguojian/selftest.py`（272 行，9 个 `@step`）。
目的照 v1 原话：**从已打包的二进制内部**证明「资源发现 → 核心 API → 流解析 →
下载 → 解密 → 合并」在打包布局里真的可用，并输出机器可读报告；
屏显播放**故意不进自检**（v1 注释：以前会在构建中途弹 mpv 窗口，已移除，人工验）。

v2 运行方式：`client-v2.exe --selftest`（`main.go:31` 判定，不起 GUI、不开 mpv）。
报告：`selftest-report.json`，落在 exe 同级（v1 同位置语义），拿不到 exe 目录才退回数据目录。

## 步骤对等表

| # | v1 步骤 | v2 步骤 | 状态 | v2 靠什么证明同一件事 |
|---|---|---|---|---|
| 1 | `resources` | `resources` | ✅ | mpv/ffmpeg/ffprobe 三件按「env → exe 同级 → PATH」查；**sidecar 记 `in-process`**（见下方差异 1）；末尾 `native.Init()` 真调一次核心 initialize |
| 2 | `library` | `library` | ✅ | `native.Recommendations` 必须有条目 + `RankingBoards` 计数（v1 也顺手查 rankings） |
| 3 | `newseries` | `discover` | ✅（改名） | `native.Catalog(lane="discover")` 第一页非空（见差异 2） |
| 4 | `search` | `search` | ✅ | `Suggestions("穿越")` + `Catalog(lane="search")` 有结果，取第一条当后续步骤的剧目 |
| 5 | `detail` | `detail` | ✅ | `native.Detail` 且 **chapters ≥ 2**（v1 原条件） |
| 6 | `preload` | `preload` | ✅ | `native.Preload` 且 `url` 非空、`local \|\| url 含 127.0.0.1`（v1 原条件逐条搬） |
| 7 | `download` | `download` | ✅ 代码完成，**从未执行过** | `downloadsvc.Enqueue` 前 2 集 → 轮询 `Snapshot`（900s / 3s，照 v1）→ 要求 2 条 `completed`；跑在**沙箱数据目录**里（见下） |
| 8 | `decrypt` | — | ⛔ 不适用（显式 SKIP） | **v2 没有独立的解密发布子系统**：产物保持加密存放，解密发生在 mpv 播放 key（`internal/mpv/player.go:248`）与合并时 ffmpeg `-decryption_key`（`internal/downloadsvc/merge.go:138`）。实质保证由第 9 步的产物校验承担 |
| 9 | `merge` | `merge` | ✅ 代码完成，**从未执行过** | `MergeStart("selected", …)` → 轮询 merges 落定 → 要求 `state=done` 且 `output` 真实存在且非空（v1 那句 "merged file is not playable on its own" 的 v2 等价物；ffmpeg 全片校验在 downloadsvc 内部） |

## 沙箱：v2 自检不碰用户真库（与 v1 的**行为差异**，不是改进掉链子）

v1 的 download 步先 `core.downloads()` 全 remove、再
`EpisodePublisher.purge(drama_id, remove_files=True)`——**删的是真实库文件**。
v2 不把这个权力给自检：`main.go` 在 `Run` 之前调 `selftest.Prepare()`，
建临时目录并把 `ZHENGUO_DATA` 指过去，**必须早于 `native.Init()`**
（`DataDir` 只在第一次 initialize 生效，晚设就写进真库了）。

- `newPipeline()` 在沙箱未建立时**直接拒绝**入队（有单测钉住）；
- 全绿 → 删临时目录；**任何 FAIL → 保留现场并打印路径**
  （一次真跑的下载/合并成本很高，毁掉现场等于让人再跑一遍）。

## 两处刻意不同（照抄会永远红，假装通过更糟）

1. **没有 sidecar 可查**。v2 引擎是进程内 `duanjuapp/native`，v1 四件套里的 sidecar
   在 v2 不存在，所以这一步查三件并把 `sidecar` 字段写成 `in-process`。
2. **`new_series` 在 v2 没有同名动作**。核心把「上新」折叠进 catalog
   （`lane=discover`）/ `BrowseFiltered`，所以步骤名叫 `discover`：
   能力对等、名字不同。前端实际用到的 lane 只有 `discover / library / search / series:auto / series:next`。

## SKIP 的机器可读承诺

`Run()` 不会让 SKIP 沉默：

- 终端打一行 `[selftest] PENDING N step(s) not implemented yet: download, decrypt, merge`；
- 报告里有顶层 `pending: [...]`，每个 SKIP 步骤还带 `reason`；
- 退出码沿用 v1：**只有 FAIL 才非 0**，SKIP 不算失败。

也就是说：**六步绿不等于"已与 v1 完全等同"**，`pending` 字段就是这句话的机器版本。
（这条是这套自检存在的意义——一个把"还没做什么"藏起来的自检比没有更危险。）

## 「代码完成」≠「跑过」——但边界比上一版窄了

**现在 CI 真执行过的**（run 43264726895，`-v` 逐条留名，15 个用例全 PASS）：
沙箱生命周期（建目录 / 设 `ZHENGUO_DATA` / `DataDir` 确实指向沙箱而不是用户配置目录 /
cleanup 后目录与状态都复位）、工具查找回退顺序（env 优先、env 指错必须报错、
PATH 兜底）、mpv 候选路径布局（同级 → `runtime\` → `mpv\`）、
报告写不出去时退出码不变且必须说出来、终态与轮询落定判断、
两种快照形状、无沙箱时拒绝入队、FAIL 才保留现场、九步清单守护、中文截断。

**仍然从未执行过的**：九步各自的**真实管线调用**——
`native.Init` 之后的 1–6 步要连核心网络，第 7/9 步还要真实 ffmpeg 与打包布局。
CI 的 ubuntu runner 不开核心网络、Windows job 只编译不跑，本机也没有 Go。
所以首次在打包机上跑 `client-v2.exe --selftest` 时，请把 1–6 与 7/9 都当成"可能红"看待：
- 1–6 红通常是网络/站点结构变化 → 看 detail 里的 count/hits/episodes；
- 7 红看 `jobs` 摘要（`id/state/detail`）；
- 9 红分三种：`合并未成功：state=…`（ffmpeg 侧）、`合并产物文件不存在`（路径/落盘）、
  `合并产物为空文件`（解码/写入）。

（CI 步骤里那条 `-v -run …` 过滤器是刻意留的：`ok 包名 0.006s` 只能证明"没失败"，
 证明不了**哪几条跑过**。新增用例时记得把名字加进过滤器。）

## 本机与 CI 的验证边界

- 本机（Linux，无 Go 工具链）：**编不了也跑不了**这些文件，见
  `.trellis/spec/backend/verification-harness.md` 的环境说明。
- CI：`build-v2.yml` 的 `workflow_dispatch`（不发 Release）——
  `internal/selftest` 单测在 ubuntu job 跑（`ok ... internal/selftest`），
  根包 `--selftest` 分支的编译由 windows job 的 `go vet + go build` 覆盖。
- **真跑 `client-v2.exe --selftest` 只能在打包后的 Windows 上做**：第 1–6 步会真连核心网络，
  这不是单测能替代的。所以本包的单测只验"自检这套机器本身"的契约
  （结局分类、退出码、PENDING 不漏说、报告结构、中文标题截断不产生非法 UTF-8），
  管线对错留给打包机。
