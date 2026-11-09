import { defineStore } from 'pinia'
import { api } from '@/services/api'
import type { SourceListItem } from '@/services/types'

/** 全局源状态：所有页面共享当前源与源清单；选择持久化 localStorage。 */
const STORE_KEY = 'zhenguojian.source'

export const useAppStore = defineStore('app', {
  state: () => ({
    sources: [] as SourceListItem[],
    currentSlug: localStorage.getItem(STORE_KEY) || '',
    sourcesLoading: false,
    sourcesError: '',
    /** 导航折叠状态（对齐参照项目三段式侧栏的可折叠语义） */
    navCollapsed: localStorage.getItem('zhenguojian.navCollapsed') === '1'
  }),
  getters: {
    current(state): SourceListItem | undefined {
      return state.sources.find((s) => s.slug === state.currentSlug)
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
        this.sources = await api.listSources()
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
    toggleNav() {
      this.navCollapsed = !this.navCollapsed
      localStorage.setItem('zhenguojian.navCollapsed', this.navCollapsed ? '1' : '0')
    }
  }
})
