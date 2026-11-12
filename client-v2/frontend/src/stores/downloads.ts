/* 下载与合并状态容器（V2-3）。
   - 真实模式：Go downloadsvc 每 1500ms 轮询核心，快照有变化时 EventsEmit('downloads:update', JSON)；
     本 store 双轨探测 wails/runtime 事件总线（同 stores/player.ts attach 写法），总线缺失回落 api.downloadList 轮询。
   - VITE_MOCK：api.downloadList 轮询（fixtures.ts 内置 1s 状态机模拟下载/合并推进）。
   载荷形状 = DownloadSnapshot { jobs, merges, status, mergeVerify }（对齐 internal/downloadsvc/service.go payloadLocked）。 */
import { defineStore } from 'pinia'
import { api, MOCK_MODE } from '@/services/api'
import type {
  ArchiveGroupRow,
  ArchiveScanResult,
  DownloadDirectoryInfo,
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

/* 统计条分组（对齐 v1 downloads.py _StatBar.GROUPS:849-855）：
   "" = 全部（不筛选，含 removing 等瞬时态，恒等于队列全集）；
   active = queued+downloading；paused/completed/failed 各自单态。 */
export type StatKey = '' | 'active' | 'paused' | 'completed' | 'failed'

export const STAT_GROUPS: Array<{ key: Exclude<StatKey, ''>; states: DownloadJobRow['state'][] }> = [
  { key: 'active', states: ['queued', 'downloading'] },
  { key: 'paused', states: ['paused'] },
  { key: 'completed', states: ['completed'] },
  { key: 'failed', states: ['failed'] }
]

/** 判断任务状态是否命中分组（纯派生，与 statCounts 同一张表） */
export function statMatch(key: StatKey, state: DownloadJobRow['state']): boolean {
  if (!key) return true
  return STAT_GROUPS.some((g) => g.key === key && (g.states as string[]).includes(state))
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
  /** R22a③「下载时保存弹幕」：真值在 Go（downloads.state.json），这里只是镜像。
   *  默认关，与 v1 一致。 */
  danmakuDownload: boolean
  settings: DownloadSettingsSnapshot | null
  /** 核心下载根目录（bindings DownloadDirectory），归档页自证扫描位置 */
  downloadDir: DownloadDirectoryInfo | null
  /** 归档目录扫描结果（App.ArchiveScan，交付 C）；null=尚未扫描（回落索引 archived 列表） */
  archive: ArchiveScanResult | null
  archiveLoading: boolean
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
    danmakuDownload: false,
    settings: null,
    downloadDir: null,
    archive: null,
    archiveLoading: false,
    attached: false,
    lastError: '',
    busyId: ''
  }),

  getters: {
    /** 未归档任务（下载队列 tab） */
    activeJobs: (s): DownloadJobRow[] => s.jobs.filter((j) => !j.archived),
    /** 归档任务（归档 tab）——扫描失败/未扫描时的回落数据源（保留单条恢复/删除入口） */
    archivedJobs: (s): DownloadJobRow[] => s.jobs.filter((j) => j.archived && j.state === 'completed'),
    /** 归档扫描分组（文件系统为准，mtime 倒序由 Go 侧保证）；null=尚无结果 */
    archiveGroups: (s): ArchiveGroupRow[] | null => (s.archive ? s.archive.groups : null),
    /** 归档计数：扫描分组数优先，未扫描回落索引 archived 行数 */
    archiveCount(): number {
      return this.archive ? this.archive.groups.length : this.archivedJobs.length
    },
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
    runningMerge: (s): MergeEntryRow | undefined => s.merges.find((m) => m.state === 'running'),

    /** 统计条计数（下载队列 = 未归档 jobs 快照聚合，点击不发请求）：
        '' 全部恒等于队列行数（含 removing 瞬时态），其余按 STAT_GROUPS 归组。 */
    statCounts(): Record<StatKey, number> {
      const out: Record<StatKey, number> = { '': 0, active: 0, paused: 0, completed: 0, failed: 0 }
      for (const j of this.activeJobs) {
        out[''] += 1
        for (const g of STAT_GROUPS) {
          if ((g.states as string[]).includes(j.state)) out[g.key] += 1
        }
      }
      return out
    },

    /** 队列任务出现过的站源 slug 去重（首次出现序），供统计条右侧站源下拉；
        displayName 由视图从 app store sources 解析（v1 _source_label 等价物）。 */
    queueSources(): string[] {
      const seen: string[] = []
      for (const j of this.activeJobs) {
        const src = j.drama?.source
        if (src && !seen.includes(src)) seen.push(src)
      }
      return seen
    }
  },

  actions: {
    /** 进入页面时挂接事件/轮询（PlayerView 同款 attach/detach 生命周期） */
    attach() {
      if (this.attached) return
      this.attached = true
      void this.refresh()
      void this.loadDirectory()
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
      // 轮询载荷里带上了旗标（payloadLocked），所以"另一处改了开关"能自我纠正，
      // 不靠进设置页才读一次
      if (typeof snap.danmakuDownload === 'boolean') this.danmakuDownload = snap.danmakuDownload
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
        this.danmakuDownload = !!this.settings.danmakuDownload
      } catch (err) {
        this.lastError = err instanceof Error ? err.message : String(err)
      }
    },

    /** 下载根目录：归档 tab 头部展示「当前在哪个目录找文件」，空态区分目录不存在 */
    async loadDirectory() {
      try {
        this.downloadDir = await api.downloadDirectory()
      } catch {
        this.downloadDir = null
      }
    },

    /** 归档目录扫描（交付 C）：force=忽略已缓存结果重扫；失败保留旧值并记 lastError */
    async loadArchive(force = false) {
      if (this.archiveLoading) return
      if (this.archive && !force) return
      this.archiveLoading = true
      try {
        this.archive = await api.archiveScan()
      } catch (err) {
        this.lastError = err instanceof Error ? err.message : String(err)
      } finally {
        this.archiveLoading = false
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
      return this.run(job.id, () => map[command](job.id)).then((ok) => {
        // 归档/恢复改变 archived 旗标 → 扫描分组徽标需重取（交付 C 联动）
        if (ok && (command === 'archive' || command === 'restore')) void this.loadArchive(true)
        return ok
      })
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

    /** 即点即存（v1 那个勾选框就是 toggled 直接写设置，不等"保存"按钮）。
     *  失败时**先回滚再抛**：调用方要能区分"写成功了"和"写失败弹回来了"。
     *  ⚠ 上一版返回最终值，于是失败(=prev) 与幂等成功(=prev) 在调用方看来一模一样，
     *  界面把一次写失败说成了「已关闭：不再保存弹幕文件」——
     *  这话是真的（现在确实是关着），但它把"没写进去、重启就会变回去"这件事藏掉了。
     *  这是 danmaku_toggle_verify ⑥b 实测到的，不是我读代码想到的。 */
    async setDanmakuDownload(on: boolean): Promise<boolean> {
      const prev = this.danmakuDownload
      this.danmakuDownload = on
      try {
        const res = await api.setDanmakuDownload(on)
        this.danmakuDownload = !!res.danmakuDownload
        return this.danmakuDownload
      } catch (err) {
        this.danmakuDownload = prev // 写失败就回滚，别让勾选框骗人
        this.lastError = err instanceof Error ? err.message : String(err)
        throw err
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
