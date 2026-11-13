import io
import ast

p = 'views/SearchView.vue'
s = io.open(p, encoding='utf-8').read()

# 1) 导入 DEGRADED / MOCK_MODE
old = "import { api } from '@/services/api'"
assert s.count(old) == 1
s = s.replace(old, "import { api, DEGRADED, MOCK_MODE } from '@/services/api'")

# 2) 状态
old2 = "const error = ref('')"
assert s.count(old2) == 1
s = s.replace(old2, """const error = ref('')
/* ⚠ 第五十九轮补（子代理 A 报出、我复核确认）：
   搜索页原来 **一处都没有** banner / MOCK_MODE / DEGRADED / warning
   （grep `banner|MOCK_MODE|DEGRADED|warning` 在这个文件里 0 命中），
   而找剧页（DiscoverView.vue:179）与剧库页（LibraryView.vue:81）都有。
   api.ts 在无桥时整对象落到 mock 面（api.ts:1065-1067），
   所以真机上只要桥没起来，搜索页就把**示例数据当真实结果**显示，
   而隔壁两个页面都会挂"示例数据/降级"横幅。这是同一件事在三个页面里
   只有一页没做——不是设计选择，是漏。
   顺带接上 v1 的另一半：v1 空结果时拿核心的 `warning` 当空态文案
   （main.py:1453-1455，含红果「使用更完整的剧名…」提示），
   v2 之前完全不读 `res.warning`。现在读，并按 DiscoverView 的做法
   放在同一条横幅上（位置与 v1 不同：v1 在空态区、v2 在横幅——
   这条差异如实记在这里，不装作与 v1 一模一样）。 */
const banner = ref('')''')

# 3) 赋值：单源分支读 res.warning + 降级标注
old3 = """    items.value = res.items ?? []
    page.value = 1
    hasMore.value = !!res.hasMore"""
assert s.count(old3) == 1
s = s.replace(old3, """    items.value = res.items ?? []
    page.value = 1
    hasMore.value = !!res.hasMore
    banner.value = res.warning
      || (MOCK_MODE ? t('banner.mock') : DEGRADED ? t('banner.degraded') : '')""")

# 4) 每次新搜索先清掉上一条横幅（否则换源后还挂着旧的 warning）
old4 = """  items.value = []
  if (searchAll.value) {"""
assert s.count(old4) == 1
s = s.replace(old4, """  items.value = []
  banner.value = ''
  if (searchAll.value) {""")

# 5) 「全部来源」分支也要有标注：它并行打多源，mock 时同样是假数据
old5 = """async function loadMore() {"""
assert s.count(old5) == 1
s = s.replace(old5, """/* 「全部来源」分支：结果由 SearchGroups 流式填，
   这里补同样的降级标注（v1 没有这个功能，属 v2 新增面，
   但"示例数据要标出来"这条与 v1 的横幅语义一致）。 */
async function markFanoutBanner() {
  if (MOCK_MODE || DEGRADED) {
    banner.value = MOCK_MODE ? t('banner.mock') : t('banner.degraded')
  }
}

async function loadMore() {""")

# 6) 在 doSearchAll 入口调用它
old6 = "    await doSearchAll(term, seq)"
assert s.count(old6) == 1
s = s.replace(old6, "    await markFanoutBanner()\n    await doSearchAll(term, seq)")

# 7) 模板：横幅放在搜索条之后、结果之前
old7 = '  <div class="search-bar">'
assert s.count(old7) == 1
s = s.replace(old7, """  <div
    v-if="banner"
    class="banner"
    data-testid="search-banner"
  >{{ banner }}</div>

  <div class="search-bar">""")

io.open(p, 'w', encoding='utf-8').write(s)
print('OK SearchView 补降级标注 + 读 res.warning')
