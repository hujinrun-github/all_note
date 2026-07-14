import { APIError, api } from './client'

export interface Task {
  id: string
  title: string
  content: string
  project?: string
  project_id?: string
  project_type?: string
  due?: number
  planned_date?: string
  priority: number
  done: number
  status: string
  horizon: 'week' | 'long'
  scope: string
  sort_order: number
  note_id?: string
  roadmap_node_id?: string
  execution_type?: 'single' | 'recurring'
  occurrence_date?: string
  occurrence_status?: 'open' | 'done' | 'skipped'
  recurrence_label?: string
  completed_at?: number
  created_at: number
  updated_at: number
}

export interface RecurrenceConfig {
  start_date: string
  end_date?: string
  frequency: 'daily' | 'weekly' | 'monthly'
  interval?: number
  weekdays?: number[]
  month_days?: number[]
  timezone?: string
  enabled?: boolean
}

export interface TaskOccurrence {
  task_id: string
  occurrence_date: string
  status: 'open' | 'done' | 'skipped'
  completed_at?: number
  title: string
  content: string
  project_id?: string
  project?: string
  roadmap_node_id?: string
  recurrence_label: string
}

export interface TaskProject {
  id: string
  name: string
  type: 'personal' | 'regular' | 'learning'
  description: string
  created_at: number
  updated_at: number
}

export interface LearningRoadmap {
  id: string
  project_id: string
  title: string
  goal: string
  status: 'draft' | 'ready' | 'failed'
  nodes: RoadmapNode[]
  edges: RoadmapEdge[]
  created_at: number
  updated_at: number
}

export interface RoadmapNode {
  id: string
  roadmap_id: string
  parent_id?: string
  type: 'phase' | 'module' | 'task' | 'choice'
  title: string
  description: string
  path_type: 'required' | 'recommended' | 'optional' | 'alternative'
  status: 'todo' | 'active' | 'done' | 'skipped'
  deliverable: string
  acceptance_criteria: string
  x: number
  y: number
  order_index: number
  article_search_queries?: string[]
  resources: RoadmapResource[]
  created_at: number
  updated_at: number
}

export interface RoadmapEdge {
  id: string
  roadmap_id: string
  source_node_id: string
  target_node_id: string
  style: 'solid' | 'dotted'
  created_at: number
}

export interface RoadmapResource {
  id: string
  node_id: string
  title: string
  url: string
  summary: string
  source_type: string
  added_by: string
  created_at: number
  updated_at: number
}

export async function getTasks(params: {
  project?: string
  project_id?: string
  status?: string
  scope?: string
  horizon?: string
  planned_date?: string
  page?: number
  page_size?: number
}) {
  const res = await api.get<{ tasks: Task[] }>('/api/tasks', {
    project: params.project ?? '',
    project_id: params.project_id ?? '',
    status: params.status ?? 'all',
    scope: params.scope ?? '',
    horizon: params.horizon ?? '',
    planned_date: params.planned_date ?? '',
    page: String(params.page ?? 1),
    page_size: String(params.page_size ?? 100),
  })
  return { tasks: res.data.tasks, pagination: res.pagination! }
}

export async function getTaskProjects() {
  try {
    const res = await api.get<{ projects: string[] }>('/api/tasks/projects')
    return res.data.projects
  } catch (error) {
    if (error instanceof APIError && error.status === 404) return []
    throw error
  }
}

export async function listTaskProjects() {
  const res = await api.get<{ projects: TaskProject[] }>('/api/task-projects')
  return res.data.projects
}

export async function createTaskProject(body: {
  name: string
  type: TaskProject['type']
  description?: string
}) {
  const res = await api.post<{ project: TaskProject }>(
    '/api/task-projects',
    body
  )
  return res.data.project
}

export async function updateTaskProject(
  id: string,
  body: Partial<Pick<TaskProject, 'name' | 'type' | 'description'>>
) {
  const res = await api.patch<{ project: TaskProject }>(
    `/api/task-projects/${id}`,
    body
  )
  return res.data.project
}

export async function deleteTaskProject(id: string) {
  await api.del(`/api/task-projects/${id}`)
}

export async function createTask(body: {
  title: string
  content?: string
  project?: string
  project_id?: string
  due?: number
  planned_date?: string
  priority?: number
  scope?: string
  horizon?: 'week' | 'long'
  roadmap_node_id?: string
  execution_type?: 'single' | 'recurring'
  recurrence?: RecurrenceConfig
}) {
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

export async function generateLearningRoadmap(projectID: string) {
  const res = await api.post<{ roadmap: LearningRoadmap }>(
    `/api/task-projects/${projectID}/roadmap/generate`
  )
  return res.data.roadmap
}

export async function getLearningRoadmap(projectID: string) {
  try {
    const res = await api.get<{ roadmap: LearningRoadmap }>(
      `/api/task-projects/${projectID}/roadmap`
    )
    return res.data.roadmap
  } catch (error) {
    if (error instanceof APIError && error.status === 404) return null
    throw error
  }
}

export async function updateRoadmapNode(
  id: string,
  body: Partial<RoadmapNode>
) {
  const res = await api.patch<{ node: RoadmapNode }>(
    `/api/roadmap-nodes/${id}`,
    body
  )
  return res.data.node
}

export async function createRoadmapNode(
  roadmapID: string,
  body: {
    parent_id?: string
    title: string
    type?: RoadmapNode['type']
    description?: string
    path_type?: RoadmapNode['path_type']
    status?: RoadmapNode['status']
    deliverable?: string
    acceptance_criteria?: string
    edge_style?: 'solid' | 'dotted'
  }
) {
  const res = await api.post<{ node: RoadmapNode }>(
    `/api/roadmaps/${roadmapID}/nodes`,
    body
  )
  return res.data.node
}

export async function deleteRoadmapNode(id: string) {
  await api.del(`/api/roadmap-nodes/${id}`)
}

export async function saveRoadmapLayout(
  roadmapID: string,
  nodes: Array<{ id: string; x: number; y: number }>
) {
  await api.patch(`/api/roadmaps/${roadmapID}/layout`, { nodes })
}

export async function optimizeRoadmapLayout(roadmapID: string) {
  const res = await api.post<{ roadmap: LearningRoadmap }>(
    `/api/roadmaps/${roadmapID}/layout/optimize`
  )
  return res.data.roadmap
}

export async function searchRoadmapNodeResources(
  nodeID: string,
  body: { sources?: string[] } = {}
) {
  const res = await api.post<{
    node_id: string
    query: string
    resources: RoadmapResource[]
  }>(`/api/roadmap-nodes/${nodeID}/resources/search`, body)
  return res.data
}

export async function addRoadmapNodeResource(
  nodeID: string,
  body: { title: string; url: string; summary?: string }
) {
  const res = await api.post<{ resource: RoadmapResource }>(
    `/api/roadmap-nodes/${nodeID}/resources`,
    body
  )
  return res.data.resource
}

export async function deleteRoadmapResource(id: string) {
  await api.del(`/api/roadmap-resources/${id}`)
}

export async function completeOccurrence(taskId: string, date: string) {
  const res = await api.post<{ occurrence: TaskOccurrence }>(
    `/api/tasks/${taskId}/occurrences/${date}/complete`
  )
  return res.data.occurrence
}

export async function reopenOccurrence(taskId: string, date: string) {
  const res = await api.post<{ occurrence: TaskOccurrence }>(
    `/api/tasks/${taskId}/occurrences/${date}/reopen`
  )
  return res.data.occurrence
}

export async function skipOccurrence(taskId: string, date: string) {
  const res = await api.post<{ occurrence: TaskOccurrence }>(
    `/api/tasks/${taskId}/occurrences/${date}/skip`
  )
  return res.data.occurrence
}

export async function getTaskOccurrences(from: string, to: string) {
  const res = await api.get<{ occurrences: TaskOccurrence[] }>(
    '/api/task-occurrences',
    { from, to }
  )
  return res.data.occurrences
}

export async function getRecurringTasks(params?: {
  page?: number
  page_size?: number
}) {
  const res = await api.get<{ tasks: Task[] }>('/api/tasks', {
    execution_type: 'recurring',
    page: String(params?.page ?? 1),
    page_size: String(params?.page_size ?? 100),
  })
  return { tasks: res.data.tasks, pagination: res.pagination! }
}
