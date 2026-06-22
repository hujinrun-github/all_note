import { api } from './client'
import type { TaskProject } from './tasks'

export interface NoteRef {
  id: string
  title: string
}

export interface TaskSummaryItem {
  id: string; title: string; done: number
  planned_date?: string; due?: number; completed_at?: number
  note_id?: string
  project?: TaskProject
  linked_notes?: NoteRef[]
}

export interface DateGroup {
  date: string; tasks: TaskSummaryItem[]; count: number
}

export interface SummaryResponse {
  groups: DateGroup[]
  active_days: number
  project_count: number
}

export interface Pagination {
  page: number; page_size: number; total: number
}

export interface SummaryResult {
  summary: SummaryResponse
  pagination: Pagination
}

export async function getSummary(from: string, to: string, page: number, pageSize = 50): Promise<SummaryResult> {
  const res = await api.get<SummaryResponse>('/api/summary', {
    from, to, page: String(page), page_size: String(pageSize),
  })
  return { summary: res.data, pagination: res.pagination! }
}
