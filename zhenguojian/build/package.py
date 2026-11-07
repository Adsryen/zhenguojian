# -*- coding: utf-8 -*-
"""Package dist/ into 真果鉴.zip: one folder holding the exe, its runtime and docs.

Layout matches what users unzip:

    真果鉴/
      真果鉴.exe
      README.md
      runtime/sidecar.exe, mpv/, ffmpeg/
      下载/                (empty)
      .cache/downloads/    (empty)
"""
import os
import shutil
import sys
import zipfile

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DIST = os.path.join(ROOT, "dist")
BUILD = os.path.join(ROOT, "build")
STAGE = os.path.join(BUILD, "package")
NAME = "真果鉴"
FOLDER = os.path.join(STAGE, NAME)
ARCHIVE = os.path.join(ROOT, NAME + ".zip")
REQUIRED = ("runtime/sidecar.exe", "runtime/mpv/mpv.exe",
            "runtime/ffmpeg/ffmpeg.exe", "runtime/ffmpeg/ffprobe.exe")


def log(message):
    print("[package] " + message, flush=True)


def stage():
    if os.path.isdir(STAGE):
        shutil.rmtree(STAGE, ignore_errors=True)
    os.makedirs(FOLDER)
    exe = os.path.join(DIST, NAME + ".exe")
    if not os.path.isfile(exe):
        raise SystemExit("先跑 build/build.py：" + exe + " 不存在")
    shutil.copy2(exe, os.path.join(FOLDER, NAME + ".exe"))
    shutil.copy2(os.path.join(ROOT, "README.md"), os.path.join(FOLDER, "README.md"))
    shutil.copytree(os.path.join(DIST, "runtime"), os.path.join(FOLDER, "runtime"))
    # Empty on purpose: the app fills them on first run.
    os.makedirs(os.path.join(FOLDER, "下载"))
    os.makedirs(os.path.join(FOLDER, ".cache", "downloads"))
    missing = [item for item in REQUIRED if not os.path.isfile(os.path.join(FOLDER, item))]
    if missing:
        raise SystemExit("runtime 缺件：%s" % "、".join(missing))
    log("staged -> %s" % FOLDER)


def archive():
    if os.path.isfile(ARCHIVE):
        os.remove(ARCHIVE)
    total = 0
    with zipfile.ZipFile(ARCHIVE, "w", zipfile.ZIP_DEFLATED) as bundle:
        for folder, dirs, files in os.walk(FOLDER):
            dirs.sort()
            relative = os.path.relpath(folder, STAGE).replace(os.sep, "/")
            bundle.writestr(relative + "/", b"")
            for name in sorted(files):
                path = os.path.join(folder, name)
                bundle.write(path, relative + "/" + name)
                total += os.path.getsize(path)
    log("打包 %.1f MB -> %.1f MB  %s" % (total / 1e6, os.path.getsize(ARCHIVE) / 1e6, ARCHIVE))
    return ARCHIVE


def main():
    stage()
    target = archive()
    with zipfile.ZipFile(target) as bundle:
        bad = bundle.testzip()
        if bad:
            raise SystemExit("压缩包校验失败：" + bad)
    shutil.rmtree(STAGE, ignore_errors=True)
    log("done -> %s" % target)


if __name__ == "__main__":
    sys.exit(main())
