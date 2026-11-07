# -*- coding: utf-8 -*-
"""同系列（多季）标题归并。

红果把每一季都当成一部独立短剧：标题末尾带「第 N 季 / 第 N 部」，
但没有 series_id 之类的字段把它们串起来。这里按标题做归一化：
去掉末尾季标记、去掉空白和标点，剩下「基础标题 + 单位」作为分组键。

只用于展示和“补下一季”，不改变 core 返回的剧集数据。
"""
from __future__ import annotations

import re
import unicodedata

from .watchstore import season_number

# 与 Go 核心的季号解析保持一致：第 + 数字（阿拉伯/中文）+ 季/部，允许尾部标点。
_SEASON_SUFFIX_RE = re.compile(
    r"第\s*([0-9０-９零〇一二两兩三四五六七八九十百]+)\s*([季部])[\s\W_]*$",
    re.UNICODE,
)


def base_title(title):
    """去掉「第 N 季 / 第 N 部」后缀，保留前面的基础标题。"""
    text = str(title or "").strip()
    if not text:
        return ""
    match = _SEASON_SUFFIX_RE.search(text)
    if match:
        base = text[:match.start()]
        # 标题常写成「剧名（第二季）」：季标记前会剩一个左括号。
        base = re.sub(r"[\s\W_]+$", "", base, flags=re.UNICODE).strip()
        if base:
            return base
    return text


def season_unit(title):
    """返回「季」或「部」；没有季标记时按「季」处理。"""
    match = _SEASON_SUFFIX_RE.search(str(title or ""))
    return match.group(2) if match else "季"


def series_key(title):
    """分组键：基础标题去掉空白/标点后做 NFKC + casefold。"""
    base = unicodedata.normalize("NFKC", base_title(title))
    return re.sub(r"[\s\W_]+", "", base, flags=re.UNICODE).casefold()


def season_label(number, unit="季"):
    return "第 %d %s" % (int(number), unit or "季")


def _item_season(item):
    return season_number((item or {}).get("title"))


def group(items, title):
    """把搜索结果里和 title 同系列的剧按季号收拢。

    返回按季号排序的列表，每项：
    ``{season, unit, title, episodes, drama}``。
    同一季如果出现多条（基础标题和「第一季」各一条），保留集数更多的那条。
    """
    key = series_key(title)
    unit = season_unit(title)
    if not key:
        return []
    best = {}
    for item in items or []:
        if not isinstance(item, dict):
            continue
        if series_key(item.get("title")) != key:
            continue
        if season_unit(item.get("title")) != unit:
            continue
        number = _item_season(item)
        if number <= 0:
            continue
        row = {
            "season": number,
            "unit": unit,
            "title": item.get("title") or "",
            "episodes": int(item.get("episodes") or 0),
            "drama": item,
        }
        previous = best.get(number)
        if previous is None:
            best[number] = row
            continue
        # 同一季优先保留集数多的；集数相同保留标题更完整的。
        if (row["episodes"], len(row["title"])) > (previous["episodes"],
                                                   len(previous["title"])):
            best[number] = row
    return [best[number] for number in sorted(best)]


def find_season(rows, number):
    """在一组季里找指定季号。"""
    for row in rows or []:
        if int(row.get("season") or 0) == int(number):
            return row
    return None


def next_season(rows, current):
    """找当前季之后最近的一季；没有就返回 None。"""
    current = int(current or 1)
    for row in rows or []:
        if int(row.get("season") or 0) > current:
            return row
    return None


def groups(items):
    """把任意搜索结果按分组键归并成多组，供以后做“同系列”列表用。"""
    buckets = {}
    for item in items or []:
        if not isinstance(item, dict) or not item.get("title"):
            continue
        key = (series_key(item.get("title")), season_unit(item.get("title")))
        buckets.setdefault(key, []).append(item)
    rows = []
    for bucket in buckets.values():
        rows.append(group(bucket, bucket[0].get("title")))
    rows.sort(key=lambda group_rows: group_rows[0]["title"] if group_rows else "")
    return rows
