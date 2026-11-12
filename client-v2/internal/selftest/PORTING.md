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
| 7 | `download` | `download` | ⛔ SKIP | 需要真实落盘 + 跑完清理（临时库目录、只取 2 集）。直接下真剧会污染用户库，必须先把 `downloadsvc` 的安全用法接进来 |
| 8 | `decrypt` | `decrypt` | ⛔ SKIP | 依赖第 7 步产物 |
| 9 | `merge` | `merge` | ⛔ SKIP | 依赖第 7 步产物 + ffmpeg 合成校验 |

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
