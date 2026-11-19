import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  createWorkspaceQueryParams,
  getProjectOverview,
  getProjectSummaries,
  getTaskWorkbenchFacets,
  getWorkspaceOverview,
} from './workspace'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

const params = { date: '2026-11-19', timezone: 'Asia/Shanghai' }

describe('workspace aggregate API', () => {
  it('reads bounded overview and project summaries with explicit date/timezone', async () => {
    const overview = {
      facets: { today: 90 },
      today: [{ id: 'occurrence' }],
      partial: false,
    }
    const facets = { facets: { today: 90 } }
    const projects = {
      projects: [{ id: 'project', summary: { completion_percent: null } }],
    }
    const project = { project: projects.projects[0] }
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(jsonResponse(overview))
      .mockResolvedValueOnce(jsonResponse(facets))
      .mockResolvedValueOnce(jsonResponse(projects))
      .mockResolvedValueOnce(jsonResponse(project))
    vi.stubGlobal('fetch', fetchMock)

    await expect(
      getWorkspaceOverview({
        ...params,
        today_limit: 8,
        project_limit: 3,
        notes_limit: 2,
      })
    ).resolves.toEqual(overview)
    await expect(
      getTaskWorkbenchFacets({ ...params, project_id: 'project/a' })
    ).resolves.toEqual(facets)
    await expect(
      getProjectSummaries({
        ...params,
        kind: 'standard',
        horizon: 'long',
        status: 'active',
      })
    ).resolves.toEqual(projects)
    await expect(getProjectOverview('project/a', params)).resolves.toEqual(
      project
    )

    const overviewURL = requestURL(fetchMock, 0)
    expect(overviewURL.pathname).toBe('/api/workspace/overview')
    expect(Object.fromEntries(overviewURL.searchParams)).toEqual({
      ...params,
      today_limit: '8',
      project_limit: '3',
      notes_limit: '2',
    })
    expect(Object.fromEntries(requestURL(fetchMock, 1).searchParams)).toEqual({
      ...params,
      project_id: 'project/a',
    })
    const projectsURL = requestURL(fetchMock, 2)
    expect(projectsURL.pathname).toBe('/api/projects')
    expect(Object.fromEntries(projectsURL.searchParams)).toEqual({
      ...params,
      include: 'summary',
      kind: 'standard',
      horizon: 'long',
      status: 'active',
    })
    expect(requestURL(fetchMock, 3).pathname).toBe(
      '/api/projects/project%2Fa/overview'
    )
  })

  it('preserves unavailable notes and aggregate counts independently of bounded rows', async () => {
    const payload = {
      facets: { today: 50 },
      today: [{ id: 'only-visible-row' }],
      projects: [],
      notes: {
        state: 'unavailable',
        authority: 'platform_compatibility',
        items: null,
        error: { code: 'notes_unavailable', message: 'offline' },
      },
      partial: true,
    }
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>().mockResolvedValue(jsonResponse(payload))
    )

    const result = await getWorkspaceOverview(params)
    expect(result.facets.today).toBe(50)
    expect(result.today).toHaveLength(1)
    expect(result.notes.state).toBe('unavailable')
    expect(result.notes.items).toBeNull()
    expect(result.partial).toBe(true)
  })

  it('does not serialize workspace selection or unrelated structural properties', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValue(jsonResponse({ projects: [] }))
    vi.stubGlobal('fetch', fetchMock)
    const input = {
      ...params,
      workspace_id: 'another-workspace',
      unrelated: true,
    }

    await getProjectSummaries(input)
    expect(Object.fromEntries(requestURL(fetchMock, 0).searchParams)).toEqual({
      ...params,
      include: 'summary',
    })
  })

  it('propagates aggregate errors without loading legacy lists', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({
          error: { code: 'workspace_unavailable', message: 'offline' },
        }),
        { status: 503 }
      )
    )
    vi.stubGlobal('fetch', fetchMock)

    await expect(getWorkspaceOverview(params)).rejects.toMatchObject({
      status: 503,
      code: 'workspace_unavailable',
    })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('builds calendar dates in the requested zone across UTC and DST boundaries', () => {
    expect(
      createWorkspaceQueryParams(
        new Date('2026-11-18T17:00:00Z'),
        'Asia/Shanghai'
      )
    ).toEqual(params)
    expect(
      createWorkspaceQueryParams(
        new Date('2026-11-19T00:30:00Z'),
        'America/Los_Angeles'
      )
    ).toEqual({ date: '2026-11-18', timezone: 'America/Los_Angeles' })
    expect(
      createWorkspaceQueryParams(
        new Date('2026-03-08T06:59:00Z'),
        'America/New_York'
      )
    ).toEqual({ date: '2026-03-08', timezone: 'America/New_York' })
    expect(
      createWorkspaceQueryParams(
        new Date('2026-03-08T07:01:00Z'),
        'America/New_York'
      )
    ).toEqual({ date: '2026-03-08', timezone: 'America/New_York' })
  })
})

function jsonResponse(data: unknown) {
  return new Response(JSON.stringify({ data }), {
    headers: { 'Content-Type': 'application/json' },
  })
}

function requestURL(
  fetchMock: ReturnType<typeof vi.fn<typeof fetch>>,
  index: number
) {
  return new URL(String(fetchMock.mock.calls[index][0]), window.location.origin)
}
