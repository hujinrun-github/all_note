import { api } from './client'

export interface Note {
  id: string; title: string; body: string; folder_id: string; tags: string; created_at: number; updated_at: number
}

export async function getNotes(params: { folder_id?: string; sort?: string; page?: number; page_size?: number }) {
  const res = await api.get<{ notes: Note[] }>('/api/notes', {
    folder_id: params.folder_id ?? '',
    sort: params.sort ?? 'recent',
    page: String(params.page ?? 1),
    page_size: String(params.page_size ?? 20),
  })
  return { notes: res.data.notes, pagination: res.pagination! }
}

export async function getNote(id: string) {
  const res = await api.get<{ note: Note }>(`/api/notes/${encodeURIComponent(id)}`)
  return res.data.note
}

export async function createNote(body: { title: string; body?: string; folder_id?: string; tags?: string }) {
  const res = await api.post<{ note: Note }>('/api/notes', body)
  return res.data.note
}

export async function updateNote(id: string, body: Partial<Note>) {
  const res = await api.patch<{ note: Note }>(`/api/notes/${encodeURIComponent(id)}`, body)
  return res.data.note
}

export async function deleteNote(id: string) {
  await api.del(`/api/notes/${encodeURIComponent(id)}`)
}
