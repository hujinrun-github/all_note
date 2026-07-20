import { api } from './client'

export interface UserProfile {
  user_id: string
  email: string
  display_name: string
  locale: 'zh-CN' | 'en-US' | 'ja-JP'
  time_zone: string
  avatar_url?: string
  updated_at: number
}

export interface UpdateUserProfile {
  display_name: string
  locale: UserProfile['locale']
  time_zone: string
}

export async function getUserProfile() {
  const response = await api.get<UserProfile>('/api/settings/profile')
  return response.data
}

export async function updateUserProfile(input: UpdateUserProfile) {
  const response = await api.patch<UserProfile>('/api/settings/profile', input)
  return response.data
}

export async function uploadUserAvatar(file: File) {
  const response = await api.putBlob<{
    avatar_url: string
    sha256: string
    width: number
    height: number
  }>('/api/settings/profile/avatar', file)
  return response.data
}

export async function deleteUserAvatar() {
  await api.del('/api/settings/profile/avatar')
}

export type ServiceKind =
  | 'data_store'
  | 'object_s3'
  | 'llm_chat'
  | 'llm_transcription'

export interface ServiceBinding {
  kind: ServiceKind
  mode: 'default' | 'custom' | 'disabled' | 'reuse_chat'
  endpoint_id?: string
  endpoint_name?: string
  provider?: string
  profile_version_id?: string
  has_credentials: boolean
  revision: number
}

export interface RuntimeSettings {
  workspace_id: string
  mode: 'active' | 'draining' | 'migrating' | 'activating' | 'blocked'
  epoch: number
  binding_revision: number
  bindings: ServiceBinding[]
}

export interface ProfileDraftInput {
  id: string
  family_id: string
  kind: ServiceKind
  name: string
  provider: string
  config: Record<string, unknown>
  secret?: string
  preserve_from_version_id?: string
}

export async function getRuntimeSettings() {
  const response = await api.get<RuntimeSettings>('/api/settings/runtime')
  return response.data
}

export async function testServiceProfile(
  input: Pick<ProfileDraftInput, 'kind' | 'provider' | 'config' | 'secret'>
) {
  const response = await api.post<{
    ok: boolean
    code: string
    message: string
  }>('/api/settings/profiles/test', input)
  return response.data
}

export async function saveServiceProfile(input: ProfileDraftInput) {
  const response = await api.post<{
    id: string
    family_id: string
    kind: ServiceKind
    version: number
    state: 'draft'
    has_credentials: boolean
  }>('/api/settings/profiles', input)
  return response.data
}

export async function verifyServiceProfile(input: {
  kind: ServiceKind
  versionId: string
}) {
  const response = await api.post<{
    endpoint_id: string
    profile_version_id: string
    kind: ServiceKind
  }>(`/api/settings/profiles/${input.kind}/${input.versionId}/verify`, {})
  return response.data
}

export async function setServiceBinding(input: {
  kind: ServiceKind
  mode: ServiceBinding['mode']
  endpoint_id?: string
  expected_revision: number
}) {
  const response = await api.put<ServiceBinding>(
    `/api/settings/bindings/${input.kind}`,
    {
      mode: input.mode,
      endpoint_id: input.endpoint_id,
      expected_revision: input.expected_revision,
    }
  )
  return response.data
}

export interface CodexDeviceAuthorization {
  flow_id: string
  user_code: string
  verification_url: string
  interval_seconds: number
  expires_at: string
}

export async function startCodexSubscription() {
  const response = await api.post<CodexDeviceAuthorization>(
    '/api/settings/ai/codex/device/start',
    {}
  )
  return response.data
}

export async function pollCodexSubscription(input: {
  flowId: string
  expectedRevision: number
}) {
  const response = await api.post<{
    status: 'pending' | 'connected' | 'expired' | 'failed'
    endpoint_id?: string
    profile_version_id?: string
  }>(`/api/settings/ai/codex/device/${input.flowId}/poll`, {
    expected_revision: input.expectedRevision,
  })
  return response.data
}
