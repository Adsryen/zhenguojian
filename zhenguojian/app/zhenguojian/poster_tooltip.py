"""Compact, escaped synopsis previews shared by all poster surfaces."""
from __future__ import annotations

import html
import unicodedata

LINE_UNITS = 52
MAX_TITLE = 52
MAX_DESCRIPTION = 180


def _preview(text, limit):
    text = " ".join(str(text or "").split())
    return text[:limit].rstrip() + "…" if len(text) > limit else text


def _lines(text):
    lines, current, units = [], "", 0
    for char in text:
        width = 0 if unicodedata.combining(char) else (2 if unicodedata.east_asian_width(char) in "WF" else 1)
        if current and units + width > LINE_UNITS:
            lines.append(current.rstrip())
            current, units = "", 0
        current += char
        units += width
    if current.strip():
        lines.append(current.strip())
    return "<br>".join(html.escape(line) for line in lines)


def poster_tooltip(drama):
    title = _preview(drama.get("title"), MAX_TITLE)
    description = _preview(drama.get("description"), MAX_DESCRIPTION)
    if not title and not description:
        return ""
    if not description or description == title:
        return "<qt>%s</qt>" % _lines(title or description)
    title_html = "<b>%s</b><br><br>" % _lines(title) if title else ""
    return "<qt>%s%s</qt>" % (title_html, _lines(description))
