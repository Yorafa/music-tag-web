import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'path'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8005',
        changeOrigin: true,
      },
    },
  },
  base: '/static/dist/',
  build: {
    outDir: '../static/dist',
    emptyOutDir: true,
    // This is an internal tool — the main vendor bundle (react + ui kit +
    // axios + tailwind runtime) consistently lands around ~700 kB. Bump the
    // warning ceiling so the build doesn't print a noisy chunk-size advisory
    // every time. Code-splitting is the proper answer if size ever becomes a
    // concern; for now we just want a clean build log.
    chunkSizeWarningLimit: 1024,
  },
})
