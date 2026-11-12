# v1 ↔ v2 下载面板对账（第三十三轮）

方法同 `v1-v2-shortcut-parity.md` / `v1-v2-settings-parity.md`：
先把 v1 侧**逐行抄全**（`zhenguojian/app/zhenguojian/downloads.py`），再和 v2
（`client-v2/frontend/src/views/DownloadsView.vue` + `stores/downloads.ts` + Go 侧）比。
每条都带文件+行号，下一个人可以自己核，不用信我的转述。

## 行内动作（按任务状态）

v1 侧是硬映射（`downloads.py:52-58`）：

```python
ROW_ACTIONS = {
    "downloading": ("pause", "remove"),
    "queued":      ("pause", "remove"),
    "paused":      ("resume", "remove"),
    "completed":   ("play", "folder", "remove"),
    "failed":      ("retry", "remove"),
}
ACTION_TEXT = {"pause": "暂停", "resume": "继续", "play": "播放",
               "retry": "重试", "folder": "打开目录", "remove": "删除"}
ALWAYS_ON_ICONS = {"failed": ("retry",)}   # 失败行的「重试」常驻提亮
```

| v1 动作 | v2 对应（行号） | 状态 |
|---|---|---|
| 暂停 / 继续 / 删除 / 播放 / 打开目录 | `DownloadsView.vue:641`（`row-open`）等行内按钮组 | ✅ 齐 |
| 重试（失败行） | `:667 store.retry(job)`；`stores/downloads.ts:258` 注释写明 **v1 语义 = 核心没有独立 retry，重试即 resume** | ✅ 语义一致；键名本轮从 `search.retry` 改成 `downloads.retry`（文案不变，见下） |
| 状态文案 6 个（排队中/下载中/已暂停/已完成/失败/删除中） | `downloads.state.*`（含 merging、canceled） | ✅ v2 更全 |

## 面板级动作

| v1（`downloads.py` 行号） | v2（`DownloadsView.vue` 行号） | 状态 |
|---|---|---|
| 打开下载目录 `:1084` | `statbar.openFolder` `:455` 一带 | ✅ |
| 全部暂停 `:1090` / 全部继续 `:1092` | `:455 / :456 controlAll('pauseAll'\|'resumeAll')` | ✅ |
| 一键合并整季 `:1207` | `:577 downloads.mergeSeason`（按剧组） | ✅ |
| 合并选中分集 `:1213` | `:585 downloads.mergeSelected` | ✅ |
| **按季合并选中 `:1219`（勾选左侧多部剧，按季序逐季各出一个成品）** | 无 | ❌ **缺**，且**Go 层表达不了**：`Service.MergeStart` 的 `season` 分支要求**单个** `dramaID`（`internal/downloadsvc/*.go`：`if dramaID == "" { return nil, errors.New("缺少剧集信息") }`），没有"跨剧分组、按季排序、逐季排队"的入参形状 |
| **删除该剧 `:1195`**（danger；tooltip「移除左侧选中剧集的全部下载任务和已保存分集」） | 无该按钮 | ❌ **缺按钮，但能力已在**：`stores/downloads.ts:267 removeBatch(ids)` + `selectAll` 现成。缺的是"按剧一次性选中并删除"的**入口位置**——v1 有左侧剧列表，v2 队列页是平铺列表 + 筛选，这个按钮该放哪是 UI 决定，不猜 |
| 停止全部合并 | `:694 store.mergeCancel('')` | ✅ v2 多出 |
| 下载位置…（选择文件夹 / 恢复默认 / 取消 / 确定 `:335-357`） | 无 | ⚠ 刻意不做，理由与证据见 `v1-v2-settings-parity.md` 第 1 条（v2 下载根由核心决定，客户端无写入口） |
| 并发槽位 | `:468 concurrencyLabel` + `:479 save` | ✅（v2 还多 `:474 verifyDecode` 解码校验开关） |

## 归档（成品文件）页签

| v1 | v2 | 状态 |
|---|---|---|
| 组行「打开」`:726` | `:776 row-open`（+ 复制路径、重新扫描） | ✅ |
| **归档文件「播放」`:655`**（`playRequested.emit(target)`，直接播这个路径） | 无 | ❌ **缺**，需要 Go 绑定：`PlayerOpen` 只接受 drama/chapter 并走 resolve（`app.go:326`），没有"按本地路径播"的入口（mpv `loadfile`） |
| **归档文件「删除」`:661`**（tooltip「删除该文件（加密缓存保留，可重新解密）」） | 无 | ❌ **缺**，需要 Go 绑定：现有只有按任务号的 `DownloadRemoveBatch`（`bindings_download.go:133`），没有文件级删除；而且这个绑定**必须自带路径约束**（只允许删下载根之下），否则等于给前端一个删任意文件的口子——这条安全边界要在实现时定，不能事后补 |

## 本轮做完的 / 待决定的

- ✅ 行内动作与状态文案全部对得上（含"重试 = resume"这条容易被误当 bug 的语义）
- ✅ `downloads.retry` 键独立出来：之前借用 `search.retry`，文案一致但**键名撒谎**
  （下载行的按钮挂在下拉菜单的键上），改名字不改文字
- ❌ 四个缺口按性质分两类，都在等决定而不是等我猜：
  - 需要 Go（跨季批量合并 / 归档播放 / 归档删除文件）——三个都要新增绑定或改 `MergeStart` 入参形状；
    CI 验证路径已经打通（`workflow_dispatch`，不发 Release），但**做成什么样要先定**
    （尤其归档删除的安全边界与"播放本地文件"要不要进播放器 UI）
  - 只需要 UI 决定（「删除该剧」放哪）——v2 队列页没有 v1 那个左侧剧列表
