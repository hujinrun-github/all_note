import { api } from './client'

export interface NoteProject {
  id: string
  name: string
  type: 'personal' | 'regular' | 'learning'
}

export interface Note {
  id: string
  title: string
  body: string
  folder_id: string
  tags: string
  projects: NoteProject[]
  created_at: number
  updated_at: number
}

export async function getNotes(params: {
  folder_id?: string
  project_id?: string
  unassigned?: boolean
  sort?: string
  page?: number
  page_size?: number
} = {}) {
  const searchParams = new URLSearchParams()
  if (params.folder_id) searchParams.set('folder_id', params.folder_id)
  if (params.project_id) searchParams.set('project_id', params.project_id)
  if (params.unassigned) searchParams.set('unassigned', 'true')
  if (params.sort) searchParams.set('sort', params.sort)
  if (params.page) searchParams.set('page', String(params.page))
  if (params.page_size) searchParams.set('page_size', String(params.page_size))
  const qs = searchParams.toString()
  const res = await api.get<{ notes: Note[] }>(`/api/notes${qs ? `?${qs}` : ''}`)
  return { notes: res.data.notes, pagination: res.pagination! }
}

export async function getNote(id: string) {
  const res = await api.get<{ note: Note }>(`/api/notes/${encodeURIComponent(id)}`)
  return res.data.note
}

export async function createNote(body: {
  title: string
  body?: string
  folder_id?: string
  tags?: string
  project_ids?: string[]
}) {
  const res = await api.post<{ note: Note }>('/api/notes', body)
  return res.data.note
}

export async function updateNote(id: string, body: {
  title?: string
  body?: string
  folder_id?: string
  tags?: string
  project_ids?: string[]
}) {
  const res = await api.patch<{ note: Note }>(`/api/notes/${encodeURIComponent(id)}`, body)
  return res.data.note
}

export async function deleteNote(id: string) {
  await api.del(`/api/notes/${encodeURIComponent(id)}`)
}
