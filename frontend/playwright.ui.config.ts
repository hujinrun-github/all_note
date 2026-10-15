import { defineConfig, devices } from '@playwright/test'

// Isolated UI checks: every API request is fulfilled by v2 fixtures in the spec.
// No backend or workspace database is started or modified.
export default defineConfig({
  testDir: './tests/ui',
  timeout: 30_000,
  workers: 2,
  use: {
    baseURL: 'http://127.0.0.1:4100',
    timezoneId: 'Asia/Shanghai',
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
  },
  webServer: {
    command: 'npm run dev -- --host 127.0.0.1 --port 4100 --strictPort',
    env: { VITE_BACKEND_HOST: '127.0.0.1', VITE_BACKEND_PORT: '4101' },
    url: 'http://127.0.0.1:4100',
    reuseExistingServer: false,
  },
  projects: [
    {
      name: 'desktop',
      use: {
        ...devices['Desktop Chrome'],
        viewport: { width: 1440, height: 1000 },
      },
    },
    { name: 'android', use: { ...devices['Pixel 7'] } },
    { name: 'iphone', use: { ...devices['iPhone 13'] } },
  ],
})
