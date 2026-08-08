import { api, APIError } from './client'

export type ContentImportStatus =
  | 'active'
  | 'completed'
  | 'failed'
  | 'needs_review'
  | 'canceled'

export interface ResolvedPodcastEpisode {
  source_type: 'apple' | 'xiaoyuzhou'
  submitted_url: string
  canonical_url: string
  external_id: string
  feed_url?: string
  title: string
  podcast_title?: string
  cover_url?: string
  description?: string
  duration_seconds?: number
  has_public_transcript: boolean
}

export interface ContentImport {
  id: string
  source_url: string
  source_type?: string
  canonical_url?: string
  title?: string
  podcast_title?: string
  cover_url?: string
  description?: string
  duration_seconds?: number
  status: ContentImportStatus
  stage: string
  progress: number
  summarize_with_ai: boolean
  summary_prompt?: string
  include_transcript: boolean
  language: string
  folder_id?: string
  project_ids: string[]
  tags: string[]
  result_note_id?: string
  result_note_available?: boolean
  error_code?: string
  error_message?: string
  retryable?: boolean
  revision: number
  created_at: number
  updated_at: number
}

export interface CreateContentImportRequest {
  source_url: string
  summarize_with_ai: boolean
  summary_prompt?: string
  include_transcript: boolean
  language: string
  folder_id?: string
  project_ids: string[]
  tags: string[]
}

export async function resolveContentImport(sourceURL: string) {
  const response = await api.post<{ episode: ResolvedPodcastEpisode }>(
    '/api/content-imports/resolve',
    { source_url: sourceURL }
  )
  return response.data.episode
}

export async function createContentImport(request: CreateContentImportRequest) {
  const response = await api.post<{ import: ContentImport }>(
    '/api/content-imports',
    request,
    { headers: { 'Idempotency-Key': crypto.randomUUID() } }
  )
  return response.data.import
}

export async function listContentImports(status = 'all') {
  const response = await api.get<{ imports: ContentImport[] }>(
    '/api/content-imports',
    { status, page: '1', page_size: '20' }
  )
  return response.data.imports
}

export async function cancelContentImport(id: string) {
  const response = await api.post<{ import: ContentImport }>(
    `/api/content-imports/${encodeURIComponent(id)}/cancel`
  )
  return response.data.import
}

export async function retryContentImport(id: string) {
  const response = await api.post<{ import: ContentImport }>(
    `/api/content-imports/${encodeURIComponent(id)}/retry`,
    undefined,
    { headers: { 'Idempotency-Key': crypto.randomUUID() } }
  )
  return response.data.import
}

export async function deleteContentImport(id: string) {
  await api.del(`/api/content-imports/${encodeURIComponent(id)}`)
}

export async function getContentImportForNote(noteID: string) {
  try {
    const response = await api.get<{ import: ContentImport }>(
      `/api/notes/${encodeURIComponent(noteID)}/content-import`
    )
    return response.data.import
  } catch (error) {
    if (error instanceof APIError && error.status === 404) return null
    throw error
  }
}

export async function getContentImportTranscript(importID: string) {
  const response = await api.get<{ transcript: string }>(
    `/api/content-imports/${encodeURIComponent(importID)}/transcript`
  )
  return response.data.transcript
}
