import { spawn } from 'node:child_process'
import { fileURLToPath } from 'node:url'

const disableNodeWebStorage = '--no-experimental-webstorage'
const existingNodeOptions = process.env.NODE_OPTIONS?.trim() ?? ''
const nodeOptions = existingNodeOptions.includes(disableNodeWebStorage)
  ? existingNodeOptions
  : [existingNodeOptions, disableNodeWebStorage].filter(Boolean).join(' ')
const vitestEntry = fileURLToPath(
  new URL('../node_modules/vitest/vitest.mjs', import.meta.url)
)
const child = spawn(
  process.execPath,
  [vitestEntry, 'run', ...process.argv.slice(2)],
  {
    stdio: 'inherit',
    env: { ...process.env, NODE_OPTIONS: nodeOptions },
  }
)

for (const signal of ['SIGINT', 'SIGTERM']) {
  process.on(signal, () => child.kill(signal))
}

child.on('error', (error) => {
  console.error(error)
  process.exit(1)
})

child.on('exit', (code) => process.exit(code ?? 1))
