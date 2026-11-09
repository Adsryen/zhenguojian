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

src_overlay_dir = rel(os.path.join(root, "zhenguojian", "sidecar", "core_overlay"))
core_dir = rel(os.path.join(root, "guoapp-src", "native", "core"))

Replace = {}
for name in ("discovery.go", "poster_metadata.go"):
    real = os.path.join(src_overlay_dir, name)
    fake = os.path.join(core_dir, "sidecar_overlay_" + name)
    Replace[fake] = real

with open(out, "w", encoding="utf-8") as f:
    json.dump({"Replace": Replace}, f, indent=1)
print("overlay written:", out, "entries:", len(Replace))
