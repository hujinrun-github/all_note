import { defineConfig, loadEnv } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'

const TAILSCALE_ALLOWED_HOSTS = ['tylerhu-1.king-shiner.ts.net', 'tylerhu-1.tail5cec87.ts.net']

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  const BACKEND_HOST = env.VITE_BACKEND_HOST || 'localhost'
  const BACKEND_PORT = env.VITE_BACKEND_PORT || '4101'
  const BACKEND_TARGET = `http://${BACKEND_HOST}:${BACKEND_PORT}`
  const APP_BASE = env.VITE_APP_BASE || '/'
  const appBasePath = APP_BASE === '/' ? '' : APP_BASE.replace(/\/$/, '')

  return {
    base: APP_BASE,
    plugins: [react(), tailwindcss()],
    resolve: {
      alias: { '@': path.resolve(__dirname, 'src') },
    },
    preview: {
      allowedHosts: TAILSCALE_ALLOWED_HOSTS,
      proxy: {
        '/api': BACKEND_TARGET,
        ...(appBasePath
          ? {
              [`${appBasePath}/api`]: {
                target: BACKEND_TARGET,
                changeOrigin: true,
                rewrite: (requestPath) => requestPath.replace(`${appBasePath}/api`, '/api'),
              },
            }
          : {}),
      },
    },
    server: {
      allowedHosts: TAILSCALE_ALLOWED_HOSTS,
      proxy: {
        '/api': BACKEND_TARGET,
        ...(appBasePath
          ? {
              [`${appBasePath}/api`]: {
                target: BACKEND_TARGET,
                changeOrigin: true,
                rewrite: (requestPath) => requestPath.replace(`${appBasePath}/api`, '/api'),
              },
            }
          : {}),
      },
    },
  }
})
