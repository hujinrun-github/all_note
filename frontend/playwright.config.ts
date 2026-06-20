import { defineConfig, devices } from '@playwright/test'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const backendPort = 18180
const frontendPort = 15197
const configDir = path.dirname(fileURLToPath(import.meta.url))

export default defineConfig({
  testDir: './tests/e2e',
  timeout: 30_000,
  expect: { timeout: 8_000 },
  fullyParallel: false,
  use: {
    baseURL: `http://127.0.0.1:${frontendPort}`,
    trace: 'on-first-retry',
  },
  webServer: [
    {
      command: 'go run ./cmd/server',
      cwd: path.resolve(configDir, '../backend'),
      env: {
        ...process.env,
        PORT: String(backendPort),
        FLOWSPACE_ENV: 'test',
        FLOWSPACE_DB_PATH: '../.codex-run/playwright-roadmap.db',
        AI_PROVIDER: 'mock',
        ARTICLE_SEARCH_PROVIDER: 'mock',
        NOTION_PROVIDER: 'mock',
        FLOWSPACE_NOTION_TOKEN: 'mock-token',
      },
      url: `http://127.0.0.1:${backendPort}/api/task-projects`,
      reuseExistingServer: false,
      timeout: 60_000,
    },
    {
      command: `npm run dev -- --host 127.0.0.1 --port ${frontendPort}`,
      cwd: configDir,
      env: {
        ...process.env,
        VITE_BACKEND_PORT: String(backendPort),
      },
      url: `http://127.0.0.1:${frontendPort}/tasks`,
      reuseExistingServer: false,
      timeout: 60_000,
    },
  ],
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
    {
      name: 'mobile-chrome',
      use: { ...devices['Pixel 7'] },
    },
  ],
})
