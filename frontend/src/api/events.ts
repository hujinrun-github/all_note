import { api } from './client'

export interface Event {
  id: string; title: string; start_time: number; end_time: number; location?: string; kind: string; note_id?: string; project_id?: string; project?: string; project_type?: string; created_at: number; updated_at: number
}

export async function getEvents(params: { month?: string; page?: number; page_size?: number }) {
  const res = await api.get<{ events: Event[] }>('/api/events', {
    month: params.month ?? '',
    page: String(params.page ?? 1),
    page_size: String(params.page_size ?? 50),
  })
  return { events: res.data.events, pagination: res.pagination! }
}

export async function createEvent(body: { title: string; start_time: number; end_time: number; location?: string; kind?: string; project_id?: string }) {
  const res = await api.post<{ event: Event }>('/api/events', body)
  return res.data.event
}

export async function updateEvent(id: string, body: Partial<Event>) {
  const res = await api.patch<{ event: Event }>(`/api/events/${id}`, body)
  return res.data.event
}

export async function deleteEvent(id: string) {
  await api.del(`/api/events/${id}`)
}
