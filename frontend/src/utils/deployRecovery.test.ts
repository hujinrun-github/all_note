import { describe, expect, it, vi } from 'vitest'

import { installDeployRecovery } from './deployRecovery'

describe('installDeployRecovery', () => {
  it('reloads once when a deployed lazy chunk is no longer available', () => {
    const target = new EventTarget()
    const storage = new Map<string, string>()
    const sessionStorage: Pick<Storage, 'getItem' | 'setItem'> = {
      getItem: (key: string) => storage.get(key) ?? null,
      setItem: (key: string, value: string) => {
        storage.set(key, value)
      },
    }
    const reload = vi.fn()
    const now = vi.fn(() => 100_000)
    const uninstall = installDeployRecovery({
      target,
      storage: sessionStorage,
      reload,
      now,
    })

    const firstError = new Event('vite:preloadError', { cancelable: true })
    target.dispatchEvent(firstError)

    expect(firstError.defaultPrevented).toBe(true)
    expect(reload).toHaveBeenCalledTimes(1)

    const repeatedError = new Event('vite:preloadError', { cancelable: true })
    target.dispatchEvent(repeatedError)

    expect(repeatedError.defaultPrevented).toBe(false)
    expect(reload).toHaveBeenCalledTimes(1)

    now.mockReturnValue(131_000)
    target.dispatchEvent(new Event('vite:preloadError', { cancelable: true }))
    expect(reload).toHaveBeenCalledTimes(2)

    uninstall()
  })
})
