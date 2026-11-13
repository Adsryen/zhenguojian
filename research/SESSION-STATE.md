<!-- 本文件由 `client-v2/frontend/verification/session_state.py` 生成，
     不要手改：里面每个数字都是现测的。我在这个任务里六次把提交数写错
     （凭记忆），所以数字从此只从这里抄。 -->

# 会话状态（现测）

生成时间：2026-11-13 03:52:07 +0000

| 指标 | 值 | 怎么量的 |
|---|---|---|
| 累计提交（相对 `a1a1c89`） | **136** | `git rev-list --count` + 首笔 |
| HEAD | `e984dff` | `git rev-parse --short HEAD` |
| 工作区未提交条目 | 1 | `git status --porcelain` 行数 |
| 前端验收脚本 | 29 只 | `verification/*_verify.py` |
| 端口号（独占/冲突） | 33 个 / 0 处冲突 | `ports_audit.audit_dir` |
| Go 绑定 | 87 个，前端零引用 3 个 | `evidence_audit` |
| 绑定证据等级 | 仅桥接 57 / 桥接+Go单测 27 / 只有编译级证据 2 / 仅Go单测 1 | `evidence_audit` |
| 核心动作 | 生产 26，假核心覆盖 11，只有真机第一次执行 15 | `core_action_audit` |
| i18n 键 | zh 614 / en 614，字面量用到 565，动态取键 12，真死候选 17 | `i18n_key_audit` |

## 只有真实核心才会第一次执行的动作（真机清单的机器生成版）

`browseFiltered browseFilters calendar catalog creatorProfile creatorWorks danmaku detail dramaCreators initialize listSources preload setSourceEnabled soraniAccount suggestions`

## 前端零引用的绑定（API 面收窄是决定，不自动删）

`BrowseFiltered MergeList TrayStatus`

## i18n 真死候选（删或接都是决定）

`covers.banner.partial detail.reserve hero.fallbackChar hero.fallbackTitle hero.heat hero.views settings.storage.commitUnknown status.finished tray.latest tray.menu.check tray.menu.quit tray.menu.show update.applyManual update.applyNotDone update.notes update.notesTrunc update.published`

## Go 测试声明数（现算）

| 包 | 声明 |
|---|---|
| internal/danmaku | 27 |
| internal/downloadsvc | 44 |
| internal/mpv | 22 |
| internal/native | 18 |
| internal/player | 20 |
| internal/selftest | 21 |
| internal/tray | 7 |
| internal/updater | 42 |
| internal/watchstore | 27 |
| (root) | 18 |
| **合计** | **246** |

## 验收脚本清单

- `calendar_paging_verify.py`
- `calendar_scroll_detail_verify.py`
- `contextmenu_verify.py`
- `creator_verify.py`
- `danmaku_toggle_verify.py`
- `detail_copy_verify.py`
- `detail_cover_save_verify.py`
- `detail_download_verify.py`
- `detail_reserve_verify.py`
- `discover_hero_verify.py`
- `downloads_archive_scan_verify.py`
- `downloads_statbar_verify.py`
- `esc_semantics_verify.py`
- `follow_check_verify.py`
- `history_local_play_verify.py`
- `hitbands_verify.py`
- `i18n_sweep_verify.py`
- `merge_multi_verify.py`
- `next_season_verify.py`
- `player_verify.py`
- `preload_openpath_verify.py`
- `rankings_verify.py`
- `reserve_calendar_verify.py`
- `searchall_verify.py`
- `settings_storage_verify.py`
- `sorani_verify.py`
- `sourceorder_verify.py`
- `suggest_verify.py`
- `update_verify.py`

