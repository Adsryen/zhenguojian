import io
import os

base = os.path.dirname(os.path.abspath(__file__))


def rw(rel, reps):
    p = os.path.join(base, rel)
    s = io.open(p, encoding='utf-8').read()
    for old, new in reps:
        c = s.count(old)
        assert c == 1, (rel, old[:60], c)
        s = s.replace(old, new)
    io.open(p, 'w', encoding='utf-8').write(s)
    print('OK', rel)


# ── 1) Go：给一条"放弃内嵌、改用独立窗口"的逃生门 ──
rw('app.go', [
    ('''func marshalJSON(v any) (string, error) {''',
     '''// PlayerForceWindow 逃生门：把播放从"内嵌子窗"退回"mpv 独立窗口"。
//
// 为什么要它：真机连续两版反馈"有声音、画面全黑"，而 attach 已经不再报错
// （「画面未内嵌」徽标没出现），说明子窗建出来了、mpv 也拿到了 --wid，
// 问题只剩"画面没显示出来"这一层——可能是 z-order、可能是尺寸事件、
// 也可能是 mpv 的渲染上下文。这些我在本机一个都复现不了，
// 但用户不该在我查清之前一直看黑屏。
//
// 做法：藏掉子窗 + 把服务的 wid 归零（0 = 不内嵌，v1 `--wid` 缺省语义），
// 由前端重开当前集，mpv 就会自己开一个顶层窗。
// ⚠ 这只改"画面落在哪个窗"，不改播放/会话/弹幕任何语义。
func (a *App) PlayerForceWindow() (string, error) {
	a.embedMu.Lock()
	defer a.embedMu.Unlock()
	if a.embed != nil {
		a.embed.Hide()
	}
	a.playerSvc.SetWid(0)
	return marshalJSON(map[string]any{"ok": true, "embedded": false})
}

func marshalJSON(v any) (string, error) {'''),
])

# ── 2) 前端 embedApi：暴露逃生门 ──
rw('frontend/src/services/bridge.ts', [
    ('''  detach: async (): Promise<void> => {
    try {
      await callGo('PlayerDetach')
    } catch {
      /* 同上 */
    }
  }
}''',
     '''  detach: async (): Promise<void> => {
    try {
      await callGo('PlayerDetach')
    } catch {
      /* 同上 */
    }
  },
  /* 逃生门：内嵌黑屏时退回 mpv 独立窗口（App.PlayerForceWindow）。
     返回后调用方要重开当前集，mpv 才会自己建顶层窗。 */
  forceWindow: async (): Promise<boolean> => {
    if (isMock || !hasBridge) return false
    try {
      await callGo('PlayerForceWindow')
      return true
    } catch (err: any) {
      console.info('[embed] forceWindow 失败', String(err?.message ?? err))
      return false
    }
  }
}'''),
])

# ── 3) PlayerView：诊断默认显示 + 逃生门按钮 ──
rw('frontend/src/views/PlayerView.vue', [
    ("const embedDebugOn = localStorage.getItem('zgj.embedDebug') === '1'",
     """/* ⚠ 临时改成**默认开启**：连续两版要求用户手动开开关才能拿到现场，
   而用户回报的还是同一句"还是没画面"。查不清的原因是没有数据，
   所以这一版把诊断常驻显示（含一行"调试信息，查清后移除"），
   并附一个逃生门按钮——黑屏期间用户至少能看。
   TODO(内嵌黑屏查清后)：改回 localStorage 开关。 */
const embedDebugOn = true"""),
    ('''          >{{ embedDebugText }}</div>''',
     '''          >{{ embedDebugText }}
            <button
              class="pl-btn tiny"
              type="button"
              data-testid="embed-force-window"
              @click="forceWindowMode"
            >{{ t('player.embedForceWindow') }}</button></div>'''),
])

# ── 4) 处理函数 + i18n 文案 ──
rw('frontend/src/views/PlayerView.vue', [
    ('''/* 播放失败重试：重放整条 boot 链（解析超时/网络抖动后可直接再试一次）。 */
async function retryBoot() {''',
     '''/* 逃生门：放弃内嵌、改用 mpv 独立窗口，然后重开当前集。
   只改"画面落在哪个窗"，不改播放/会话/弹幕语义。 */
async function forceWindowMode() {
  const ok = await embedApi.forceWindow()
  if (!ok) return
  embedActive = false
  embedReason.value = 'forced-window'
  await store.bootFromQuery({ ...route.query, title: dramaTitleFromQuery() })
}

/* 播放失败重试：重放整条 boot 链（解析超时/网络抖动后可直接再试一次）。 */
async function retryBoot() {'''),
])

rw('services/i18n.ts', [
    ("  'player.embedFallback': '画面未内嵌（独立窗口播放）：{reason}',",
     "  'player.embedFallback': '画面未内嵌（独立窗口播放）：{reason}',\n"
     "  'player.embedForceWindow': '改用独立窗口播放',"),
    ("  'player.embedFallback': 'Video not embedded (separate window): {reason}',",
     "  'player.embedFallback': 'Video not embedded (separate window): {reason}',\n"
     "  'player.embedForceWindow': 'Play in separate window',"),
])
