/// <reference types="vite/client" />

declare module '*.vue' {
  import type { DefineComponent } from 'vue'
  const component: DefineComponent<{}, {}, any>
  export default component
}

interface Window {
  // Wails v2 运行时注入的 Go 绑定命名空间
  go?: {
    main?: {
      App?: Record<string, (...args: any[]) => Promise<string>>
    }
  }
}
