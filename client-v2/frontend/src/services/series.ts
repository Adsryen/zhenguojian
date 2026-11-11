/* services/series.ts — 同系列（多季）标题归并，v1 series.py + watchstore.season_number
   的 1:1 TS 端口（纯函数）。红果把每一季当独立短剧，无 series_id，只按标题归并；
   仅用于展示与补下一季，不改动核心返回数据。
   语义锚点：zhenguojian/app/zhenguojian/series.py 全文 +
   watchstore.py:31-59（_SEASON_RE/_chinese_number/season_number 1..200 钳位）。 */

import type { Drama } from './types'

/* 与 Go 核心一致：第 + 数字（全角/阿拉伯/中文）+ 季/部，允许尾部标点。 */
const SEASON_SUFFIX_RE =
  /第\s*([0-9０-９零〇一二两兩三四五六七八九十百]+)\s*([季部])[\s\p{P}\p{S}\p{C}\p{M}]*$/u

const CN_DIGITS: Record<string, number> = {
  零: 0, 〇: 0, 一: 1, 二: 2, 两: 2, 兩: 2, 三: 3, 四: 4, 五: 5,
  六: 6, 七: 7, 八: 8, 九: 9
}

function isAsciiDigits(text: string): boolean {
  return /^[0-9]+$/.test(text)
}

/* v1 unicodedata.normalize('NFKC', x) 等价：全角→半角 + 兼容预组/分解
   （覆盖常见 CJK 标点/兼容区），其余字符原样。 */
const NFKC_MAP: Record<string, string> = {
  '（': '(', '）': ')', '［': '[', '］': ']', '｛': '{', '｝': '}',
  '　': ' ', '．': '.', '，': ',', '：': ':', '；': ';', '！': '!', '？': '?',
  '％': '%', '＃': '#', '＆': '&', '＊': '*', '＋': '+', '－': '-', '／': '/',
  '＜': '<', '＞': '>', '＝': '=', '＾': '^', '＿': '_', '‘': "'", '’': "'",
  '“': '"', '”': '"', '＄': '$', '＠': '@', '｜': '|', '＇': "'"
}
export function nfkc(text: string): string {
  let out = ''
  for (const ch of text) {
    if (ch.charCodeAt(0) >= 0xff10 && ch.charCodeAt(0) <= 0xff5a) out += String.fromCharCode(ch.charCodeAt(0) - 0xfee0)
    else if (ch.charCodeAt(0) >= 0xff21 && ch.charCodeAt(0) <= 0xff3a) out += String.fromCharCode(ch.charCodeAt(0) - 0xfee0)
    else out += NFKC_MAP[ch] ?? ch
  }
  return out.normalize('NFC')
}

/* v1 watchstore._chinese_number：阿拉伯直读；中文按 十/百 段累加。 */
export function chineseNumber(raw: string): number {
  const text = nfkc(String(raw ?? '')).trim()
  if (!text) return 0
  if (isAsciiDigits(text)) return parseInt(text, 10)
  let section = 0
  let number = 0
  for (const char of text) {
    if (char in CN_DIGITS) number = CN_DIGITS[char]
    else if (char === '十') { section += (number || 1) * 10; number = 0 }
    else if (char === '百') { section += (number || 1) * 100; number = 0 }
  }
  return section + number
}

/* v1 watchstore.season_number：无标记按第 1 季；1..200 之外回落 1。 */
export function seasonNumber(title: string | undefined | null): number {
  const match = SEASON_SUFFIX_RE.exec(String(title ?? ''))
  if (!match) return 1
  const value = chineseNumber(match[1])
  return value >= 1 && value <= 200 ? value : 1
}

/* v1 base_title：去季标记；「剧名（第二季）」样式去掉标记前残余标点。 */
export function baseTitle(title: string | undefined | null): string {
  const text = String(title ?? '').trim()
  if (!text) return ''
  const match = SEASON_SUFFIX_RE.exec(text)
  if (match) {
    const base = text.slice(0, match.index).replace(/[\s\p{P}\p{S}\p{C}\p{M}]+$/u, '').trim()
    if (base) return base
  }
  return text
}

/* v1 season_unit：无标记默认「季」。 */
export function seasonUnit(title: string | undefined | null): '季' | '部' {
  const match = SEASON_SUFFIX_RE.exec(String(title ?? ''))
  return match ? (match[2] as '季' | '部') : '季'
}

/* v1 series_key：NFKC + 去空白/标点 + casefold。 */
export function seriesKey(title: string | undefined | null): string {
  const base = nfkc(baseTitle(title))
  return base.replace(/[\s\p{P}\p{S}\p{C}\p{M}_]+/gu, '').toLowerCase()
}

export interface SeasonRow {
  season: number
  unit: '季' | '部'
  title: string
  episodes: number
  drama: Drama
}

/* v1 season_label：「第 N 季/部」（注意 %d 后有空格）。 */
export function seasonLabel(number: number, unit = '季'): string {
  return `第 ${Math.trunc(number)} ${unit || '季'}`
}

/* v1 group：同系列按季号收拢；同季保留集数多者，集数同保留标题长者。 */
export function groupSeasons(items: Drama[], title: string): SeasonRow[] {
  const key = seriesKey(title)
  const unit = seasonUnit(title)
  if (!key) return []
  const best = new Map<number, SeasonRow>()
  for (const item of items ?? []) {
    if (!item || typeof item !== 'object') continue
    if (seriesKey(item.title) !== key) continue
    if (seasonUnit(item.title) !== unit) continue
    const number = seasonNumber(item.title)
    if (number <= 0) continue
    const row: SeasonRow = {
      season: number,
      unit,
      title: item.title ?? '',
      episodes: Math.trunc(Number(item.episodes ?? 0)) || 0,
      drama: item
    }
    const previous = best.get(number)
    if (!previous) { best.set(number, row); continue }
    if (row.episodes > previous.episodes ||
        (row.episodes === previous.episodes && row.title.length > previous.title.length)) {
      best.set(number, row)
    }
  }
  return [...best.keys()].sort((a, b) => a - b).map((n) => best.get(n)!)
}

/* v1 find_season / next_season */
export function findSeason(rows: SeasonRow[], number: number): SeasonRow | null {
  for (const row of rows ?? []) if (Math.trunc(Number(row.season ?? 0)) === Math.trunc(number)) return row
  return null
}

export function nextSeason(rows: SeasonRow[], current: number): SeasonRow | null {
  const cur = Math.trunc(Number(current ?? 1))
  for (const row of rows ?? []) if (Math.trunc(Number(row.season ?? 0)) > cur) return row
  return null
}
