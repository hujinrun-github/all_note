import { api } from './client'

export interface InboxItem {
  id: string
  kind: string
  title: string
  body?: string
  source: string
  archived: number
  converted_to?: string
  created_at: number
  updated_at: number
}

export interface ConvertInboxInput {
  kind: string
  title?: string
  content?: string
  project_id?: string
  due?: number
  priority?: number
}

export async function getInbox(params: {
  kind?: string
  page?: number
  page_size?: number
}) {
  const res = await api.get<{ items: InboxItem[] }>('/api/inbox', {
    kind: params.kind ?? 'all',
    page: String(params.page ?? 1),
    page_size: String(params.page_size ?? 20),
  })
  return { items: res.data.items, pagination: res.pagination! }
}

export async function createInboxItem(body: {
  kind: string
  title: string
  body?: string
}) {
  const res = await api.post<{ item: InboxItem }>('/api/inbox', body)
  return res.data.item
}

export async function convertInboxItem(id: string, body: ConvertInboxInput) {
  const res = await api.post<{ item: unknown }>(
    `/api/inbox/${id}/convert`,
    body
  )
  return res.data.item
}

export async function deleteInboxItem(id: string) {
  await api.del(`/api/inbox/${id}`)
}

export async function batchInbox(ids: string[], action: 'archive' | 'delete') {
  const res = await api.post<{ affected: number }>('/api/inbox/batch', {
    ids,
    action,
  })
  return res.data.affected
}
