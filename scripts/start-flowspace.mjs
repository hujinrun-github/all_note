#!/usr/bin/env node

import { spawn } from 'node:child_process'
import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import process from 'node:process'
import { fileURLToPath } from 'node:url'

const rootDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const isWindows = process.platform === 'win32'

loadLocalEnvFile(path.join(rootDir, '.codex-run', 'flowspace-ai.env'))
loadLocalEnvFile(path.join(rootDir, '.codex-run', 'flowspace.local.env'))

const defaults = {
  env: process.env.FLOWSPACE_ENV || 'prod',
  dbPath: process.env.FLOWSPACE_DB_PATH || '',
  backendPort: process.env.PORT || '8080',
  frontendPort: process.env.FRONTEND_PORT || '5199',
  frontendBase: process.env.VITE_APP_BASE || '',
  backendCmd: process.env.FLOWSPACE_BACKEND_CMD || 'go run ./cmd/server',
  frontendCmd: process.env.FLOWSPACE_FRONTEND_CMD || '',
  killPorts: true,
  killOnly: false,
  backendOnly: false,
  frontendOnly: false,
}

const options = parseArgs(process.argv.slice(2), defaults)
if (options.help) {
  printHelp()
  process.exit(0)
}

if (!options.frontendCmd) {
  options.frontendCmd = `npm run dev -- --host 127.0.0.1 --port ${options.frontendPort}`
}

const backendDir = path.join(rootDir, 'backend')
const frontendDir = path.join(rootDir, 'frontend')
const children = new Set()
let shuttingDown = false

main().catch((error) => {
  console.error(`[flowspace] ${error instanceof Error ? error.message : String(error)}`)
  shutdown(1)
})

async function main() {
  ensureDirectory(backendDir)
  ensureDirectory(frontendDir)

  const portsToKill = []
  if (options.killPorts && !options.frontendOnly) portsToKill.push(options.backendPort)
  if (options.killPorts && !options.backendOnly) portsToKill.push(options.frontendPort)

  for (const port of portsToKill) {
    await killPort(port)
  }

  if (options.killOnly) {
    console.log('[flowspace] configured ports released')
    return
  }

  console.log('[flowspace] starting services')
  console.log(`[flowspace] env=${options.env}`)
  console.log(`[flowspace] db=${options.dbPath || defaultDBForEnv(options.env)}`)
  console.log(`[flowspace] backend port=${options.backendPort}`)
  console.log(`[flowspace] frontend port=${options.frontendPort}`)
  if (options.frontendBase) {
    console.log(`[flowspace] frontend base=${options.frontendBase}`)
  }

  if (!options.frontendOnly) {
    startProcess({
      name: 'backend',
      command: options.backendCmd,
      cwd: backendDir,
      env: {
        PORT: options.backendPort,
        FLOWSPACE_ENV: options.env,
        ...(options.dbPath ? { FLOWSPACE_DB_PATH: options.dbPath } : {}),
      },
    })
  }

  if (!options.backendOnly) {
    startProcess({
      name: 'frontend',
      command: options.frontendCmd,
      cwd: frontendDir,
      env: {
        VITE_BACKEND_PORT: options.backendPort,
        ...(options.frontendBase ? { VITE_APP_BASE: options.frontendBase } : {}),
      },
    })
  }

  console.log('[flowspace] press Ctrl+C to stop')
}

function parseArgs(args, base) {
  const parsed = { ...base }

  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index]
    const next = () => {
      index += 1
      if (index >= args.length) throw new Error(`Missing value for ${arg}`)
      return args[index]
    }

    switch (arg) {
      case '--help':
      case '-h':
        parsed.help = true
        break
      case '--env':
        parsed.env = next()
        break
      case '--db':
      case '--db-path':
        parsed.dbPath = next()
        break
      case '--backend-port':
        parsed.backendPort = next()
        break
      case '--frontend-port':
        parsed.frontendPort = next()
        break
      case '--frontend-base':
      case '--app-base':
        parsed.frontendBase = normalizeBasePath(next())
        break
      case '--backend-cmd':
      case '--backend-script':
        parsed.backendCmd = next()
        break
      case '--frontend-cmd':
      case '--frontend-script':
        parsed.frontendCmd = next()
        break
      case '--backend-only':
        parsed.backendOnly = true
        break
      case '--frontend-only':
        parsed.frontendOnly = true
        break
      case '--no-kill':
        parsed.killPorts = false
        break
      case '--kill-only':
        parsed.killOnly = true
        break
      default:
        throw new Error(`Unknown option: ${arg}`)
    }
  }

  if (parsed.backendOnly && parsed.frontendOnly) {
    throw new Error('Use only one of --backend-only or --frontend-only')
  }

  parsed.env = normalizeEnv(parsed.env)
  parsed.backendPort = normalizePort(parsed.backendPort, 'backend port')
  parsed.frontendPort = normalizePort(parsed.frontendPort, 'frontend port')
  parsed.frontendBase = parsed.frontendBase ? normalizeBasePath(parsed.frontendBase) : ''
  return parsed
}

function normalizeEnv(value) {
  const normalized = String(value || '').trim().toLowerCase()
  if (normalized === 'test' || normalized === 'testing') return 'test'
  return 'prod'
}

function defaultDBForEnv(env) {
  return env === 'test' ? 'flowspace.test.db' : 'flowspace.db'
}

function normalizeBasePath(value) {
  const raw = String(value || '').trim()
  if (!raw || raw === '/') return '/'
  const withLeadingSlash = raw.startsWith('/') ? raw : `/${raw}`
  return withLeadingSlash.endsWith('/') ? withLeadingSlash : `${withLeadingSlash}/`
}

function normalizePort(value, label) {
  const normalized = String(value || '').trim()
  if (!/^\d+$/.test(normalized)) {
    throw new Error(`Invalid ${label}: ${value}`)
  }

  const port = Number(normalized)
  if (port < 1 || port > 65535) {
    throw new Error(`Invalid ${label}: ${value}`)
  }

  return normalized
}

function ensureDirectory(dir) {
  if (!existsSync(dir)) {
    throw new Error(`Directory does not exist: ${dir}`)
  }
}

function loadLocalEnvFile(filePath) {
  if (!existsSync(filePath)) return
  const content = readFileSync(filePath, 'utf8')
  for (const rawLine of content.split(/\r?\n/)) {
    const line = rawLine.trim()
    if (!line || line.startsWith('#')) continue
    const match = line.match(/^([A-Za-z_][A-Za-z0-9_]*)=(.*)$/)
    if (!match) continue

    const key = match[1]
    if (process.env[key] !== undefined) continue
    process.env[key] = unquoteEnvValue(match[2].trim())
  }
}

function unquoteEnvValue(value) {
  if (
    (value.startsWith('"') && value.endsWith('"')) ||
    (value.startsWith("'") && value.endsWith("'"))
  ) {
    return value.slice(1, -1)
  }
  return value
}

function startProcess({ name, command, cwd, env }) {
  console.log(`[flowspace] ${name}: ${command}`)
  const child = spawn(command, {
    cwd,
    env: { ...process.env, ...env },
    shell: true,
    stdio: ['ignore', 'pipe', 'pipe'],
    windowsHide: true,
    detached: !isWindows,
  })

  children.add(child)
  prefixStream(child.stdout, name)
  prefixStream(child.stderr, name)

  child.on('exit', (code, signal) => {
    children.delete(child)
    if (!shuttingDown) {
      console.error(`[flowspace] ${name} exited code=${code ?? 'null'} signal=${signal ?? 'null'}`)
      shutdown(code || 1)
    }
  })
}

function prefixStream(stream, name) {
  let buffer = ''
  stream.on('data', (chunk) => {
    buffer += chunk.toString()
    const lines = buffer.split(/\r?\n/)
    buffer = lines.pop() || ''
    for (const line of lines) {
      if (line.trim()) console.log(`[${name}] ${line}`)
    }
  })
  stream.on('end', () => {
    if (buffer.trim()) console.log(`[${name}] ${buffer}`)
  })
}

async function killPort(port) {
  if (!port) return
  console.log(`[flowspace] releasing port ${port}`)
  if (isWindows) {
    const command = [
      '$connections = Get-NetTCPConnection -LocalPort',
      escapePowerShellString(port),
      '-ErrorAction SilentlyContinue;',
      '$connections | Where-Object { $_.State -eq "Listen" } |',
      'Select-Object -ExpandProperty OwningProcess -Unique |',
      'ForEach-Object { Stop-Process -Id $_ -Force -ErrorAction SilentlyContinue }',
    ].join(' ')
    await runOneShot('powershell.exe', ['-NoProfile', '-Command', command])
  } else {
    const command = [
      `pids=""`,
      `if command -v lsof >/dev/null 2>&1; then pids="$(lsof -tiTCP:${port} -sTCP:LISTEN 2>/dev/null || true)"; fi`,
      `if [ -z "$pids" ] && command -v fuser >/dev/null 2>&1; then pids="$(fuser -n tcp ${port} 2>/dev/null || true)"; fi`,
      `if [ -n "$pids" ]; then kill -9 $pids 2>/dev/null || true; fi`,
    ].join('; ')
    await runOneShot('sh', ['-lc', command])
  }
}

function runOneShot(command, args) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { stdio: 'ignore', windowsHide: true })
    child.on('error', reject)
    child.on('exit', (code) => {
      if (code && code !== 0) {
        reject(new Error(`${command} exited with code ${code}`))
      } else {
        resolve()
      }
    })
  })
}

function escapePowerShellString(value) {
  return `'${String(value).replaceAll("'", "''")}'`
}

function shutdown(code = 0) {
  if (shuttingDown) return
  shuttingDown = true
  const stops = [...children].map(stopChild)
  Promise.allSettled(stops).finally(() => process.exit(code))
}

function stopChild(child) {
  return new Promise((resolve) => {
    if (child.exitCode !== null || child.killed) {
      resolve()
      return
    }
    if (isWindows) {
      const stopper = spawn('taskkill.exe', ['/PID', String(child.pid), '/T', '/F'], {
        stdio: 'ignore',
        windowsHide: true,
      })
      stopper.on('exit', () => resolve())
      stopper.on('error', () => resolve())
    } else {
      try {
        process.kill(-child.pid, 'SIGTERM')
      } catch {
        child.kill('SIGTERM')
      }
      setTimeout(resolve, 500)
    }
  })
}

function printHelp() {
  console.log(`Usage: node scripts/start-flowspace.mjs [options]

Options:
  --env <prod|test>              Storage environment (default: prod)
  --db, --db-path <path>         Explicit SQLite DB path; overrides --env default
  --backend-port <port>          Backend port (default: 8080)
  --frontend-port <port>         Frontend port (default: 5199)
  --frontend-base <path>         Frontend base path, e.g. /all-note-test/
  --backend-cmd <command>        Backend startup command (default: go run ./cmd/server)
  --frontend-cmd <command>       Frontend startup command
  --backend-only                 Start only the backend
  --frontend-only                Start only the frontend
  --no-kill                      Do not release configured ports before startup
  --kill-only                    Release configured ports and exit
  -h, --help                     Show this help

Environment variables:
  FLOWSPACE_ENV                  Same as --env
  FLOWSPACE_DB_PATH              Same as --db
  PORT                           Same as --backend-port
  FRONTEND_PORT                  Same as --frontend-port
  VITE_APP_BASE                  Same as --frontend-base
  FLOWSPACE_BACKEND_CMD          Same as --backend-cmd
  FLOWSPACE_FRONTEND_CMD         Same as --frontend-cmd
  AI_PROVIDER                    AI roadmap provider, defaults to deepseek in the backend
  AI_BASE_URL                    OpenAI-compatible base URL
  AI_MODEL                       OpenAI-compatible model name
  AI_API_KEY                     API key for the configured provider

Local env files:
  .codex-run/flowspace-ai.env    Optional ignored AI provider secrets
  .codex-run/flowspace.local.env Optional ignored local overrides

Examples:
  node scripts/start-flowspace.mjs --env prod
  node scripts/start-flowspace.mjs --env test
  node scripts/start-flowspace.mjs --db tmp/sandbox.db
  node scripts/start-flowspace.mjs --env test --frontend-port 15198 --frontend-base /all-note-test/ --frontend-only
  node scripts/start-flowspace.mjs --backend-cmd "go run ./cmd/server" --frontend-cmd "npm run dev -- --host 127.0.0.1 --port 5199"
`)
}

process.on('SIGINT', () => shutdown(0))
process.on('SIGTERM', () => shutdown(0))
