import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  // Relative addresses in the built files, so the same build works at the root
  // and under any URL_BASE prefix. The server rewrites the few files that need
  // an absolute address (internal/api/static_prefix.go).
  base: './',
  build: {
    outDir: 'dist',
  },
  server: {
    // Go backend during `npm run dev` (the UI talks to the same REST
    // API the production single binary serves itself in prod).
    proxy: {
      '/api': {
        target: process.env.API_TARGET ?? 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
})
