# zgj-v3 容器版使用手册

v3 容器形态：真果鉴前后端单容器单进程（sidecar web 模式），浏览器访问即用。
设计依据见 [`research/web-container-design.md`](../../research/web-container-design.md)。

## 部署（115 本机，已就绪）

```sh
cd /home/ryen/Git/zhenguojian
# 首次：凭据（compose 文件同目录的 .env，已 gitignore）
cat client-v2/docker/.env        # ZGJ_WEB_USER=ryen / ZGJ_WEB_PASS=… / ZGJ_WEB_PORT=9320
# 起/重建（代码或镜像变更后）
docker compose -f client-v2/docker/docker-compose.yml up -d --build
```

- 访问 `http://192.168.0.115:9320` → 登录页（账号密码= `.env` 里那组）
- 数据卷：`client-v2/data/` → 容器 `/data`（下载/追更/缓存都在这，宿主机 root 属主）
- 镜像无 mpv、无 X11：播放走浏览器（mp4 原生 Range、m3u8 走 hls.js）；ffmpeg 只做合并管线

## 更新（代码有新版时）

```sh
cd /home/ryen/Git/zhenguojian && git pull
docker compose -f client-v2/docker/docker-compose.yml up -d --build
```

（compose 场景没有「客户端自更新」——更新即重新 build+up，等价桌面版的 apply-restart。
前端设置页的「更新」段在 web 形态已隐藏，不要找。）

## 凭据轮换

改 `client-v2/docker/.env` 的 `ZGJ_WEB_USER` / `ZGJ_WEB_PASS` 后 `up -d` 即生效
（env 缺失容器拒绝启动=设计行为）。注意：**登录态是内存 session，容器重启全失效**，
重启后浏览器会跳回登录页——这是设计内的强度（内部工具级，不是公开服务）。

## 端口与网络

- `9320:8080`：唯一暴露端口（HTTP+WS 同口，`/rpc` 是 WS）
- 容器内出站直连（无代理 ENV）；源站可达性与桌面版同源
- 局域网其他设备直接访问同 URL 即可（同一登录态各自独立）

## 验收与排障

| 现象 | 查法 |
|---|---|
| 进不去（502/拒绝连接） | `docker ps`（容器在跑吗）→ `docker logs zgj-v3`（sidecar 崩没崩） |
| 登录页转圈 | 容器健康检查：`curl http://127.0.0.1:9320/health` 应回 `{"ok":true,"mode":"web"}` |
| 剧卡/榜单空白 | 数据面源站问题（桌面版同源），`docker logs` 看 core 抓取报错 |
| 视频黑屏但页面在 | F12 Network 看 `/video/seg` 是否 206；Console 有无 MSE/hls.js 报错 |
| 本地成片打不开 | 文件必须在 `data/appdata/downloads/` 之下（越界 403 是安全设计，不是 bug） |
| 弹幕没有 | hongguo 源上游抽样稀疏是常态（桌面版同数据）；看 F12 里 `DanmakuAll` 返回 |

CI 门禁：[v3-docker-image](../../.github/workflows/v3-docker-image.yml) 在 push 到
`client-v2/docker/**`、`client-v2/sidecar_web.go`、前端、core overlay 时自动
build 镜像 + 起容器 + 跑 14 项验收（`docker/ci-acceptance.js`）。

## 形态对照（web vs 桌面）

| 能力 | 桌面（Electron+mpv） | v3 容器（浏览器） |
|---|---|---|
| 播放 | mpv（ASS 弹幕/X11 嵌窗） | 原生 `<video>`+hls.js+canvas 弹幕 |
| 本地成片 | 直接 open | `/file` 代理（下载根白名单+Range） |
| 自更新 | NSIS/apply-restart | `compose up -d --build` |
| 系统托盘/快捷键嵌入 | 有 | 无（浏览器内快捷键仍可用） |
| 下载/合并/追更/设置 | WS 同源 | 同（纯 WS 绑定，零差异） |
