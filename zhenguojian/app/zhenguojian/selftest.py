# -*- coding: utf-8 -*-
"""Built-in self check: exercises the real pipeline from inside the frozen exe.

Run with:  真果鉴.exe --selftest
It proves resource discovery, the core API, stream resolution, downloading and
merging all work in the packaged layout, and prints a machine-readable
PASS/FAIL report. Real on-screen playback is intentionally not part of the
check (it used to pop an mpv window mid-build); verify it manually.
"""
from __future__ import annotations

import json
import os
import sys
import time
import traceback

STEPS = []


class _Recorder(object):
    """QWidget stand-in so PlayerPane works without a QApplication."""

    def __init__(self):
        self._layout = None

    def setLayout(self, layout):
        self._layout = layout

    def layout(self):
        return self._layout

    def setParent(self, parent):
        pass

    def resize(self, *args):
        pass

    def show(self):
        pass

    def hide(self):
        pass

    def setMinimumSize(self, *args):
        pass


def step(name):
    def wrap(function):
        STEPS.append((name, function))
        return function
    return wrap


@step("resources")
def _resources(state):
    from zhenguojian import tools
    found = {
        "app_root": tools.app_root(),
        "sidecar": os.path.isfile(os.path.join(tools.app_root(), "runtime", "sidecar.exe")),
        "mpv": bool(tools.mpv_path()),
        "ffmpeg": bool(tools.ffmpeg_path()),
        "ffprobe": bool(tools.ffprobe_path()),
        "frozen": bool(getattr(sys, "frozen", False)),
    }
    if not all(found[k] for k in ("sidecar", "mpv", "ffmpeg", "ffprobe")):
        raise RuntimeError("missing runtime component: %s" % found)
    return found


@step("library")
def _library(state):
    from zhenguojian import tools
    from zhenguojian.core_client import CoreClient
    core = CoreClient(tools.data_dir())
    core.start()
    state["core"] = core
    recs = core.recommendations(category="short_play", force=True)
    items = recs.get("items", [])
    if not items:
        raise RuntimeError("no recommendations returned")
    state["drama"] = items[0]
    return {"drama": state["drama"].get("title"),
            "items": len(items),
            "boards": len(core.boards()),
            "rankings": len(core.rankings("hongguo-hot"))}


@step("newseries")
def _newseries(state):
    """新剧推荐走 cell/change，和原版客户端同一个接口与分页。"""
    core = state["core"]
    page = core.new_series(gender="2") or {}
    items = page.get("items") or []
    if not items:
        raise RuntimeError("no new series returned")
    return {"items": len(items),
            "first": items[0].get("title"),
            "score": items[0].get("score"),
            "hasMore": bool(page.get("hasMore"))}


@step("search")
def _search(state):
    core = state["core"]
    hints = core.suggestions("穿越")
    hits = core.catalog(query="穿越", page=1)
    if not hits.get("items"):
        raise RuntimeError("search returned nothing")
    state["drama"] = hits["items"][0]
    return {"suggestions": len(hints),
            "results": len(hits["items"]),
            "drama": state["drama"].get("title")}


@step("detail")
def _detail(state):
    core = state["core"]
    detail = core.detail(state["drama"])
    chapters = detail.get("chapters", [])
    if len(chapters) < 2:
        raise RuntimeError("need at least 2 episodes, got %d" % len(chapters))
    state["chapters"] = chapters
    return {"title": (state["drama"].get("title") or "")[:24],
            "episodes": len(chapters)}


# 曾有一个 "play" 步骤：起真实 mpv 走 IPC 验证解码。wid=None 时 mpv 在
# Windows 上会弹出可见播放窗口并播一段视频，自检期间观感突兀；取流与
# 解密链路已由 resolve/preload/download/decrypt/merge 覆盖，故移除该步骤。


@step("preload")
def _preload(state):
    """下一集预热：核心应返回可直接播放的本地流会话（预取字节数尽力而为）。"""
    core = state["core"]
    plan = core.preload(state["drama"], state["chapters"][1], 2)
    session = plan.get("session")
    local = bool(plan.get("local"))
    # 已下载过的集会直接返回本地文件计划；没下载过则应是本地流会话。
    ok = bool(plan.get("url")) and (local or (bool(session) and "127.0.0.1" in plan["url"]))
    if not ok:
        raise RuntimeError(
            "preload did not return a playable plan: url=%r local=%r keys=%s drama=%s chapter=%s"
            % (plan.get("url"), plan.get("local"), sorted(plan.keys()),
               state["drama"].get("id"),
               str(state["chapters"][1])[:200]))
    if session:
        core.release(session)
    return {"local": local,
            "prefetched": plan.get("prefetchedBytes") or 0,
            "expires": bool(plan.get("expiresAt"))}


@step("download")
def _download(state):
    core = state["core"]
    for job in core.downloads():
        try:
            core.control(job["id"], "remove")
        except Exception:
            pass
    time.sleep(1)
    from zhenguojian import library
    library.EpisodePublisher(core).purge(state["drama"].get("id"), remove_files=True)
    entries = [{"chapter": state["chapters"][i], "index": i + 1} for i in range(2)]
    added = core.enqueue(state["drama"], entries)
    deadline = time.time() + 900
    while time.time() < deadline:
        jobs = [j for j in core.downloads() if j.get("state") in ("completed", "failed", "error")]
        if len(jobs) >= 2 and all(j["state"] in ("completed", "failed", "error") for j in jobs):
            break
        time.sleep(3)
    done = [j for j in core.downloads() if j.get("state") == "completed"]
    if len(done) < 2:
        raise RuntimeError("only %d of 2 episodes completed" % len(done))
    state["jobs"] = done
    return {"added": (added or {}).get("added"),
            "completed": len(done),
            "bytes": sum(j.get("bytes") or 0 for j in done),
            "cache_dir": core.download_directory()}

@step("decrypt")
def _decrypt(state):
    """The published episodes must be clear, named and inside 下载/<剧名>/."""
    from zhenguojian import decrypt, library, tools
    publisher = library.EpisodePublisher(state["core"])
    entries = publisher.publish_pending(state["jobs"])
    if len(entries) < 2:
        raise RuntimeError("only %d of 2 episodes decrypted" % len(entries))
    root = os.path.abspath(tools.download_dir())
    for entry in entries:
        path = entry["path"]
        if not os.path.isfile(path):
            raise RuntimeError("published file missing: %s" % path)
        if os.path.abspath(path).lower().find(root.lower()) < 0:
            raise RuntimeError("episode published outside the library: %s" % path)
        if not decrypt.is_decrypted(path):
            raise RuntimeError("published file is still encrypted: %s" % path)
        if not os.path.basename(path).startswith(entry["title"][:8]):
            raise RuntimeError("unexpected episode name: %s" % os.path.basename(path))
    state["library"] = entries
    return {"root": root,
            "folder": os.path.dirname(entries[0]["path"]),
            "files": [os.path.basename(e["path"]) for e in entries],
            "sizes": [e["bytes"] for e in entries],
            "clear": True}

@step("merge")
def _merge(state):
    from zhenguojian.media import MergeTask, probe
    jobs = sorted(state["jobs"], key=lambda j: j["index"])[:2]
    task = MergeTask(state["core"], jobs,
                     on_status=lambda text: None,
                     on_progress=lambda ratio, text: None)
    result = task.run()
    info = probe(result["path"])
    if not os.path.isfile(result["path"]):
        raise RuntimeError("merged file missing on disk")
    from zhenguojian import decrypt
    if not decrypt.is_decrypted(result["path"]):
        raise RuntimeError("merged file is not playable on its own")
    return {"path": result["path"],
            "duration": round(info["duration"], 1),
            "bytes": info["bytes"],
            "episodes": result["episodes"],
            "transcoded": result["transcoded"]}


def main():
    state = {}
    report = []
    for name, function in STEPS:
        started = time.time()
        try:
            detail = function(state)
            report.append({"step": name, "ok": True,
                           "seconds": round(time.time() - started, 1),
                           "detail": detail})
            print("[selftest] %-10s OK   %s" % (name, json.dumps(detail, ensure_ascii=False)[:160]),
                  flush=True)
        except Exception as exc:
            report.append({"step": name, "ok": False, "error": str(exc),
                           "traceback": traceback.format_exc()})
            print("[selftest] %-10s FAIL %s" % (name, exc), flush=True)
            break
    try:
        core = state.get("core")
        if core is not None:
            core.close()
    except Exception:
        pass
    passed = len(report) == len(STEPS) and all(entry["ok"] for entry in report)
    summary = {"frozen": bool(getattr(sys, "frozen", False)),
               "passed": passed, "steps": report}
    line = "SELFTEST_RESULT " + json.dumps(summary, ensure_ascii=False)
    print(line, flush=True)
    # The GUI build has no console, so always leave a report on disk.
    try:
        from zhenguojian import tools
        target = os.path.join(tools.app_root(), "selftest-report.json")
        with open(target, "w", encoding="utf-8") as handle:
            handle.write(json.dumps(summary, ensure_ascii=False, indent=2))
        print("[selftest] report -> %s" % target, flush=True)
    except Exception:
        pass
    return 0 if passed else 1


if __name__ == "__main__":
    sys.exit(main())
