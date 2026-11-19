// zgj 容器 HEALTHCHECK 探针：随 sidecar 一起构建的零依赖静态小二进制。
//
// 为什么不用容器里现成的工具：
//   · debian:bookworm-slim 的 /bin/sh 是 dash，没有 bash 的 /dev/tcp
//     （首版 healthcheck 用 /dev/tcp → docker ps 恒 unhealthy 而服务其实好的，
//     2026-11-19 P4 首跑在 115 实锤，failing streak 344）；
//   · 为此往运行时加 curl 会废掉 stage-3 的 apt 层缓存、并给探针引入一条
//     新的外网依赖（115 的 deb 源不稳，构建曾挂 25 分钟）——不值得。
//
// 所以探针自己就是交付物：stdlib-only、独立 module、CGO_ENABLED=0、~2MB，
// 读 ZGJ_WEB_ADDR（与 sidecar 同一 env 面）探 127.0.0.1:<port>/health，
// 非 200 退出 1。
package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	addr := os.Getenv("ZGJ_WEB_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	// 只取端口（:8080 / 127.0.0.1:8080 / 8080 都归一；探针在容器内跑，探本机同端口）
	port := addr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		port = addr[i+1:]
	}
	if port == "" {
		port = "8080"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/health")
	if err != nil {
		fmt.Fprintln(os.Stderr, "zgj-healthcheck:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "zgj-healthcheck: /health status", resp.StatusCode)
		os.Exit(1)
	}
}
