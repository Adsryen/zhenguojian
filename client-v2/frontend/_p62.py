import io
import ast

# ── 1) i18n_key_audit：键名字符要含连字符 ──
p = 'verification/i18n_key_audit.py'
s = io.open(p, encoding='utf-8').read()

reps = [
    ("""KEY_DEF_RE = re.compile(r"(?m)^\\s{2}'([\\w.]+)'\\s*:")""",
     """# ⚠ 第五十九轮修：键名字符类原来只有 `[\\w.]+`，**不含连字符**。
# 而仓里就有活键 `source.note.huangguo-video`（i18n.ts:580 中文、:1259 英文，
# 经 components/settings/SourceCard.vue 动态取键）。
# 于是它既不进 zh_set/en_set，也不算"用到"或"未用" ——
# 后果不是名单少一项，而是**把英文那条删掉，这道门照样全绿**，
# 英文界面直接把 `source.note.huangguo-video` 显示给用户 ——
# 正是第 52 轮立这道门要拦的事故形，换个键名形状就绕过了。
KEY_DEF_RE = re.compile(r"(?m)^\\s{2}'([\\w.\\-]+)'\\s*:")"""),
]
for old, new in reps:
    assert s.count(old) == 1, old[:60]
    s = s.replace(old, new)

# 字面量取键与"当数据用的键形状"也要跟着放宽
old2 = '''USE_LITERAL_RE = re.compile(r"(?<![\\w$.])(?:tf|t)\\(\\s*['\\"]([\\w.]+)['\\"]")'''
if s.count(old2) == 1:
    s = s.replace(old2, '''USE_LITERAL_RE = re.compile(r"(?<![\\w$.])(?:tf|t)\\(\\s*['\\"]([\\w.\\-]+)['\\"]")''')
    print('OK USE_LITERAL_RE 放宽')

for probe in [
    'r"^\\w[\\w-]*(?:\\.\\w[\\w-]*)+$"',
    'CALL_HEAD_RE',
    'QUOTED_ANY_RE',
    'PURE_LITERAL_RE',
    'CONCAT_PREFIX_RE',
]:
    if probe in s:
        print('含判据:', probe)

io.open(p, 'w', encoding='utf-8').write(s)

# ── 2) run_all：端口正则跟 ports_audit 对齐 ──
p2 = 'verification/run_all.py'
s2 = io.open(p2, encoding='utf-8').read()
old_a = '''    return {p for p in re.findall(r"\\b(51\\d{2})\\b", text)}'''
assert s2.count(old_a) == 1
new_a = '''    # ⚠ 第五十九轮修：这里原来只认 `51xx`，而 ports_audit 早就是 `5[12]\\d{2}`。
    #   5200 段是第 44 轮之后逐步用起来的（5205…5212），
    #   所以这些脚本的 declared_ports **全是空集** ——
    #   "残留端口"那一列对它们恒显示 `-`：不是"没泄漏"，是**看不见**。
    #   一个恒显示"没问题"的列，比没有这一列更危险。
    return {p for p in re.findall(r"\\b(5[12]\\d{2})\\b", text)}'''
s2 = s2.replace(old_a, new_a)

old_b = '''    return set(re.findall(r":(51\\d{2})\\b", out))'''
assert s2.count(old_b) == 1
new_b = '''    return set(re.findall(r":(5[12]\\d{2})\\b", out))'''
s2 = s2.replace(old_b, new_b)
io.open(p2, 'w', encoding='utf-8').write(s2)
print('OK run_all 端口正则对齐 5[12]')

ast.parse(io.open(p2, encoding='utf-8').read())
ast.parse(io.open(p, encoding='utf-8').read())
