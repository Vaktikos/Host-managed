import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The dev server proxies the API (including WebSockets) to a local
// control plane started with insecure_http = true.
export default defineConfig({
  plugins: [react()],
  build: { outDir: 'dist', sourcemap: false, chunkSizeWarningLimit: 900 },
  server: {
    proxy: {
      '/api': { target: 'http://127.0.0.1:18443', ws: true },
    },
  },
})
