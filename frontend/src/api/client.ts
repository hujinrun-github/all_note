export interface APIResponse<T> {
  data: T
  pagination?: { page: number; page_size: number; total: number }
  error?: { code: string; message: string }
}

class APIClient {
  private basePath = import.meta.env.BASE_URL === '/' ? '' : import.meta.env.BASE_URL.replace(/\/$/, '')

  async get<T>(path: string, params?: Record<string, string>): Promise<APIResponse<T>> {
    const url = new URL(`${this.basePath}${path}`, window.location.origin)
    if (params) {
      Object.entries(params).forEach(([k, v]) => { if (v) url.searchParams.set(k, v) })
    }
    const res = await fetch(url.toString(), { credentials: 'include' })
    if (!res.ok) {
      const body = await res.json().catch(() => ({}))
      this.redirectToLoginOnUnauthorized(path, res.status)
      throw new APIError(res.status, body?.error?.code ?? 'UNKNOWN', body?.error?.message ?? 'Request failed')
    }
    if (res.status === 204) return { data: undefined as T }
    return res.json()
  }

  async post<T>(path: string, body?: unknown): Promise<APIResponse<T>> {
    const res = await fetch(`${this.basePath}${path}`, {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: body !== undefined ? JSON.stringify(body) : undefined,
    })
    if (!res.ok) {
      const errBody = await res.json().catch(() => ({}))
      this.redirectToLoginOnUnauthorized(path, res.status)
      throw new APIError(res.status, errBody?.error?.code ?? 'UNKNOWN', errBody?.error?.message ?? 'Request failed')
    }
    if (res.status === 204) return { data: undefined as T }
    return res.json()
  }

  async put<T>(path: string, body?: unknown): Promise<APIResponse<T>> {
    const res = await fetch(`${this.basePath}${path}`, {
      method: 'PUT',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: body !== undefined ? JSON.stringify(body) : undefined,
    })
    if (!res.ok) {
      const errBody = await res.json().catch(() => ({}))
      this.redirectToLoginOnUnauthorized(path, res.status)
      throw new APIError(res.status, errBody?.error?.code ?? 'UNKNOWN', errBody?.error?.message ?? 'Request failed')
    }
    if (res.status === 204) return { data: undefined as T }
    return res.json()
  }

  async patch<T>(path: string, body?: unknown): Promise<APIResponse<T>> {
    const res = await fetch(`${this.basePath}${path}`, {
      method: 'PATCH',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: body !== undefined ? JSON.stringify(body) : undefined,
    })
    if (!res.ok) {
      const errBody = await res.json().catch(() => ({}))
      this.redirectToLoginOnUnauthorized(path, res.status)
      throw new APIError(res.status, errBody?.error?.code ?? 'UNKNOWN', errBody?.error?.message ?? 'Request failed')
    }
    return res.json()
  }

  async del(path: string, body?: unknown): Promise<void> {
    const res = await fetch(`${this.basePath}${path}`, {
      method: 'DELETE',
      credentials: 'include',
      headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    })
    if (!res.ok) {
      const errBody = await res.json().catch(() => ({}))
      this.redirectToLoginOnUnauthorized(path, res.status)
      throw new APIError(res.status, errBody?.error?.code ?? 'UNKNOWN', errBody?.error?.message ?? 'Delete failed')
    }
  }

  private redirectToLoginOnUnauthorized(path: string, status: number) {
    if (status !== 401 || path === '/api/auth/login' || window.location.pathname === '/login') return

    const next = `${window.location.pathname}${window.location.search}${window.location.hash}`
    window.location.assign(`${this.basePath}/login?next=${encodeURIComponent(next)}`)
  }
}

export class APIError extends Error {
  constructor(public status: number, public code: string, message: string) {
    super(message)
  }
}

export const api = new APIClient()
