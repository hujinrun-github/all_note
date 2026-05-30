import { api } from './client'

export interface Task {
  id: string; title: string; project?: string; due?: number; priority: number; done: number; scope: string; sort_order: number; note_id?: string; created_at: number; updated_at: number
}

export async function getTasks(params: { project?: string; status?: string; scope?: string; page?: number; page_size?: number }) {
  const res = await api.get<{ tasks: Task[] }>('/api/tasks', {
    project: params.project ?? '',
    status: params.status ?? 'all',
    scope: params.scope ?? '',
    page: String(params.page ?? 1),
    page_size: String(params.page_size ?? 20),
  })
  return { tasks: res.data.tasks, pagination: res.pagination! }
}

export async function createTask(body: { title: string; project?: string; due?: number; priority?: number; scope?: string }) {
  const res = await api.post<{ task: Task }>('/api/tasks', body)
  return res.data.task
}

export async function updateTask(id: string, body: Partial<Task>) {
  const res = await api.patch<{ task: Task }>(`/api/tasks/${id}`, body)
  return res.data.task
}

export async function deleteTask(id: string) {
  await api.del(`/api/tasks/${id}`)
}
