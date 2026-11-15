import io
import os

base = os.path.dirname(os.path.abspath(__file__))          # .../client-v2
svc = os.path.join(base, 'frontend', 'src', 'services')


def rw(path, reps):
    s = io.open(path, encoding='utf-8').read()
    for old, new in reps:
        c = s.count(old)
        assert c == 1, (path, old[:56], c)
        s = s.replace(old, new)
    io.open(path, 'w', encoding='utf-8').write(s)
    print('OK', os.path.relpath(path, base))


# ① bridge：去掉我凭空写的 isMock 判断（分层里 mock 由 facade 决定）
rw(os.path.join(svc, 'bridge.ts'), [
    ('''  /* 逃生门：放弃内嵌、改用 mpv 独立窗口（App.PlayerForceWindow）。
     返回后调用方要重开当前集，mpv 才会自己建顶层窗。 */
  forceWindow: async (): Promise<boolean> => {
    if (isMock || !hasBridge) return false
    try {''',
     '''  /* 逃生门：放弃内嵌、改用 mpv 独立窗口（App.PlayerForceWindow）。
     返回后调用方要重开当前集，mpv 才会自己建顶层窗。
     ⚠ 这里**不判 isMock**：按本文件分层，mock/无桥的分支在 facade（api.ts）
     与 mock/embed.ts，桥面只管调桥。我第一版写了 `if (isMock || !hasBridge)`，
     而 `isMock` 在本文件并不存在——那是编译期就会拒的凭空标识符。 */
  forceWindow: async (): Promise<boolean> => {
    try {'''),
])

# ② mock/embed：补对应实现（no-op，返回 false）
rw(os.path.join(svc, 'mock', 'embed.ts'), [
    ('''  detach: async (): Promise<void> => {
    /* mock/无桥：no-op */
  }
}''',
     '''  detach: async (): Promise<void> => {
    /* mock/无桥：no-op */
  },
  forceWindow: async (): Promise<boolean> => {
    /* mock/无桥没有内嵌可放弃，返回 false 让调用方知道没生效 */
    return false
  }
}'''),
])

# ③ facade：路由
rw(os.path.join(svc, 'api.ts'), [
    ('''  detach: async (): Promise<void> => {
    if (isMock || !hasBridge) return
    return embedBridge.detach()
  }
}''',
     '''  detach: async (): Promise<void> => {
    if (isMock || !hasBridge) return
    return embedBridge.detach()
  },
  forceWindow: async (): Promise<boolean> => {
    if (isMock) return mockEmbedApi.forceWindow()
    if (!hasBridge) return false
    return embedBridge.forceWindow()
  }
}'''),
])

# ④ i18n 两条（中英成对）
rw(os.path.join(svc, 'i18n.ts'), [
    ("  'player.embedFallback': '画面未内嵌（独立窗口播放）：{reason}',",
     "  'player.embedFallback': '画面未内嵌（独立窗口播放）：{reason}',\n"
     "  'player.embedForceWindow': '改用独立窗口播放',"),
    ("  'player.embedFallback': 'Video not embedded (separate window): {reason}',",
     "  'player.embedFallback': 'Video not embedded (separate window): {reason}',\n"
     "  'player.embedForceWindow': 'Play in separate window',"),
])
