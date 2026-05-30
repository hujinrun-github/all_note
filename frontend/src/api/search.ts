import { api } from './client'

export interface SearchResult {
  type: string; id: string; title: string; highlight: string; folder_id?: string; done?: number; kind?: string; updated_at: number
}

export async function search(q: string, page?: number, pageSize?: number) {
  if (!q.trim()) return { items: [], pagination: { page: 1, page_size: 20, total: 0 } }
  const res = await api.get<{ items: SearchResult[] }>('/api/search', {
    q,
    page: String(page ?? 1),
    page_size: String(pageSize ?? 20),
  })
  return { items: res.data.items, pagination: res.pagination! }
}
