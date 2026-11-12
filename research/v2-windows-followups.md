# v2 真机待验清单（本次会话的增量）

> 对应范围：`a1a1c89`（本轮工作起点）→ 当前 `master`，共 50 笔提交。
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

- **前端全链**：类型检查、构建、以及 21 个 Playwright 验收脚本（`run_all.py` 全量 21/21、
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
