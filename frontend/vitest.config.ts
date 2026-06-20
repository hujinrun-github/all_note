import { defineConfig, defaultExclude } from 'vitest/config'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    globals: true,
    include: ['src/**/*.test.{ts,tsx}'],
    exclude: [...defaultExclude, 'tests/e2e/**'],
    maxWorkers: 1,
    setupFiles: './src/test/setup.ts',
  },
})
