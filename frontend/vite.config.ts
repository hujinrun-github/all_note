import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'

const BACKEND_PORT = process.env.VITE_BACKEND_PORT || '8080'
const APP_BASE = process.env.VITE_APP_BASE || '/'
const appBasePath = APP_BASE === '/' ? '' : APP_BASE.replace(/\/$/, '')

export default defineConfig({
  base: APP_BASE,
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@': path.resolve(__dirname, 'src') },
  },
  preview: {
    allowedHosts: ['tylerhu-1.tail5cec87.ts.net'],
    proxy: {
      '/api': `http://localhost:${BACKEND_PORT}`,
      ...(appBasePath
        ? {
            [`${appBasePath}/api`]: {
              target: `http://localhost:${BACKEND_PORT}`,
              changeOrigin: true,
              rewrite: (requestPath) => requestPath.replace(`${appBasePath}/api`, '/api'),
            },
          }
        : {}),
    },
  },
  server: {
    allowedHosts: ['tylerhu-1.tail5cec87.ts.net'],
    proxy: {
      '/api': `http://localhost:${BACKEND_PORT}`,
      ...(appBasePath
        ? {
            [`${appBasePath}/api`]: {
              target: `http://localhost:${BACKEND_PORT}`,
              changeOrigin: true,
              rewrite: (requestPath) => requestPath.replace(`${appBasePath}/api`, '/api'),
            },
          }
        : {}),
    },
  },
})
