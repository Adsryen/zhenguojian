/**
 * contract.ts + bridge.ts P0 单测（第九十五轮）。
 * 错误码约定（`message [code]` 尾巴剥码）是 alpha.40 真机诊断里反复用到的
 * 唯一协议——UI 靠 ApiError.code 组句，错一个字用户看到的就是一句
 * 说不通的话。这条剥码逻辑全仓只有一处（bridge.ts:68），必须秒级可回归。
 * 纯 node 环境：不碰真 window，用 globalThis 造假 wailsApp 桩。
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { ApiError, clampOpacityPercent } from './contract'
import { callGoRaw, callGo } from './bridge'

describe('ApiError', () => {
  it('code 默认空串、message 原样', () => {
    const e = new ApiError('boom')
    expect(e.code).toBe('')
    expect(e.message).toBe('boom')
    expect(e).toBeInstanceOf(Error)
  })
  it('带 code 与 retryAt', () => {
    const e = new ApiError('src blocked', 'source_blocked', 1700000000)
    expect(e.code).toBe('source_blocked')
    expect(e.retryAt).toBe(1700000000)
  })
})

describe('clampOpacityPercent（v1 tools.danmaku_opacity：20–100，非法回落 100）', () => {
  it('区间内四舍五入', () => {
    expect(clampOpacityPercent(50)).toBe(50)
    expect(clampOpacityPercent(50.4)).toBe(50)
    expect(clampOpacityPercent(50.5)).toBe(51)
  })
  it('越界钳到 20/100', () => {
    expect(clampOpacityPercent(5)).toBe(20)
    expect(clampOpacityPercent(200)).toBe(100)
    expect(clampOpacityPercent(20)).toBe(20)
    expect(clampOpacityPercent(100)).toBe(100)
  })
  it('非有限值回落 100', () => {
    expect(clampOpacityPercent(NaN)).toBe(100)
    expect(clampOpacityPercent(Infinity)).toBe(100)
    expect(clampOpacityPercent(-Infinity)).toBe(100)
  })
})

/* ── 假 Wails 桥：wailsApp() 在调用时才读 window.go.main.App，所以桩在
   beforeEach 里装、afterEach 里拆，模块加载时（node 无 window）不受影响。── */
type FakeMethod = (...args: any[]) => any
function installFakeApp(methods: Record<string, FakeMethod>) {
  ;(globalThis as any).window = { go: { main: { App: methods } } }
}
function uninstallFakeApp() {
  delete (globalThis as any).window
}

describe('bridge 错误码约定（全仓唯一剥码处 bridge.ts:68）', () => {
  beforeEach(() => uninstallFakeApp())
  afterEach(() => uninstallFakeApp())

  it('Go 错误 `msg [code]` 尾巴 → ApiError{message 去尾, code}', async () => {
    installFakeApp({ Foo: () => { throw new Error('源被屏蔽 [source_blocked]') } })
    await expect(callGoRaw('Foo')).rejects.toMatchObject({
      name: 'Error',
      message: '源被屏蔽',
      code: 'source_blocked',
    })
  })

  it('错误尾带空白（`msg [code]  `）照样剥净', async () => {
    installFakeApp({ Foo: () => { throw new Error('boom [discovery_unavailable]  ') } })
    await expect(callGoRaw('Foo')).rejects.toMatchObject({
      message: 'boom',
      code: 'discovery_unavailable',
    })
  })

  it('无 code 尾巴 → message 原样、code 空串（不谎报）', async () => {
    installFakeApp({ Foo: () => { throw new Error('plain failure') } })
    const err = await callGoRaw('Foo').catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err.message).toBe('plain failure')
    expect(err.code).toBe('')
  })

  it('Go 侧传字符串错误（非 Error 对象）也能剥码', async () => {
    installFakeApp({ Foo: () => { throw 'string err [upstream_timeout]' } })
    await expect(callGoRaw('Foo')).rejects.toMatchObject({
      message: 'string err',
      code: 'upstream_timeout',
    })
  })

  it('方法不存在 → bridge_missing（alpha.40 version=? 的那类指纹）', async () => {
    installFakeApp({})
    await expect(callGoRaw('NoSuchMethod')).rejects.toMatchObject({ code: 'bridge_missing' })
  })

  it('字符串返回原样透传（Ping/DataPath 这类非 JSON 值）', async () => {
    installFakeApp({ Ping: () => 'pong' })
    await expect(callGoRaw('Ping')).resolves.toBe('pong')
  })

  it('非字符串返回 JSON.stringify（Wails 自动解包后的对象）', async () => {
    installFakeApp({ Foo: () => ({ a: 1 }) })
    await expect(callGoRaw('Foo')).resolves.toBe('{"a":1}')
  })
})

describe('bridge.callGo（JSON 解析层）', () => {
  afterEach(() => uninstallFakeApp())

  it('空串 → undefined（Go marshalJSON 的空返回约定）', async () => {
    installFakeApp({ Foo: () => '' })
    await expect(callGo('Foo')).resolves.toBeUndefined()
  })

  it('JSON 字符串解析成对象', async () => {
    installFakeApp({ Foo: () => '{"ok":true}' })
    await expect(callGo('Foo')).resolves.toEqual({ ok: true })
  })

  it('坏 JSON → 抛 SyntaxError（不吞）', async () => {
    installFakeApp({ Foo: () => '{bad' })
    await expect(callGo('Foo')).rejects.toThrow()
  })
})

describe('bridge hasBridge 探测（模块加载时读 window）', () => {
  it('node 环境（无 window）下模块可安全导入', () => {
    // 本文件 import bridge 时 window 不存在——若模块顶层裸读 window 会在此红。
    expect(typeof callGoRaw).toBe('function')
  })
})
