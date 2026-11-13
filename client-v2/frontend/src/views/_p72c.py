import io
import ast

p = 'DiscoverView.vue'
s = io.open(p, encoding='utf-8').read()

# 1) 计数器（v1 self._empty_pages，browse_panel.py:112）
old = "const page = ref(1)"
assert s.count(old) == 1, 'page ref anchor'
s = s.replace(old, old + """

/* v1 的"连续空页才停"计数器（browse_panel.py:112 self._empty_pages，
   :402-403 `empty_pages = empty_pages+1 if not added else 0;
   if empty_pages>=2: has_more=False`）。
   原来 v2 是 `hasMore = res.hasMore && more.length` ——
   只要某一页**全是重复**就当场停，用户点不到后面真正的新内容。 */
const emptyPages = ref(0)""")

# 2) 首页加载：重置计数器
old2 = """    items.value = injectCatalog(res.items ?? [], 1)
    page.value = 1
    hasMore.value = !!res.hasMore"""
assert s.count(old2) == 1, 'first load anchor'
s = s.replace(old2, """    items.value = injectCatalog(res.items ?? [], 1)
    page.value = 1
    emptyPages.value = 0 // v1 browse_panel.py:381 重置路径同款
    hasMore.value = !!res.hasMore""")

# 3) 追加：按 id 去重 + 连续两页无新增才停
old3 = """    // 真实引擎 id 在翻页会重复（同推荐位），页内按 id#page 去重即可
    const more = injectCatalog(res.items ?? [], page.value + 1)
    items.value = items.value.concat(more)
    page.value += 1
    hasMore.value = !!res.hasMore && !!more.length"""
assert s.count(old3) == 1, 'append anchor'
s = s.replace(old3, """    /* ⚠ 上一行原来那句注释写的是「页内按 id#page 去重即可」，
       而那个 `id#i` 是 **PosterGrid 的渲染 :key**（components/PosterGrid.vue:377），
       它只保证 Vue 不报重复 key，**根本不去掉重复内容**——
       同一个推荐位翻页重复给出时，页面上会出现两张一样的卡片。
       v1 在数据层去重（poster_wall.py:650-662 `append_items`：
       按 drama.id 比对已有集合，重复的直接不收，batch 空就整页不追加）。
       这里照 v1 做，并把停止条件改成 v1 的"连续 2 页无新增"
       （browse_panel.py:402-403），两者是同一条规则的两半。 */
    const raw = injectCatalog(res.items ?? [], page.value + 1)
    const seen = new Set(items.value.map((d) => d.id))
    const more: Drama[] = []
    for (const d of raw) {
      if (!d.id || seen.has(d.id)) continue
      seen.add(d.id)
      more.push(d)
    }
    if (more.length) items.value = items.value.concat(more)
    emptyPages.value = more.length ? 0 : emptyPages.value + 1
    page.value += 1
    hasMore.value = !!res.hasMore && emptyPages.value < 2""")

io.open(p, 'w', encoding='utf-8').write(s)
print('OK DiscoverView 数据层去重 + 连续两页才停')
