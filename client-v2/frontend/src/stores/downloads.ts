/* 下载与合并状态容器（V2-3）。
   - 真实模式：Go downloadsvc 每 1500ms 轮询核心，快照有变化时 EventsEmit('downloads:update', JSON)；
     本 store 双轨探测 wails/runtime 事件总线（同 stores/player.ts attach 写法），总线缺失回落 api.downloadList 轮询。
   - VITE_MOCK：api.downloadList 轮询（fixtures.ts 内置 1s 状态机模拟下载/合并推进）。
   载荷形状 = DownloadSnapshot { jobs, merges, status, mergeVerify }（对齐 internal/downloadsvc/service.go payloadLocked）。 */
import { defineStore } from 'pinia'
import { api, MOCK_MODE } from '@/services/api'
import type {
  DownloadJobRow,
  DownloadSettingsSnapshot,
  DownloadSnapshot,
  Drama,
  MergeEntryRow
} from '@/services/types'

const POLL_MS = 1500
const EVENT_NAME = 'downloads:update'

interface Bus {
  EventsOn?: (n: string, cb: (data: string) => void) => void
  EventsOff?: (n: string) => void
}

function eventBus(): Bus | null {
  const w = window as unknown as { wails?: Bus; runtime?: Bus }
  if (w.wails?.EventsOn) return w.wails
  if (w.runtime?.EventsOn) return w.runtime
  return null
}

export interface DownloadsStoreState {
  jobs: DownloadJobRow[]
  merges: MergeEntryRow[]
  status: string
  mergeVerify: boolean
  settings: DownloadSettingsSnapshot | null
  attached: boolean
  lastError: string
  busyId: string
  ticker?: ReturnType<typeof setInterval>
}

export const useDownloadsStore = defineStore('downloads', {
  state: (): DownloadsStoreState => ({
    jobs: [],
    merges: [],
    status: '',
    mergeVerify: true,
    settings: null,
    attached: false,
    lastError: '',
    busyId: ''
  }),

  getters: {
    /** 未归档任务（下载队列 tab） */
    activeJobs: (s): DownloadJobRow[] => s.jobs.filter((j) => !j.archived),
    /** 归档任务（归档 tab） */
    archivedJobs: (s): DownloadJobRow[] => s.jobs.filter((j) => j.archived && j.state === 'completed'),
    /** 按剧分组的已完成分集（合并入口），≥2 集才可整季合并（v1 _merge_targets 语义） */
    mergeGroups: (s): Array<{ dramaId: string; drama: Drama; title: string; jobs: DownloadJobRow[] }> => {
      const byDrama = new Map<string, DownloadJobRow[]>()
      for (const j of s.jobs) {
        if (j.archived || j.state !== 'completed' || !j.drama) continue
        const list = byDrama.get(j.drama.id) ?? []
        list.push(j)
        byDrama.set(j.drama.id, list)
      }
      const out: Array<{ dramaId: string; drama: Drama; title: string; jobs: DownloadJobRow[] }> = []
      for (const [dramaId, list] of byDrama) {
        if (list.length < 2) continue
        list.sort((a, b) => a.index - b.index)
        out.push({ dramaId, drama: list[0].drama as Drama, title: list[0].drama?.title ?? dramaId, jobs: list })
      }
      return out.sort(
        (a, b) =>
          (b.jobs[b.jobs.length - 1].created ?? 0) - (a.jobs[a.jobs.length - 1].created ?? 0)
      )
    },
    runningMerge: (s): MergeEntryRow | undefined => s.merges.find((m) => m.state === 'running')
  },

  actions: {
    /** 进入页面时挂接事件/轮询（PlayerView 同款 attach/detach 生命周期） */
    attach() {
      if (this.attached) return
      this.attached = true
      void this.refresh()
      if (MOCK_MODE) {
        this.ticker = setInterval(() => void this.refresh(), POLL_MS)
        return
      }
      const bus = eventBus()
      if (bus?.EventsOn) {
        bus.EventsOn(EVENT_NAME, (payload: string) => {
          try {
            this.applySnapshot(JSON.parse(payload) as DownloadSnapshot)
          } catch {
            /* 忽略坏载荷，等下一轮兜底轮询 */
          }
        })
        // 事件驱动为主，3 倍周期轮询兜底（防漏发）
        this.ticker = setInterval(() => void this.refresh(), POLL_MS * 3)
      } else {
        this.ticker = setInterval(() => void this.refresh(), POLL_MS)
      }
    },

    detach() {
      this.attached = false
      if (this.ticker) {
        clearInterval(this.ticker)
        this.ticker = undefined
      }
      const bus = eventBus()
      bus?.EventsOff?.(EVENT_NAME)
    },

    applySnapshot(snap: DownloadSnapshot) {
      this.jobs = snap.jobs ?? []
      this.merges = snap.merges ?? []
      this.status = snap.status ?? ''
      if (typeof snap.mergeVerify === 'boolean') this.mergeVerify = snap.mergeVerify
    },

    async refresh() {
      try {
        this.applySnapshot(await api.downloadList())
        this.lastError = ''
      } catch (err) {
        this.lastError = err instanceof Error ? err.message : String(err)
      }
    },

    async loadSettings() {
      try {
        this.settings = await api.downloadSettings()
        this.mergeVerify = this.settings.mergeVerify !== false
      } catch (err) {
        this.lastError = err instanceof Error ? err.message : String(err)
      }
    },

    /** 统一动作包装：记录 busyId 禁用按钮，成功后立即刷一次快照（事件回推有 ≤1.5s 延迟） */
    async run(id: string, fn: () => Promise<unknown>): Promise<boolean> {
      this.busyId = id
      try {
        await fn()
        await this.refresh()
        this.lastError = ''
        return true
      } catch (err) {
        this.lastError = err instanceof Error ? err.message : String(err)
        return false
      } finally {
        this.busyId = ''
      }
    },

    control(job: DownloadJobRow, command: 'pause' | 'resume' | 'remove' | 'archive' | 'restore') {
      const map = {
        pause: api.downloadPause,
        resume: api.downloadResume,
        remove: api.downloadCancel,
        archive: api.downloadArchive,
        restore: api.downloadRestore
      } as const
      return this.run(job.id, () => map[command](job.id))
    },

    retry(job: DownloadJobRow) {
      // v1 语义：核心没有独立 retry 命令，重试 = resume（failed → queued）
      return this.run(job.id, () => api.downloadRetry(job.id))
    },

    controlAll(command: 'pauseAll' | 'resumeAll') {
      return this.run(`all:${command}`, () => api.downloadControlAll(command))
    },

    async removeBatch(
      ids: string[]
    ): Promise<{ completed: string[]; failures: Record<string, string> } | null> {
      this.busyId = 'batch'
      try {
        const res = await api.downloadRemoveBatch(ids)
        await this.refresh()
        this.lastError = ''
        return res
      } catch (err) {
        this.lastError = err instanceof Error ? err.message : String(err)
        return null
      } finally {
        this.busyId = ''
      }
    },

    saveSettings(concurrency: number, bySource: boolean, mergeVerify: boolean) {
      return this.run('settings', async () => {
        this.settings = await api.saveDownloadSettings(concurrency, bySource, mergeVerify)
        this.mergeVerify = this.settings.mergeVerify !== false
      })
    },

    mergeSeason(drama: Drama) {
      return this.run(`merge:${drama.id}`, () => api.mergeStart('season', drama, []))
    },

    mergeSelected(ids: string[]) {
      return this.run('merge:selected', () => api.mergeStart('selected', null, ids))
    },

    mergeCancel(id: string) {
      return this.run(`merge:cancel:${id}`, () => api.mergeCancel(id))
    }
  }
})
