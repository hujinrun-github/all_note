import { api } from './client'

export interface Folder {
  id: string; name: string; sort_order: number; note_count: number; created_at: number
}

export async function getFolders() {
  const res = await api.get<{ folders: Folder[] }>('/api/folders')
  return res.data.folders
}
