import { api } from './client'
import type { AccountUser } from './admin'

export interface AuthWorkspace {
  id: string
  name: string
  owner_user_id: string
  created_at: number
  updated_at: number
}

export interface LoginResponse {
  user: AccountUser
  workspace: AuthWorkspace
}

export interface CurrentUserResponse extends LoginResponse {
  must_change_password: boolean
  avatar_url?: string
}

export async function login(body: {
  email: string
  password: string
  remember_me: boolean
}) {
  const res = await api.post<LoginResponse>('/api/auth/login', body)
  return res.data
}

export async function getCurrentUser() {
  const res = await api.get<CurrentUserResponse>('/api/auth/me')
  return res.data
}

export type AuthProvider = 'github'

export async function listAuthProviders() {
  const res = await api.get<{ providers: AuthProvider[] }>(
    '/api/auth/providers'
  )
  return res.data.providers
}

export async function changePassword(body: {
  current_password: string
  new_password: string
}) {
  await api.post<void>('/api/auth/change-password', body)
}

export async function logout() {
  await api.post<void>('/api/auth/logout')
}
