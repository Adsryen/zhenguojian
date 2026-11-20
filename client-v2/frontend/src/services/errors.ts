// errors.ts —— 错误 → 用户可读话术的统一映射（v3 可观测性，2026-11-20）。
//
// 背景：web 容器形态下用户看不到后端（那是开发者的 docker logs），前端 toast
// 以前直接透传 `e.message`——Go 侧的中文原文能看，但 WS 断连（「WS 连接已断开」）、
// 120s 超时（「PlayerState 超时(120s)」）、英文兜底（「not found」）这类
// 技术话术直接糊到用户脸上。这里集中做一层映射：
//   - 已知错误（ApiError.code + 高频消息指纹）→ i18n 友好话术；
//   - 未知错误 → 原文透出（不吞——排查时原文是唯一的证据），但调用方
//     把原文进 console，不让它独占 toast。
//
// 桌面形态同样受益（映射只改话术不改语义）；桌面专属的错误（Wails 桥、
// 剪贴板等）没有指纹命中，照旧原文透出，行为零变化。

import { t } from './i18n'

/** ApiError 的 code（contract.ts 剥 `[code]` 尾）→ 话术 key。 */
const CODE_KEYS: Record<string, string> = {
  bridge_missing: 'err.bridge_missing',
  local_missing: 'err.local_missing',
  outside_root: 'err.outside_root',
  source_blocked: 'err.source_blocked',
  discovery_unavailable: 'err.discovery_unavailable',
}

/** 消息指纹 → 话术 key（contains 匹配，按序第一个命中生效）。
    只收**高频且用户可行动**的；长尾错误保持原文透出。 */
const MESSAGE_KEYS: [RegExp, string][] = [
  [/WS 连接已断开|WS bridge disconnected/i, 'err.ws_down'],
  [/WS 桥未就绪|still connecting/i, 'err.ws_unready'],
  [/超时[（(]|timeout|timed ?out/i, 'err.timeout'],
  [/unauthorized|401|登录.*过期|session expired/i, 'err.unauthorized'],
  [/fetch failed|networkerror|network error|failed to fetch|load failed/i, 'err.network'],
  [/not found|404/i, 'err.not_found'],
  [/internal server error|500/i, 'err.server'],
  [/MEDIA_ERR|media error/i, 'err.play_failed'],
]

function codeOf(e: unknown): string {
  const c = (e as { code?: unknown })?.code
  return typeof c === 'string' ? c : ''
}

function messageOf(e: unknown): string {
  if (e instanceof Error) return e.message
  if (typeof e === 'string') return e
  try {
    return String((e as { message?: unknown })?.message ?? e ?? '')
  } catch {
    return String(e)
  }
}

/**
 * 把任意错误转成用户可读话术（永远返回非空字符串）。
 * `withReason=true` 时未知错误附带原文（toast 里给出可定位的细节）；
 * 已知映射不附（话术本身就是结论，原文进 console 即可）。
 */
export function friendlyError(e: unknown, withReason = true): string {
  const code = codeOf(e)
  if (code && CODE_KEYS[code]) return t(CODE_KEYS[code])
  const msg = messageOf(e)
  if (!msg) return t('err.generic')
  for (const [re, key] of MESSAGE_KEYS) {
    if (re.test(msg)) return t(key)
  }
  return withReason ? msg : t('err.generic')
}

/**
 * 错误是否「值得用户看见」——toast 分级用。
 * 成功类提示不走这里；错误语义的调用点用它决定是否出 error 变体。
 * （这里保持简单：能映射到话术或消息非空=可展示；空错误不出 toast。）
 */
export function hasErrorMessage(e: unknown): boolean {
  return messageOf(e).length > 0 || codeOf(e).length > 0
}
