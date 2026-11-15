/* ── 青空次元账号 mock 面素材与四方法（字段形状对齐 provider_sorani_account.go 真实应答；
   图为假 SVG data URL，尺寸与真实一致：背景 600×360、模板 110×360）。
   ⚠ 只存在于 mock：登录只校验 track 形状即成功；下列注入缝
     __zgj.mockLoginFail / mockCaptchaFail / mockStatusFail / mockSoraniNoName / mockCaptchaSize
     与观测缝 __zgjSoraniCalls / __zgjSoraniLastLogin（真实桥无这些键）。 */
import type { NativeApi } from '../types'
import { ApiError } from '../contract'
import { t } from '../i18n'
import { delay } from './util'

const soraniMock = { loggedIn: false, name: '', seq: 0 }
const svgDataURL = (svg: string) => 'data:image/svg+xml;utf8,' + encodeURIComponent(svg)
const MOCK_SORANI_BG = svgDataURL(
  '<svg xmlns="http://www.w3.org/2000/svg" width="600" height="360">' +
    '<rect width="600" height="360" fill="#8b93a1"/>' +
    '<rect x="48" y="70" width="240" height="16" rx="8" fill="#7b8392"/>' +
    '<rect x="48" y="140" width="150" height="16" rx="8" fill="#7b8392"/>' +
    '<rect x="300" y="250" width="220" height="16" rx="8" fill="#7b8392"/>' +
    '<rect x="372" y="96" width="110" height="110" rx="12" fill="#5b6472"/>' +
    '<text x="14" y="344" font-size="18" fill="#6b7482">MOCK SORANI CAPTCHA 600x360</text>' +
    '</svg>'
)
const MOCK_SORANI_TPL = svgDataURL(
  '<svg xmlns="http://www.w3.org/2000/svg" width="110" height="360">' +
    '<rect x="0" y="96" width="110" height="110" rx="12" fill="#3f8cff" stroke="#2f6fd0" stroke-width="2"/>' +
    '<text x="10" y="230" font-size="20" fill="#3f8cff">滑块</text>' +
    '</svg>'
)

export const mockSoraniApi = {
  // ── 青空次元账号 mock：login 只校验 track 形状（首 down 尾 up、≥2 点、尺寸/时间自洽）即成功；
  //    window.__zgj.mockLoginFail='1' 强制失败（M5 断言用）；入参快照落 __zgjSoraniLastLogin（M8 断言用）
  //
  //    第五十六轮再加三个注入缝 + 一个调用计数（都只对 mock 生效，默认行为一字不变）：
  //      __zgj.mockCaptchaFail='1'   取验证码失败（v1 :328 那条：回主页 + 红字）
  //      __zgj.mockStatusFail='1'    读状态失败（v1 :404 那条：徽标「未知」而不是谎报未登录）
  //      __zgj.mockSoraniNoName='1'  状态里没有 name（v1 :411「只有账号的核心」那一族：
  //                                  徽标该是「已登录」，不能拼出 "已登录 · undefined"）
  //      __zgjSoraniCalls            三次调用的次数（login/captcha/status），
  //                                  v1 :321 要断言"本地拦下、根本没打核心"，
  //                                  没有计数就只能靠时间差猜。──
  soraniStatus: async () => {
    await delay(80)
    const w = typeof window !== 'undefined' ? (window as any) : null
    if (w) w.__zgjSoraniCalls = { ...(w.__zgjSoraniCalls || {}), status: (w.__zgjSoraniCalls?.status || 0) + 1 }
    if (w?.__zgj?.mockStatusFail === '1' || w?.mockStatusFail === '1') {
      // 同上：ASCII，避免英文界面露中文
      throw new ApiError('mock sorani status read failed', 'status_fail')
    }
    const name = (w?.__zgj?.mockSoraniNoName === '1' || w?.mockSoraniNoName === '1') ? '' : soraniMock.name
    return { loggedIn: soraniMock.loggedIn, name }
  },
  soraniCaptcha: async () => {
    await delay(150)
    const w = typeof window !== 'undefined' ? (window as any) : null
    if (w) w.__zgjSoraniCalls = { ...(w.__zgjSoraniCalls || {}), captcha: (w.__zgjSoraniCalls?.captcha || 0) + 1 }
    if (w?.__zgj?.mockCaptchaFail === '1' || w?.mockCaptchaFail === '1') {
      // ASCII：这条会作为 {msg} 嵌进 sorani.captchaFail 的句子，
      // 中文会在英文界面里露出来（第 42 轮立的规矩）
      throw new ApiError('mock captcha image fetch failed', 'captcha_fail')
    }
    soraniMock.seq += 1
    // ⚠ 第五十六轮我写下"真正还没覆盖的只剩 :188 的变尺寸换算"，
    // 因为这里把 600/360/110/360 写死了 —— v1 `test_display_geometry`
    // 测的正是"显示空间与原图空间是两套数"。现在给一个尺寸注入缝（默认不变）：
    //   __zgj.mockCaptchaSize = [背景宽, 背景高, 模板宽, 模板高]
    const cs = (w?.__zgj?.mockCaptchaSize || (w as any)?.mockCaptchaSize) as
      | [number, number, number, number] | undefined
    const [bgW, bgH, tplW, tplH] = cs && cs.length === 4 ? cs : [600, 360, 110, 360]
    return {
      id: `mock-captcha-${soraniMock.seq}`,
      backgroundImage: MOCK_SORANI_BG,
      templateImage: MOCK_SORANI_TPL,
      backgroundWidth: bgW,
      backgroundHeight: bgH,
      templateWidth: tplW,
      templateHeight: tplH
    }
  },
  soraniLogin: async (identifier, password, captchaId, track) => {
    await delay(220)
    if (typeof window !== 'undefined') {
      window.__zgjSoraniLastLogin = { identifier, password, captchaId, track }
      const w = window as any
      w.__zgjSoraniCalls = { ...(w.__zgjSoraniCalls || {}), login: (w.__zgjSoraniCalls?.login || 0) + 1 }
    }
    if (!identifier.trim() || !password) throw new ApiError(t('api.errLoginInput'), 'bad_input')
    const pts = track.trackList ?? []
    const dims = [track.bgImageWidth, track.bgImageHeight, track.templateImageWidth, track.templateImageHeight]
    const trackOk =
      dims.every((n) => n > 0 && n <= 4000) &&
      pts.length >= 2 &&
      pts.length <= 5000 &&
      pts[0].type === 'down' &&
      pts[pts.length - 1].type === 'up' &&
      track.startTime > 0 &&
      track.stopTime >= track.startTime
    if (!trackOk) throw new ApiError(t('api.errCaptcha'), 'sorani_captcha_fail')
    if (typeof window !== 'undefined' && window.__zgj?.mockLoginFail === '1')
      throw new ApiError(t('api.errCaptcha'), 'sorani_captcha_fail')
    soraniMock.loggedIn = true
    soraniMock.name = identifier.trim()
    // ⚠ 第五十六轮：v1 :411 的 `AccountOnlyCore` 是"核心回的状态里没有 name"，
    // 而 v2 的徽标数据源有两条：`soraniStatus`（读）与 `soraniLogin`（登录后立刻回的那份）。
    // 上一版我只在 status 上打了 noName 缝，所以 M18 测不到 —— 徽标用的是 login 那份。
    // 现在两条都认这个缝（默认行为不变：不设缝时 name 照旧）。
    const noName = (window as any)?.__zgj?.mockSoraniNoName === '1' || (window as any)?.mockSoraniNoName === '1'
    return { loggedIn: true, name: noName ? '' : identifier.trim() }
  },
  soraniLogout: async () => {
    await delay(120)
    soraniMock.loggedIn = false
    soraniMock.name = ''
    return { loggedIn: false, name: '' }
  }
} satisfies Pick<NativeApi, 'soraniStatus' | 'soraniCaptcha' | 'soraniLogin' | 'soraniLogout'>
