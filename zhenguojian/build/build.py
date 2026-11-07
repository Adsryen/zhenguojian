# -*- coding: utf-8 -*-
"""Build dist/真果鉴.exe plus the external runtime/ folder.

Steps:
  1. compile the Go sidecar that embeds duanjuapp/native/core
  2. stage mpv / ffmpeg / ffprobe into runtime/
  3. run PyInstaller in onefile mode
"""
import os
import json
import shutil
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DIST = os.path.join(ROOT, "dist")
BUILD = os.path.join(ROOT, "build")
RUNTIME = os.path.join(ROOT, "runtime")
NAME = "真果鉴"

def first_existing(*candidates):
    for candidate in candidates:
        if candidate and os.path.isfile(candidate):
            return candidate
    return ""


# 可移植默认值：优先环境变量，其次 PATH，最后才是本机以前的绝对路径。
# 换台机器/交给别人编译时只需要把 go 放进 PATH，或者设置 ZHENGUO_GO。
GO = first_existing(os.environ.get("ZHENGUO_GO"), shutil.which("go"),
                    r"E:\脚本备份\红果下载器\_toolchain\go\bin\go.exe")
GO_SOURCES = (os.environ.get("ZHENGUO_GO_SRC")
              or os.path.join(os.path.dirname(ROOT), "guoapp-src", "native"))
if not os.path.isdir(GO_SOURCES):
    GO_SOURCES = r"E:\脚本备份\红果下载器\guoapp-src\native"


def log(message):
    print("[build] " + message, flush=True)


def run(args, cwd=None):
    log(" ".join(str(a) for a in args))
    result = subprocess.run([str(a) for a in args], cwd=cwd)
    if result.returncode != 0:
        raise SystemExit("command failed (%d): %s" % (result.returncode, args[0]))


def build_sidecar():
    target = os.path.join(RUNTIME, "sidecar.exe")
    if not os.path.isfile(GO) or not os.path.isdir(GO_SOURCES):
        existing = os.path.join(RUNTIME, "sidecar.exe")
        if os.path.isfile(existing):
            log("go toolchain or native sources missing, keeping existing sidecar.exe")
            return
        raise SystemExit(
            "找不到 Go 工具链或 native 源码。请安装 Go 1.24+ 并放进 PATH，"
            "或用环境变量 ZHENGUO_GO 指向 go.exe、ZHENGUO_GO_SRC 指向 guoapp-src/native。"
            "当前 GO=%r，GO_SOURCES=%r" % (GO, GO_SOURCES))
    env = dict(os.environ)
    # 源码包自带 vendor/ 时优先离线构建，否则走模块代理。
    if os.path.isdir(os.path.join(ROOT, "sidecar", "vendor")):
        env.setdefault("GOFLAGS", "-mod=vendor")
    else:
        env.setdefault("GOFLAGS", "-mod=mod")
    env.setdefault("GOPROXY", "https://goproxy.cn,direct")
    extension = os.path.join(ROOT, "sidecar", "core_overlay", "poster_metadata.go")
    core_root = (os.path.join(ROOT, "sidecar", "vendor", "duanjuapp", "native", "core")
                 if os.path.isdir(os.path.join(ROOT, "sidecar", "vendor"))
                 else os.path.join(GO_SOURCES, "core"))
    overlay = os.path.join(BUILD, "core-overlay.json")
    os.makedirs(BUILD, exist_ok=True)
    provider = os.path.join(core_root, "provider_hongguo.go")
    with open(provider, encoding="utf-8") as handle:
        provider_source = handle.read()
    if provider_source.count("Heat: hongguoHeat(vd)") != 1:
        raise SystemExit("poster heat overlay no longer matches provider_hongguo.go")
    patched_provider = os.path.join(BUILD, "poster-provider_hongguo.go")
    with open(patched_provider, "w", encoding="utf-8") as handle:
        handle.write(provider_source.replace("Heat: hongguoHeat(vd)", "Heat: posterHeat(vd)"))
    replacements = {provider: patched_provider}
    for filename in sorted(os.listdir(os.path.join(ROOT, "sidecar", "core_overlay"))):
        if filename.endswith(".go") and not filename.endswith("_test.go"):
            replacements[os.path.join(core_root, "zhenguojian_" + filename)] = os.path.join(ROOT, "sidecar", "core_overlay", filename)
    detail_provider = os.path.join(core_root, "provider_hongguo_detail.go")
    with open(detail_provider, encoding="utf-8") as handle:
        detail_source = handle.read()
    anchor = "entry := hongguoDetailEntry{Drama: hongguoDramaFromAny(detail, \"短剧\")}"
    if detail_source.count(anchor) != 1:
        raise SystemExit("creator overlay no longer matches provider_hongguo_detail.go")
    patched_detail = os.path.join(BUILD, "creator-provider_hongguo_detail.go")
    with open(patched_detail, "w", encoding="utf-8") as handle:
        detail_source = detail_source.replace(anchor, "rememberCreatorDetail(seriesID, detail)\n\t" + anchor)
        old_request = 'map[string]any{"series_id": seriesID}'
        if detail_source.count(old_request) != 1:
            raise SystemExit("creator request overlay no longer matches provider_hongguo_detail.go")
        request = 'map[string]any{"series_id": seriesID, "biz_param": map[string]any{"caller_scene": "single_col", "detail_page_version": 1, "disable_digg_stat": false, "disable_video_relate_book": false, "from_video_id": "", "need_all_video_definition": false, "need_mp4_align": false, "screen_width_px": "1080", "source": 4, "use_os_player": false, "use_server_dns": false, "video_id_type": 1}}'
        handle.write(detail_source.replace(old_request, request))
    replacements[detail_provider] = patched_detail
    with open(overlay, "w", encoding="utf-8") as handle:
        json.dump({"Replace": replacements}, handle)
    command = [GO, "build", "-overlay", overlay, "-trimpath", "-ldflags", "-s -w", "-o", target, "."]
    log(" ".join(command))
    result = subprocess.run(command, cwd=os.path.join(ROOT, "sidecar"), env=env)
    if result.returncode != 0:
        raise SystemExit("go build failed; poster metadata requires the rebuilt sidecar")
    else:
        log("sidecar -> %.1f MB" % (os.path.getsize(target) / 1e6))


def stage(subdir, name, candidates):
    """Copy a binary to runtime/<subdir>/<name>.exe, matching tools._tool()."""
    dest_dir = os.path.join(RUNTIME, subdir)
    os.makedirs(dest_dir, exist_ok=True)
    dest = os.path.join(dest_dir, name + ".exe")
    if os.path.isfile(dest) and os.path.getsize(dest) > 0:
        log("%s already staged" % name)
        return
    for candidate in candidates:
        if candidate and os.path.isfile(candidate):
            shutil.copy2(candidate, dest)
            log("staged %s/%s.exe (%.1f MB)" % (subdir, name, os.path.getsize(dest) / 1e6))
            return
    log("WARNING: could not find %s.exe" % name)


def stage_mpv_dlls():
    """把 mpv 静态依赖的运行库放到 mpv.exe 旁边。

    mpv.exe 静态链接了 Vulkan 加载器：机器上没有 vulkan-1.dll（通常由显卡驱动
    提供）时，Windows 会直接拒绝启动它并弹「丢失 vulkan-1.dll」，界面只能报
    「无法连接播放器控制通道」。带上一份以后，没装 Vulkan 运行库的电脑也能播
    （mpv 实际用的是 D3D11，不需要 Vulkan 驱动）。
    """
    target = os.path.join(RUNTIME, "mpv", "vulkan-1.dll")
    if os.path.isfile(target) and os.path.getsize(target) > 0:
        log("vulkan-1.dll already staged")
        return
    candidates = (os.environ.get("ZHENGUO_VULKAN"),
                  os.path.join(os.environ.get("WINDIR", r"C:\Windows"), "System32", "vulkan-1.dll"))
    for candidate in candidates:
        if candidate and os.path.isfile(candidate):
            os.makedirs(os.path.dirname(target), exist_ok=True)
            shutil.copy2(candidate, target)
            log("staged mpv/vulkan-1.dll (%.1f MB)" % (os.path.getsize(target) / 1e6))
            return
    log("WARNING: 没找到 vulkan-1.dll，未装 Vulkan 运行库的电脑会起不来 mpv")


def free_dist():
    """A previous run may still hold the exe; a locked file fails the build."""
    if os.name != "nt":
        return
    target = os.path.join(DIST, NAME + ".exe")
    if not os.path.isfile(target):
        return
    try:
        os.replace(target, target + ".old")
    except OSError:
        return
    try:
        os.remove(target + ".old")
    except OSError:
        pass


def build_exe():
    free_dist()
    run([sys.executable, "-m", "PyInstaller", "--noconfirm", "--clean", "--onefile",
         "--noconsole", "--name", NAME,
         "--distpath", DIST, "--workpath", BUILD, "--specpath", BUILD,
         "--icon", os.path.join(ROOT, "app", "zhenguojian", "app.ico"),
         "--add-data", os.path.join(ROOT, "app", "zhenguojian", "app.ico") + os.pathsep + "zhenguojian",
         "--paths", os.path.join(ROOT, "app"),
         "--hidden-import", "PyQt5.QtCore",
         "--hidden-import", "PyQt5.QtGui",
         "--hidden-import", "PyQt5.QtWidgets",
         "--hidden-import", "win32file",
         "--hidden-import", "win32pipe",
         "--hidden-import", "pywintypes",
         os.path.join(ROOT, "app", "run.py")])


def publish_runtime():
    """Mirror runtime/ into dist/ so the exe finds its siblings after a build."""
    target = os.path.join(DIST, "runtime")
    if os.path.isdir(target):
        shutil.rmtree(target, ignore_errors=True)
    os.makedirs(target, exist_ok=True)
    for name in ("sidecar.exe", "mpv", "ffmpeg"):
        source = os.path.join(RUNTIME, name)
        if not os.path.exists(source):
            continue
        destination = os.path.join(target, name)
        if os.path.isdir(source):
            # A leftover half-copied tree makes copytree raise WinError 183.
            if os.path.isdir(destination):
                shutil.rmtree(destination, ignore_errors=True)
            os.makedirs(destination, exist_ok=True)
            shutil.copytree(source, destination, dirs_exist_ok=True)
        else:
            shutil.copy2(source, destination)
    log("published runtime -> %s" % target)


def main():
    os.makedirs(RUNTIME, exist_ok=True)
    os.makedirs(DIST, exist_ok=True)
    build_sidecar()
    stage("mpv", "mpv", [os.environ.get("ZHENGUO_MPV"), shutil.which("mpv"),
                         os.path.join(os.environ.get("TEMP", ""), "mpv_pkg", "mpv.exe")])
    stage_mpv_dlls()
    stage("ffmpeg", "ffmpeg", [os.environ.get("ZHENGUO_FFMPEG"), shutil.which("ffmpeg"), r"D:\bin\ffmpeg.exe"])
    stage("ffmpeg", "ffprobe", [os.environ.get("ZHENGUO_FFPROBE"), shutil.which("ffprobe"), r"D:\bin\ffprobe.exe"])
    build_exe()
    publish_runtime()
    log("done -> %s" % os.path.join(DIST, NAME + ".exe"))


if __name__ == "__main__":
    main()
