import { defineConfig, defaultExclude } from 'vitest/config'
import react from '@vitejs/plugin-react'
import { fileURLToPath } from 'node:url'

export default defineConfig({
  plugins: [react()],
  resolve: { alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) } },
  test: {
    environment: 'jsdom',
    globals: true,
    include: ['src/**/*.test.{ts,tsx}'],
    exclude: [...defaultExclude, 'tests/e2e/**'],
    maxWorkers: 1,
    setupFiles: './src/test/setup.ts',
  },
})
