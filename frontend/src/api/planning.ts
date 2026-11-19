import { api } from './client'
import type { ScheduleV2Input } from './taskDomain'

export interface PlanningCapabilities {
  available: boolean
  transcription_available: boolean
  generation_available: boolean
  max_audio_bytes: number
  max_duration_seconds: number
  reason?: string
}

export interface PlanItem {
  id: string
  selected: boolean
  title: string
  description: string
  priority: number
  schedule: ScheduleV2Input
  due_at?: string
}

export interface PlanJob {
  id: string
  draft_id: string
  stage: 'transcribe' | 'generate'
  state:
    | 'queued'
    | 'processing'
    | 'completed'
    | 'failed'
    | 'cancelled'
    | 'stale'
  error_code?: string
  retryable: boolean
}

export interface PlanReceipt {
  draft_id: string
  project_id: string
  task_ids: string[]
  occurrence_ids: string[]
}

export interface PlanDraft {
  id: string
  revision: number
  status: string
  input_text: string
  transcript: string
  timezone: string
  reference_time: string
  project_id: string
  title: string
  items: PlanItem[]
  clarifications: string[]
  assumptions: string[]
  latest_job?: PlanJob
  audio_available: boolean
  created_at: string
  updated_at: string
  receipt?: PlanReceipt
}

export interface PlanValidation {
  draft_revision: number
  issues: string[]
  conflicts: string[]
  preview?: unknown
}

export interface UpdatePlanDraft {
  expected_revision: number
  input_text?: string
  project_id?: string
  title?: string
  items?: PlanItem[]
}
const draftPath = (id: string) => `/api/plan-drafts/${encodeURIComponent(id)}`
const jobPath = (id: string) => `/api/plan-jobs/${encodeURIComponent(id)}`
export function newPlanningRequestID() {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  bytes[6] = (bytes[6] & 0x0f) | 0x40
  bytes[8] = (bytes[8] & 0x3f) | 0x80
  const hex = Array.from(bytes, (value) =>
    value.toString(16).padStart(2, '0')
  ).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

export async function getPlanningCapabilities() {
  return (await api.get<PlanningCapabilities>('/api/planning/capabilities'))
    .data
}
export async function listPlanDrafts() {
  return (
    await api.get<{ drafts: PlanDraft[] }>('/api/plan-drafts', {
      status: 'open',
    })
  ).data.drafts
}
export async function getPlanDraft(id: string) {
  return (await api.get<{ draft: PlanDraft }>(draftPath(id))).data.draft
}
export async function createPlanDraft(input: {
  request_id: string
  input_text: string
  timezone: string
  project_id?: string
}) {
  return (await api.post<{ draft: PlanDraft }>('/api/plan-drafts', input)).data
    .draft
}
export async function updatePlanDraft(id: string, input: UpdatePlanDraft) {
  return (await api.patch<{ draft: PlanDraft }>(draftPath(id), input)).data
    .draft
}
export async function uploadPlanAudio(
  id: string,
  expectedRevision: number,
  blob: Blob
) {
  return (
    await api.putBlob<{ draft: PlanDraft }>(
      `${draftPath(id)}/audio?expected_revision=${expectedRevision}`,
      blob
    )
  ).data.draft
}
export async function startPlanJob(
  id: string,
  stage: PlanJob['stage'],
  expectedRevision: number,
  requestID: string
) {
  return (
    await api.post<{ draft: PlanDraft; job: PlanJob }>(
      `${draftPath(id)}/${stage}`,
      { expected_revision: expectedRevision, request_id: requestID }
    )
  ).data
}
export async function getPlanJob(id: string) {
  return (await api.get<{ job: PlanJob }>(jobPath(id))).data.job
}
export async function controlPlanJob(
  id: string,
  action: 'retry' | 'cancel',
  requestID: string
) {
  return (
    await api.post<{ draft: PlanDraft; job: PlanJob }>(
      `${jobPath(id)}/${action}`,
      { request_id: requestID }
    )
  ).data
}
export async function validatePlanDraft(id: string, expectedRevision: number) {
  return (
    await api.post<PlanValidation>(`${draftPath(id)}/validate`, {
      expected_revision: expectedRevision,
    })
  ).data
}
export async function commitPlanDraft(
  id: string,
  input: {
    expected_revision: number
    request_id: string
    publish: boolean
    allow_conflicts: boolean
    new_project?: {
      name: string
      kind: 'standard' | 'learning'
      horizon: 'short' | 'long'
    }
  }
) {
  return (
    await api.post<{ receipt: PlanReceipt }>(`${draftPath(id)}/commit`, input)
  ).data.receipt
}
export async function deletePlanDraft(id: string) {
  await api.del(draftPath(id))
}
