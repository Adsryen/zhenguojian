/**
 * series.ts P0 单测（第九十五轮，vitest 脚手架第一批）。
 * 语义锚点：v1 series.py + watchstore.py:31-59（season_number 1..200 钳位）。
 * 纯函数、不碰 window/fetch——本层测试一旦需要 DOM/网络即写错了地方
 * （那属于 verification/ 的 E2E 层）。
 */
import { describe, it, expect } from 'vitest'
import {
  nfkc,
  chineseNumber,
  seasonNumber,
  baseTitle,
  seasonUnit,
  seriesKey,
  seasonLabel,
  groupSeasons,
  findSeason,
  nextSeason,
} from './series'
import type { Drama } from './types'

function drama(title: string, episodes = 1): Drama {
  return { id: `d-${title}`, source: 'hongguo', title, episodes }
}

describe('nfkc（v1 unicodedata.normalize NFKC 等价）', () => {
  it('全角数字/字母转半角', () => {
    expect(nfkc('１２３')).toBe('123') // 0xff10-0xff19
    expect(nfkc('ＡＢＣ')).toBe('ABC') // 0xff21-0xff3a
  })
  it('全角标点走 NFKC_MAP', () => {
    expect(nfkc('（）')).toBe('()')
    expect(nfkc('，．：')).toBe(',.:') // ．=U+FF0E 全角句点（在 map 里）
  })
  it('。=U+3002 句号保留（与 Python NFKC 同形：U+3002 无兼容分解）', () => {
    expect(nfkc('。')).toBe('。')
  })
  it('普通 CJK 原样', () => {
    expect(nfkc('真果鉴')).toBe('真果鉴')
  })
})

describe('chineseNumber（v1 watchstore._chinese_number）', () => {
  it('阿拉伯直读（含全角→半角后）', () => {
    expect(chineseNumber('2')).toBe(2)
    expect(chineseNumber('２')).toBe(2)
  })
  it('空/未知 → 0', () => {
    expect(chineseNumber('')).toBe(0)
    expect(chineseNumber(undefined as unknown as string)).toBe(0)
  })
  it('十/百 段累加', () => {
    expect(chineseNumber('十')).toBe(10)
    expect(chineseNumber('十一')).toBe(11)
    expect(chineseNumber('二十')).toBe(20)
    expect(chineseNumber('二十三')).toBe(23)
    expect(chineseNumber('百')).toBe(100)
    expect(chineseNumber('两百')).toBe(200)
  })
  it('两/兩 = 2', () => {
    expect(chineseNumber('两季'.slice(0, 1))).toBe(2)
    expect(chineseNumber('兩')).toBe(2)
  })
})

describe('seasonNumber（v1 season_number：无标记=1，1..200 之外回落 1）', () => {
  it('无标记 → 1', () => {
    expect(seasonNumber('某剧')).toBe(1)
    expect(seasonNumber('')).toBe(1)
    expect(seasonNumber(null)).toBe(1)
  })
  it('阿拉伯/全角/中文季号', () => {
    expect(seasonNumber('某剧 第2季')).toBe(2)
    expect(seasonNumber('某剧 第 3 季')).toBe(3) // 允许中间空白
    expect(seasonNumber('某剧 第２季')).toBe(2)
    expect(seasonNumber('某剧 第二季')).toBe(2)
    expect(seasonNumber('某剧 第兩季')).toBe(2)
    expect(seasonNumber('某剧 第十季')).toBe(10)
    expect(seasonNumber('某剧 第23季')).toBe(23)
  })
  it('部 同样算季号', () => {
    expect(seasonNumber('某剧 第3部')).toBe(3)
  })
  it('越界回落 1（1..200 钳位，v1 同）', () => {
    expect(seasonNumber('某剧 第0季')).toBe(1)
    expect(seasonNumber('某剧 第201季')).toBe(1)
    expect(seasonNumber('某剧 第200季')).toBe(200)
  })
  it('季号不在标题末尾 → 不匹配（v1 $ 锚点语义）', () => {
    // 「（更新至10）」里含汉字（\p{L}，不在尾部允许集 \p{P}\p{S}\p{C}\p{M} 内）
    expect(seasonNumber('某剧 第2季（更新至10）')).toBe(1)
  })
  it('尾部标点允许', () => {
    expect(seasonNumber('某剧 第2季。')).toBe(2)
  })
})

describe('baseTitle / seasonUnit（v1 base_title / season_unit）', () => {
  it('去季标记', () => {
    expect(baseTitle('某剧 第2季')).toBe('某剧')
    expect(baseTitle('某剧（第二季）')).toBe('某剧') // 标记前残余标点一并去掉
    expect(baseTitle('某剧')).toBe('某剧')
    expect(baseTitle('')).toBe('')
  })
  it('unit 默认 季，标记为部时取 部', () => {
    expect(seasonUnit('某剧')).toBe('季')
    expect(seasonUnit('某剧 第2季')).toBe('季')
    expect(seasonUnit('某剧 第3部')).toBe('部')
  })
})

describe('seriesKey（NFKC + 去空白/标点/下划线 + casefold）', () => {
  it('大小写/全角/空白/标点归一', () => {
    expect(seriesKey('My 剧')).toBe('my剧')
    expect(seriesKey('ＡＢＣ')).toBe('abc')
    expect(seriesKey('某 剧！')).toBe('某剧')
    expect(seriesKey('a_b')).toBe('ab')
  })
  it('带季号的标题与裸标题同 key', () => {
    expect(seriesKey('某剧 第2季')).toBe(seriesKey('某剧'))
  })
})

describe('seasonLabel（v1 season_label：「第 N 季」%d 后有空格）', () => {
  it('默认 季、可指定 部', () => {
    expect(seasonLabel(2)).toBe('第 2 季')
    expect(seasonLabel(3, '部')).toBe('第 3 部')
  })
})

describe('groupSeasons（v1 group：同系列按季收拢）', () => {
  it('同 key 同 unit 按季号升序收拢，跨 key/unit 不混', () => {
    const rows = groupSeasons(
      [
        drama('某剧 第2季', 10),
        drama('某剧 第3季', 10),
        drama('某剧', 8), // 无标记 = 第 1 季
        drama('另一剧 第2季', 99), // key 不同，排除
        drama('某剧 第2部', 99), // unit 不同（部 vs 季），排除
      ],
      '某剧 第2季'
    )
    expect(rows.map((r) => r.season)).toEqual([1, 2, 3])
    expect(rows.map((r) => r.title)).toEqual(['某剧', '某剧 第2季', '某剧 第3季'])
  })
  it('同季保留集数多者；集数同保留标题长者（key 相同、全长不同）', () => {
    const rows = groupSeasons(
      [drama('某剧 第2季', 5), drama('某剧 第2季', 5), drama('某剧· 第2季', 5)],
      '某剧 第2季'
    )
    expect(rows).toHaveLength(1)
    expect(rows[0].title).toBe('某剧· 第2季') // 同 key 同集数 → 取全长较长者
    const rows2 = groupSeasons(
      [drama('某剧· 第2季', 5), drama('某剧 第2季', 6)],
      '某剧 第2季'
    )
    expect(rows2[0].title).toBe('某剧 第2季') // 集数多者无条件胜出
  })
  it('季号不在标题末尾的标题被合法排除（$ 锚点，v1 同形）', () => {
    // 「第2季·重制版」以汉字结尾，SEASON_SUFFIX_RE 的 $ 匹配不到 →
    // baseTitle 不剥标记 → seriesKey ≠ 裸剧名 key → 不进组。
    const rows = groupSeasons(
      [drama('某剧 第2季', 5), drama('某剧 第2季·重制版', 9)],
      '某剧 第2季'
    )
    expect(rows).toHaveLength(1)
    expect(rows[0].title).toBe('某剧 第2季')
  })
  it('空 key（纯标点标题）→ 空', () => {
    expect(groupSeasons([drama('第2季')], '!!!')).toEqual([])
  })
})

describe('findSeason / nextSeason（v1 find_season / next_season）', () => {
  const rows = groupSeasons(
    [drama('某剧', 1), drama('某剧 第2季', 1), drama('某剧 第3季', 1)],
    '某剧'
  )
  it('findSeason 精确匹配', () => {
    expect(findSeason(rows, 2)?.title).toBe('某剧 第2季')
    expect(findSeason(rows, 9)).toBeNull()
  })
  it('nextSeason 取严格大于当前季的第一行', () => {
    expect(nextSeason(rows, 1)?.title).toBe('某剧 第2季')
    expect(nextSeason(rows, 2)?.title).toBe('某剧 第3季')
    expect(nextSeason(rows, 3)).toBeNull()
    expect(nextSeason(rows, 0)?.title).toBe('某剧') // 0 → 从第 1 季起
  })
})
