import { defineConfig, type UserConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath } from 'node:url'

// https://vitejs.dev/config/
export default defineConfig(({ mode }) => {
  const base: UserConfig = {
    plugins: [vue()],
    resolve: {
      alias: {
        '@': fileURLToPath(new URL('./src', import.meta.url))
      }
    },
    // wails 打包产物路径固定为 frontend/dist
    build: {
      outDir: 'dist',
      emptyOutDir: true,
      chunkSizeWarningLimit: 1200
    },
    server: {
      host: '127.0.0.1',
      port: 5173,
      strictPort: true
    },
    clearScreen: false
  }
  // `npm run dev:mock` = 强制 VITE_MOCK=1，无需 .env 文件
  if (mode === 'mock') {
    base.define = { 'import.meta.env.VITE_MOCK': JSON.stringify('1') }
  } else if (mode === 'production' && process.env.VITE_MOCK !== '1') {
    // 发布构建锁死真实模式：防止误带 mock 资源出厂（显式 VITE_MOCK=1 仍可通过）
    base.define = { 'import.meta.env.VITE_MOCK': JSON.stringify('0') }
  }
  return base
})
