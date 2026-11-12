# v1 ↔ v2 设置页条目对账

方法同 `research/v1-v2-shortcut-parity.md`：**先把 v1 的行标签逐行抄全**，再和 v2 比。
v1 侧来源：`zhenguojian/app/zhenguojian/settings_panel.py` 的
`_combo_row`(681,682) / `_path_row`(776,777) / `_spin_row`(883,884,886) / `_kv_row`(1012-1015)
调用点，加上两个 `GuoSwitch`（691 弹幕、895 按站源分道）与站源节（457）。

| # | v1 行 | v1 控件 | v2 对应 | 状态 |
|---|---|---|---|---|
| 1 | 站源开关 | SourceGrid（可拖拽排序） | `settings.sources` 同源网格 | ✅ v2 更多（拖拽排序、18+ 徽标、重试） |
| 2 | 倍速 | combo `s1_speed` | `settings.playback.speed` | ✅ |
| 3 | 清晰度 | combo `s1_quality` | `settings.playback.quality` | ✅ |
| 4 | 弹幕开关 | GuoSwitch `s1_danmaku` | `settings.playback.danmaku` | ✅ |
| 5 | **成品库（下载）** | path `s1_libpath` + 「修改…」 | `settings.storage.library` | ✅ **本轮补**（路径 + exists 徽章 + 打开目录）；**「修改…」刻意不做**，见下 |
| 6 | 加密缓存（只读） | path `s1_cachepath` | —— | ⚠ **v2 无此概念**，见下 |
| 7 | 代理模式 | combo `s1_proxymode` | `settings.network.proxyMode` | ✅ |
| 8 | 代理地址（v1 在代理模式内） | —— | `settings.network.proxyUrl` | ✅ v2 显式一行 |
| 9 | 目录并发 | spin `s1_catcon`(1-6) | `settings.network.catalogConcurrency` | ✅ |
| 10 | 请求间隔 | spin `s1_catgap`(0-5000ms) | `settings.network.interval` | ✅ |
| 11 | 下载并发 | spin `s1_dlcon`(1-6) | `settings.network.downloadConcurrency` | ✅ |
| 12 | 按站源分道下载 | GuoSwitch `s1_bysource` | `settings.network.bySource` | ✅ |
| 13 | 应用版本 / 本地核心 / 打包提交 / 数据目录 | 4 × kv | `settings.storage.{version,core,commit,dataDir}` | ✅ |
| 14 | 主题（**不在设置页**：顶栏按钮「主题 深色/浅色」，`main.py:866-880 _toggle_theme` + `tools.set_theme` 持久化） | toolbar button | `settings.theme`（外观节） | ⚠ **换了位置且多一档**：v1 只有 dark/light 两态互切，v2 在设置页给 auto/light/dark 三态。不算缺失，但也不是"v2 新增能力" |
| 15 | ——（v1 全应用只有中文，无语言开关：`grep -i language\|locale` 在 settings_panel/ui/tools 为空） | —— | `settings.language` | ✅ v2 新增（zh/en 全站） |

## 两处刻意的不对等（照抄会造假，所以不做）

1. **「修改…」按钮**（v1 `_on_choose_location`，`settings_panel.py:816`）：
   选目录 → 建 `<chosen>/下载` → `tools.set_library_root(chosen)`（写**客户端** settings 的
   `libraryRoot`）→ `stash_index` 把旧库记录搬给新根；等默认位置时把设置清空；
   同目录/不可用/保存失败各有明确提示文案。
   v2 的下载根由**核心**给出（`downloadDirectory` 动作，`bindings_download.go:240`），
   客户端没有对应的写入口，Go 侧也没有 `SetLibraryRoot` 一类动作。
   **结论**：这条要补，得先决定核心是否支持换根目录（以及旧库怎么迁移）——
   属于产品/架构决定，不是遗漏。所以 v2 不放一个点了没作用的按钮，
   `settings_storage_verify` ④ 专门钉这一点。
2. **「加密缓存」只读行**（v1 `tools.core_cache_dir()` = `project_dir()/.cache/downloads`，
   tooltip 写「位置固定，暂不可修改」）：v2 的加密原件就在核心下载根内 ——
   分集成品 `<root>/<jobId>/media.mp4`、合并成品 `<root>/合并/`
   （`bindings_download.go:238-240`；`.cache` 只是归档扫描时忽略的中间产物目录名，
   见 `downloadsvc/archive_scan.go:31`）。
   没有"另一个固定缓存目录"这回事，**造一行去猜路径等于往设置页写假数据**。

## 本轮已验 / 待验

- 已在本机验：`settings_storage_verify.py`（@5205）六条断言全绿，且做了 HEAD 上
  before/after（HEAD 无该行 → 选择器超时 exit 1）；`i18n_sweep` 36/36 含 en 整页审计；
  静态门禁 12 组形状自测 + 实仓 0 处硬编码。
- 待真机：「打开目录」在 WebView2 下是否真的打开资源管理器、
  exists=false 时的真实表现（mock 里目录恒存在，徽章分支只能靠注入）。
  → 已并入 `research/v2-windows-followups.md` 的下载/设置节。
