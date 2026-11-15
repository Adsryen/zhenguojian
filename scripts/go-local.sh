#!/usr/bin/env bash
# go-local —— 本机可用的 Go 入口（CI 不再是唯一编译器）
#
# 为什么需要这个文件：我前几十轮一直写着"本机没有可用 Go 工具链"
# （GAPS §七），于是每个 Go 改动都要花 10–20 分钟等 CI 才知道
# 有没有编译错 —— 本轮因此白跑了 3 次构建（未使用导入、`:=` 重复声明、
# `%LOCALAPPDATA%` 的 %L 被 vet 判非法动词）。
#
# 真实原因不是"没装 Go"，而是 `~/.local/go` 是**混版 GOROOT**：
# VERSION 写 go1.24.6，stdlib 汇编却与工具不配，连 hello world 都编不过。
# 解法是让 go 自己下载一套自洽工具链：`GOTOOLCHAIN=go1.25.3`。
# 注意两个坑（都是本轮实测）：
#   · `GOSUMDB=off` 会让工具链模块**拒绝下载**（"checksum database disabled"），
#     必须保留默认 sumdb（goproxy.cn 有 /sumdb 镜像）；
#   · 根包**带 tag** 在 linux 编不出来（wails linux 要 cgo），
#     不带 tag 时 vet/test 都能跑 —— 我原先在 workflow 注释里写的是"编不出来"，
#     那句只对带 tag 成立，已在本轮纠正。
set -euo pipefail
export PATH=/home/ryen/.local/go/bin:$PATH
export GOTOOLCHAIN="${GOTOOLCHAIN:-go1.25.3}"
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
export GOFLAGS="${GOFLAGS:--mod=mod}"
export CGO_ENABLED="${CGO_ENABLED:-0}"
unset GOSUMDB 2>/dev/null || true

cd "$(dirname "$0")/../client-v2"
OV=/tmp/zhenguojian-overlay.json
python3 scripts/make-overlay.py "$OV" >/dev/null
TAGS="v2overlay production devtools"

case "${1:-help}" in
  vet)   shift; go vet -tags "$TAGS" -overlay "$OV" ./... "$@" ;;
  winvet) GOOS=windows go vet -tags "$TAGS" -overlay "$OV" ./... ;;
  test)  shift; go test -tags "$TAGS" -overlay "$OV" -count=1 -timeout 600s ./internal/... "$@" ;;
  race)  shift; go test -tags "$TAGS" -overlay "$OV" -race -count=1 -timeout 600s "$@" ;;
  root)  shift; go test -count=2 -timeout 300s . "$@" ;;   # 根包不带 tag（linux 可跑）
  *)     sed -n '1,26p' "$0" ;;
esac
