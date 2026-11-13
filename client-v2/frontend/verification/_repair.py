import io
import ast

p = 'session_state.py'
s = io.open(p, encoding='utf-8').read()

# 1) 从 norm() 里切走那段"滞留"的代码（它在 norm 的 return 之后，属于不可达死代码；
#    而 main() 末尾因此整段丢失 —— 表现是"生成分支 exit 0 但什么都不写"，
#    这是我移动代码块时按行号切错了边界造成的。教训：**移动代码要按函数边界，
#    不要按"我以为的第几行"，并在事后用 ast 确认每个函数该有的语句都还在。**
i0 = s.index('''    if args.check:
        old = io.open(OUT, encoding="utf-8").read() if os.path.exists(OUT) else ""''')
i1 = s.index('''    print("已生成 %s（累计提交 %d、Go 声明用例见表内）" % (OUT, commits()))
    return 0''') + len('''    print("已生成 %s（累计提交 %d、Go 声明用例见表内）" % (OUT, commits()))
    return 0''')
stranded = s[i0:i1]
assert 'norm(old) == norm(body)' in stranded and 'io.open(OUT, "w"' in stranded
s = s[:i0] + s[i1:]

# 顺手清掉移动后留下的多余空行
s = s.replace('''    return "\\n".join(l for l in text.splitlines()
                      if not any(l.startswith(p) for p in VOLATILE_PREFIXES))




def main(argv=None):''', '''    return "\\n".join(l for l in text.splitlines()
                      if not any(l.startswith(p) for p in VOLATILE_PREFIXES))


def main(argv=None):''')
s = s.replace('''    return "\\n".join(l for l in text.splitlines()
                      if not any(l.startswith(p) for p in VOLATILE_PREFIXES))


def main(argv=None):''', '''    return "\\n".join(l for l in text.splitlines()
                      if not any(l.startswith(p) for p in VOLATILE_PREFIXES))


def main(argv=None):''')

# 2) 接回 main() 末尾（缩进即函数体）
anchor = '''    body = "\\n".join(lines) + "\\n"


def _selftest():'''
assert s.count(anchor) == 1
new = '''    body = "\\n".join(lines) + "\\n"

''' + stranded + '''


def _selftest():'''
s = s.replace(anchor, new)

ast.parse(s)
io.open(p, 'w', encoding='utf-8').write(s)

# 3) 结构自检：main 必须真的有"写文件"和"返回 0"两条语句
tree = ast.parse(s)
main_fn = [n for n in tree.body if isinstance(n, ast.FunctionDef) and n.name == 'main'][0]
body_src = ast.dump(main_fn)
assert 'Write' in body_src and 'RETURN' in body_src, "main 还是缺尾巴"
norm_fn = [n for n in tree.body if isinstance(n, ast.FunctionDef) and n.name == 'norm'][0]
# norm 只该有一条 return
assert len([x for x in norm_fn.body if isinstance(x, ast.Return)]) == 1
assert not any(isinstance(x, (ast.If, ast.Expr)) and 'args' in ast.dump(x) for x in norm_fn.body)
print("OK main 尾巴接回、norm 恢复单一 return")
