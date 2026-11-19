import { act, renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { PropsWithChildren } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as workspaceAPI from '../api/workspace'
import {
  useProjectOverview,
  useProjectSummaries,
  useTaskWorkbenchFacets,
  useWorkspaceOverview,
  workspaceQueryKeys,
} from './useWorkspace'

vi.mock('../api/workspace', async () => ({
  ...(await vi.importActual<typeof import('../api/workspace')>(
    '../api/workspace'
  )),
  getWorkspaceOverview: vi.fn(),
  getTaskWorkbenchFacets: vi.fn(),
  getProjectSummaries: vi.fn(),
  getProjectOverview: vi.fn(),
}))

const params = { date: '2026-11-19', timezone: 'Asia/Shanghai' }
const metadata: workspaceAPI.WorkspaceQueryMetadata = {
  ...params,
  workspace_id: 'workspace-1',
  task_model_version: 2,
  runtime_epoch: 3,
  generated_at: '2026-11-19T01:00:00Z',
  read_consistency: 'planning_snapshot',
}
const facets: workspaceAPI.WorkspaceFacets = {
  today: 25,
  week: 40,
  month: 100,
  overdue: 3,
  unscheduled: 6,
  inbox: 4,
  completed_today: 2,
  completed_week: 8,
  completed_total: 60,
  draft: 1,
  recurring: 2,
}
const project: workspaceAPI.ProjectWithSummary = {
  id: 'project-1',
  name: 'Project',
  kind: 'standard',
  horizon: 'long',
  status: 'active',
  revision: 2,
  summary: {
    completed_task_count: 1,
    total_actionable_task_count: 3,
    open_occurrence_count: 5,
    blocked_occurrence_count: 1,
    has_open_ended_recurrence: true,
    next_occurrence: null,
    completion_percent: null,
  },
}
const overview: workspaceAPI.WorkspaceOverview = {
  ...metadata,
  facets,
  today: [],
  projects: [project],
  partial: false,
  notes: {
    state: 'available',
    authority: 'platform_compatibility',
    as_of: metadata.generated_at,
    items: [],
  },
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(workspaceAPI.getWorkspaceOverview).mockResolvedValue(overview)
  vi.mocked(workspaceAPI.getTaskWorkbenchFacets).mockResolvedValue({
    ...metadata,
    facets,
  })
  vi.mocked(workspaceAPI.getProjectSummaries).mockResolvedValue({
    ...metadata,
    projects: [project],
  })
  vi.mocked(workspaceAPI.getProjectOverview).mockResolvedValue({
    ...metadata,
    project,
  })
})

describe('workspace aggregate hooks', () => {
  it('shares an overview read across consumers and preserves server totals and metadata', async () => {
    const { wrapper } = createQueryWrapper()
    const { result } = renderHook(
      () => ({
        first: useWorkspaceOverview(params),
        second: useWorkspaceOverview({
          timezone: params.timezone,
          date: params.date,
        }),
      }),
      { wrapper }
    )

    await waitFor(() => expect(result.current.first.isSuccess).toBe(true))
    expect(result.current.second.data).toEqual(overview)
    expect(result.current.first.data?.facets.today).toBe(25)
    expect(result.current.first.data?.today).toHaveLength(0)
    expect(workspaceAPI.getWorkspaceOverview).toHaveBeenCalledExactlyOnceWith(
      params
    )
  })

  it('connects facet, summary and project reads without deriving progress or changing legacy lists', async () => {
    const { wrapper } = createQueryWrapper()
    const summaryParams = {
      ...params,
      kind: 'standard' as const,
      status: 'active' as const,
    }
    const { result } = renderHook(
      () => ({
        facets: useTaskWorkbenchFacets({ ...params, project_id: project.id }),
        summaries: useProjectSummaries(summaryParams),
        project: useProjectOverview(project.id, params),
      }),
      { wrapper }
    )

    await waitFor(() => expect(result.current.project.isSuccess).toBe(true))
    await waitFor(() => expect(result.current.facets.isSuccess).toBe(true))
    await waitFor(() => expect(result.current.summaries.isSuccess).toBe(true))
    expect(workspaceAPI.getTaskWorkbenchFacets).toHaveBeenCalledWith({
      ...params,
      project_id: project.id,
    })
    expect(workspaceAPI.getProjectSummaries).toHaveBeenCalledWith(summaryParams)
    expect(workspaceAPI.getProjectOverview).toHaveBeenCalledWith(
      project.id,
      params
    )
    expect(
      result.current.summaries.data?.projects[0].summary.completion_percent
    ).toBeNull()
    expect(result.current.project.data?.runtime_epoch).toBe(3)
  })

  it('isolates dates, timezones, filters and project identities in the cache', () => {
    expect(workspaceQueryKeys.overview(params)).toEqual(
      workspaceQueryKeys.overview({
        ...params,
        today_limit: 8,
        project_limit: 3,
        notes_limit: 3,
      })
    )
    expect(workspaceQueryKeys.overview(params)).not.toEqual(
      workspaceQueryKeys.overview({ ...params, date: '2026-11-20' })
    )
    expect(workspaceQueryKeys.overview(params)).not.toEqual(
      workspaceQueryKeys.overview({ ...params, timezone: 'UTC' })
    )
    expect(workspaceQueryKeys.overview(params)).not.toEqual(
      workspaceQueryKeys.overview({ ...params, today_limit: 2 })
    )
    expect(workspaceQueryKeys.facets(params)).not.toEqual(
      workspaceQueryKeys.facets({ ...params, project_id: 'project-1' })
    )
    expect(workspaceQueryKeys.projectSummaries(params)).not.toEqual(
      workspaceQueryKeys.projectSummaries({ ...params, status: 'paused' })
    )
    expect(workspaceQueryKeys.projectOverview('project-1', params)).not.toEqual(
      workspaceQueryKeys.projectOverview('project-2', params)
    )
  })

  it('defers disabled and missing-project reads until enabled', async () => {
    const { wrapper } = createQueryWrapper()
    const { result, rerender } = renderHook(
      ({ enabled }) => ({
        overview: useWorkspaceOverview(params, { enabled }),
        facets: useTaskWorkbenchFacets(params, { enabled }),
        summaries: useProjectSummaries(params, { enabled }),
        project: useProjectOverview('', params),
      }),
      { wrapper, initialProps: { enabled: false } }
    )

    expect(result.current.overview.fetchStatus).toBe('idle')
    expect(workspaceAPI.getWorkspaceOverview).not.toHaveBeenCalled()
    expect(workspaceAPI.getTaskWorkbenchFacets).not.toHaveBeenCalled()
    expect(workspaceAPI.getProjectSummaries).not.toHaveBeenCalled()
    expect(workspaceAPI.getProjectOverview).not.toHaveBeenCalled()

    rerender({ enabled: true })
    await waitFor(() => expect(result.current.overview.isSuccess).toBe(true))
    expect(workspaceAPI.getProjectOverview).not.toHaveBeenCalled()
  })

  it('refreshes every active aggregate consumer through the shared namespace', async () => {
    const { client, wrapper } = createQueryWrapper()
    const { result } = renderHook(
      () => ({
        overview: useWorkspaceOverview(params),
        facets: useTaskWorkbenchFacets(params),
        summaries: useProjectSummaries(params),
        project: useProjectOverview(project.id, params),
      }),
      { wrapper }
    )
    await waitFor(() => expect(result.current.project.isSuccess).toBe(true))
    await act(async () => {
      await client.invalidateQueries({ queryKey: workspaceQueryKeys.all })
    })

    expect(workspaceAPI.getWorkspaceOverview).toHaveBeenCalledTimes(2)
    expect(workspaceAPI.getTaskWorkbenchFacets).toHaveBeenCalledTimes(2)
    expect(workspaceAPI.getProjectSummaries).toHaveBeenCalledTimes(2)
    expect(workspaceAPI.getProjectOverview).toHaveBeenCalledTimes(2)
  })
})

function createQueryWrapper() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 30_000 } },
  })
  return {
    client,
    wrapper: ({ children }: PropsWithChildren) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    ),
  }
}
