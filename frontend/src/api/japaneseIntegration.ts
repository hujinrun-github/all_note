import { api } from './client'

const base = '/api/integrations/japanese'
export interface JapaneseConnection {
  connection_id: string
  workspace_id: string
  actor_id: string
  source_instance_id: string
  source_user_id: string
  source_user_name: string
  key_id: string
  state: string
  created_at: string
}
export interface JapaneseStudyTask {
  id: string
  title: string
  path: string
  minutes: number
  weekdays?: number[]
}
export interface JapanesePlan {
  plan_id: string
  revision: number
  title: string
  timezone: string
  start_date: string
  end_date: string
  weekdays: number[]
  time_slots:
    | {
        id: string
        title: string
        time: string
        duration_minutes: number
        tasks: JapaneseStudyTask[]
      }[]
    | null
}
export interface JapaneseBindingInput {
  connection_id: string
  plan_id: string
  slot_id: string
  task_id: string
  target_task_id: string
  effective_from?: string
  enabled?: boolean
}
export interface JapaneseBinding extends JapaneseBindingInput {
  id: string
  target_title: string
  binding_version: number
  effective_from: string
  effective_to?: string
  schedule_revision: number
  source_fingerprint: string
  enabled: boolean
}
export interface JapaneseReceipt {
  event_id: string
  connection_id: string
  status: string
  study_date: string
  source_task_id: string
  completed: boolean
  error_code?: string
  occurrence_id?: string
  target_task_id?: string
  source_occurred_at: string
  processed_at?: string
}
export interface JapaneseIntegrationStatus {
  connections: JapaneseConnection[]
  catalogs: {
    connection_id: string
    catalog: {
      schema_version: number
      source_instance_id: string
      source_user_id: string
      catalog_revision: number
      plan: JapanesePlan | null
    }
  }[]
  bindings: JapaneseBinding[]
  targets: {
    task_id: string
    title: string
    timezone: string
    recurrence_type: string
  }[]
  events: JapaneseReceipt[]
}
export async function getJapaneseIntegration() {
  return (await api.get<JapaneseIntegrationStatus>(base)).data
}
export async function createJapanesePairingCode() {
  return (
    await api.post<{ pairing_code: string; expires_at: string }>(
      `${base}/pairing-codes`
    )
  ).data
}
export async function saveJapaneseBinding(input: JapaneseBindingInput) {
  return (await api.post<JapaneseBinding>(`${base}/bindings`, input)).data
}
export async function revokeJapaneseConnection(id: string) {
  await api.del(`${base}/connections/${encodeURIComponent(id)}`)
}
export async function retryJapaneseEvent(id: string) {
  await api.post(`${base}/events/${encodeURIComponent(id)}/retry`)
}
