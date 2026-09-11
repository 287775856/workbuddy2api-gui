import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// 构建产物直接落到 Go 的 embed 目录，`npm run build` 后 go build 即可打包。
// dev 模式下把 /api 代理到本地后端（默认 8787），前后端可分别热更新。
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
    // 单页应用，不需要 sourcemap 进产物（体积更小）。
    sourcemap: false,
    chunkSizeWarningLimit: 1200,
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: process.env.WBGUI_BACKEND || 'http://127.0.0.1:8787',
        changeOrigin: true,
      },
    },
  },
})
