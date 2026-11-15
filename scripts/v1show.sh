#!/usr/bin/env bash
# v1show —— 从不可漂移的 tag `v1-final` 里读 v1 原文
#
# 为什么需要它：research/ 下十几份对账文档引用的是 v1 的**具体行号**
# （如 main.py:2511-2522、poster_wall.py:650-662、player.py:728-762）。
# v1 源码树已从 master 删除（D2），这些引用如果没有一个固定取法，
# 就会慢慢变成"谁也核不了的注释"。tag 不会漂移，分支会。
#
# 用法：
#   scripts/v1show.sh main.py                 # 整份
#   scripts/v1show.sh main.py 2511,2522       # 一段（含首尾）
#   scripts/v1show.sh test_sorani_login.py 280,292
#   scripts/v1show.sh --list                  # 列出 v1 树里都有什么文件
set -euo pipefail
TAG="${V1_TAG:-v1-final}"
PREFIX="zhenguojian/app/zhenguojian"

if [ "${1:-}" = "--list" ]; then
  git ls-tree -r --name-only "$TAG" -- "$PREFIX" | sed "s#$PREFIX/##"
  exit 0
fi

[ $# -ge 1 ] || { sed -n '1,14p' "$0"; exit 2; }
file="$1"; range="${2:-}"
path="$PREFIX/$file"
git cat-file -e "$TAG:$path" 2>/dev/null || {
  echo "找不到 $path @ $TAG（试试 scripts/v1show.sh --list）" >&2; exit 1; }
if [ -z "$range" ]; then
  git show "$TAG:$path"
else
  # 行号按文档里的写法（1 起始、含尾）；awk 比 sed 更稳：
  # sed -n 'a,bp' 在 a>b 时静默输出空，会让人以为"那段不存在"。
  awk -F' ' -v r="$range" 'BEGIN{split(r,x,","); s=x[1]; e=x[2]==""?s:x[2]}
    NR>=s && NR<=e {printf "%d\t%s\n", NR, $0}' <(git show "$TAG:$path")
fi
