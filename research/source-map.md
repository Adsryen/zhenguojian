# R0 · Guoapp 站源参照克隆与源映射研究

生成时间：2026-11-07（curl 实测同日）。参照项目：`reference/guoapp`（Flutter UI + Go 核心 `native/core`，浅 clone，只读）。本地核心：`guoapp-src/native/core`（只读对照）。

> 计数核对：guoapp 实际展示源 **12 个**（README 相符），对应 **14 个内部 source id**——"黄果"为分组名（groupId `huangguo`，`lib/models.dart:24-28`），下分 视频 / AI / 旧版 3 变体（`lib/models.dart:29-34` entryName）。本文表格按 14 id 展开。

---

## ① 12 源总表

列内文件路径均指 `reference/guoapp/`；端点省略公共前缀；"状态"以本地 `guoapp-src/native/core` 是否有对应 provider 判定。

| 展示名 | 内部标识 | 端点（目录/搜索/详情/分集/取流） | 搜索形态 | lanes | seasons | catalog kinds | 本地 slug 对照 | 状态 | 连通实测（2026-11-07，curl 10s） |
|---|---|---|---|---|---|---|---|---|---|
| 红果 | `hongguo` | H5 站 `hongguoduanju.com`：`/category/{route}`、`/detail?series_id=`、`/player/{series}/{video}/`（native/core/provider_hongguo.go:17,111,151,219）；搜索 H5 `GET /api/search/result/?query=&page=&count=`（本地 provider_hongguo_search.go:151）；App API `api5-normal-sinfonlineb.fqnovel.com`：`/reading/bookapi/search/tab/v/`、bookmall `tab/v1/`+`cell/change/v1/`、feed `POST /reading/distribution/category/landpage/v/`（provider_hongguo_search.go:25、provider_hongguo_app.go:18）；设备注册 `log.snssdk.com/service/2/device_register/`（provider_hongguo_device.go:16）；媒体 Referer `novel.snssdk.com` | 在线搜索 + 联想词（全 app 唯一，models.dart:23） | 无 | **有**（唯一：provider_hongguo_search_seasons.go:32,81,124,196 按「第N季」补搜合并） | genres（app hongguoAppGenres + `tag:` 题材，app_categories.go:44-50,177-194）+ 推荐 tab（recommendGenre=`find_drama`，source_catalog.dart:26,29）+ **榜单 4 个**（provider_rankings.go:23-27：总热播/真人剧/漫剧/AI剧） | `sourceHongguo` 恒可用（app_build.go:8-11）；本地文件比 ref 更全（web_category/newseries/calendar/identity/media_cache/public + sign6/） | **已有** | 目录 ✓ 200 SPA；搜索 ✓ 200 JSON searchList；fqnovel 未签名请求不可 curl（需 sign6 X-Gorgon 等，本地 sign6/sign6.go） |
| 韩小圈 | `hanxiaoquan` | `www.jennyhow.com`：目录 `GET /hxq/{cat}.html`、`/hxq/{cat}-{page}.html`（provider_hanxiaoquan.go:187-189）；详情 `GET /hanxiaoquan/{id}.html`（:315）；线路 tabs `href="#playlist{N}"`（:38）；分集 `/play/{id}-{a}-{b}.html`（:39）；取流 play 页 `var now=` / 首个 m3u8（:40-41,398-404）；搜索 `GET /search.php?searchword={q}` 仅 1 页（:430-433） | 在线（站内页抓取） | **有**（playlist tabs，取集数最多线路 :215-287） | 无 | 硬编码 4 类：最新韩剧/韩国电影/韩国综艺/韩国动漫（provider_hanxiaoquan.go:20-25） | 本地 grep `hanxiaoquan` 无任何实现 | **缺失** | 目录 ✓ 200 HTML 38KB；搜索 ✗ 200 但 733B JS cookie 挑战页（`idc_search_browser`+`_idc_pass`），curl 过不去 |
| 鬼片 | `guipian` | `guipianwu.com`：目录 `GET /list/{cat}.html`、`/list/{cat}_{page}.html`（provider_guipian.go:231-233）；详情 `GET /nv/{id}.html`（:370）；线路 tabs `ewave-playlist-N`（:58）；分集 play 页限本站 `/play/*.html`（:360-363）；取流 `var now=` / 首个 m3u8/mp4，Referer=站根（:60-61,481） | **RSS 匹配**：拉 `GET /xml/rss.xml`（:494），regex 取 title+`/nv/(\d+).html`（:63-65），本地包含匹配（假分页） | **有**（ewave tabs，取集数最多 :271-342） | 无 | 硬编码 19-20 类（provider_guipian.go:20-40，id 3,6-18,23-26） | 本地无 provider_guipian.go | **缺失** | HTML 目录/首页 ✗ 403「没有找到站点」（vhost 未绑定，疑似暂时故障）；RSS ✓ 200 XML 82KB |
| 青空 | `sorani` | 站 `www.sorani.net`，API `https://api.sorani.cc/sorani-cms/api`（provider_sorani_client.go:18-19）：目录 `GET /video?page&size=24[&categoryId=]`（provider_sorani.go:196-205）；搜索 `/video/search?keyword=`（:201）；详情 `/video/{id}`（:245）；分集在 detail `episodes[]`（:260-307，VideoURL=`sorani-play://{episodeID}`）；取流 `GET /video/episode/{id}/play?lineCode=` Bearer（:311）；账号 `/auth/behavior-captcha/generate|verify`、`/auth/login|refresh|logout`（provider_sorani_account.go:218,276,290,186,315） | 在线 | 线路=lineCode（默认 `anime_jp_m3u8`，:17,335） | 无 | 硬编码 3 类：TV番剧/剧场动画/特摄剧场（:24-28） | `sourceSorani` 有 provider_sorani.go/_client.go（diff 78/32 行），**本地缺 provider_sorani_account.go**（登录/验证码） | **已有（账号模块缺）** | 目录 ✓ 200 JSON；搜索 ✓ 200 JSON（无鉴权可浏览）；播放需登录（canPlay=false→登录，:320-361） |
| 黄豆 | `huangdou` | hostPool 轮换 `ferncider.cc / onyxripple.cc / reeflumen.cc / niniharbor.top / halomeadow.top / larksummit.icu`（provider_huangdou.go:70-86）；全 `POST {host}/api{path}`：`/drama/list`、`/drama/detail`（provider_huangdou_detail.go:49）、`/drama/play{id,seq}`（:146）；兜底流 `{host}/api/drama/hls/{id}/{seq}/play.m3u8?line=free`（:155）；body=IV+AES-CBC(gzip(JSON))，key=HMAC-SHA256(platformKey,rid)，sign=sha256('Dart\|sid\|rid\|ts\|path')+'-'+ts（:291-337,371-380） | **只搜本机缓存**（app_runtime.go:627-643 fall-through LocalSearch=true） | 无 | 无 | 仅「全部」（app_categories.go:95-96）+ **榜单 5 个**（provider_rankings.go:28-32：总榜/魔改/搜索/收藏/完结） | `sourceHuangdou` 已有（provider_huangdou*.go 齐，diff 128 行） | **已有** | 明文 POST → `{"status":"n","error":"请求数据错误","errorCode":2001}`=服务活着需加密 ✓(条件)；站首页 200 SPA | 
| 剧果 | `huangju` | API `https://api.huangju.net`（provider_huangju_client.go:19）：`GET /categories`（provider_huangju.go:111）+硬编码 `@new` 最新（:18）；目录 `/dramas?page&[q=|sort=new|sort=hot|category=]`（:136-151）；详情 `/dramas/{id}`（:208）；播放 `/play/{id}`（:330）；访客 `POST /auth/guest{deviceId:uuid}`→token（client:96） | 在线（`q=` 参数） | 无 | 无 | 远程 categories + 热门/最新 tab | `sourceHuangju` 已有（diff 14/4 行，近同源） | **已有** | 目录 ✓ 200 JSON total=3713（未鉴权）；搜索 ✓ 200（q 合法） |
| 野果 | `yeguo` | base `attract.vgbyziuz.cc`、transit `ygdj15.com`、备份 `as/anyway.vgbyziuz.cc`、`analyze.buxefaex.cc`（provider_yeguo_client.go:32-37）；**协议参数（key/iv/signKey/identifier）运行时从 Nuxt JS bundle 正则收割**（:38-45,364，API base=origin+`/api.php`）；路由：分类 `/api/home/contentOptions`（provider_yeguo.go:52,621）、目录 `/api/theater/exploreList?page&limit=20`（category=`field:value` 拆分，:136-147）、搜索 `/api/search/result?keyword=&tab=video`（:139）、详情 `/api/playlet/detail`（:208）、播放 `/api/playlet/play`（:275）、分集页 `site+/drama/video/{id}/`（:197）；公共参数 bundleId=com.pwa.mater、version=1.3.2、oauth_type=web、via=pwa、oauth_id=trace_id=identifier（client:615-616） | 在线 | 无 | 无 | 远程 contentOptions | `sourceYeguo` 已有，但本地 client diff 343 行（实现分叉较大） | **已有（分叉）** | ✗ curl 不可验：公共请求 → 404 Nuxt SPA 页（需 JS bundle 协议收割+AES，无法脱壳测试） |
| 帝果 | `dsd` | `www.dsd.com.se`（provider_dsd_catalog.go:18），AppleCMS `/index.php/vod/{type,show,search,play,detail}`（:49-55）：目录 `/index.php/vod/type/id/{cat}/page/{n}.html`（:240）；搜索 `/index.php/vod/search.html?wd=&page=`（:250）；分类抓首页（:99）；分页校验 el-pagination active==请求页，不符报错（:185-222）；播放 player_* JS JSON、sid=线路 nid=集（provider_dsd_playback.go:19-64） | 在线 | **有**（sid 多线路） | 无 | 远程 vod type | `sourceDsd` 已有（provider_dsd_catalog.go 与 ref **SAME**，playback diff 16 行） | **已有** | ✗ 443/80 Connection timed out（www+apex），本机 DoH 亦不可达 → 不可达/被墙未知 |
| 禁果短剧 | `jinguoduanju` | `jinguoduanju.app`（provider_jinguoduanju_client.go:19），API base 候选 `…/api/v1`、`jgdj.app/api/v1`、`play.jinguoduanju.app/api/v1`（:144-145）；分类 `GET /top-navigation`（frontendName、active/valid、targetType 过滤）；目录 `GET /dramas?page&pageSize[&q=|&sort=heat|&navigationId=|&tags=][&displayArea=HOME_FEED]`（provider_jinguoduanju.go:100-121,203）；搜索同 `/dramas?q=`；详情 `/dramas/{id}`（:280，episodes[] 内联）；取流 `GET /episodes/{id}/play`（:323，VideoURL=`jinguoduanju-play://{id}` marker，:269）。**JSON API 无加密无鉴权** | 在线 | 无 | 无 | 动态 top-navigation（硬编码兜底 home'推荐'/heat'热度'，:13-14） | 本地无任何 jinguoduanju 文件 | **缺失** | ✓ 目录 200 原始 JSON 数组；✓ 搜索 q=闯 过滤生效（**完全无需 auth**） |
| 禁果 | `jinguo` | API 双线路 `[https://d1wyuxwdrlykj1.cloudfront.net, https://hghts.rlkfus.cn]`（provider_jinguo_client.go:27）+ 故障切换（:204-224）；AES 协议 legacyProtocol{InterfaceKey=65dc07d1b7915c6b2937432b091837a7, ParamKey=ParamIV=BxJand%xf5h3sycH}（:28），`?data=` 加密参数（:235-244），回包 hash=true 时 decryptResponse（:312-320）；访客登录 `POST /api/app/mine/login/h5{devID,sysType:ios}`（:153-165），session 文件 jinguo-session.json（:22），X-User-Agent `BuildID=com.hgaidj.ozThree;SysType=android;…IsH5=1`（:23），错误码 5005=重新登录（:176-180）；路由同 legacy 家族：分类 `/api/app/playlet-tab/all`（provider_jinguo.go:58）、精选 `/api/app/playlet-recommend/home/choose`（:97）、最新 `/api/app/playlet-recommend/home/new`（:99）、tab `/api/app/playlet/home/tab/{id}?tabSortType=1`（:105）、搜索 `/api/app/playlet/keyword/search?keyword=`（:95）、详情 `/api/app/playlet/detail/{id}`（:180）、分集 `/api/app/playlet-chapter/list/{id}`（:196）、取流 `GET /api/app/vid/h5/m3u8/{path}?token=`（:250，locked=VIP/金币 gate :234） | 在线 | 无 | 无 | tabs(远程) + 精选/最新 | 本地无 jinguo 文件（注意：本地 `cloudfront` slug ≠ 禁果） | **缺失** | ping ✓ 200 {code:200,hash:true}；无 token 目录 → {code:5005,"no token"} = 活着，需加密登录 ✓(条件) |
| 黑桃 | `heitao` | `heitaodj.com`（provider_heitao.go:18），AppleCMS：分类抓首页 nav（`dsdRouteParameters` 解析 vod/type 链接，:130-154）；目录 `/index.php/vod/show[/id/{cat}][/page/{n}].html`（:233-249）；搜索 `/index.php/vod/search/wd/{q}.html[/page/{n}/]`（:236-239）；详情 `/index.php/vod/detail/id/{id}.html`（:325）；分集 tabs sid=线路（取最多 :271-318），episode `/index.php/vod/play/id/{id}/sid/{s}/nid/{n}.html`（:311 marker heitao-player://）；取流 `GET /index.php/vod/player/id/{id}/nid/{n}/sid/{s}.html` → JS 变量 `player_aaaa={...}` 大括号扫描（:396,448+）{url,encrypt}，encrypt 0原样/1 URL 反转义/2 base64 再反转义（:460-462）；**年龄墙 cookie `heitao_age_confirmed=1`**（:20-21，resolve 注入）；用 golang.org/x/net/html DOM（providerHTMLNodes/FirstClass/Attr/Text helpers） | 在线 | **有**（sid） | 无 | 远程首页解析（非硬编码） | 本地无 heitao 文件 | **缺失** | ✓ 目录 200 HTML 40KB；✓ 搜索 200 15KB；首页带 age cookie 200 |
| 黄果·视频 | `huangguo-video` | `huangguo.video`（provider_huangguo.go:47）：目录 `GET /videos?page={n}[&category={id}]` HTML 抓取（app_runtime.go:715-725，卡片类 `bg-black/55`、`text-gold-dim` provider_huangguo.go:632）；分类抓 /videos 页 a[href] 的 `category=` 参数（app_categories.go:115-121,209-227） | 只搜本机缓存 | 无 | 无 | 远程 HTML 分类 | `sourceHuangguoVideo` 已有（本地 enum/switch 均在） | **已有** | ✗ huangguo.video 443 timeout（不可达） |
| 黄果·AI | `huangguoai` | `huangguoai.com` + **8 mirrors**（provider_huangguo.go:44，rehostProviderURL 换 host 保路径 :470-476）；**所有调用走信封 `POST {base}/api/edge/request` {method,path,body,content_type}→{status:1,msg,data}**（provider_huangguo_api.go:16-40，本地缺该文件）；内部路由 detail `/api/videos/{id}`（:49）、play `/api/videos/{id}/play?ep=N&page_texts=0`（:91）、search `/api/search?q=&page=&size=20`（:109）、分类页 4 类并行拉 `/api/videos/category/{id}?sort=hot&page&size=24`（app_categories.go:229-280，并发槽 2） | 在线（app_runtime.go:571 `searchHuangguoAI`；**README:89 写"只搜本机缓存"与代码不符**） | 无 | 无 | 硬编码 4 类 ai-duanju/ai-manju/ai-huanlian/ai-mogai（app_categories.go:21-26）+ **榜单 3 个**（provider_rankings.go:33-35：热播/推荐/潜力 TOP20） | `sourceHuangguoAI` 已有但**实现陈旧分叉**：本地无 edge 信封（app_categories.go:200 直连 `/api/videos/category/...`）、详情=HTML 抓取 `/detail/{id}/`（provider_huangguo.go:243-249）、mirrors 只 5 个 | **已有（存疑：实现落后 ref）** | 主域 ✗ timeout；mirror `thu.lbvrhyir.cc` → 301 → `https://26d.gitiecgad.cc/` 200 157KB ✓（**注意 gitiecgad.cc 不在 pool**）；edge/request 明文构造 → {"error":"请求无法处理"}（活着，需 app 精确信封）；ttvoij.ediayikma.cc ✗ |
| 黄果·旧版 | `cloudfront` | API 发现制：前端 `d2pypzndaqisk.cloudfront.net`（provider_discovery.go:15），从其 main-*.js 正则收割 `https://[a-z0-9]+\.cloudfront\.net` 至多 8 个（:111-150），fallback 3 hosts（:18-22），探活 `GET {h}/api/app/ping/check` 期望 code==200（:80-100）；**协议密钥实时 parse 自前端 JS**（interfaceKey/parameterKey/parameterIv，provider_legacy_session.go:211-222，缓存 24h）；访客 POST `/api/app/mine/login/h5`（:184）session 文件 huangguo-session.json；路由 `/api/app/playlet-tab/all`、`/api/app/playlet/home/tab/{id}`、`/detail/{id}`、`/playlet-chapter/list/{id}`、m3u8 `/api/app/vid/h5/m3u8/{path}`（provider_legacy_app.go:19,35,84,89,143）；`?data=` AES（provider_legacy.go:232）、回包 hash 解密（:38-60）、Origin/Referer=前端站（:264-265）；默认配置 dr6skssi3nxbk.cloudfront.net + CDN sjljsla.lkkwip.cn + iPhone UA `BuildID=com.abc.Butterfly`（config_defaults.go:3-7） | 只搜本机缓存 | 无 | 无 | tabs `/api/app/playlet-tab/all`（app_categories.go:107-114） | `sourceCloudFront` 已有，provider_legacy*.go 与 ref **SAME** | **已有** | ping ✓ 200；前端 ✓ 200 Vue SPA；目录需 token+AES ✓(条件) |

### 状态计数
- **已有 7 / 12**（红果、青空*、黄豆、剧果、野果、帝果、黄果3变体）——*青空账号登录模块（captcha）本地缺文件。
- **缺失 5 / 12**（鬼片、韩小圈、禁果短剧、禁果、黑桃）。
- **存疑 0**（任务点名的 5 个存疑映射全部查实，见③；另注：野果本地实现分叉 343 行、黄果AI 本地实现落后 ref，属"已有但需对齐"）。
- 搜索形态盘点（ref app_runtime.go 分发）：在线 11 源（含鬼片 RSS 形态、黄果AI），本机缓存 3 源（黄豆、黄果·视频、黄果·旧版）；联想词仅红果；seasons 仅红果；榜单仅红果/黄豆/黄果AI。

---

## ② 缺失源移植要点清单

公共 plumbing（每个新源都要动，本地文件均给出）：
1. `guoapp-src/native/core/provider_huangguo.go:23-31` 增 const、`:90-97` isHuangguoProviderSource 增 case、`:99-120` canonicalProviderSource 增域名 case（含域名→slug，如 heitaodj.com、guipianwu.com、jennyhow.com、jinguoduanju.app/jgdj.app、d1wyuxwdrlykj1.cloudfront.net、hghts.rlkfus.cn）。
2. `app_categories.go` validNativeCategory + nativeCategories switch 增 case；`app_runtime.go` nativeCatalog/nativeDetail/search/resolve switch 增分发；`app_build.go` nativeSourceAvailable 由 isHuangguoProviderSource 自动覆盖（buildAllSources=true 时）。
3. `models.dart` 展示名/开关与 Go 侧 slug 字符串保持一致（`lib/models.dart:36-59`）。

| 源 | 建议新建文件 | 关键逻辑/鉴权/加密点 | 预计难度 |
|---|---|---|---|
| 禁果短剧 | `provider_jinguoduanju.go` + `provider_jinguoduanju_client.go` | JSON API **零加密零鉴权**（直接搬 ref，client:144-145 多 base 探活轮换 + listing 缓存 256KB 上限）；`/top-navigation` 分类；取流 `/episodes/{id}/play`。连通已实测通过，**首选快赢** | **低**（~1 天含测试） |
| 鬼片 | `provider_guipian.go` | 纯 HTML regex 抓取（无 API 无鉴权）：卡片/详情/ewave tabs/play 页 `var now=`（ref :44-61）；**搜索=RSS `/xml/rss.xml` 标题匹配**（:489-531）；Android UA；HTML 站当前 403（vhost 故障）——先照 ref 写好，联调靠 RSS + 换网重测目录 | **低**（与韩小圈同构，可共享 helper） |
| 韩小圈 | `provider_hanxiaoquan.go` | 同鬼片模板（eWave/macms 类）：`/hxq/`、`/hanxiaoquan/`、playlist tabs、play `var now=`（ref :28-41）；广告文本 `@…@` 清洗（:32,340）；搜索 `/search.php?searchword=` 已实测出现 **JS cookie 挑战闸**（idc_search_browser/_idc_pass），需在 Go client 里模拟"设 cookie→带 `_idc_pass` 重进"两跳，否则搜索 5xx/空 | **低→中**（挑战闸适配） |
| 黑桃 | `provider_heitao.go` | AppleCMS **DOM 解析**（golang.org/x/net/html，复用 dsdRouteParameters/providerHTMLNodes helper，与帝果共享）；年龄墙 cookie `heitao_age_confirmed=1`（ref :20-21,494）；取流=player 页 `player_aaaa` JSON + encrypt 三态解密（0原样/1 URL 反转义/2 base64，:448-475）；分类动态抓首页（非硬编码） | **中**（~1-2 天） |
| 禁果 | `provider_jinguo.go` + `provider_jinguo_client.go` | **AES 加密信封 + 访客登录**：协议常量硬编码（client:28 InterfaceKey/ParamKey/ParamIV）、`?data=` 参数加密（:235-244）、回包 hash=true 解密（:312-320）、session 文件 jinguo-session.json + 5005→重登、X-User-Agent 伪装 android。**好消息：与本地已有的 provider_legacy*.go（黄果旧版，SAME 同源）同族协议，可直接复用其 AES/envelope/login 代码**，把密钥换成禁果常量即可 | **中高**（1-2 天，靠 legacy 复用降半） |
| （补缺）青空账号 | `provider_sorani_account.go` | 行为验证码闭环：`/auth/behavior-captcha/generate|verify` + `/auth/login` + `/auth/refresh`(RefreshToken) + session 持久化 + captcha track 校验（ref :218,276,290,186,112-150,251）；本地现有 sorani provider 播到 canPlay=false 只会报「需要登录」而无登录通道 | **中**（~1 天，含 lib/ui/settings/sorani_account_view.dart 对应 GUI 另计） |
| （对齐）黄果AI | `provider_huangguo_api.go` + 改 provider_huangguo.go mirrors→8 个 | 把本地"HTML 详情 + 直连 category"升级为 **edge/request 信封 API**（ref api.go:16-40,49,91,109）；mirror pool 对齐 8 个 + rehostProviderURL 换域（ref :44-47,470-476）；注意实测镜像已 301 漂到未收录域 `gitiecgad.cc` → 需跟随 301/更新 pool | **中低**（0.5-1 天） |

---

## ③ 本地存疑映射结论

| 本地 slug | 实为 | 依据 |
|---|---|---|
| `dsd` | **帝果**（www.dsd.com.se，AppleCMS HTML 站） | provider_dsd_catalog.go:18；canonicalProviderSource `dsd\|dsd.com.se`（provider_huangguo.go:99-120） |
| `sorani` | **青空（青空次元）**，www.sorani.net / api.sorani.cc | provider_sorani_client.go:18-19；models.dart:38 展示名"青空" |
| `cloudfront` | **黄果·旧版**（legacy 家族），**不是禁果**！默认域 dr6skssi3nxbk.cloudfront.net（config_defaults.go:4）。禁果是另一家 `d1wyuxwdrlykj1.cloudfront.net`（jinguo_client.go:27），本地完全没有 | provider_discovery.go:15-22；canonicalProviderSource 仅裸 `cloudfront`→legacy 默认域 |
| `huangju` | **剧果**（huangju.net / api.huangju.net，JSON API） | provider_huangju_client.go:19-20 |
| `tideember` | **黄豆旧域名**（tideember.cc、xqjurgek.top 在 canonical switch 映射到 huangdou），**不是青空** | provider_huangguo.go canonicalProviderSource `huangdou\|tideember.cc\|xqjurgek.top` |
| 鬼片 / 韩小圈 | 本地**确认完全无实现**（grep `guipian\|hanxiaoquan` 零命中，除测试噪音） | guoapp-src/native/core 全目录 grep |

---

## ④ GUI 版式参数附录（lib/ui，供 UI 翻新直接抄）

主题（`lib/ui/theme/app_theme.dart`）
- M3 `ColorScheme.fromSeed`，seed=系统动态色（`system_color.dart:6-16`：Android palette.primary40 / Win/mac accent）；scaffold=surface；appBar 标题 20/w700、scrolledUnderElevation 0；divider outlineVariant 1px；TV 焦点：按钮 min 52x48，focus 叠加 onSurface α.12，focusColor primary α.16（app_theme.dart:36-95）。

海报卡（`lib/ui/widgets/drama_tile.dart`）
- 封面圆角 12（:11）；宽高比 2/3（:61）；按压/悬停蒙层 onSurface α.12/.08、120ms（:45,70-78）；TV 焦点缩放 1.06（:95-99）；标题 14（TV 15）/w600/高1.3 两行，副题 12/13 onSurfaceVariant 单行；间距 cover→title 9、title→subtitle 4（:184-187）；行高公式 width×1.5+9+title+4+subtitle（:157-162）。

网格（`lib/ui/widgets/drama_grid.dart:14-28`）
- phone 3 列/间距 10/行距 22；window 列=max(2,(w+18)/178)/18/24；pad clamp(w/180,4,9)/18/22；TV clamp((w+20)/120,3,8)/20/24；触底自动加载 extentAfter<600（:181）；FeedFooter 高 72（:216-217），加载点 radius 999（:250）。

封面角标（`lib/ui/widgets/cover_image.dart`）
- DramaCover radius 12，placeholder surfaceContainerHighest + Icon movie_creation 36（:57-64）；底部渐变 transparent→0xC7000000（center→bottom，:100-110）；左下 badge/`共N集` 12 号白 left9/bottom9（:111-119）；右下评分 0xFFFFC542 13/w700（:120-132）。
- VIP 角标：金底 0xFFF6C86B / 字 0xFF40300D 11/w800/radius6/pad 7x3，有名次时挪 right8/top8（:11-35,133-139）。
- **榜单名次角标 RankBadge**：minW28×h26 pad x8；top3 名 primary（dark 模式 inversePrimary）、其余 0x8C000000；**圆角仅右下 10**；字号 top3=15 其余 13/w800；Positioned left0 top0（:140-179）。封面解码宽 440（:200），失败给 refresh 重试。
- ProgressPoster（追剧/历史卡）：左下 tag 黑 α.55 r6 pad7x2 11/w600（:246-269）；右上 badge primary 底 onPrimary 11/w700 r6（:270-293）；进度条非 TV 贴底（inset radius12、minHeight3、r1.5、色 primaryFixedDim、轨白 α.3），TV left/right/bottom8、h4、r2（:310-326）；选中态 primary 边框 w3 + 右上 26 圆勾选（:327-370）。

发现页（`lib/ui/explore/explore_page.dart:251-352`）
- tab 顺序：推荐（仅红果 find_drama recommend）/ 全部 / 各榜单（"榜单·名"，名次用 RankBadge，rankOf :205-237）/ 各题材；Hero 作用域 `browse|category`（:317）。

详情页（`lib/ui/detail/detail_page.dart`）
- **封面模糊底 CoverBackdrop**（`widgets/cover_backdrop.dart`）：ImageFiltered blur sigma=36，opacity dark .55 / light .45，上叠渐变 surface α0→1 stops[0,.92]（detail_page.dart:411-435）；顶栏高 desktop44/其余48（:630）；三档 Metrics（phone/desktop/pad，:81 起）；**选集 30 集一组** _GroupChip（:1387 起），选中 FilledButton.tonal；EpisodeGrid 分页组=30（`player/episode_grid.dart:34-49`，组页签 "1-30"）。

观看历史（`lib/ui/history/history_page.dart:18-47`）
- 本地零点分桶 今天/昨天/更早 三段 header。

追剧（`lib/ui/follow/follow_page.dart:23-120`）
- 「有更新」置顶排序 + 更新集数角标（右上 badge）；后台每 30 分钟检查更新（README:54）。

播放页（横屏/桌面 `lib/ui/player/landscape_player_page.dart`、`player_surface.dart`）
- **右侧选集栏**：_Panel{episodes,settings,speed,quality}（:32）；侧板宽 360 slideFrom(1,0)（:522-542）；面板底 surfaceContainerLow α.94（:1098-1141）。
- **悬停控制条（清晰度/倍速/音量）**：player_surface.dart 控制条 Row=上一集/播放(48,icon30)/下一集+时间(13 白 α.86 tabular)+弹幕开关+Spacer+清晰度/倍速文字按钮，MouseRegion onEnter 弹 popup offset(0,-6)、onExit 延迟关（:693-700,884-900）；音量 popup 宽 44 高 156、竖 Slider trackHeight4 thumb r7 overlay r12、active 白/inactive 白 α.25（:822-860）；底部 80px 悬停热区（:381-395）；hint 气泡黑 α.72 r12 pad 16x10（:866-900）。
- 全屏控制条：上排高 56 top8，标题 16/w700、副 12 白 α.78 shadow 0x66000000 blur2；进度条 bottom60 高 28 thumb8（mini 20/6）；下排高 48 bottom8，播放键 44(icon30)，倍速/清晰度文字钮 14 号，上/下一集、选集 icon28；**3s 自动隐藏**（landscape :200-207），双击/单击判定 300ms（:246）。
- 选项列表样式：行高 36 minW72 pad14 字号 14，选中 primary w700，容器 0xE0141414 r12 pad6（landscape :1190-1230）。倍速档 [.5,.75,1,1.25,1.5,2,3]（playback_preferences.dart:3）。

---

## ⑤ 风险注记

1. **端点漂移（高）**：黄果 AI 主域 huangguoai.com 实测 timeout，mirror `thu.lbvrhyir.cc` 301 到 **不在 pool 的新域 `26d.gitiecgad.cc`**——ref 硬编码 8 mirrors 已滞后，移植时应实现 301 跟随/域收割，而非只抄 pool。帝果 dsd.com.se 全超时（被墙或换域未知）。黄果·视频 huangguo.video timeout。
2. **运行时协议收割（高）**：野果 key/iv/signKey/identifier 从 Nuxt bundle 正则收割（yeguo_client.go:38-45）、黄果旧版协议密钥 parseLegacyProtocol 从前端 JS 实时解析并 24h 缓存（legacy_session.go:211-222）——前端一改即崩，必须保留 ref 的多 fallback host（discovery.go:18-22）与失败告警。
3. **鉴权/反爬闸（中）**：鬼片 HTML 站 403（RSS 仍活，目录/详情/取流暂不可用，需网络侧或等待恢复）；韩小圈 search.php JS cookie 挑战（需两跳模拟）；禁果/旧版需访客登录+AES+hash 解密，禁果 5005=重登；黑桃年龄墙 cookie；青空播放必须账号（behavior-captcha，本地缺登录模块=当前青空在本地**能逛不能播**）。
4. **RSS-only / 本机搜索源（中）**：鬼片搜索仅匹配最新 RSS 条目（覆盖面窄，旧片搜不到）；黄豆/黄果视频/旧版 README 标"只搜本机缓存"——若 GUI 照抄需在搜索框文案上区分，否则用户预期落空。README:89 与代码冲突处（黄果AI 实为在线搜索）以代码为准。
5. **红果字节风控（中）**：本地注释明示网页 `/search` 被 JS 风控 500，走 H5 `/api/search/result/`（provider_hongguo_search.go:151）；app API 依赖 sign6 签名 + 设备注册（log.snssdk.com），UA/参数一改即挂。
6. **计数口径**：12 展示源=14 内部 id（黄果 3 变体共享 groupId），移植任务估工时按 14 id 展开避免漏做；`buildAllSources` ldflags 关闭时本地仅红果可见（app_build.go:5-11），实测需带 flag 构建。

---
*实测方法：curl GET（个别 POST 探活），10s 超时，桌面 Chrome126 UA（黄豆/禁果另带对应 header 组合）；只记录状态与响应头/截断体，未落盘大响应。所有 ref 行为描述均来自源码阅读，未执行任何 ref 代码。*
