---
name: zhenguojian-ui
description: 真果鉴（PyQt5 桌面客户端）的界面美化与样式调整规范。凡是用户想改这个客户端的配色、间距、控件样式、整体观感，或给新控件做视觉处理时使用——即使用户只是说"美化一下界面""好看点""调一下样式""这个控件太丑"。
---

# 真果鉴 UI 美化规范

深色主题的 PyQt5 客户端。样式集中在 `app/zhenguojian/ui.py`（设计令牌 + 全局 QSS），
改观感只动这个文件；组件内不允许再散落硬编码颜色（历史遗留的局部 QSS 见到就迁走）。

## 架构

- `app/zhenguojian/ui.py`：`DESIGN` 令牌字典（颜色/圆角/间距）+ `build_stylesheet()`
  返回全局 QSS，`main.py` 启动时 `app.setStyleSheet(build_stylesheet())`。
- `downloads.py` 的 PRIMARY_QSS / SECONDARY_QSS 从 ui.py 读取令牌拼装（主按钮/次按钮）。
- 新控件样式加在 `build_stylesheet()` 里，按控件分组并注释。

## 设计令牌（DESIGN）

| 令牌 | 值 | 用途 |
| --- | --- | --- |
| bg | #0f1117 | 窗口底色 |
| surface | #141821 | 列表/表格/输入框底色 |
| surface2 | #171b23 | 菜单栏/状态栏/标签页 |
| surface3 | #1c2130 | 悬停底色 |
| border | #262c3a | 边框（描边） |
| accent | #4a83ff | 主色（选中/焦点/主按钮） |
| accent_hover | #3f7bff | 主按钮悬停 |
| success | #2ea043 | 成功/下载完成 |
| danger | #e06c75 | 失败/危险操作 |
| warning | #d29922 | 暂停 |
| text | #d7dce5 | 正文 |
| muted | #8d97a8 | 次要文字 |
| radius | 6px | 圆角（按钮/输入框/进度条） |

## 硬性规则（都是踩过坑的）

1. **每个控件必须同时处理 normal / hover / pressed / disabled 四态**，只写
   normal 的新控件过两天就会被报告"点着没反应"。
2. **QToolTip、QScrollBar、QComboBox 下拉列表必须样式化**，系统默认是白色的，
   一出深色主题就被打穿。
3. **圆角一律 6px**（按钮/输入框/进度条/工具提示）；表格和列表容器不加大圆角。
4. 中文字体统一 `Microsoft YaHei`；正文 13px，标题 15-16px bold，次要 12px。
5. **表格行高 30px 固定**（downloads.py 已设置），列表条目 padding 6px 8px；
   密度过低会显得空，过高会挤。
6. 主按钮（合并/搜索）用 accent 填充 + 白字，次按钮用 surface + border；
   危险操作用文字按钮或 danger 描边，不用大红填充。
7. 布局边距：页面 10px，面板内 6px，控件组间 6-8px。

## 工作流（改完必须按顺序走完，跳步会交付半成品）

1. 编辑 `app/zhenguojian/ui.py`（或组件局部 QSS）。
2. **离屏渲染自检**——样式问题肉眼不看图永远发现不了：

   ```python
   os.environ["QT_QPA_PLATFORM"] = "offscreen"
   os.environ["QT_QPA_FONTDIR"] = r"C:\Windows\Fonts"   # 否则文字渲染不出来
   # FakeCore 假核心构造 MainWindow（见 _player_test.py 的写法），
   # 注入几条假 jobs 让表格有内容，window.grab().save("ui.png")，然后 Read 查看。
   ```

   重点看：对比度、圆角是否生效、悬停态、文字是否溢出/重叠。
   **测试渲染必须用真实形状的素材**：QListView 按位图自身尺寸绘制图标，
   用恰好等于槽位的占位图测试会漏掉"封面比格子高、吃掉文字区"这类问题
   （踩过两次：KeepAspectRatioByExpanding 产物 150×225 > 槽 150×210）。
   封面统一走 `widgets.cover_pixmap()`：填满后居中裁切到精确槽位。
3. **QListWidget 图标条目默认 sizeHint 紧贴内容**，条目按它裁剪——标题折行后
   元信息行会被拦腰截断（一行标题的没事，两行的出事，极具迷惑性）。
   封面墙已挂 `_CoverDelegate` 把 sizeHint 钉成整格 GRID_SIZE，新网格照抄。
3. `python build/build.py` 打包（会同时重编 sidecar）。
4. 部署：exe 若在运行 → `Move-Item 真果鉴.exe 真果鉴.old*.exe` 改名后 `cp dist/真果鉴.exe .`
   （运行中的 exe 不能覆盖但能改名）；没运行 → 直接覆盖、清理 .old 并重启。
5. 用户看到的是打包版，别忘了重启或提醒重启。
