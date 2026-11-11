import { defineStore } from 'pinia'
import { api } from '@/services/api'
import type { SourceListItem } from '@/services/types'

/** 全局源状态：所有页面共享当前源与源清单；选择持久化 localStorage。 */
/* 站源备注表：核心 listSources 不带 note，v1 口径来自
   zhenguojian/app/zhenguojian/settings_panel.py FALLBACK_SOURCES
   （连通性观察/使用提示，展示于设置页站源卡片）。 */
const SOURCE_NOTES: Record<string, string> = {
  hongguo: '主源，恒可用。',
  sorani: '播放需登录：设置 > 站源 > 站源账号。',
  dsd: '上游连通性不稳定，失败时稍后重试。',
  'huangguo-video': '上游连通性不稳定，失败时稍后重试。',
  youzi: '18+ 内容，出厂默认停用，需手动打开。',
}

const STORE_KEY = 'zhenguojian.source'
/** 站源手动排序（设置页拖拽卡片产生），存完整可见 slug 序列 */
const ORDER_KEY = 'zgj.source-order'

function loadSourceOrder(): string[] {
  try {
    const raw = localStorage.getItem(ORDER_KEY)
    if (!raw) return []
    const arr = JSON.parse(raw)
    if (!Array.isArray(arr)) return []
    return arr.every((x) => typeof x === 'string') ? (arr as string[]) : []
  } catch {
    return [] // 读档失败/类型错 → 视为未排序
  }
}

export const useAppStore = defineStore('app', {
  state: () => ({
    sources: [] as SourceListItem[],
    currentSlug: localStorage.getItem(STORE_KEY) || '',
    sourceOrder: loadSourceOrder(),
    sourcesLoading: false,
    sourcesError: '',
    /** 导航折叠状态（对齐参照项目三段式侧栏的可折叠语义） */
    navCollapsed: localStorage.getItem('zhenguojian.navCollapsed') === '1'
  }),
  getters: {
    current(state): SourceListItem | undefined {
      return state.sources.find((s) => s.slug === state.currentSlug)
    },
    /** 排序后的源清单：设置页网格与站源切换弹层共用。
        规则①默认（order 空）：红果 hongguo 置顶，其余保持 registry 原序；
        规则②已存 order：按 order 排列，order 里不存在的 slug 忽略；
        规则③sources 中不在 order 里的新源追加尾部（registry 原相对序）。 */
    orderedSources(state): SourceListItem[] {
      if (!state.sourceOrder.length) {
        const head = state.sources.filter((s) => s.slug === 'hongguo')
        const rest = state.sources.filter((s) => s.slug !== 'hongguo')
        return [...head, ...rest]
      }
      const bySlug = new Map(state.sources.map((s) => [s.slug, s]))
      const out: SourceListItem[] = []
      for (const slug of state.sourceOrder) {
        const s = bySlug.get(slug)
        if (s) {
          out.push(s)
          bySlug.delete(slug)
        }
      }
      for (const s of state.sources) if (bySlug.has(s.slug)) out.push(s)
      return out
    },
    /** 默认可用源：优先 hongguo，其次第一个 implemented && enabled */
    usable(state): boolean {
      return state.sources.some((s) => s.implemented && s.enabled)
    }
  },
  actions: {
    async loadSources(force = false) {
      if (this.sources.length && !force) return
      this.sourcesLoading = true
      this.sourcesError = ''
      try {
        this.sources = (await api.listSources()).map((row) =>
          row.note || !SOURCE_NOTES[row.slug]
            ? row
            : { ...row, note: SOURCE_NOTES[row.slug] }
        )
        const valid = this.sources.find(
          (s) => s.slug === this.currentSlug && s.implemented
        )
        if (!valid) {
          const preferred =
            this.sources.find((s) => s.slug === 'hongguo' && s.implemented) ||
            this.sources.find((s) => s.implemented && s.enabled) ||
            this.sources[0]
          this.currentSlug = preferred?.slug || ''
          localStorage.setItem(STORE_KEY, this.currentSlug)
        }
      } catch (e: any) {
        this.sourcesError = e?.message ?? String(e)
      } finally {
        this.sourcesLoading = false
      }
    },
    setSource(slug: string) {
      this.currentSlug = slug
      localStorage.setItem(STORE_KEY, slug)
    },
    /** 提交站源手动排序（拖拽落位后调用）：存完整可见 slug 序列 */
    setSourceOrder(slugs: string[]) {
      this.sourceOrder = [...slugs]
      try {
        localStorage.setItem(ORDER_KEY, JSON.stringify(slugs))
      } catch {
        /* 写入失败：本会话内仍生效 */
      }
    },
    toggleNav() {
      this.navCollapsed = !this.navCollapsed
      localStorage.setItem('zhenguojian.navCollapsed', this.navCollapsed ? '1' : '0')
    }
  }
})
