import { api } from './client'

export type AccountRole = 'admin' | 'user'
export type AccountStatus = 'active' | 'disabled'

export interface AccountUser {
  id: string
  email: string
  display_name: string
  must_change_password: boolean
  default_workspace_id: string
  role: AccountRole
  status: AccountStatus
  created_at: number
  updated_at: number
  last_login_at?: number
  password_changed_at?: number
}

export interface AccountPagination {
  page: number
  page_size: number
  total: number
}

export async function listAdminUsers(params: { page: number; pageSize: number; q?: string }) {
  const res = await api.get<{ users: AccountUser[] }>('/api/admin/users', {
    page: String(params.page),
    page_size: String(params.pageSize),
    q: params.q ?? '',
  })
  return {
    users: res.data.users,
    pagination: res.pagination ?? {
      page: params.page,
      page_size: params.pageSize,
      total: res.data.users.length,
    },
  }
}

export async function createAdminUser(body: {
  email: string
  display_name: string
  temporary_password: string
  role: AccountRole
}) {
  const res = await api.post<{ user: AccountUser }>('/api/admin/users', body)
  return res.data.user
}

export async function updateAdminUser(id: string, body: Partial<Pick<AccountUser, 'email' | 'display_name' | 'role'>>) {
  const res = await api.patch<{ user: AccountUser }>(`/api/admin/users/${id}`, body)
  return res.data.user
}

export async function resetAdminUserPassword(id: string, temporaryPassword: string) {
  await api.post<void>(`/api/admin/users/${id}/reset-password`, {
    temporary_password: temporaryPassword,
  })
}

export async function setAdminUserStatus(id: string, status: AccountStatus) {
  const action = status === 'active' ? 'enable' : 'disable'
  await api.post<void>(`/api/admin/users/${id}/${action}`)
}
