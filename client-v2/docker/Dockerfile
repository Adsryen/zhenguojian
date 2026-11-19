# v3 容器版：sidecar(web 模式) + 前端静态 + ffmpeg，单进程单端口。
# 构建（仓库根执行）：docker compose -f client-v2/docker/docker-compose.yml up -d --build
# 或手动：docker build -f client-v2/docker/Dockerfile .
# 无 mpv、无 X11：播放走浏览器 MSE（P1），ffmpeg 只做下载合并管线。
#
# 网络：115 能直连非 Google 主机（goproxy.cn/npm/deb 实测直连 OK），
#       只有 Google 的 proxy.golang.org 被墙——用 GOPROXY=goproxy.cn 绕开。
#       所以构建期**不走宿主代理**：走代理时 apt 拉 deb.debian.org 会 502
#       （代理对 debian pool 不稳定），直连反而干净。运行期不注入任何代理。

# ── 阶段 1：前端静态（vite build → dist）─────────────────────────────────
FROM node:24-bookworm-slim AS web
WORKDIR /build
COPY client-v2/frontend/package.json client-v2/frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY client-v2/frontend/ ./
# 容器形态**不能**开 VITE_MOCK（mock 面会绕过 WS 桥）；vite build 默认无该变量。
RUN npm run build

# ── 阶段 2：sidecar（web 模式二进制，CGO_ENABLED=0 纯静态）────────────────
FROM golang:1.24-bookworm AS go
# proxy.golang.org 被墙 → goproxy.cn（与 build-linux.sh 对齐），直连可达。
ENV GOPROXY=https://goproxy.cn,direct
WORKDIR /build
COPY guoapp-src/ guoapp-src/
COPY client-v2/go.mod client-v2/go.sum client-v2/
RUN cd client-v2 && go mod download
COPY client-v2/ client-v2/
ARG ZGJ_BUILD_VERSION=dev
RUN cd client-v2 \
    && python3 scripts/make-overlay.py /tmp/ov.json \
    && CGO_ENABLED=0 go build -tags "zgj_sidecar v2overlay" -overlay /tmp/ov.json \
       -ldflags "-X main.buildVersion=${ZGJ_BUILD_VERSION}" -o /out/sidecar .

# ── 阶段 3：运行时（debian + ca + ffmpeg；无图形栈）────────────────────────
FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates ffmpeg \
    && rm -rf /var/lib/apt/lists/*
COPY --from=web /build/dist /opt/zgj/web
COPY --from=go /out/sidecar /opt/zgj/sidecar
# web 模式开关 + 静态目录 + 数据目录（bind mount 到 /data，compose 注入凭据）。
# 镜像**不含**任何代理 ENV：容器内是 LAN 流量，Go 的 http.ProxyFromEnvironment
# 若读到 HTTP_PROXY 会让 /video/seg 等所有出站误走宿主代理。
ENV ZGJ_WEB=1 \
    ZGJ_WEB_ADDR=:8080 \
    ZGJ_WEB_STATIC=/opt/zgj/web \
    ZHENGUO_DATA=/data/appdata
RUN mkdir -p /data/appdata
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s \
    CMD /bin/sh -c 'exec 3<>/dev/tcp/127.0.0.1/8080 && printf "GET /health HTTP/1.0\r\n\r\n" >&3 && head -1 <&3 | grep -q 200'
CMD ["/opt/zgj/sidecar"]
