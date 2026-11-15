#!/usr/bin/env python3
"""生成 go build -overlay 文件：把 sidecar core_overlay 的两文件注入 core 包。
用法: python3 make-overlay.py <输出路径>
（v1 build.py 的同款机制；overlay 相对路径以输出文件所在目录为基准解析。）"""
import json, os, sys

out = os.path.abspath(sys.argv[1])
base = os.path.dirname(out)  # 仅作记录；键值已用绝对路径
root = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))

def rel(p):
    # cmd/go 的 -overlay 里相对路径按「当前工作目录」解析而非 overlay 文件
    # 所在目录，跨目录调用极易错位——统一写绝对路径，CI/本地行为一致。
    return os.path.abspath(p).replace("\\", "/")

# v2 自己的副本（D2 第一步）。
# 原来这里指向 `zhenguojian/sidecar/core_overlay/`，也就是**v2 的构建输入住在 v1 目录树里**——
# 那意味着"删掉 v1"会当场打断两个 CI job，而且失败症状（overlay 找不到文件）
# 看起来像构建脚本坏了，很难归因。
# 现在 v2 拥有自己的副本；v1 那份继续归 v1（它的构建由 build.yml 在 v1.* tag 上负责）。
# ⚠ 分叉政策：这两份**从本提交起允许不同**。若以后要同步 v1 的修复，
#   必须显式 diff 两边并说明理由，不要假设它们还一样。
#   取 v1 原文：`git show v1-final:zhenguojian/sidecar/core_overlay/discovery.go`
src_overlay_dir = rel(os.path.join(root, "client-v2", "coreoverlay"))
core_dir = rel(os.path.join(root, "guoapp-src", "native", "core"))

# 仓库里存的是 `.go.txt`，**不是 `.go`**：
# 这两个文件的 `package core` + 对 `nativeDrama` 等符号的引用，
# 只有被 overlay 注入 core 包之后才成立；放在模块里当 `.go` 看，
# `go vet ./...` 会真的去编译这个目录然后报
# `coreoverlay\discovery.go:18:11: undefined: nativeDrama`（CI 实测）。
# v1 那边没暴露，只是因为它的构建只 `go build .`（根包），没下钻。
# 所以这里让脚本在构建时把 `.go.txt` 落地成临时 `.go`，仓库里那个目录
# 从此**不是一个 Go 包**，谁也不会再误编译它。
materialize_dir = os.path.join(base, "overlay-src")
os.makedirs(materialize_dir, exist_ok=True)

Replace = {}
for name in ("discovery.go", "poster_metadata.go"):
    src = os.path.join(src_overlay_dir, name + ".txt")
    dst = rel(os.path.join(materialize_dir, name))
    with open(src, "r", encoding="utf-8") as f_in, open(dst, "w", encoding="utf-8") as f_out:
        f_out.write(f_in.read())
    fake = os.path.join(core_dir, "sidecar_overlay_" + name)
    Replace[fake] = dst

with open(out, "w", encoding="utf-8") as f:
    json.dump({"Replace": Replace}, f, indent=1)
print("overlay written:", out, "entries:", len(Replace))
