#!/usr/bin/env bash
# 复现 actions/checkout@v4 + fetch-tags:true 在 tag 触发时为什么会死，并验证「分两步 fetch」这条修法。
# 为什么要在本地复现：这段逻辑只在 CI 里跑，而 CI 里失败的症状（checkout 步骤 exit 128）
# 看起来像基础设施抖动，很容易被当成网络问题重跑一次混过去——alpha.34/35 两个 tag 就是这么白烧的。
set -uo pipefail
REPO=/home/ryen/Git/zhenguojian
TAG=${1:-v2.0.0-alpha.35}
SHA=$(git -C "$REPO" rev-parse "$TAG^{commit}")
export HTTPS_PROXY=http://192.168.0.36:17890
G=(git -c "credential.helper=!gh auth token" -c "http.proxy=http://192.168.0.36:17890")

rm -rf /tmp/tcrepro && git init -q /tmp/tcrepro
cd /tmp/tcrepro || exit 1
git remote add origin https://github.com/Adsryen/zhenguojian.git

echo "== A) 合并 refspec（checkout 带 fetch-tags 时实际执行的形式）=="
# ⚠ 两条 refspec 的**目的地必须相同**（都是 refs/tags/<tag>）：checkout 在 tag 触发时
#   把 github.ref 直接当目的地。我第一次只写 "$SHA"（目的地不同）→ rc=0，
#   差点得出「本地复现不了，是 CI 环境抖动」的错误结论。
"${G[@]}" fetch --depth=1 origin "$SHA:refs/tags/$TAG" "refs/tags/$TAG:refs/tags/$TAG" >/tmp/tcrepro/a.log 2>&1
rcA=$?
echo "rc=$rcA"; tail -2 /tmp/tcrepro/a.log
if [ $rcA -ne 0 ]; then echo "  → 复现成功：CI 的报错就是这一条"; fi

echo "== B) 分两步 fetch（拟议修法）=="
"${G[@]}" fetch --depth=1 origin "$SHA" >/tmp/tcrepro/b1.log 2>&1; echo "  fetch sha rc=$?"
"${G[@]}" fetch --force --depth=1 origin "refs/tags/$TAG:refs/tags/$TAG" >/tmp/tcrepro/b2.log 2>&1; echo "  fetch tag rc=$?"
echo -n "  objecttype="; git cat-file -t "refs/tags/$TAG" 2>&1
echo -n "  annotation 首行="; git for-each-ref --format='%(contents)' "refs/tags/$TAG" 2>/dev/null | head -1

echo "== C) release_notes.py 在这个浅克隆里能否取到注释 =="
python3 "$REPO/client-v2/scripts/release_notes.py" --tag "$TAG" --cwd /tmp/tcrepro --explain /tmp/tcrepro/ns.log >/tmp/tcrepro/notes.out 2>/tmp/tcrepro/ns.err
echo "  rc=$?  来源=$(cat /tmp/tcrepro/ns.log 2>/dev/null)  notes=$(head -c 60 /tmp/tcrepro/notes.out)"
