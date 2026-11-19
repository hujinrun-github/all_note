import { useQuery } from '@tanstack/react-query'
import * as workspaceAPI from '../api/workspace'

const workspaceRootKey = ['workspace'] as const

export const workspaceQueryKeys = {
  all: workspaceRootKey,
  overview: (params: workspaceAPI.WorkspaceOverviewParams) =>
    [
      ...workspaceRootKey,
      'overview',
      params.date,
      params.timezone,
      params.today_limit ?? 8,
      params.project_limit ?? 3,
      params.notes_limit ?? 3,
    ] as const,
  facets: (params: workspaceAPI.WorkspaceFacetsParams) =>
    [
      ...workspaceRootKey,
      'facets',
      params.date,
      params.timezone,
      params.project_id ?? null,
    ] as const,
  projectSummaries: (params: workspaceAPI.ProjectSummariesParams) =>
    [
      ...workspaceRootKey,
      'projects',
      'summaries',
      params.date,
      params.timezone,
      params.kind ?? null,
      params.horizon ?? null,
      params.status ?? null,
    ] as const,
  projectOverview: (
    projectID: string,
    params: workspaceAPI.WorkspaceQueryParams
  ) =>
    [
      ...workspaceRootKey,
      'projects',
      'overview',
      projectID,
      params.date,
      params.timezone,
    ] as const,
}

export function useWorkspaceOverview(
  params: workspaceAPI.WorkspaceOverviewParams,
  options: { enabled?: boolean } = {}
) {
  return useQuery({
    queryKey: workspaceQueryKeys.overview(params),
    queryFn: () => workspaceAPI.getWorkspaceOverview(params),
    enabled: options.enabled ?? true,
  })
}

export function useTaskWorkbenchFacets(
  params: workspaceAPI.WorkspaceFacetsParams,
  options: { enabled?: boolean } = {}
) {
  return useQuery({
    queryKey: workspaceQueryKeys.facets(params),
    queryFn: () => workspaceAPI.getTaskWorkbenchFacets(params),
    enabled: options.enabled ?? true,
  })
}

export function useProjectSummaries(
  params: workspaceAPI.ProjectSummariesParams,
  options: { enabled?: boolean } = {}
) {
  return useQuery({
    queryKey: workspaceQueryKeys.projectSummaries(params),
    queryFn: () => workspaceAPI.getProjectSummaries(params),
    enabled: options.enabled ?? true,
  })
}

export function useProjectOverview(
  projectID: string,
  params: workspaceAPI.WorkspaceQueryParams,
  options: { enabled?: boolean } = {}
) {
  return useQuery({
    queryKey: workspaceQueryKeys.projectOverview(projectID, params),
    queryFn: () => workspaceAPI.getProjectOverview(projectID, params),
    enabled: projectID !== '' && (options.enabled ?? true),
  })
}
