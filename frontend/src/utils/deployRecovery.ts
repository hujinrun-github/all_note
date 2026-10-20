const PRELOAD_ERROR_EVENT = 'vite:preloadError'
const LAST_RELOAD_KEY = 'flowspace:last-preload-reload-at'
const RELOAD_COOLDOWN_MS = 30_000

type DeployRecoveryOptions = {
  target?: {
    addEventListener: (type: string, listener: EventListener) => void
    removeEventListener: (type: string, listener: EventListener) => void
  }
  storage?: Pick<Storage, 'getItem' | 'setItem'>
  reload?: () => void
  now?: () => number
}

export function installDeployRecovery(options: DeployRecoveryOptions = {}) {
  const target = options.target ?? window
  const storage = options.storage ?? window.sessionStorage
  const reload = options.reload ?? (() => window.location.reload())
  const now = options.now ?? Date.now

  const handlePreloadError = (event: Event) => {
    const currentTime = now()
    let lastReloadAt = 0

    try {
      lastReloadAt = Number(storage.getItem(LAST_RELOAD_KEY)) || 0
    } catch {
      // A restricted storage context must not prevent recovery.
    }

    if (currentTime - lastReloadAt < RELOAD_COOLDOWN_MS) return

    event.preventDefault()
    try {
      storage.setItem(LAST_RELOAD_KEY, String(currentTime))
    } catch {
      // Reloading still repairs stale chunks when session storage is unavailable.
    }
    reload()
  }

  target.addEventListener(PRELOAD_ERROR_EVENT, handlePreloadError)

  return () => {
    target.removeEventListener(PRELOAD_ERROR_EVENT, handlePreloadError)
  }
}
