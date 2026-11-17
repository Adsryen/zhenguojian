import { defineConfig, type UserConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath } from 'node:url'

// https://vitejs.dev/config/
export default defineConfig(({ mode }) => {
  const cfg: UserConfig = {
    // 相对 base：dist 产物以 file:// 直接加载（Electron 壳 loadFile）。
    // './' 让 index.html 引用 ./assets/...（相对文档 URL，file:// 下成立）；
    // Wails 壳从 assetserver 根伺服时 './assets' 同样解析到 /assets，行为不变。
    // （vite dev 服务器忽略相对 base 并回退 '/'，dev:mock/E2E 不受影响。）
    base: './',
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
    cfg.define = { 'import.meta.env.VITE_MOCK': JSON.stringify('1') }
  } else if (mode === 'production' && process.env.VITE_MOCK !== '1') {
    // 发布构建锁死真实模式：防止误带 mock 资源出厂（显式 VITE_MOCK=1 仍可通过）
    cfg.define = { 'import.meta.env.VITE_MOCK': JSON.stringify('0') }
  }
  return cfg
})
