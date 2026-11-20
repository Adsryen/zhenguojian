// errors.ts 单测（v3 可观测性）：错误映射是唯一把「技术话术」换成
// 「用户话术」的地方——映射错一个字，用户看到的就是说不通的话。
// 纯 node 环境：i18n 的 t() 默认 locale=zh（localStorage 缺失回落 zh），
// 不碰 window/DOM。
import { describe, it, expect } from 'vitest'
import { ApiError } from './contract'
import { friendlyError, hasErrorMessage } from './errors'

describe('friendlyError', () => {
  it('ApiError.code 优先于消息指纹（source_blocked）', () => {
    const e = new ApiError('not found [source_blocked]', 'source_blocked')
    // 消息里有 "not found" 但 code 命中应走 source_blocked 话术
    expect(friendlyError(e)).toBe('该站源被限流，请稍后再试')
  })

  it('local_missing / outside_root code 映射', () => {
    expect(friendlyError(new ApiError('本地文件已不存在：/x.mp4', 'local_missing')))
      .toBe('本地文件已不存在（可能被清理），请重新下载')
    expect(friendlyError(new ApiError('越界', 'outside_root')))
      .toBe('无法访问该文件（不在允许的目录内）')
  })

  it('WS 断连/未就绪/超时 → 友好话术（不再糊「WS 连接已断开」原文）', () => {
    expect(friendlyError(new Error('WS 连接已断开'))).toBe('与服务器的连接已断开，请刷新页面重试')
    expect(friendlyError(new Error('WS 桥未就绪'))).toBe('服务器连接尚未就绪，请稍后再试')
    expect(friendlyError(new Error('PlayerState 超时(120s)'))).toBe('请求超时，请重试')
  })

  it('英文常见错误（fetch failed / not found / 401）→ 中文话术', () => {
    expect(friendlyError(new Error('Failed to fetch'))).toBe('网络异常，请检查连接后重试')
    expect(friendlyError(new Error('404 not found'))).toBe('资源不存在或已下线')
    expect(friendlyError(new Error('unauthorized'))).toBe('登录已过期，请重新登录')
  })

  it('未知错误 → 原文透出（不吞，排查证据保留）', () => {
    expect(friendlyError(new Error('合并剧集清单无效: xxx'))).toBe('合并剧集清单无效: xxx')
  })

  it('空错误 → 通用话术；空值不抛', () => {
    expect(friendlyError(null)).toBe('操作失败，请重试')
    expect(friendlyError(undefined)).toBe('操作失败，请重试')
    expect(friendlyError('')).toBe('操作失败，请重试')
  })

  it('普通对象（带 message 字段）也能取原文', () => {
    expect(friendlyError({ message: '封面请求超时（60s）' })).toBe('请求超时，请重试')
  })
})

describe('hasErrorMessage', () => {
  it('空错误=false（不出 toast）', () => {
    expect(hasErrorMessage(null)).toBe(false)
    expect(hasErrorMessage('')).toBe(false)
  })
  it('有消息或有 code=true', () => {
    expect(hasErrorMessage(new Error('boom'))).toBe(true)
    expect(hasErrorMessage(new ApiError('', 'local_missing'))).toBe(true)
  })
})
