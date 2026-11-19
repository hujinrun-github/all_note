import { api } from './client'
import type {
  OccurrenceV2,
  ProjectHorizon,
  ProjectKind,
  ProjectStatus,
  ProjectV2,
} from './taskDomain'

export interface WorkspaceQueryParams {
  date: string
  timezone: string
}

export interface WorkspaceOverviewParams extends WorkspaceQueryParams {
  today_limit?: number
  project_limit?: number
  notes_limit?: number
}

export interface WorkspaceFacetsParams extends WorkspaceQueryParams {
  project_id?: string
}

export interface ProjectSummariesParams extends WorkspaceQueryParams {
  kind?: ProjectKind
  horizon?: ProjectHorizon
  status?: ProjectStatus
}

export interface WorkspaceQueryMetadata extends WorkspaceQueryParams {
  workspace_id: string
  task_model_version: 2
  runtime_epoch: number
  generated_at: string
  read_consistency: 'planning_snapshot'
}

export interface WorkspaceFacets {
  today: number
  week: number
  month: number
  overdue: number
  unscheduled: number
  inbox: number
  completed_today: number
  completed_week: number
  completed_total: number
  draft: number
  recurring: number
}

export interface WorkspaceOccurrence extends OccurrenceV2 {
  project_name: string
}

export interface ProjectSummary {
  completed_task_count: number
  total_actionable_task_count: number
  open_occurrence_count: number
  blocked_occurrence_count: number
  has_open_ended_recurrence: boolean
  next_occurrence: WorkspaceOccurrence | null
  completion_percent: number | null
}

export interface ProjectWithSummary extends ProjectV2 {
  summary: ProjectSummary
}

export interface WorkspaceNotePreview {
  id: string
  title: string
  updated_at: number
}

export interface WorkspaceNotesSection {
  state: 'available' | 'unavailable'
  authority: string
  as_of: string
  items: WorkspaceNotePreview[] | null
  error?: { code: string; message: string }
}

export interface WorkspaceOverview extends WorkspaceQueryMetadata {
  facets: WorkspaceFacets
  today: WorkspaceOccurrence[]
  projects: ProjectWithSummary[]
  notes: WorkspaceNotesSection
  partial: boolean
}

export interface WorkspaceFacetsResponse extends WorkspaceQueryMetadata {
  facets: WorkspaceFacets
}

export interface ProjectSummariesResponse extends WorkspaceQueryMetadata {
  projects: ProjectWithSummary[]
}

export interface ProjectOverviewResponse extends WorkspaceQueryMetadata {
  project: ProjectWithSummary
}

export async function getWorkspaceOverview(params: WorkspaceOverviewParams) {
  const response = await api.get<WorkspaceOverview>(
    '/api/workspace/overview',
    queryParams(params, {
      today_limit: params.today_limit,
      project_limit: params.project_limit,
      notes_limit: params.notes_limit,
    })
  )
  return response.data
}

export async function getTaskWorkbenchFacets(params: WorkspaceFacetsParams) {
  const response = await api.get<WorkspaceFacetsResponse>(
    '/api/task-workbench/facets',
    queryParams(params, { project_id: params.project_id })
  )
  return response.data
}

export async function getProjectSummaries(params: ProjectSummariesParams) {
  const response = await api.get<ProjectSummariesResponse>('/api/projects', {
    ...queryParams(params, {
      kind: params.kind,
      horizon: params.horizon,
      status: params.status,
    }),
    include: 'summary',
  })
  return response.data
}

export async function getProjectOverview(
  projectID: string,
  params: WorkspaceQueryParams
) {
  const response = await api.get<ProjectOverviewResponse>(
    `/api/projects/${encodeURIComponent(projectID)}/overview`,
    queryParams(params)
  )
  return response.data
}

// Calendar dates must be formatted in the requested timezone, not sliced from
// a UTC ISO timestamp, which selects the wrong day near local midnight.
export function createWorkspaceQueryParams(
  reference = new Date(),
  timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
): WorkspaceQueryParams {
  const parts = new Intl.DateTimeFormat('en-CA', {
    timeZone: timezone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).formatToParts(reference)
  const part = (type: Intl.DateTimeFormatPartTypes) =>
    parts.find((item) => item.type === type)?.value
  return {
    date: `${part('year')}-${part('month')}-${part('day')}`,
    timezone,
  }
}

function queryParams(
  params: WorkspaceQueryParams,
  extras: Record<string, string | number | undefined> = {}
): Record<string, string> {
  return Object.fromEntries(
    Object.entries({ date: params.date, timezone: params.timezone, ...extras })
      .filter(([, value]) => value !== undefined && value !== '')
      .map(([key, value]) => [key, String(value)])
  )
}
