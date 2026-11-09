#!/usr/bin/env python3
"""生成 go build -overlay 文件：把 sidecar core_overlay 的两文件注入 core 包。
用法: python3 make-overlay.py <输出路径>
（v1 build.py 的同款机制；overlay 相对路径以输出文件所在目录为基准解析。）"""
import json, os, sys

out = os.path.abspath(sys.argv[1])
base = os.path.dirname(out)
root = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))

def rel(p):
    r = os.path.relpath(p, base)
    return r.replace("\\", "/")

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
