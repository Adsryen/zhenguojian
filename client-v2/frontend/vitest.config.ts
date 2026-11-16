import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'

// vitest P0（第九十五轮）：纯逻辑单测——秒级、不依赖浏览器、不依赖 Go 桥。
// 与 verification/ 的 30 只 Playwright E2E 分工：E2E 管整链行为，vitest 管
// 纯函数/store 逻辑（回归最便宜、覆盖最容易变绿的那一层）。
// 用户 m08133 质问「自动化测试应该是 vitest 啊」→ 本脚手架即该欠账的落地。
export default defineConfig({
  plugins: [vue()],
  test: {
    environment: 'node',
    include: ['src/**/*.{test,spec}.ts'],
    testTimeout: 10000,
    // 纯逻辑层：不需要 DOM、不需要网络。一旦某测试想碰 window/fetch，
    // 这里会立刻红——提醒它属于 E2E 层（verification/），不该混进来。
    server: { deps: { inline: [] } }
  }
})
