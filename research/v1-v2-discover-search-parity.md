# v1↔v2 行为对账：搜索 / 找剧(discover) / 剧库(library)

- 审计日期：2026-11-13（只读盘点，未改任何代码）
- v1：`zhenguojian/app/zhenguojian/`（PyQt5）+ `guoapp-src/native/core/`（v1 sidecar 核心，v2 通过
  `client-v2/go.mod:52` 的 `replace duanjuapp/native => ../guoapp-src/native` **共用同一份核心**）
  + `zhenguojian/sidecar/core_overlay/discovery.go`（browseFilters/browseFiltered 的 overlay 实现，v2 经
  `-tags v2overlay` 同一份源码合入，见 `client-v2/internal/native/discovery_overlay.go:17`、CI
  `.github/workflows/build-v2.yml:201`）。
- v2：`client-v2/frontend/src/views/{SearchView,DiscoverView,LibraryView}.vue`、
  `components/{SearchGroups,PosterGrid,SourceSwitch}.vue`、`stores/{app,searchAll}.ts`、
  `services/{api,fixtures,i18n}.ts`、`client-v2/app.go`、`client-v2/bindings_*.go`、
  `client-v2/internal/native/*.go`。

## 0. 先修正任务书里的两个坐标（避免后人找空文件）

| 任务书说法 | 实际 | 证据 |
|---|---|---|
| `stores/search.ts`、`stores/discover.ts` | **不存在**。搜索/找剧的共享状态在 `stores/app.ts`（当前源）与 `stores/searchAll.ts`（“全部来源”扇出纯函数） | `ls client-v2/frontend/src/stores/` → app/calendar/downloads/player/searchAll/watch |
| `components/EpisodeGrid.vue`、`components/HotkeyHelp.vue` 属本面 | **未被这三个 view 引用**；只在 DetailView/PlayerView 出现 | `DetailView.vue:25`、`PlayerView.vue:37-38`（grep import） |
| creatorWorks 相关 Go | 属创作者页（V2-L），与本三面无关；三个 view 无调用 | `grep creatorWorks client-v2/frontend/src/views/{Search,Discover,Library}View.vue` = 0 命中 |

另一个重要事实（决定“后端差异”是否可能存在）：**v2 与 v1 跑的是同一份核心代码**。
所以凡“核心返回什么”（warning 文案、联想词上限、搜索是否分页、榜单页校验）v1/v2 天然一致，
差异只可能出在**壳层（谁调用、传什么参、显示什么）**。下表凡是“核心侧”的行都给出同一出处。

核心侧公共事实（v1=v2 同源）：
- 搜索即 catalog：`guoapp-src/native/core/app_runtime.go:510-511`（dispatch）→ `:557-560`（非法源报「请选择有效站源」）；
  红果搜索整批返回、`Page` 固定 1、Limited 时 `Warning=「已显示当前可获取的匹配结果，使用更完整的剧名可继续查找」`：`app_runtime.go:587-599`；
  其余在线源搜索按页返回 `HasMore`：`app_runtime.go:601-658`；黄豆无服务端搜索→本地缓存匹配（必要时 warm）：`app_runtime.go:660-687`。
- 联想词只有红果、去重后**上限 10**、只回字符串：`guoapp-src/native/core/app_suggestions.go:11-19`、`:21-53`（10 上限在 :50-52）。
- 个性化 recommendations 是**红果专属**（锁 hongguo catalog、genre 校验）：`guoapp-src/native/core/app_recommendations.go:32-40`。
- 榜单候选按“源可用”过滤：`app_runtime.go:457-462`；候选常量表 12 项：`guoapp-src/native/core/provider_rankings.go:23-37`。
- 红果体裁枚举 `short_play/comic_series/ai_series`：`guoapp-src/native/core/provider_hongguo_catalog.go:14-22`。

---

## 表 1：v1 行为清单（实现为准，非注释）

### 1A. 搜索页

| # | v1 行为 | 出处（文件:行） |
|---|---|---|
| S1 | 页面 = 站源弹层 + 输入框 + 「搜索」按钮；回车即搜 | main.py:1289-1302（returnPressed :1296） |
| S2 | 占位文案带**当前源名**：`输入剧名、演员或题材（仅搜索 {源名}），回车开始搜索`，切源时刷新 | main.py:1066-1070；调用点 :1043、:1295 |
| S3 | 搜索**永远只搜当前源**（单源），`catalog(query,page=1,source=current,lane="search")` | main.py:1434-1435、1442-1443；lane 会话拼装 core_client.py:344-358 |
| S4 | 同词在途重复提交被忽略（防取消抖动）；过期回包按 token 丢弃 | main.py:1429-1433（pending 清于 :1448/:1462）、token :1431-1432/1446-1447 |
| S5 | 发起搜索即写入“最近搜索”（**成功失败都记**），每源去重置顶、上限 10 条，QSettings `search/recent` | main.py:1436、78-80、112-124、91-109 |
| S6 | 空输入框获得焦点 → 弹“最近搜索”下拉（带不可点的「最近搜索」分组头） | main.py:1328-1333、1335-1353 |
| S7 | 输入时：非红果源 → 用输入**子串过滤最近搜索**（大小写不敏感、排除全等词）；红果源 → 走联想 | main.py:1361-1372、1338-1340 |
| S8 | 红果联想：每次按键即请求（**无防抖**），token 丢弃过期应答；条目显示 `label    type`（核心实际只回字符串，dict 分支是防御）；联想空/失败 → **回落过滤后的最近搜索下拉** | main.py:1373-1402（回落 :1394-1400）；core_client.py:341-342 |
| S9 | 点联想/最近词 → 回填输入框并立即搜索 | main.py:1404-1410 |
| S10 | 结果页**分页条被刻意隐藏**（“A search is one result set, not a feed”），只显示第 1 页；结果卡副行=`(更新至 )N 集`+分类 | main.py:1312-1314；widgets.py:603-612、397-401、563-566 |
| S11 | 搜索中状态行 `正在搜索…`，且期间屏蔽结果点击 | main.py:1439-1440；widgets.py:450-458 |
| S12 | 成功计数行 `共 N 部`（单页时）；0 结果 → 居中空态标题=`warning 或「没有找到相关剧集」`+ 副提示「换个关键词，或去「剧库推荐」按分类逛逛」+ 状态行 `共 0 部` | widgets.py:625-636；main.py:1453-1457 |
| S13 | 失败 → 空态 `搜索失败`+原因，状态行 `搜索失败：%s`；**无重试按钮** | main.py:1459-1466 |
| S14 | 结果渲染时按 id 去重（重复 id 不再建卡） | widgets.py:600-601 |
| S15 | 站源弹层：行内可勾选 enabled（发 source_toggled → `core.set_source_enabled`，失败状态栏报「站源开关失败：%s」）；切到未接入源被拒（状态栏「「%s」尚未接入，暂不可切换」并把弹层拉回）；切换成功状态栏 `当前站源：%s（搜索范围已同步）` | widgets_guo.py:699-783（勾选行 :671-676、:755/:761）；main.py:1055-1064、1029-1035、1052-1053；注意：**回退表与 list_sources 归一后所有源 implemented=True**（main.py:55-70、1003），拒绝路径只对“清单里查不到的 slug”生效 |
| S16 | 找剧页顶部搜索框不是页内搜索：回车/点按钮 → **跳到搜索页签并用该词执行搜索** | browse_panel.py:129-131、211-213；main.py:913、941-944 |
| S17 | 点结果 → 详情覆盖页 | main.py:1315、669-671 |

### 1B. 找剧（BrowsePanel）

| # | v1 行为 | 出处 |
|---|---|---|
| D1 | 顶部：标题「找剧」+ 页内搜索框（占位 `输入剧名 / 关键词`）+「搜索」按钮 | browse_panel.py:128-132 |
| D2 | 体裁 Tab 条（兜底 全部/真人剧/漫剧/AI剧），服务器 genre 组到达后**以服务器选项重建**，首个恒为「全部」 | browse_panel.py:31-32、160-187、341-345 |
| D3 | **多维筛选面板**：9 个维度（genre/category/theme/role/epoch/sort/gender/creation_status/online_time），由 `browseFilters(genre,source)` 服务器驱动，每行前插「全部」；首次可见才加载 | browse_panel.py:25-26、98、144-147、311-358；core_client.py:445-449 |
| D4 | 筛选面板第一行是**站源芯片行**（与弹层双向同步；未接入源置灰+tooltip「尚未接入，暂不可切换」；18+ 源名字后拼 ` 18+`；slug 不进筛选条件/摘要） | browse_panel.py:254-310；main.py:929 |
| D5 | 摘要行 `已选：a · b` / `全部剧集` +「重置」（无选中时禁用）+「收起筛选/展开筛选」 | browse_panel.py:137-141、214-220、369-376 |
| D6 | 面板缓存 key=(genre,source)，命中不发请求；切体裁**重拉面板**（不同体裁的服务器选项不同） | browse_panel.py:312、320、328、367 |
| D7 | 数据请求=`browseFiltered(filters,offset,cursor,source)`（非 catalog！）：cursor=服务器 sessionId；`hasMore=hasMore && nextOffset>offset`；连续 2 个“零新增页”停止 | browse_panel.py:377-416（:400-403）；core_client.py:451-455；overlay 红果路径 `zhenguojian/sidecar/core_overlay/discovery.go:413-458`（limit 18、`session_id` cursor、「筛选分页未前进」校验 :455-457），非红果= filters.category 映射到 nativeCatalog、cursor 即页码 `discovery.go:386-410` |
| D8 | 首个 Tab（全部）的 items[0] 上 **hero 大卡**且不进网格；其余 Tab 无 hero | browse_panel.py:387-396；poster_wall.py:557 |
| D9 | 追加结果按 id 去重（PosterWall.append_items `seen`） | poster_wall.py:650-662 |
| D10 | 底部状态行：`等待加载`→`正在加载剧集…/正在加载更多…`→`已加载 %d 部 · 下滑继续浏览/已全部加载`；失败=`加载失败：%s`+「重试加载」按钮（并广播 `找剧加载失败：%s` 到状态栏）；另有常驻「刷新」按钮和墙顶钉住刷新 | browse_panel.py:153-156、383、404、406-409；poster_wall.py:576-588 |
| D11 | 滚动到底（余量 400px）自动翻页；失败后（retry_page 非空）停止自动翻页，只有点重试才继续 | browse_panel.py:152、379、408、417-421；scroll_loader.py:8、40 |
| D12 | 切站源：清面板缓存并整套重刷（已加载过时） | browse_panel.py:221-240 |
| D13 | 刷新=重置游标重拉当前筛选 | browse_panel.py:422-424 |
| D14 | 海报双徽标（VIP/18+ + 集数/状态角标，`_badge_text`：newSeries 优先 episodeText；episodes: ongoing→`更新至N集`、newSeries→`共N集`、默认→`全N集`；无 episodes→episodeText/remark/publishText），副信息=`分类 · 热度`（无则 tags 前 2） | poster_wall.py:52-84、149(browse 开启双徽标) |

### 1C. 剧库推荐（Library tab）

| # | v1 行为 | 出处 |
|---|---|---|
| L1 | 页头：`分类` 下拉（**静态 4 项** 全部/真人剧/漫剧/AI剧）+ `榜单` 下拉（change 即加载第 1 页）+「查看榜单」+「刷新推荐」；**本页没有站源控件** | main.py:1096-1117、1107-1110、44、1193-1198 |
| L2 | 启动后延时 200ms 自动加载推荐流；页签建立时立刻加载候选榜单 | main.py:755、692-694、1170-1187 |
| L3 | 「全部」分类 = **catalog 页码走查**（lane=library；more→页码+1，绝不重发当前页；成功后按 generation 提交游标；**source 不传 → 恒为红果**）；切分类/换一批前先 `reset_recommendation_page` | core_client.py:278-301（source 默认 :344）、323-327；recommendation_panel.py:84-85 |
| L4 | 具体分类 = 核心 `recommendations(category,force,command="more")`（红果专属个性化流，核心回的是**累计合并表**） | core_client.py:300-301；app_recommendations.go:32-40 |
| L5 | more 结果按“已见 id（含未渲染 pending）”去重后入 pending；每页滚动只追加 `BATCH_SIZE=50` | recommendation_panel.py:13、79、106-111、122、146-161 |
| L6 | `hasMore=服务器 hasMore`；连续 2 个零新增页 → 停止 | recommendation_panel.py:116、123-125、183-184 |
| L7 | 状态行 `正在加载推荐…/正在加载更多推荐…` → `已加载 %d 部 · 下滑继续浏览/已全部加载` | recommendation_panel.py:93、172-177 |
| L8 | 空态：`warning 或「暂时没有推荐内容」`盖在墙上；加载失败：状态行 `加载失败：%s` +「重试加载」按钮；墙为空时另叠 `加载失败，请重试`；重试沿用原 (category,force,more,replace) 参数、期间禁止自动翻页 | recommendation_panel.py:127-129、133-142、153-155、163-166 |
| L9| 「刷新推荐」按钮：**有内容时=换一批**（more=True,replace=True：拉下一页新剧替换整墙；若一页无新剧回退 force）；空时=force | main.py:1193-1198；recommendation_panel.py:113-120 |
| L10 | 榜单侧栏（左列 300-340px）：`已加载 N 部 · 下滑继续/已全部加载`、失败 `加载失败，点击重试`（tooltip=原始报错）+「重试加载」；空榜 `warning 或「榜单为空」`；更新时间取 `updatedText` 首段（截「·」）显示 `更新：X` | main.py:1119-1148、1204-1214、1237、1259-1272 |
| L11 | 榜单分页：page>totalPages 拒绝；**按 drama.id 去重、无 id 丢弃**（rank 缺失只是不画徽标，行仍保留）；`hasMore && (无total或page<total)`；连续 2 空页停止；token 丢弃过期回包；滚动到底自动下一页 | main.py:1216-1278；rank_list.py:272-311（seen 去重 :283-291，rank 容错 :121-126） |
| L12 | 榜单行=名次圆徽(1/2/3 特色)+剧名+metric；点行进详情 | rank_list.py:113-165、329-333 |

---

## 表 2：v2 现状

### 2A. SearchView.vue（+ SearchGroups/searchAll）

| # | v2 行为 | 出处 | 对齐 |
|---|---|---|---|
| S1 | 页头标题 + SourceSwitch；输入框 + 按钮 + 「清除」按钮 + 「全部来源」勾选 | SearchView.vue:302-306、308-353、345-352 | ✅（清除/全部来源=➕） |
| S2 | 占位为**静态** i18n：`search.placeholder`＝「搜索剧名 / 标签，回车开始」/“Search titles / tags — press Enter”，不含源名（源名显示在未搜索时的 hint 块） | SearchView.vue:315；i18n.ts:230、929；SearchView.vue:360-365 | ⚠ 文案差异（不含「演员」；动态源名没了） |
| S3 | 单源搜索：`catalog(source=currentSlug, lane='search')`；未勾选「全部来源」时语义与 v1 一致 | SearchView.vue:211-246（:235） | ✅ |
| S4 | 同词重复提交**不拦截**（连点会连发请求，旧回包按 requestSeq 丢弃不上屏；v1 的“取消抖动”问题在 v2 由 seq 丢弃规避） | SearchView.vue:221、236/241/244 | ⚠（少 v1 的 pending 短路，多请求） |
| S5 | 最近搜索：localStorage `zgj.recent.<slug>`，去重置顶、上限 10、发起即记；另有「清空」按钮 | SearchView.vue:48-66、220、355-358 | ✅（清空=➕） |
| S6/S7 | 最近搜索显示为**页内 chips**（未搜索时恒显，不按输入过滤）；无“聚焦弹下拉”交互 | SearchView.vue:355-359 | ⚠ 交互形态改；**“输入时过滤最近词”丢失** |
| S8 | 联想：300ms 防抖 + token 丢弃；门控 = 红果 **或勾选全部来源**；面板含键盘导航(↑↓EnterEsc)+IME 守卫+底部快捷键提示；空/错 → 收起面板（不回落最近词下拉） | SearchView.vue:68-134、155-175、324-343 | ⚠（防抖/键盘=➕改良；回落最近词缺失；非红果+未勾全源时输入无任何下拉） |
| S9 | 点联想 → 回填 + doSearch（只记一次最近） | SearchView.vue:143-153 | ✅ |
| S10 | 单源结果**无限翻页**（page+1, lane=search）；v1 是刻意单页 | SearchView.vue:248-263；PosterGrid.vue:154-160 | ➕v2 多出 |
| S11 | 请求中：按钮禁用+按钮字变「搜索中…」；结果区骨架 12 卡；**不屏蔽旧结果点击**（首搜无旧结果，影响小） | SearchView.vue:345-347；PosterGrid.vue:359-361 | ⚠ 轻微 |
| S12 | **无计数行**：不显示「共 N 部」；0 结果显示 `search.noResult`「没有找到与「q」相关的剧」，**忽略 res.warning**，无副提示行 | SearchView.vue:379-381；对照 v1 main.py:1453-1457、widgets.py:636 | ❌ warning/计数/副提示缺失 |
| S13 | 失败：状态块「搜索失败：」+原因+「重试」按钮 | SearchView.vue:367-370；i18n.ts:241 | ✅（+重试=➕） |
| S14 | 单源结果**不按 id 去重**；扇出组内也不去重；loadMore concat 不去重 | SearchView.vue:237/255；searchAll.ts:98 | ❌（v1 widgets.py:600-601 去重） |
| S15 | 「全部来源」扇出：目标= `enabled && implemented` 源、保持 orderedSources 顺序（红果置顶）；并发≤4；各源 page1、lane=`search:<slug>` 互不取消；组头=站源名+18++`N 条`；组内失败行=「该源搜索失败：<原因（剥 [code] 尾缀）>」+单源重试；全败→顶部条+全部重试；空组=「该源无匹配结果」；组到先渲染（骨架先行）；勾选态持久化 `zgj.search.all` | SearchView.vue:197-209、226-231、265-271、281-288；searchAll.ts:16-18、34-47、51-63、66-69、82-124；SearchGroups.vue:60-85 | ➕ v2 新功能（v1 明确单源；实现自洽） |
| S16 | mock/DEGRADED（无桥→自动落 mock 面）时：搜索页**不显示任何“示例数据/降级”横幅，也不透传 mockCatalog 自带的 warning**；discover/library 会显示 | api.ts:1065-1069、491-493；fixtures.ts:536-554（warning :552）；DiscoverView.vue:179；LibraryView.vue:80-82 | ❌ 谎报面：搜索页 mock 数据无标识 |
| S17 | 点结果 → `router.push(detailRoute(d))` | SearchView.vue:377/388 | ✅（机制=路由替代覆盖页，c 类） |
| S18 | 联想请求次数验证钩子 `window.__zgjSuggestCalls`（mock 面计数）；扇出故障钩子 `window.__zgjSearchFault` | api.ts:971-974；searchAll.ts:72-79 | （钉锚点，见表 3） |

### 2B. DiscoverView.vue

| # | v2 行为 | 出处 | 对齐 |
|---|---|---|---|
| D1 | 标题 + SourceSwitch + 「刷新」按钮；**没有页内搜索框** | DiscoverView.vue:256-267；v1 browse_panel.py:129-131 | ❌ 页内搜索入口缺失 |
| D2 | 芯片行：数据=`browseFilters(source, '')`，红果取 genre 组∩GENRE_TAB_IDS、其它源取 category 组，前插「全部」；失败/缺 overlay → **兜底 hongguo 三体裁**（`discovery_unavailable` 时静默，仅 console.info） | DiscoverView.vue:85-97、134-152；discovery_stub.go:26-36；api.ts:274 | ⚠（有芯片；兜底表对非红果源会发起核心不认的 category→报错，见表3 B-d） |
| D3/D5/D6 | 多维筛选面板/摘要/重置/收起/面板缓存：**全无** | DiscoverView.vue 全文无相应模板/状态 | ❌ v2 缺失（Go 侧 BrowseFiltered 绑定在 app.go:248-260 已有，前端 0 处调用） |
| D7 | 数据请求=`catalog(source, category=chip, page, lane='discover')`：页码自增；`hasMore=!!res.hasMore`（首屏）→`&& !!more.length`（追加）；**无 nextOffset>offset 前进校验**、空 1 页即停 | DiscoverView.vue:154-217（:168-178、:196-207） | ⚠ 数据面/游标语义均与 v1 browseFiltered(sessionId) 不同（b 类，见 B-a） |
| D8 | hero：已按用户反馈移除（注释 + 验收脚本断言不存在） | DiscoverView.vue:6；discover_hero_verify.py:11、142-148 | ➕b（有记录的取舍） |
| D9 | 追加结果**不去重**（注释自认“真实引擎 id 在翻页会重复……页内按 id#page 去重即可”——只保证 key 不撞车，不剔除重复展示） | DiscoverView.vue:203-207；PosterGrid.vue:377 | ❌ 同表3 A-d |
| D10 | 无底部状态行（“已加载 N 部 / 下滑继续浏览 / 已全部加载”不存在）；错误=整页 state-block+重试；loadMore 失败=**toast 3.2s 后消失、永久停翻页（hasMore=false）无原地重试** | DiscoverView.vue:305-326、190-223；对照 v1 browse_panel.py:404、406-409、419-421 | ❌ 状态行缺失；⚠ 翻页失败不可恢复（表3 A-e/A-f） |
| D11 | 滚动自动翻页（最后 1.5 行阈值）；翻页中防重 | PosterGrid.vue:154-160；DiscoverView.vue:192 | ✅（机制 c 类） |
| D12 | 切站源：chips+首页整套重拉；点芯片=loadFirst(false)（不 force） | DiscoverView.vue:230-238、225-228 | ✅ 近似（v1 切源还会重拉面板+缓存失效，因面板缺失无从对齐） |
| D13 | 刷新按钮/下拉手势 = `loadFirst(true)`→catalog **force=true**（跳核心缓存回源；v1 browse 刷新无 force 概念、重新请求即可） | DiscoverView.vue:126-128、261-266；PosterGrid PTR :217-241；actions.go:13-27 | ✅（更强刷，无碍） |
| D14 | 海报：VIP/18+ + 集数徽标（ongoing→「更新至N集」否则「共N集」；releaseStatus 自带「N集」原样用；无集数→枚举映射/原样）；副行=`分类 · 热度` | PosterGrid.vue:59-69、397-408；对照 poster_wall.py:52-84 | ⚠ 文案族差异（v1 默认「全N集」、newSeries 特判 episodeText/共N集；v2 统一「共N集」且丢了 newSeries 优先分支） |
| D15 | warning/降级横幅：`res.warning ||（mock?banner.mock:degraded?banner.degraded）` | DiscoverView.vue:179、269-274 | ✅（比 v1 多 mock 标识） |

### 2C. LibraryView.vue

| # | v2 行为 | 出处 | 对齐 |
|---|---|---|---|
| L1 | 页头：标题+**SourceSwitch**+「刷新」+（窄屏）榜单抽屉开关；分类=chips（browseFilters genre∩GENRE_IDS，失败兜底静态 4 项，展示名走 i18n） | LibraryView.vue:429-456、56-77、94-110 | ⚠（v1 页头无站源开关，见 B-c） |
| L2 | 挂载即 loadChips+loadFirst（红果首屏自然带上）；榜单侧栏并行初始化 | LibraryView.vue:412-421 | ✅ |
| L3 | 「全部」tab：**前端自持游标**（allPage 成功后才 +1；失败不+1 不重发；用 `res.page` 校正核心游标）——但 `source=store.currentSlug || 'hongguo'` 跟随当前源；v1 恒红果。Go 壳 `Recommendations("")` 也实现了同款（source 传空→actions.go 兜底 hongguo），但前端「全部」走的是 api.catalog，**没用 Go 壳这条** | LibraryView.vue:141-150、175；对照 core_client.py:278-301+344、library.go:54-92 | ⚠❌（表3 B-c/B-b） |
| L4 | 具体分类=`api.recommendations(category,force,more)`→核心 recommendations（仍红果专属） | LibraryView.vue:142-144；api.ts:261；bindings_library.go:24-31 | ✅ |
| L5 | more 去重=knownIds（只含已上屏；无 pending 概念，因为不分批）；一次全量上屏（虚拟滚动代替 50/批） | LibraryView.vue:116-139；对照 recommendation_panel.py:79、146-151 | ✅ 语义等价（分批=c 类，见 B-f） |
| L6 | 连续 2 零新增页停（serverMore=false）；hasMore=服务器 hasMore | LibraryView.vue:46-47、134-138 | ✅ |
| L7 | 状态行 `已加载 {n} 部 · 下滑继续浏览/已全部加载`（i18n）+ `retryPending` 时 `加载失败：{m}` | LibraryView.vue:84-90、511；i18n.ts:266-270 | ✅ |
| L8 | 空态 `warningText || library.empty(暂时没有推荐内容)`+重试按钮；more 失败→黄条「加载失败：m」+「重试加载」，重试期禁止自动翻页 | LibraryView.vue:499-509、189-206、208-214 | ✅ |
| L9 | 「刷新」按钮= `loadFirst(true)`（reset+force 回第 1 页）；**“换一批（more+replace）”未移植**（头注释自述，且其理由失实：v1 该分支正是本页「刷新推荐」按钮触发） | LibraryView.vue:13、438-452；对照 main.py:1115-1117、1193-1198 | ❌→b（表3 B-e，注释误导） |
| L10 | 榜单侧栏：候选 chips（正常用 App.RankingBoards=核心可用性过滤；桥失败回退**常量 12 榜**）；`更新：{t}` 取「·」前首段；状态行同格式；空榜 `warning || 榜单为空`；失败 `library.rank.loadFailed`「加载失败，点击重试」**+「重试加载」按钮**（原始报错文本未展示，v1 在 tooltip） | LibraryView.vue:230-243、245-334、372-383；i18n.ts:279、278；对照 main.py:1204-1214、1259-1272 | ✅（报错原文不露出=⚠轻微） |
| L11 | 榜单分页：token、page>1 防并发/越界、seen(id) 去重、2 空页停、`hasMore&&(totalPages==0||page<totalPages)` 全对齐；但**多了 rank 必须为 number 的过滤**（v1 只要求 id，缺 rank 的行仍显示、只是无徽标）；翻页触发=**「加载更多」按钮**，非滚动自动（v1 有 ScrollLoader 自动续页） | LibraryView.vue:336-394（:365 rank 过滤、:588 按钮）；对照 main.py:1216-1278、rank_list.py:283-291+121-126、main.py:1147-1148 | ⚠（表3 A-i/A-j） |
| L12 | 榜单行=名次号(1/2/3 特色)+小封面+标题+metric（metric 缺时用 分类·热度 兜底）；点行进详情 | LibraryView.vue:547-573、288-291、708-720；对照 rank_list.py:113-165 | ✅（兜底行为➕） |

### 2D. Go 壳层（v2）

| # | 事实 | 出处 |
|---|---|---|
| G1 | `App.Catalog(source,query,category,page,lane,force)`→核心 catalog；source 空→hongguo；lane→session `owner:catalog:lane`（同 lane 新请求取消旧请求）；默认 session=`owner:<action>` | app.go:199-206；actions.go:13-27；engine.go:142-157、159-166；engine_test.go:97（laneSession 测试） |
| G2 | `App.Suggest(query)`→核心 suggestions（items 字符串数组、核心限 10） | bindings_suggest.go:10-15；suggest.go:12-14；app_suggestions.go:11-53 |
| G3 | `App.BrowseFilters/BrowseFiltered` 存在但走 discovery 通道；**默认构建返回 code=discovery_unavailable**，`-tags v2overlay` 才接真实现；前端只用了 BrowseFilters，**BrowseFiltered 前端 0 处使用** | app.go:239-260；engine.go:69-73、132-133；discovery_stub.go:26-36；discovery_overlay.go:1-19；`grep browseFiltered client-v2/frontend/src` = 0 |
| G4 | `App.Recommendations/RecommendationPageReset`：category=""→catalog(lane=library) 页码走查 + generation 提交保护（v1 core_client 同款）；非空→recommendations action | bindings_library.go:24-38；library.go:50-99；library_test.go:57/105/119/150 |
| G5 | `App.Rankings/RankingBoards` 位置参数透传核心；页面 500 上限交核心；测试在 rankings_test.go | bindings_rankings.go:26-41；rankings.go:46-58 |
| G6 | 核心错误统一带 `[code]` 尾，callGoRaw 剥掉并保留 ApiError.code | engine.go:40-51；api.ts:77-90 |
| G7 | ListSources 逐行透传核心字段（enabled 缺省补 true、name 缺省回退 displayName/slug）——**与 v1 不同：不覆写 implemented**（v1 main.py:1003 强制 True） | actions.go:41-69；对照 core_client.py:363-377、main.py:1003 |
| G8 | creatorWorks/creatorProfile 绑定存在（bindings_creator.go），属创作者页，三面未调用 | app.go（grep 无调用）|

### 2E. i18n 完备性（三面）

- 用脚本抽取三 view + SearchGroups/PosterGrid/SourceSwitch + searchAll.ts/fixtures 用到的全部 `t()/tf()` 静态键（72 个）逐一比对 zh/en 两表：**0 缺失**（含 `searchAll.errUnknown` i18n.ts:603/1282、`banner.mock|degraded` :254-255/:952-953、`grid.*` :588/:686/:683-685 与 :1267/:1352/:1349-1351）。动态键只有 `library.genre.*` 三个（LibraryView.vue:66-70），均有。
- 机制：`t()` 缺键会回退 zh 再回退**键名本身**（i18n.ts:1414-1421）——即“漏键=界面直接显示键名”，本轮未发现新增实例。

---

## 表 3：差集分类

### (a) v2 缺失/做错，实现者可直接修（不需要产品决定）

| id | 差异 | v1 证据 | v2 证据 | 最小修法建议 | 钉住的断言 |
|---|---|---|---|---|---|
| A-a | **搜索页丢弃 catalog.warning**：0 结果时 v1 用 warning 当空态标题（如红果受限提示、黄豆 warm 提示） | main.py:1453-1455 | SearchView.vue:235-239（未读 res.warning）、379-381 | 单源 doSearch 里 `warning.value=res.warning??''`；空态 `warning || t('search.noResult')` | 扩展 `searchall_verify.py` 或 suggest_verify：mock 下 mockCatalog 恒有 warning（fixtures.ts:552）→ 造 0 结果词，断言空态文本含「示例数据」；Go 不需要 |
| A-b | **搜索页在 mock/DEGRADED 下面无标注**：无桥自动落 mock 面却显示为正常结果（discover/library 都有横幅） | v1 无 mock 面（核心没起直接报「本地核心尚未启动」core_client.py:236-237） | api.ts:1065-1067；SearchView 无 banner（grep `banner\|MOCK\|DEGRADED` SearchView.vue = 0） | 抄 DiscoverView.vue:179 的 `res.warning || MOCK/DEGRADED` 横幅 | Playwright（VITE_MOCK=1）：/#/search 搜索后断言页面存在「示例数据」文案 |
| A-c | **搜索无结果计数行**：v1 有「共 N 部 / 共 0 部」 | widgets.py:625-636、main.py:1457 | SearchView 无 | 结果上方加 `tf('search.count',{n})` 行（新增键 zh/en 成对） | 断言搜索后存在 `共 \d+ 部`/`N titles` 文本 |
| A-d | **追加不去重**：找剧/搜索翻页与扇出组间重复 id 会重复渲染；v1 墙与网格都按 id 去重 | poster_wall.py:650-662、widgets.py:600-601 | DiscoverView.vue:205（concat）、SearchView.vue:255、searchAll.ts:98 | append 时 `Set<id>` 过滤（Library 已是范例：LibraryView.vue:127-133） | 新 mock 钩子 `__zgjDiscoverInjectCatalog='dup'` 返回上页同 id；断言 DOM 卡数不增（照 discover_hero_verify.py 的钩子用法） |
| A-e | **找剧翻页失败后无法恢复**：toast 一闪、hasMore 永久置 false，无原地重试 | browse_panel.py:406-409、417-421（retry_page+重试按钮） | DiscoverView.vue:208-214 | 失败置 `moreError`，渲染「加载失败：m ·重试加载」（复用 library.loadFailedMsg/retryLoad 两键），点击沿用 page 游标 | Playwright：`__zgjSearchFault` 不适用（catalog 单源面），建议新钩子 `__zgjDiscover.failOnce`；断言重试后卡数增长 |
| A-f | **找剧无“已加载 N 部 · 下滑继续浏览/已全部加载”状态行**（页脚信息整体缺失） | browse_panel.py:153-156、404 | DiscoverView 模板无状态行（grep loadedCount = 0） | 加一行 computed（直接复用 `library.loadedCount/scrollMore/allLoaded`） | 断言滚动到底后出现「已全部加载」 |
| A-g | **找剧筛选面板加载失败被静默吞**：v1 显示「筛选条件加载失败：%s」+可重试；v2 只 console.info 后给兜底芯片 | browse_panel.py:325-327、419-421 | DiscoverView.vue:148-151 | catch 里 toast/tf('discover.loadFailedMsg') 一次（不阻断兜底） | mock 钩子 `__zgjBrowseFiltersFail=1`（需新增，仿 maybeInjectFault）+ 断言可见报错 |
| A-h | **找剧翻页停止规则**：v1 允许 1 个空页（连续 2 个才停）；v2 首个空页即停 | browse_panel.py:400-403 | DiscoverView.vue:207 | 加 emptyPages 计数，同 LibraryView.vue:134-136 | 注入钩子返回 page2 空 page3 有数据；断言最终拿到 page3 |
| A-i | **榜单行过滤过严**：`rank` 非数字的行被丢，v1 只丢无 id 行（缺 rank 照样显示无徽标行） | rank_list.py:283-291、121-126 | LibraryView.vue:365 | 去掉 `typeof it.rank==='number'` 条件，rank 缺省显示空位 | Go 单测不合适（前端过滤）；vitest/或 Playwright 注入 mockRankings 去 rank 字段断言行数 |
| A-j | **榜单不随滚动自动续页**，仅手动「加载更多」（v1 滚动到底自动下一页） | main.py:1147-1148、1276-1278 | LibraryView.vue:588（按钮） | rank-scroll 加 IntersectionObserver/滚动阈值触发 loadRankings(rankPage+1) | Playwright：mock 下滑 rank-scroll 到底，断言行数增长且无点击 |
| A-k | **搜索页内去重缺失**（同 A-d 的单源分支）+ 连点同词重复发请求（v1 pending 短路） | main.py:1429-1433；widgets.py:600-601 | SearchView.vue:211-246 | pending 短路（`term===active && loading` 直接 return）并入 A-d 的去重集合 | suggest_verify.py 扩一条：连按两次 Enter 后 `__zgjSuggestCalls` 不变 + catalog 计数不翻倍（需给 api.catalog 加 mock 计数钩子） |

### (b) 需要人类决定（范围/位置/要不要做）

| id | 差异 | v1 证据 | v2 证据 | 决策问句 |
|---|---|---|---|---|
| B-a | **找剧整块筛选体系未移植**：v1 = 服务器多维筛选面板（9 维，含排序/性别/完结状态/上线时间/题材/角色/年代）+摘要/重置/收起；数据走 browseFiltered(offset,sessionId 游标)。v2 = 单排芯片 + catalog 页码。Go 绑定 `App.BrowseFiltered` 已存在（app.go:248-260），前端 0 使用 | browse_panel.py:25-26、311-416；discovery.go:413-458 | DiscoverView.vue:2、134-217；grep browseFiltered=0 | 找剧要不要按 v1 恢复多维面板（含依赖 v2overlay 构建）？还是永久定案“芯片简化版”？ |
| B-b | **剧库「全部」跟随站源**：v1 剧库恒红果（core_client.recommendations 不传 source→默认 hongguo；页头无源开关）；v2 传 `store.currentSlug` 且切源整页重载 | core_client.py:294+344、main.py:1096-1117 | LibraryView.vue:145、400-410、431-436 | 剧库该不该源感知？注意 v2 自己 Go 壳（library.go:74-85 + actions.go:14-16）保留的是 v1 语义，前端绕开了它 |
| B-c | **剧库非红果源 + 体裁芯片的组合未定义**：v2 芯片名从 browseFilters 的 category 组∩GENRE_IDS 取不到时兜底 hongguo 三体裁，但 recommendations 是红果专属（app_recommendations.go:32-40）。选中黄豆/黄果时点「漫剧」=红果内容（或被核心 genre 校验拒绝）。v1 根本不提供此组合 | main.py:1150（grid 无 source 参数） | LibraryView.vue:94-110、141-144 | 非红果源下剧库体裁芯片是隐藏、禁用，还是换实现路径？ |
| B-d | **找剧兜底芯片对非红果源会报「内容分类无效」**：默认构建无 overlay 时，非红果当前源拿到 hongguo 体裁 id，catalog→核心 validNativeCategory 拒绝 | 不存在此问题（v1 overlay 恒在，discovery.go:344-356 给该源自己的 categories） | DiscoverView.vue:87-94、148-151 + app_runtime.go:562-564、app_categories.go:35-83 | （若保留 overlay 构建为唯一发行渠道则影响=dev；否则需按源区分兜底表） |
| B-e | **“换一批”按钮语义**：v1 刷新推荐=有内容时拉下一页替换（空结果回退 force）；v2 刷新=reset+force 回第 1 页。LibraryView 头注释称“v1 该分支无入口”——**失实**：main.py:1115-1117 正是本页按钮（这是本仓第三次“注释≠实现”，此处是 v2 注释曲解 v1） | main.py:1115-1117、1193-1198；recommendation_panel.py:113-120 | LibraryView.vue:13、438-452 | 恢复 replace 语义 or 定案现状（并修正注释）？ |
| B-f | **找剧页内搜索框**（回车跳搜索页执行）整体缺失 | browse_panel.py:129-131、211-213；main.py:941-944 | DiscoverView.vue 无 | 加入口还是靠导航栏搜索页定为替代？ |
| B-g | **搜索分页定位反转**：v1 明确“搜索=一份结果集，分页条隐藏”；v2 做成无限滚动 | main.py:1312-1314 | SearchView.vue:248-263 | 保留 v2 增强 or 回退单页对齐 v1？ |
| B-h | **“全部来源”扇出是 v1 没有的新功能**（组顺序/计数后缀/单源重试/全败横幅均已自洽实现并有验收脚本），无 v1 对齐基准 | 无任何 all-source UI（grep main.py 无） | SearchView.vue:197-209；searchAll.ts 全文；searchall_verify.py A1-A12 | 只需产品确认该特性“超出对齐范围”，无需修法 |
| B-i | **最近搜索=常驻 chips（未搜索时）+可清空**；v1=聚焦/输入才弹的下拉且随输入过滤。切源后 v2 会**用新源自动重搜当前词**（v1 不动） | main.py:1328-1340；1026-1053（不重跑搜索） | SearchView.vue:355-359、290-294 | chips 形态已定案的话，仅需决定是否补“输入过滤最近词”和“切源自动重搜”去留 |
| B-j | **联想门控扩到 searchAll 模式**、v1 仅红果；空/错时 v1 回落最近词下拉，v2 直接收起 | main.py:1369-1372、1394-1400 | SearchView.vue:83、109-113（注释自称“v1 退化路径同款”，实际只有 chips 常驻兜底，形态不同） | 确认注释表述 or 补回落行为 |
| B-k | **搜索占位文案**不含 v1 的「演员」与动态源名（v2 hint 块有源名） | main.py:1066-1070 | i18n.ts:230/929；SearchView.vue:360-365 | 文案定稿 |
| B-l | **18+ 源出厂停用**的语义：v1 靠核心 enabled；v2 mock 面 youzi `enabled:true`（注释自述“便于演示”，与 v1 出厂状态相悖） | main.py:72-73（ADULT 出厂停用注释+核心 registry） | fixtures.ts:46-57（enabled:true 在 :57） | mock fixture 是否应还原“出厂停用”以免验收脚本养成错误预期 |

### (c) 平台机制差异，v2 本就不该照搬

| # | v1 机制 | v2 对应 | 为什么不需要照搬 |
|---|---|---|---|
| C1 | QSettings `search/recent`、`source/current` 持久化（main.py:78-79、1037-1038） | localStorage `zgj.recent.<slug>` / `zhenguojian.source`（SearchView.vue:49；app.ts:19、104-107） | 同一意图的跨平台存储对等物 |
| C2 | 状态栏 `statusBar().showMessage`（切源回执、找剧失败广播等，main.py:1032/1052、browse_panel.py:409） | toast / banner / state-block（DiscoverView.vue:328、LibraryView.vue:507-509） | Web 壳无全局状态栏，信息都到达了用户，位置不同 |
| C3 | CoverGrid/PosterWall 手搭分页栈 + ScrollLoader(400px)（widgets.py:327+、scroll_loader.py） | PosterGrid 虚拟滚动 + 底部 1.5 行阈值（PosterGrid.vue:154-160） | DOM 重排成本模型不同；分页行为对账只看结果（见 A 类补的部分） |
| C4 | QComboBox（分类/榜单下拉）（main.py:1100-1110） | chips 行（LibraryView.vue:473-484） | 控件形态迁移，无行为缺失 |
| C5 | SourceSwitchPopup 行内 enabled 勾选（widgets_guo.py:671-676、761）+ 弹层双份（找剧/搜索各一个） | 单 SourceSwitch + 开关统一去设置页（SettingsView.vue:62；SourceSwitch.vue:38-61） | 交互收敛（b 类已记位置差；机制本身对等） |
| C6 | QLineEdit 原生 IME/回车 | isComposing 守卫 + keyup.enter（SearchView.vue:156、321） | 浏览器键盘事件模型所需 |
| C7 | 详情覆盖页 + 页签切换（open_detail / tabs.setCurrentWidget，main.py:1438、1315） | `router.push(detailRoute(d))` | SPA 路由 |
| C8 | recommendations 一次全量返回 + 前端 50/批渐进渲染（recommendation_panel.py:13、146-151） | 一次全量 + 虚拟化渲染（LibraryView.vue:116-139） | 渲染性能策略不同、可见结果集一致 |
| C9 | 图占位=预绘 QPixmap、主题切换 reload_theme 族（main.py:889） | CSS token + `<img>` | QSS/自绘专属 |
| C10 | v1 无“mock/DEGRADED”态（sidecar 起不来直接拒启动，core_client.py:236-237） | api.ts:1065-1067 无桥降级 mock + 横幅 | v2 headless 验收需要；只要求“标注诚实”（A-b） |
| C11 | v1 `_normalise_source_list` 把核心 implemented **强制改写为 True**（main.py:1003），弹层“未接入拒切”只对表外 slug 生效 | v2 逐字段透传核心 implemented（actions.go:41-69），SourceSwitch 真实禁用 | v2 修对了方向；若要“逐字对齐 v1”反而应保留 bug——建议保持 v2 行为，仅需知悉差异（不是缺陷） |

---

## 4. 我没查什么、为什么

1. **DetailView/PlayerView 全链**（含 EpisodeGrid、HotkeyHelp、SeasonMenu、详情内搜索入口）：任务书列入两个组件，但 grep 证实它们不属于本三面（见 §0）；详情/播放对账已有 `research/v1-v2-detail-parity.md`。
2. **creatorWorks/creatorProfile**：属创作者页（V2-L），三个 view 零调用；只有 bindings_creator.go 存在性检查。
3. **核心算法逐行**（provider_*.go 抓取细节、限频、缓存 TTL）：v1/v2 同一份代码（go.mod:52 replace），行为对等是构造性事实，只需核对壳层入参（已核）。
4. **new_series / calendar / browseThemes / browse（网页分类页路由）RPC**：v1 三面代码未调用（calendar 在日历页、browseThemes 无 UI 调用点，仅 core_client.py:332-339 定义），不在三面范围。
5. **v1 `test_*` 与 selftest.py**：本轮以运行代码为准逐条核对；未把 selftest 当规格源（同“注释不当规格”原则），也未运行任何测试。
6. **未核实项**：
   - 未核实「v2overlay 构建是否覆盖所有发行渠道」——CI 证据只有 `build-v2.yml:201`（Windows job）与 :62（test），若存在别的打包路径，B-d 的影响面会变；
   - 未核实核心 `listSources` 真实返回里是否真有 implemented=false 的源（provider_registry.go:49 说标记只增不减；未跑真机）——影响 C11 的实际可见性；
   - v1 搜索 `hasMore` 在 CoverGrid 隐藏分页条后是否有任何 UI 副作用（读码结论是无，但没跑 v1 本体）；
   - v2 榜单“失败原因不上屏”是否算回归取决于产品口径（已列 L10/⚠，未入 a 类）。
