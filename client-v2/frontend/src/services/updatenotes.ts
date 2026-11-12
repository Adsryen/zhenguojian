/**
 * 发布说明净化：GitHub Release body 常带 markdown（CI 自动生成的资产对照表就是
 * 表格），直接铺在设置页里就是一坨「|---|」噪音。这里把 body 压成纯文本行：
 * 标题行丢弃（版本/日期已有专门行）、表格分隔行丢弃、表格数据行转「文件 — 用途」、
 * 其余行去 ** 加粗记号原样保留。
 */
export function cleanReleaseBody(body: string, max = 600): string {
  const out: string[] = []
  for (const raw of (body || '').split(/\r?\n/)) {
    const line = raw.trim()
    if (!line) continue
    if (/^#+\s/.test(line)) continue
    if (/^\|[\s:|-]+\|?$/.test(line)) continue
    if (line.startsWith('|')) {
      const cells = line.replace(/^\|/, '').replace(/\|$/, '').split('|').map((c) => c.trim().replace(/\*\*/g, ''))
      const meaningful = cells.filter((c) => c && !/^(文件|用途|File|Purpose)$/i.test(c))
      if (meaningful.length >= 2) out.push('· ' + meaningful.slice(0, 2).join(' — '))
      else if (meaningful.length === 1) out.push('· ' + meaningful[0])
      continue
    }
    out.push(line.replace(/\*\*/g, ''))
  }
  const text = out.join('\n')
  return text.length > max ? text.slice(0, max) + '…' : text
}
