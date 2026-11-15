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


# ── 1) api.ts：AttachResult 带现场；attach/setBounds 都打到控制台 ──
rw('services/api.ts', [
    ('''export interface AttachResult {
  embedded: boolean
  reason?: string
}''',
     '''/** 内嵌现场（Go 侧 mpv.HostDebug）。真机「音频有、画面黑」只能靠它定位。 */
export interface EmbedHostDebug {
  supported?: boolean
  parentHwnd?: string
  parentClass?: string
  childHwnd?: string
  childVisible?: boolean
  childLeft?: number
  childTop?: number
  childWidth?: number
  childHeight?: number
  lastInX?: number
  lastInY?: number
  lastInW?: number
  lastInH?: number
  lastOutVisible?: boolean
  lastOutX?: number
  lastOutY?: number
  lastOutW?: number
  lastOutH?: number
}

export interface AttachResult {
  embedded: boolean
  reason?: string
  host?: EmbedHostDebug
}'''),
    ('''      return (await callGo<AttachResult>('PlayerAttach')) ?? { embedded: false }
    } catch (err: any) {
      return { embedded: false, reason: String(err?.message ?? err) }''',
     '''      const r = (await callGo<AttachResult>('PlayerAttach')) ?? { embedded: false }
      // 控制台留现场：排错时用户开 Ctrl+Shift+F12 就能复制给我，
      // 不需要我猜"是不是父窗选错/子窗没摆过"。
      console.info('[embed] attach', JSON.stringify(r))
      return r
    } catch (err: any) {
      const r = { embedded: false, reason: String(err?.message ?? err) }
      console.info('[embed] attach 失败', JSON.stringify(r))
      return r'''),
    ('''      await callGo('PlayerSetBounds', x, y, w, h)
    } catch {''',
     '''      const r = await callGo<{ ok: boolean; host?: EmbedHostDebug }>(
        'PlayerSetBounds', x, y, w, h)
      // 只报"有尺寸的那几次"，否则每帧一条把控制台刷满
      if (w > 1 && h > 1) console.info('[embed] setBounds', x, y, w, h, JSON.stringify(r?.host ?? null))
    } catch {'''),
])

# ── 2) PlayerView：attach 现场存下来，徽标可开（默认不打扰正常用户）──
rw('views/PlayerView.vue', [
    ("import { api, embedApi } from '@/services/api'",
     "import { api, embedApi, type EmbedHostDebug } from '@/services/api'"),
    ('''  embedReason.value = attach.embedded ? '' : attach.reason || 'unknown'
  if (embedActive) {''',
     '''  embedReason.value = attach.embedded ? '' : attach.reason || 'unknown'
  embedHost.value = attach.host ?? null
  if (embedActive) {'''),
    ('''const embedReason = ref('')''',
     '''const embedReason = ref('')
/* 内嵌现场（父窗类名/子窗矩形/可见性/最近一次换算）。
   默认不显示——正常用户不该看到调试信息；
   置 `localStorage['zgj.embedDebug']='1'` 后显示在视频区左上角，
   排错时截一张图就够，不用开 DevTools 复制。 */
const embedHost = ref<EmbedHostDebug | null>(null)
const embedDebugOn = typeof localStorage !== 'undefined'
  && localStorage.getItem('zgj.embedDebug') === '1''''),
])
