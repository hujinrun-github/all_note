import { api } from './client'

export interface CalendarProjectSource {
  project_id: string
  name: string
  type: string
  enabled: boolean
  default: boolean
  color: string
  order_index: number
}

export interface CalendarProjectSourcesResponse {
  sources: CalendarProjectSource[]
  available_projects: CalendarProjectSource[]
}

export interface SaveCalendarProjectSourceInput {
  project_id: string
  enabled: boolean
  color: string
  order_index: number
}

export interface SaveCalendarProjectSourcesRequest {
  sources: SaveCalendarProjectSourceInput[]
}

export async function getCalendarProjectSources() {
  const res = await api.get<CalendarProjectSourcesResponse>(
    '/api/calendar/project-sources',
  )
  return res.data
}

export async function saveCalendarProjectSources(
  body: SaveCalendarProjectSourcesRequest,
) {
  const res = await api.put<CalendarProjectSourcesResponse>(
    '/api/calendar/project-sources',
    body,
  )
  return res.data
}
