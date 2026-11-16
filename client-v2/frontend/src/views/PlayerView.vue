<script setup lang="ts">
/* PlayerView（V2-3c + V2-F 播放器体验包）：路由 query（d/ep/pos/c，dramaRef.playerRoute 约定，兼容 ?drama=<id>）→ store.bootFromQuery。
   控制条 = 播放/暂停、进度条（拖拽 Seek）、倍速 0.5–3（v1 SPEED_CHOICES，player.py:61）、
   音量（0-100，持久化 zgj.player.volume，v1 player.py:1899-1905 语义）、弹幕开关（PlayerDanmaku）、
   播完「下一集」（player:ended → playNext，v1 player.py:1185 语义）、关闭=PlayerClosePlayer+返回。
   数据由 player:tick / player:ended / player:error / danmaku:stats 事件驱动（stores/player.ts）；
   旧 ReportViewport 已随契约移除——Go 侧不再收视口，ASS 缩放由服务内部处理。
   弹幕由 mpv ASS 渲染，本组件 DOM 不画弹幕（任务定案）。
   V2-4 画面内嵌：attach（spawn 前）→ PlayerSetBounds 上报 .pl-stage 物理像素矩形；
   加载/出错/播完/未开播上报零尺寸让位 DOM 覆盖层；离开 detach 隐藏。
   V2-4b 点击穿透带：随几何上报 PlayerHitBands(top,bottom)——覆盖层与视频
   矩形的实际重叠高度（物理像素），Go 侧 WM_NCHITTEST 命中带内让给 WebView2。
   mock/无桥/回退开关下 attach 返回 embedded:false，本组件零特殊分支（reportBounds 直通短路）。
   ── V2-F 追加（体验包）──
   ① 全屏：按钮 + F 键 → window.runtime.WindowFullscreen/WindowUnfullscreen
      （可选调用：本仓库未生成 wailsjs/runtime 垫片文件，frontend/wailsjs 不存在，
       直接走注入的全局 runtime；不可用时 hint 明示，不假成功）。
   ② 快捷键全集：Space 播/暂、←→ ±5s、↑↓ 音量±5、F 全屏、M 静音、D 弹幕、? 帮助浮层
      （8s 自动关 / Esc 关，v1 player.py:1326-1329 + 861 HotkeyHelpOverlay 语义；
       输入控件聚焦时忽略，对照 v1 main.py:1765-1769）。Esc 无浮层时退出全屏（v1 同款次序）。
   ③ 常驻 ⏮/⏭（首/末集禁用）+ 播完自动连播（store.markEnded，zgj.player.autoplay 默认开）。
   ④ 右侧可折叠选集面板（300px/折叠 36px）：只读复用 EpisodeGrid（未改该组件；其
      current watch 自动切分组，DOM 落位由本层 scrollIntoView '.ep-btn.current' 补齐），
      点击切集走 store.openEpisode(drama, ep, 0, chapter)。
   ⑤ 画质下拉（自动/1080/720/540）：PlayerOpen 无 quality 形参（禁改 app.go）→
      偏好持久化 + 切档重开原位续播 + hint 两条分支（stores/player.ts setQuality：
     真实态「已切换到{q}…」／演示或无剧目「player.qualityDeferred」）。
   ⑥ 进度 5s 节流保存：见 store.applySnapshot → maybePersistWatch（v1 main.py:2495 同款）。
   布局契约不变：.pl-top/.pl-stage/.pl-bar 仍为垂直兄弟链（pl-mid 内 stage 在 bar 之上），
   hitbands 断言（top.bottom<=stage.top、bar.top>=stage.bottom）继续成立。 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { usePlayerStore, SPEED_CHOICES, QUALITY_CHOICES } from '@/stores/player'
import { dramaFromQuery, detailRoute } from '@/services/dramaRef'
import { api, embedApi, MOCK_MODE, ipcLedgerSummary, type EmbedHostDebug, type EmbedDiagInfo } from '@/services/api'
import type { Chapter } from '@/services/types'
import EpisodeGrid from '@/components/EpisodeGrid.vue'
import HotkeyHelp from '@/components/HotkeyHelp.vue'
import { t, tf } from '@/services/i18n'
import { anyModalOpen, clearEscLedger, escSawOverlay } from '@/services/overlays'

/* ── V2-F 文案已收敛至 i18n.ts player.*（原组件内 L 表）；t() 随 locale 响应 ── */
const hotkeys = computed(() => [
  { keys: t('player.hotkey.space.keys'), desc: t('player.hotkey.space.desc') },
  { keys: t('player.hotkey.arrows.keys'), desc: t('player.hotkey.arrows.desc') },
  { keys: t('player.hotkey.volume.keys'), desc: t('player.hotkey.volume.desc') },
  { keys: t('player.hotkey.fullscreen.keys'), desc: t('player.hotkey.fullscreen.desc') },
  { keys: t('player.hotkey.mute.keys'), desc: t('player.hotkey.mute.desc') },
  { keys: t('player.hotkey.danmaku.keys'), desc: t('player.hotkey.danmaku.desc') },
  { keys: t('player.hotkey.help.keys'), desc: t('player.hotkey.help.desc') }
])

const QUALITY_OPTIONS = computed(() =>
  QUALITY_CHOICES.map((q) => ({
    value: q,
    label: q === 'auto' ? t('player.qualityAuto') : `${q}p`
  }))
)

const route = useRoute()
const router = useRouter()
const store = usePlayerStore()

const dragging = ref(false)
const dragPos = ref(0)

/* V2-F：全屏态 / 帮助浮层 / 选集面板 */
const isFullscreen = ref(false)
const helpOpen = ref(false)
/* R4-2：弹幕透明度小浮层（v1 是「点开后上下拖动」的竖向滑条，这里同路数） */
const opacityOpen = ref(false)
const panelOpen = ref(true)
const chapters = ref<Chapter[]>([])
const chaptersLoading = ref(false)
const panelBodyEl = ref<HTMLElement | null>(null)
const embedReason = ref('')
/* 回退原因码 → 用户文案。裸码（forced-window / auto-fallback-unplaced /
   unknown）不该直接进给用户读的句子（alpha.40 之后文案质量就是信任），
   已知码走 i18n，未知码保留原样（诊断时还能看到真实 reason）。 */
function reasonText(code: string): string {
  if (code === 'forced-window') return t('player.embedReasonForced')
  if (code === 'auto-fallback-unplaced') return t('player.embedReasonAuto')
  return code
}
/* 内嵌现场（父窗类名 / 子窗矩形 / 可见性 / 最近一次换算）。
   默认不显示——正常用户不该看到调试信息；
   置 localStorage['zgj.embedDebug']='1' 后显示在视频区左上角，
   排错时截一张图就够，不用开 DevTools 复制。 */
const embedHost = ref<EmbedHostDebug | null>(null)
/* ⚠ 临时改成**默认开启**：连续两版要求用户手动开开关才能拿到现场，
   而用户回报的还是同一句"还是没画面"。查不清的原因是没有数据，
   所以这一版把诊断常驻显示（含一行"调试信息，查清后移除"），
   并附一个逃生门按钮——黑屏期间用户至少能看。
   TODO(内嵌黑屏查清后)：改回 localStorage 开关。 */
const embedDebugOn = true

/* ⚠ 这一行我第一版写成中文串拼接，被 `i18n_static_scan.py` 的零硬编码棘轮
   当场抓到 7 处（它抓的是**界面可见中文**，不管这段是不是调试信息）。
   抓得对：调试徽标也是界面，英文态下不该吐中文。改成走 i18n 整句参数化。 */
const embedDebugText = computed(() => {
  const h = embedHost.value
  if (!h) return ''
  return tf('player.embedDebug', {
    cls: h.parentClass || '?', hwnd: h.childHwnd || '?',
    // 数值一律 `?? 0`：HostDebug 的字段是可选的（Go 侧任何一步读失败都留零值），
    // 而 tf 的参数类型不接受 undefined。
    w: h.childWidth ?? 0, h: h.childHeight ?? 0, x: h.childLeft ?? 0, y: h.childTop ?? 0,
    vis: h.childVisible ? 1 : 0,
    inW: h.lastInW ?? 0, inH: h.lastInH ?? 0,
    outW: h.lastOutW ?? 0, outH: h.lastOutH ?? 0,
    outVis: h.lastOutVisible ? 1 : 0,
    /* 判读表最后一格（"覆盖层还看得见吗"）从人眼判断改成系统直读：
       子窗矩形中心那个像素上最靠上的窗口是谁。值一律 ASCII —— 这是界面文本，
       中文串会被 i18n 静态扫描抓（上一轮真抓过我这种写法）。
       `=ours` = 子窗压在最上 ⇒ 黑屏该怪 mpv 没往这窗画；
       `=other` = 别人压在上面（多半 Chrome_RenderWidgetHostHWND=WebView2）⇒ airspace/z-order；
       末尾 `(selftest!)` = 探测点不在返回窗口矩形内，这条读数不可信，别按它判案。 */
    top: !h.topAtCenterHwnd ? 'no-probe'
      : (h.topAtCenterClass || '?') + (h.topIsChild ? '=ours' : '=other') + (h.topContainsProbe ? '' : '(selftest!)'),
    // applyCount 来自 Go（上报到达次数）；measured 来自前端（我量到了什么）。
    // 两者并排才能分开「没送到」和「送到时就是 0」：
    //   calls=0 + measured=780×650 → 线断了（异常被吞/绑定没有这个方法/构建是旧的）
    //   calls>0 + measured=0×0      → 布局时机问题，stage 当时没尺寸
    ver: appVersion.value || '?',
    calls: h.applyCount ?? 0,
    measured: embedMeasure.value,
    setErr: embedSetErr.value || 'none',
    // 「该不该显示」的四个输入。可见=0 只告诉我子窗是藏着的，不告诉我**为什么**：
    // 真机上「mpv 在播、时间在走、画面全黑」正是 show 永远为假的形状。
    // r/l/e/f 依次 = running / loading / error / eof（1=真）。
    want: videoShouldShow() ? 1 : 0,
    r: store.running ? 1 : 0,
    l: store.loading ? 1 : 0,
    e: store.error ? 1 : 0,
    f: store.eof ? 1 : 0
  })
})

/* 把内嵌现场一次性写进剪贴板。为什么要这个按钮：
   连续四个 tag 我都是靠用户截图读徽标，而徽标整行很长，**在界面上会被左边缘裁掉**
   （alpha.31/33 的截图里连「父窗」两字都不在画面内），于是每次都要来回追问。
   复制出来的是纯文本，还额外带一份 host 的原始 JSON —— 我读到的是数字本身，
   不是渲染过的句子，省掉"这行是被裁的还是本来就是 0"这类歧义。 */
const embedCopy = ref<'idle' | 'ok' | 'fail'>('idle')

const embedDiagInfo = ref<EmbedDiagInfo | null>(null)
function embedDiagText(pingLine: string, ipcLine: string): string {
  const d = embedDiagInfo.value
  return [
    `version=${appVersion.value || '?'}`,
    `badge=${embedDebugText.value}`,
    `error=${store.error || ''}`,
    `measured=${embedMeasure.value || 'n/a'}`,
    `report-error=${embedSetErr.value || 'none'}`,
    /* 第九十七轮 alpha.42 新增两行，把定案缺口补在**复制这一刻**：
       ping= 现场心跳：复制前 2s 超时的即时 Ping 往返。alpha.41 的 diag 到达
       而 setBounds 没到——不对称在传输层；现场 Ping 直接回答「此刻 IPC 通不通」
       （pong<2000=通，timeout=此刻不通）。
       ipc= 账本：每方法 sent/settled（挂起 P、错误 e）。PlayerSetBounds:3/0P3
       = 发了 3 次一次没回 = alpha.41 指纹的前端侧读数，与 Go 的 arrived 对撞。 */
    `ping=${pingLine}`,
    `ipc=${ipcLine}`,
    /* 第九十六轮：diag= 行把 Go 侧 app.log 的「实际到达次数」带回前端现场。
       arrived 与前端量得的 #N 并排：N>0 而 arrived=0 → IPC 没到 Go；
       arrived==N 而子窗仍 1×1 → 到了 Go 但锁排队/未执行（waitMs 在日志里）。 */
    `diag=${d ? JSON.stringify(d) : 'unavailable'}`,
    `host=${JSON.stringify(embedHost.value ?? null)}`
  ].join('\n')
}

async function copyEmbedDiag() {
  embedCopy.value = 'idle'
  /* 复制前现场取数（第九十七轮）：① 重取 diag——arrived 是计数，取早了会少
     （用户先点播放再复制的场景）；② 现场 Ping——诊断按钮按下的**这一刻**
     IPC 还通不通，2s 超时（不 await 挂起：Promise.race 兜底）。 */
  let freshDiag: EmbedDiagInfo | null = embedDiagInfo.value
  if (embedActive) {
    freshDiag = await embedApi.diag().catch(() => null)
    if (freshDiag) embedDiagInfo.value = freshDiag
  }
  const pingMs = await Promise.race<number | 'timeout'>([
    embedApi.pingLive(),
    new Promise<'timeout'>(res => setTimeout(() => res('timeout'), 2000))
  ])
  const ipcLine = ipcLedgerSummary()
  /* ping 三态：pong<Nms>=现场通；timeout=2s 内没回（挂起指纹的现场证据）；
     mock/no-bridge=非真桥路径（pingLive 回 -1），不显示成负数时长。 */
  let pingLine: string
  if (pingMs === 'timeout') pingLine = 'timeout'
  else if (pingMs < 0) pingLine = MOCK_MODE ? 'mock' : 'no-bridge'
  else pingLine = `pong${Math.round(pingMs)}ms`
  const text = embedDiagText(pingLine, ipcLine)
  try {
    await navigator.clipboard.writeText(text)
    embedCopy.value = 'ok'
    return
  } catch {
    const app = (window as any).go?.main?.App
    if (app?.ClipboardWrite) {
      try {
        await app.ClipboardWrite(text)
        embedCopy.value = 'ok'
        return
      } catch {
        embedCopy.value = 'fail'
        return
      }
    }
    embedCopy.value = 'fail'
  }
}

/* ── V2-4 内嵌几何上报 ── */
const stageEl = ref<HTMLElement | null>(null)
/* 覆盖层穿透带（V2-4b）：标题条/控制条与 .pl-stage 的实际重叠才需要让出命中；
   现行 flex 兄弟布局重叠恒 0（这些像素本就在 host 窗之外），布局若改为
   叠在画面上（absolute 浮层）自动生效，WndProc 按带返回 HTTRANSPARENT。 */
const topEl = ref<HTMLElement | null>(null)
const barEl = ref<HTMLElement | null>(null)
let embedActive = false
/* 第六十三轮：真机徽标连续两个 tag 只显示「最近上报 0×0」，而我无法区分
   「setBounds 没送到 Go」和「送到时量到 0」——这两种成因的修法完全相反。
   下面三个状态把前端这一侧也变成读数：我量到多少、第几次量、送出去有没有失败。
   再加版本号：同一张截图必须能自证是哪个构建（前两轮我都是靠猜）。 */
const embedMeasure = ref('none')
const embedSetErr = ref('')
const appVersion = ref('')
let measureSeq = 0
let stageRO: ResizeObserver | undefined
let barsRO: ResizeObserver | undefined
/* ── 第九十六轮自愈合逃生 ──
   alpha.40 真机：前端 setBounds 发了 3 次、Go 侧 applyCount=0、用户黑屏，
   而我拿不到数据只能连烧 tag。这一版让产品自己逃生：running 之后若 8s 内
   子窗**从未被摆过**（回执 applyCount=0 或 childWidth 停在创建尺寸 1），
   说明「上报链」整体失效（IPC 挂起/回执撒谎/子窗没建好，具体原因由
   app.log 的 arrived 计数与看门狗的 pending>8s 区分）——不必等下一个
   release，直接切 mpv 独立窗口让用户先看到画面，并留下 reason 供诊断。
   手动逃生门（embed-force-window 按钮）仍在，自动只是兜底。 */
let autoFbTimer: ReturnType<typeof setTimeout> | undefined
const autoFbMs = (): number => {
  if (typeof window !== 'undefined') {
    const o = (window as unknown as { __zgjAutoFbMs?: number }).__zgjAutoFbMs
    if (typeof o === 'number' && o > 0) return o
  }
  return 8000
}
function unplaced(): boolean {
  const h = embedHost.value
  return !h || h.applyCount === 0 || h.childWidth === 1 || h.childHeight === 1
}
function armAutoFallback() {
  if (autoFbTimer) clearTimeout(autoFbTimer)
  if (!embedActive || !store.running || store.error) return
  autoFbTimer = setTimeout(async () => {
    autoFbTimer = undefined
    if (!embedActive || !store.running || store.error) return
    if (!unplaced()) return
    const ok = await embedApi.forceWindow()
    if (!ok) return
    embedActive = false
    embedReason.value = 'auto-fallback-unplaced'
    await store.bootFromQuery({ ...route.query, title: dramaTitleFromQuery() })
  }, autoFbMs())
}

function videoShouldShow(): boolean {
  // 仅在实际出画面期间占据视频矩形；其余状态让位给 DOM 覆盖层（原生子窗盖在 DOM 之上）。
  return store.running && !store.loading && !store.error && !store.eof
}

async function reportHitBands(el: HTMLElement) {
  const s = el.getBoundingClientRect()
  const dpr = window.devicePixelRatio || 1
  const t = topEl.value?.getBoundingClientRect()
  const top = t && t.bottom > s.top ? Math.round((t.bottom - s.top) * dpr) : 0
  const b = barEl.value?.getBoundingClientRect()
  const bottom = b && b.top < s.bottom ? Math.round((s.bottom - b.top) * dpr) : 0
  await embedApi.playerHitBands(top, bottom)
}

async function reportBounds() {
  if (!embedActive) return
  const el = stageEl.value
  if (!el) return
  const r = el.getBoundingClientRect()
  const dpr = window.devicePixelRatio || 1
  const x = Math.round(r.left * dpr)
  const y = Math.round(r.top * dpr)
  const w = Math.round(r.width * dpr)
  const h = Math.round(r.height * dpr)
  /* 第六十一轮真机数据（徽标 1×1 @328,147 · 可见=0 · 最近上报 0×0）定出的修法：
     旧写法在 videoShouldShow() 为假时上报 0×0，而 running 只能在 PlayerOpen
     成功之后才为真 —— 于是「起手摆位」这件事永远不发生，mpv 从 spawn 起
     就在一个创建尺寸 1×1 的隐藏窗上画。现在几何永远照实报，
     可见性交给 show：让位给 DOM 覆盖层是"藏"，不是"抹掉尺寸"。 */
  measureSeq += 1
  embedMeasure.value = `${w}x${h}@${dpr}#${measureSeq}`
  const st = await embedApi.setBounds(x, y, w, h, videoShouldShow())
  // 回执里的 host 比 attach 那一刻新：摆位之后的子窗矩形、上报计数都在里面
  if (st.host) embedHost.value = st.host
  embedSetErr.value = st.ok ? '' : (st.error || 'failed')
  await reportHitBands(el)
}

watch(
  () => [store.running, store.loading, store.error, store.eof],
  () => {
    void reportBounds()
    armAutoFallback()
  }
)

const sliderValue = computed({
  get: () => (dragging.value ? dragPos.value : store.position),
  set: (v: number) => {
    dragPos.value = v
  }
})

function onDragStart() {
  dragging.value = true
}

async function onDragEnd() {
  if (!dragging.value) return
  dragging.value = false
  await store.seek(dragPos.value)
}

/* ── V2-F：全屏 ── */
type WailsWindowRuntime = {
  WindowFullscreen?: () => void
  WindowUnfullscreen?: () => void
}

function toggleFullscreen() {
  const rt = (window as unknown as { runtime?: WailsWindowRuntime }).runtime
  const name = isFullscreen.value ? 'WindowUnfullscreen' : 'WindowFullscreen'
  const fn = rt?.[name]
  if (typeof fn !== 'function') {
    // 垫片文件（wailsjs/runtime）本仓库未生成且真实浏览器无 window.runtime → 明示不可用
    store.setHint(t('player.fullscreenUnavailable'))
    return
  }
  fn.call(rt)
  isFullscreen.value = !isFullscreen.value
}

/* ── V2-F：快捷键分发（输入控件聚焦时忽略；对照 v1 main.py:1765-1769 / player.py:1326-1329） ── */
function isTypingTarget(t: EventTarget | null): boolean {
  const el = t as HTMLElement | null
  if (!el || !el.tagName) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable === true
}

function onHotkey(e: KeyboardEvent) {
  if (e.repeat || e.ctrlKey || e.metaKey || e.altKey) return
  const k = e.key
  if (k === 'Escape') {
    // v1 次序：帮助浮层在最上 → Esc 先关浮层；无浮层时全屏态 Esc 退出全屏（main.py _on_playback_esc）
    if (helpOpen.value) helpOpen.value = false
    else if (isFullscreen.value) toggleFullscreen()
    // v1 `_on_playback_esc` 的最后一支：**非全屏时 Esc 就是关窗**（关窗即停播）。
    // v2 之前只做到"退全屏"就 return，等于少了一整条键位语义——
    // 用户按 Esc 期待播放器关掉，结果什么都没发生。
    // 有浮层压着时不关窗（浮层自己吃掉了这次 Esc），见 services/overlays.ts。
    else if (!escSawOverlay() && !anyModalOpen()) void closePlayer()
    clearEscLedger()
    return
  }
  if (isTypingTarget(e.target)) return
  switch (k) {
    case ' ':
      e.preventDefault() // 防聚焦按钮被原生 Space 再触发一次
      void store.togglePlay()
      break
    case 'ArrowLeft':
      e.preventDefault()
      void store.seek(store.position - 5)
      break
    case 'ArrowRight':
      e.preventDefault()
      void store.seek(store.position + 5)
      break
    case 'ArrowUp':
      e.preventDefault()
      void store.applyVolume(store.volume + 5)
      break
    case 'ArrowDown':
      e.preventDefault()
      void store.applyVolume(store.volume - 5)
      break
    case 'f':
    case 'F':
      toggleFullscreen()
      break
    case 'm':
    case 'M':
      void store.toggleMute()
      break
    case 'd':
    case 'D':
      void store.toggleDanmaku()
      break
    case '?':
      helpOpen.value = !helpOpen.value
      break
  }
}

/* ── V2-F：选集面板数据（只读复用 EpisodeGrid；其接口满足需求，未自写列表） ── */
async function loadChapters() {
  const drama = store.drama
  if (!drama || store.demoMode) {
    chapters.value = []
    return
  }
  chaptersLoading.value = true
  try {
    const d = await api.detail(drama)
    chapters.value = d.chapters ?? []
  } catch {
    chapters.value = []
  } finally {
    chaptersLoading.value = false
    void nextTick(scrollToCurrent)
  }
}


/* 下一季入口（store.checkNextSeason 查到才出现） */
const nextSeasonLabel = computed(() => {
  const n = store.nextSeason
  if (!n) return ''
  return tf('player.nextSeasonGo', { t: n.title || tf('player.seasonFallback', { s: n.season, u: n.unit }) })
})
function goNextSeason() {
  const n = store.nextSeason
  if (!n) return
  void router.push(detailRoute(n.drama))
}

function scrollToCurrent() {
  const el = panelBodyEl.value?.querySelector('.ep-btn.current')
  if (el) el.scrollIntoView({ block: 'nearest' })
}

watch(
  () => store.drama?.id,
  () => void loadChapters(),
  { immediate: true }
)
/* ── 预缓冲下一集（v1 main.py set_preload_hook/_on_player_preload_next 对齐：
   当前集会话建立后预热下一集片头，切集秒开；失败静默不打扰播放） ── */
const preloadedEp = ref(0)
async function preloadNext() {
  if (preloadedEp.value === store.episodeIndex) return
  preloadedEp.value = store.episodeIndex
  const drama = store.drama
  const next = chapters.value[store.episodeIndex] // episodeIndex 1 起 → [ep]=下一集
  if (!drama || !next || store.demoMode) return
  try {
    await api.preloadEpisode(drama, next, store.episodeIndex + 1, store.quality === 'auto' ? 0 : Number(store.quality))
  } catch {
    /* v1 同款：预缓冲失败不影响播放 */
  }
}
watch(
  () => [store.opened, store.episodeIndex] as const,
  ([opened]) => {
    if (opened && chapters.value.length) void preloadNext()
  }
)

watch(
  () => store.episodeIndex,
  () => void nextTick(scrollToCurrent)
)

function onEpisodeSelect(chapter: Chapter, _index: number, episode: number) {
  if (!store.drama || episode === store.episodeIndex) return
  void store.openEpisode(store.drama, episode, 0, chapter)
}

function onQualityChange(ev: Event) {
  void store.setQuality((ev.target as HTMLSelectElement).value)
}

function dramaTitleFromQuery(): string {
  try {
    return dramaFromQuery(String(route.query.drama ?? ''), route.query as Record<string | number | symbol, unknown>).title || ''
  } catch {
    return ''
  }
}

/** R4-2：滑条拖动 → store.setDanmakuOpacity（落盘 + 下发壳层；
 *  壳层用已抓数据重渲染 ASS 并 sub-reload，见 internal/player 的 SetDanmakuOpacity）。
 *  滑条值 = 不透明度百分比 20–100（v1 tools.danmaku_opacity 同量纲，100 = 完全不透明），
 *  所以「往上更实、往下更透」，标签沿 v1 写「透明 NN%」——那是 v1 的历史口径，不是笔误。 */
function onOpacityInput(e: Event) {
  const el = e.target as HTMLInputElement
  void store.setDanmakuOpacity(Number(el.value))
}

async function closePlayer() {
  await store.shutdown()
  if (window.history.length > 1) router.back()
  else router.push('/')
}

function onStageResize() {
  void reportBounds()
}

/* 有界等待：promise 在 ms 内 settle 就用它的结果，否则放弃等待（原 promise
   继续在后台跑，settle 后仍会更新 embedHost/embedSetErr，无害）。
   第九十七轮 alpha.42：onMounted 里 `await reportBounds()` 是我为修「起手摆位」
   加的（第六十一轮），但它有个致命副作用——若 setBounds 的 IPC 挂起（alpha.40/41
   真机指纹），整条 boot 链被卡死：不 boot、不 spawn、buildInfo 不显、自愈合
   逃生门永不 arm（它等 running）。挂起的 IPC 不该有卡死 boot 链的权力：
   起手摆位最多等 1.5s，等不到就继续 boot——spawn 前没摆位顶多是 mpv 在旧
   几何上画，ResizeObserver/状态 watch 会立刻补报；而 boot 卡死的代价是
   用户永远黑屏且没有任何自愈路径。 */
function bounded<T>(p: Promise<T>, ms: number): Promise<T | undefined> {
  return Promise.race([p, new Promise<undefined>(res => setTimeout(() => res(undefined), ms))])
}

onMounted(async () => {
  // attach 必须先于 bootFromQuery→PlayerOpen：mpv spawn 时 --wid 才已就位。
  const attach = await embedApi.attach()
  embedActive = attach.embedded
  /* 第五十九轮真机反馈：用户看到的是"弹窗播放"而不是页面内画面。
     内嵌失败本身有正常回退分支，但**原因从不显示**，所以我拿不到数据、
     只能靠猜。现在把它挂在视频区角落（真机且回退时才出现），
     用户一眼能看见、也能直接念给我。 */
  embedReason.value = attach.embedded ? '' : attach.reason || 'unknown'
  embedHost.value = attach.host ?? null
  /* 诊断面快照：exe 身份 + Go 侧 setBounds 到达计数（复制诊断的 diag= 行）。
     只在内嵌可用时取——独立窗回退态没有 app.log 计数可言。不 await：
     诊断不可用不该拖慢进入播放页的主链。 */
  if (embedActive) {
    void embedApi.diag().then(d => { embedDiagInfo.value = d }).catch(() => undefined)
  }
  if (embedActive) {
    if (stageEl.value) {
      stageRO = new ResizeObserver(() => void reportBounds())
      stageRO.observe(stageEl.value)
    }
    // 浮层自身高度变化（换行/收起）也可能改变重叠，单独观测重报带宽。
    barsRO = new ResizeObserver(() => {
      if (stageEl.value) void reportHitBands(stageEl.value)
    })
    if (topEl.value) barsRO.observe(topEl.value)
    if (barEl.value) barsRO.observe(barEl.value)
    window.addEventListener('resize', onStageResize)
  }
  /* ⚠ 起手摆位仍要"先于 spawn 的尽力等待"（spawn 那一刻的窗口尺寸就是 mpv
     的 drawable，旧写法 void 化后与 PlayerOpen 赛跑出 1×1）——但必须**有界**：
     1.5s 内 settle 最好；挂起就放弃等待继续 boot（reportBounds 是 async void
     语义的兜底路径，settle 后照样更新 embedHost/embedSetErr，ResizeObserver
     和状态 watch 也会持续补报）。见 bounded() 的注释：挂起不许卡死 boot 链。 */
  await bounded(reportBounds(), 1500)
  // 版本号进徽标：不再有「这张图是哪个包」这种靠猜的回合
  api.buildInfo().then(b => { appVersion.value = b.version || 'dev' }).catch(() => {})
  window.addEventListener('keydown', onHotkey)
  await store.bootFromQuery({ ...route.query, title: dramaTitleFromQuery() })
})

/* 逃生门：放弃内嵌、改用 mpv 独立窗口，然后重开当前集。
   只改"画面落在哪个窗"，不改播放/会话/弹幕语义。 */
async function forceWindowMode() {
  const ok = await embedApi.forceWindow()
  if (!ok) return
  embedActive = false
  embedReason.value = 'forced-window'
  await store.bootFromQuery({ ...route.query, title: dramaTitleFromQuery() })
}

/* 播放失败重试：重放整条 boot 链（解析超时/网络抖动后可直接再试一次）。 */
async function retryBoot() {
  await store.bootFromQuery({ ...route.query, title: dramaTitleFromQuery() })
}

onBeforeUnmount(() => {
  stageRO?.disconnect()
  stageRO = undefined
  barsRO?.disconnect()
  barsRO = undefined
  if (autoFbTimer) { clearTimeout(autoFbTimer); autoFbTimer = undefined }
  window.removeEventListener('resize', onStageResize)
  window.removeEventListener('keydown', onHotkey)
  if (embedActive) void embedApi.detach()
  void store.shutdown()
})
</script>

<template>
  <div class="player">
    <header class="pl-top" ref="topEl">
      <button class="pl-btn ghost" type="button" @click="closePlayer">← {{ t('common.back') }}</button>
      <h1 class="pl-title">{{ store.title || t('player.title') }}</h1>
      <div class="pl-badges">
        <span v-if="store.demoMode" class="pl-badge demo">{{ t('player.badgeDemo') }}</span>
        <span v-if="store.danmakuCount !== null" class="pl-badge">{{ tf('player.badgeDanmakuCount', { n: store.danmakuCount }) }}</span>
        <span v-if="store.danmakuNote" class="pl-badge">{{ store.danmakuNote }}</span>
        <span v-if="store.eof" class="pl-badge warn">{{ t('player.badgeEof') }}</span>
      </div>
    </header>

    <div class="pl-mid">
      <div class="pl-col">
        <div id="zgj-video-stage" ref="stageEl" class="pl-stage" @click="store.togglePlay()">
          <div
            v-if="embedDebugOn && embedHost"
            class="pl-hint embed-debug"
            data-testid="embed-debug"
          >{{ embedDebugText }}<template v-if="store.error">
            · {{ store.error }}</template>
            <button
              class="pl-btn tiny"
              type="button"
              data-testid="embed-copy-diag"
              @click="copyEmbedDiag"
            >{{ embedCopy === 'ok' ? t('player.embedCopied') : embedCopy === 'fail' ? t('player.embedCopyFail') : t('player.embedCopy') }}</button>
            <button
              class="pl-btn tiny"
              type="button"
              data-testid="embed-force-window"
              @click="forceWindowMode"
            >{{ t('player.embedForceWindow') }}</button></div>
          <div
            v-if="embedReason && !store.loading && !store.error"
            class="pl-hint embed-fallback"
            data-testid="embed-fallback"
          >{{ tf('player.embedFallback', { reason: reasonText(embedReason) }) }}</div>
          <div v-if="store.loading" class="pl-hint">{{ t('player.openingSession') }}</div>
          <div v-else-if="store.error" class="pl-hint err">
            {{ store.error }}
            <!-- 本地文件不在/越界时**不给「重试」**：重试只会拿到同一条错误，
                 那是个看起来能点的死按钮。线上解析失败仍然给重试（R30 ⑧ 断言的就是那条）。 -->
            <button v-if="!store.localFailed" class="pl-btn ghost sm" type="button" data-testid="player-retry" @click.stop="retryBoot">
              {{ t('player.retry') }}
            </button>
          </div>
          <div v-else class="pl-stage-inner">
            <div class="pl-disc" :class="{ spin: store.running && !store.paused && !store.eof }">
              <span class="pl-disc-core" />
            </div>
            <div v-if="store.eof" class="pl-ended">
              <span class="pl-ended-txt">{{ t('player.endedEpisode') }}</span>
              <button v-if="store.hasNextEpisode" class="pl-btn next" type="button" @click.stop="store.playNext()">
                {{ t('player.nextEpisode') }}
              </button>
              <!-- 本季真结局：查到下一季给入口（v1 _apply_next_season 语义——不自动跨季播放，
                   点「去看下一季」进那部剧详情，由用户决定从哪集看起）；查无 v1 状态栏 6s 提示。 -->
              <button
                v-else-if="store.nextSeason"
                class="pl-btn next"
                type="button"
                data-testid="pl-next-season"
                @click.stop="goNextSeason"
              >
                {{ nextSeasonLabel }}
              </button>
              <button v-else class="pl-btn ghost" type="button" @click.stop="store.togglePlay()">{{ t('player.replay') }}</button>
            </div>
            <div v-else-if="store.paused && !store.loading" class="pl-bigplay">▶</div>
          </div>
        </div>

        <footer class="pl-bar" ref="barEl">
          <button
            class="pl-btn nav prev"
            type="button"
            :disabled="!store.hasPrevEpisode"
            :title="t('player.prevEpisode')"
            @click="store.playPrev()"
          >⏮</button>
          <button
            class="pl-btn nav next-ep"
            type="button"
            :disabled="!store.hasNextEpisode"
            :title="t('player.nextEpisode')"
            @click="store.playNext()"
          >⏭</button>
          <button class="pl-btn" type="button" :title="store.paused ? t('player.play') : t('player.pause')" @click="store.togglePlay()">
            {{ store.paused ? '▶' : '⏸' }}
          </button>
          <span class="pl-time">{{ store.posLabel }} / {{ store.durLabel }}</span>
          <input
            v-model.number="sliderValue"
            class="pl-slider grow"
            type="range"
            min="0"
            :max="Math.max(store.duration, 1)"
            step="0.5"
            @pointerdown="onDragStart"
            @change="onDragEnd"
          />
          <label class="pl-group">
            <span class="pl-label">{{ t('player.speed') }}</span>
            <select class="pl-select" :value="store.speed" @change="store.setSpeed(Number(($event.target as HTMLSelectElement).value))">
              <option v-for="sp in SPEED_CHOICES" :key="sp" :value="sp">{{ sp }}×</option>
            </select>
          </label>
          <label class="pl-group">
            <span class="pl-label">{{ t('player.volume') }}</span>
            <input
              class="pl-slider vol"
              type="range"
              min="0"
              max="100"
              step="1"
              :value="store.volume"
              @input="store.applyVolume(Number(($event.target as HTMLInputElement).value))"
            />
            <span class="pl-time">{{ store.volume }}</span>
          </label>
          <label class="pl-group">
            <span class="pl-label">{{ t('player.quality') }}</span>
            <select class="pl-select quality" :value="store.quality" @change="onQualityChange">
              <option v-for="q in QUALITY_OPTIONS" :key="q.value" :value="q.value">{{ q.label }}</option>
            </select>
          </label>
          <button
            class="pl-btn ghost ap"
            :class="{ off: !store.autoplay }"
            type="button"
            :title="t('player.autoplayHint')"
            @click="store.setAutoplay(!store.autoplay)"
          >{{ store.autoplay ? t('player.autoplayOn') : t('player.autoplayOff') }}</button>
          <button
            class="pl-btn ghost dm"
            :class="{ off: !store.danmakuOn }"
            type="button"
            :title="t('player.danmakuToggle')"
            @click="store.toggleDanmaku()"
          >
            {{ store.danmakuOn ? t('player.danmakuOn') : t('player.danmakuOff') }}
          </button>
          <span class="pl-dmop">
            <button
              class="pl-btn ghost dmop"
              :class="{ off: !store.danmakuOn }"
              type="button"
              data-testid="dm-opacity-btn"
              :title="t('player.danmakuOpacityHint')"
              @click="opacityOpen = !opacityOpen"
            >{{ tf('player.danmOpacity', { n: store.danmakuOpacity }) }}</button>
            <span v-if="opacityOpen" class="dmop-pop" data-testid="dm-opacity-pop">
              <input
                class="dmop-range"
                type="range"
                min="20"
                max="100"
                step="5"
                :value="store.danmakuOpacity"
                :aria-label="t('player.danmakuOpacityTitle')"
                data-testid="dm-opacity-range"
                @input="onOpacityInput"
              />
              <span class="dmop-val">{{ store.danmakuOpacity }}%</span>
            </span>
          </span>
          <button
            class="pl-btn ghost fs"
            type="button"
            :title="isFullscreen ? t('player.exitFullscreen') : t('player.fullscreen')"
            @click="toggleFullscreen"
          >{{ isFullscreen ? t('player.exitFullscreen') : t('player.fullscreen') }}</button>
          <button class="pl-btn ghost hk" type="button" :title="t('player.help')" @click="helpOpen = !helpOpen">⌨</button>
          <button class="pl-btn ghost close" type="button" @click="closePlayer">{{ t('player.close') }}</button>
        </footer>

        <div v-if="store.status" class="pl-status" :class="{ warn: store.demoMode }">{{ store.status }}</div>
      </div>

      <!-- 本地成品没有"选集"可言（v1 main.py:2612-2618 直接清空选集条 + 关掉上下集），
           所以整块侧栏不渲染，而不是渲染一个空网格。 -->
      <aside v-if="!store.localPath" class="pl-panel" :class="{ collapsed: !panelOpen }">
        <template v-if="panelOpen">
          <div class="pl-panel-head">
            <span class="pl-panel-title">{{ t('player.episodes') }}<span v-if="store.episodeIndex" class="pl-panel-cur">{{ tf('player.panelEpisode', { n: store.episodeIndex }) }}</span></span>
            <button class="pl-btn ghost sm" type="button" :title="t('player.episodesCollapse')" @click="panelOpen = false">▸</button>
          </div>
          <div ref="panelBodyEl" class="pl-panel-body">
            <div v-if="chaptersLoading" class="pl-panel-note">{{ t('player.episodesLoading') }}</div>
            <div v-else-if="!chapters.length" class="pl-panel-note">{{ store.demoMode ? t('player.episodesEmptyDemo') : t('player.episodesEmpty') }}</div>
            <EpisodeGrid
              v-else
              :chapters="chapters"
              :current="store.episodeIndex"
              :drama-vip="store.drama?.vip"
              @select="onEpisodeSelect"
            />
          </div>
        </template>
        <button v-else class="pl-panel-tab" type="button" :title="t('player.episodesExpand')" @click="panelOpen = true">
          {{ t('player.episodes') }}
        </button>
      </aside>
    </div>

    <HotkeyHelp
      v-if="helpOpen"
      :title="t('player.helpTitle')"
      :rows="hotkeys"
      :auto-close-ms="8000"
      @close="helpOpen = false"
    />

    <div v-if="store.hint" class="pl-toast" role="status">{{ store.hint }}</div>
  </div>
</template>

<style scoped>
.player {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  background: var(--bg);
  color: var(--text);
}
.pl-top {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 16px;
  border-bottom: 1px solid var(--border);
  background: var(--surface-2);
}
.pl-title {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.pl-badges {
  margin-left: auto;
  display: flex;
  gap: 8px;
}
.pl-dmop {
  position: relative;
  display: inline-flex;
}

/* 竖向小浮层：对应 v1 点开式滑条；往上数值大＝更实 */
.dmop-pop {
  position: absolute;
  bottom: calc(100% + 8px);
  left: 50%;
  transform: translateX(-50%);
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  padding: 8px 6px;
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: 8px;
  z-index: 70;
}

.dmop-range {
  width: 18px;
  height: 96px;
  accent-color: var(--accent);
}

.dmop-val {
  font-size: 11px;
  color: var(--muted);
}

.pl-badge {
  font-size: 12px;
  padding: 2px 8px;
  border-radius: 999px;
  background: var(--surface-3);
  color: var(--muted, #9aa3b2);
}
.pl-badge.demo {
  background: var(--accent-soft);
  color: var(--accent);
}
.pl-badge.warn {
  color: var(--warning, #e6a23c);
}
/* V2-F：stage/bar 与选集面板并排的中间行（stage/bar 仍是 pl-col 内垂直兄弟，穿透带契约不变） */
.pl-mid {
  flex: 1;
  min-height: 0;
  display: flex;
}
.pl-col {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}
.pl-stage {
  flex: 1;
  min-height: 0;
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  background: radial-gradient(120% 120% at 50% 0%, #10141c 0%, #000 90%);
  cursor: pointer;
}
.pl-hint {
  color: var(--muted, #9aa3b2);
  font-size: 13px;
}
.embed-fallback {
  opacity: 0.75;
  font-size: 12px;
}

.pl-hint.err {
  color: var(--danger, #f56c6c);
}
.pl-stage-inner {
  position: relative;
}
.pl-disc {
  width: 120px;
  height: 120px;
  border-radius: 50%;
  border: 2px solid var(--border);
  display: flex;
  align-items: center;
  justify-content: center;
  opacity: 0.5;
}
.pl-disc.spin {
  animation: pl-rot 6s linear infinite;
  border-color: var(--accent-soft);
}
.pl-disc-core {
  width: 26px;
  height: 26px;
  border-radius: 50%;
  background: var(--accent);
  box-shadow: 0 0 24px var(--accent-soft);
}
@keyframes pl-rot {
  to {
    transform: rotate(360deg);
  }
}
.pl-ended {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
}
.pl-ended-txt {
  font-size: 13px;
  color: var(--muted, #9aa3b2);
}
.pl-btn.next {
  border-color: var(--accent);
  color: var(--accent);
}
.pl-bigplay {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 42px;
  color: #fff;
  text-shadow: 0 2px 12px rgba(0, 0, 0, 0.6);
}
.pl-bar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  padding: 10px 16px;
  border-top: 1px solid var(--border);
  background: var(--surface-2);
}
.pl-btn {
  border: 1px solid var(--border);
  background: var(--surface-3);
  color: var(--text);
  border-radius: 8px;
  padding: 6px 12px;
  font-size: 13px;
  cursor: pointer;
}
.pl-btn.ghost {
  background: transparent;
}
.pl-btn.sm {
  padding: 2px 8px;
  font-size: 12px;
}
.pl-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
.pl-btn.dm.off,
.pl-btn.ap.off {
  opacity: 0.55;
}
.pl-btn.close {
  color: var(--muted);
}
.pl-btn.close:hover {
  color: var(--danger);
}
.pl-time {
  font-variant-numeric: tabular-nums;
  font-size: 12px;
  color: var(--muted, #9aa3b2);
  white-space: nowrap;
}
.pl-slider {
  accent-color: var(--accent);
  height: 4px;
}
.pl-slider.grow {
  flex: 1;
  min-width: 120px;
}
.pl-slider.vol {
  width: 90px;
}
.pl-group {
  display: flex;
  align-items: center;
  gap: 6px;
}
.pl-label {
  font-size: 12px;
  color: var(--muted, #9aa3b2);
}
.pl-select {
  background: var(--surface-3);
  color: var(--text);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 4px 6px;
  font-size: 12px;
}
.pl-status {
  padding: 6px 16px;
  font-size: 12px;
  color: var(--muted, #9aa3b2);
  border-top: 1px solid var(--border);
  background: var(--surface);
}
.pl-status.warn {
  color: var(--accent);
}
/* V2-F：右侧选集面板（展开 300 / 折叠 36） */
.pl-panel {
  width: 300px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  border-left: 1px solid var(--border);
  background: var(--surface-2);
  overflow: hidden;
}
.pl-panel.collapsed {
  width: 36px;
}
.pl-panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 8px 12px;
  border-bottom: 1px solid var(--border);
  font-size: 13px;
}
.pl-panel-title {
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.pl-panel-cur {
  margin-left: 6px;
  font-weight: 400;
  color: var(--muted, #9aa3b2);
  font-size: 12px;
}
.pl-panel-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 10px 12px;
}
.pl-panel-note {
  font-size: 12px;
  color: var(--muted, #9aa3b2);
  padding: 6px 0;
}
.pl-panel-tab {
  writing-mode: vertical-rl;
  height: 100%;
  border: none;
  background: transparent;
  color: var(--muted, #9aa3b2);
  font-size: 12px;
  letter-spacing: 2px;
  cursor: pointer;
  padding: 10px 0;
}
.pl-panel-tab:hover {
  color: var(--accent);
}
/* V2-F：临时提示 toast（画质待接线/全屏不可用等，8s 自动消失） */
.pl-toast {
  position: fixed;
  left: 50%;
  bottom: 84px;
  transform: translateX(-50%);
  z-index: 70;
  max-width: min(560px, calc(100vw - 48px));
  padding: 8px 14px;
  border: 1px solid var(--border);
  border-radius: var(--guo-radius-lg);
  background: var(--surface-3);
  color: var(--text);
  font-size: 12px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.35);
}
</style>
