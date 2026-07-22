import { afterEach, describe, expect, it, vi } from 'vitest'
import * as taskDomain from './taskDomain'
import {
  TaskDomainRevisionConflictError,
  archiveProject,
  cancelOccurrence,
  cancelTaskDefinition,
  completeProject,
  completeOccurrence,
  createProject,
  createTaskDefinition,
  deleteProject,
  getCalendarEntries,
  getTaskDomainCapabilities,
  getOccurrence,
  getProject,
  getTaskDefinition,
  listOccurrences,
  listProjects,
  listTaskDefinitions,
  pauseTaskDefinition,
  publishTaskDefinition,
  reopenOccurrence,
  rescheduleOccurrence,
  rescheduleThisAndFollowing,
  restoreTaskDefinition,
  resumeTaskDefinition,
  skipOccurrence,
  startOccurrence,
  unblockOccurrence,
  updateProject,
  updateTaskDefinition,
  blockOccurrence,
  archiveTaskDefinition,
} from './taskDomain'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('task domain query client', () => {
  it('reads the workspace task-domain capability before v2 routes are mounted', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse({ model_version: 'v2', available: true })
    )
    vi.stubGlobal('fetch', fetchMock)

    await expect(getTaskDomainCapabilities()).resolves.toEqual({
      model_version: 'v2',
      available: true,
    })
    expect(requestPath(fetchMock, 0)).toBe('/api/task-domain/capabilities')
  })

  it('uses legacy only for an old backend 404 and never for a v2 runtime failure', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(jsonResponse({}, 404))
      .mockResolvedValueOnce(
        jsonResponse(
          {
            model_version: 'v2',
            available: false,
            error: {
              code: 'task_domain_runtime_unavailable',
              message: 'runtime unavailable',
              retryable: true,
            },
          },
          503
        )
      )
    vi.stubGlobal('fetch', fetchMock)

    await expect(getTaskDomainCapabilities()).resolves.toEqual({
      model_version: 'legacy',
      available: true,
    })
    await expect(getTaskDomainCapabilities()).rejects.toMatchObject({
      status: 503,
      code: 'task_domain_runtime_unavailable',
    })
  })

  it('queries project, task, occurrence, and calendar projections', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(
        jsonResponse({ data: { projects: [projectFixture()] } })
      )
      .mockResolvedValueOnce(
        jsonResponse({ data: { project: projectFixture() } })
      )
      .mockResolvedValueOnce(jsonResponse({ data: { tasks: [taskFixture()] } }))
      .mockResolvedValueOnce(jsonResponse({ data: { task: taskFixture() } }))
      .mockResolvedValueOnce(
        jsonResponse({ data: { occurrences: [occurrenceFixture()] } })
      )
      .mockResolvedValueOnce(
        jsonResponse({ data: { occurrence: occurrenceFixture() } })
      )
      .mockResolvedValueOnce(
        jsonResponse({ data: { entries: [calendarEntryFixture()] } })
      )
    vi.stubGlobal('fetch', fetchMock)

    expect((await listProjects())[0]?.revision).toBe(3)
    expect((await getProject('project-1')).id).toBe('project-1')
    expect(
      (await listTaskDefinitions({ project_id: 'project-1' }))[0]?.task_note_id
    ).toBe('task-note-1')
    expect((await getTaskDefinition('task-1')).revision).toBe(4)
    expect(
      (await listOccurrences({ task_id: 'task-1' }))[0]?.occurrence_note_id
    ).toBe('occurrence-note-1')
    expect(
      (await getOccurrence('occurrence-1')).generated_schedule_revision
    ).toBe(8)
    const calendar = await getCalendarEntries({
      from: '2026-07-01',
      to: '2026-07-31',
      timezone: 'Asia/Shanghai',
      project_id: 'project-1',
    })
    expect(calendar[0]?.task_note_id).toBe('task-note-1')
    expect(calendar[0]?.occurrence_note_id).toBe('occurrence-note-1')

    expect(requestPath(fetchMock, 0)).toBe('/api/projects')
    expect(requestPath(fetchMock, 1)).toBe('/api/projects/project-1')
    expect(requestPath(fetchMock, 2)).toBe('/api/tasks?project_id=project-1')
    expect(requestPath(fetchMock, 3)).toBe('/api/tasks/task-1')
    expect(requestPath(fetchMock, 4)).toBe(
      '/api/task-occurrences?task_id=task-1'
    )
    expect(requestPath(fetchMock, 5)).toBe('/api/task-occurrences/occurrence-1')
    expect(requestPath(fetchMock, 6)).toBe(
      '/api/calendar/entries?from=2026-07-01&to=2026-07-31&timezone=Asia%2FShanghai&project_id=project-1'
    )
  })

  it('round-trips decimal sort order values without coercion', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse({ data: { task: { ...taskFixture(), sort_order: 1.25 } } })
    )
    vi.stubGlobal('fetch', fetchMock)

    const task = await getTaskDefinition('task-1')

    expect(task.sort_order).toBe(1.25)
  })

  it('creates a task definition and schedule without an ambiguous note_id', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse({
        data: { task: taskFixture(), occurrences: [occurrenceFixture()] },
      })
    )
    vi.stubGlobal('fetch', fetchMock)

    await createTaskDefinition({
      project_id: 'project-1',
      task_note_id: 'task-note-1',
      title: '每日复盘',
      description: '记录进展与阻塞',
      priority: 1,
      schedule: {
        recurrence_type: 'daily',
        timing_type: 'time_block',
        timezone: 'Asia/Shanghai',
        starts_on: '2026-07-21',
        local_start_time: '21:00:00',
        duration_minutes: 30,
        rule: { interval: 1 },
      },
    })

    expect(requestPath(fetchMock, 0)).toBe('/api/tasks')
    expect(requestInit(fetchMock, 0).method).toBe('POST')
    const body = requestBody(fetchMock, 0)
    expect(body.task_note_id).toBe('task-note-1')
    expect(body).not.toHaveProperty('note_id')
    expect(body.schedule).toMatchObject({
      recurrence_type: 'daily',
      rule: { interval: 1 },
    })
  })

  it('queries occurrence projections by scope and recurring dimension', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse({ data: { occurrences: [occurrenceFixture()] } })
    )
    vi.stubGlobal('fetch', fetchMock)

    await listOccurrences({
      project_id: 'project-1',
      scope: 'upcoming',
      recurring: true,
      execution_status: 'active',
    })

    expect(requestPath(fetchMock, 0)).toBe(
      '/api/task-occurrences?project_id=project-1&execution_status=active&scope=upcoming&recurring=true'
    )
  })

  it('rejects task creation without a project before sending a request', async () => {
    const fetchMock = vi.fn<typeof fetch>()
    vi.stubGlobal('fetch', fetchMock)

    await expect(
      createTaskDefinition({
        project_id: '   ',
        title: '没有归属的任务',
        priority: 0,
        schedule: {
          recurrence_type: 'none',
          timing_type: 'unscheduled',
          timezone: 'Asia/Shanghai',
        },
      })
    ).rejects.toThrow('project_id is required')
    expect(fetchMock).not.toHaveBeenCalled()
  })
})

describe('occurrence reschedule client', () => {
  it('patches timing with task, schedule, and occurrence revisions', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse({ data: commandResultFixture() })
    )
    vi.stubGlobal('fetch', fetchMock)

    await rescheduleOccurrence('occurrence-1', {
      expected_task_revision: 4,
      expected_schedule_revision: 8,
      expected_occurrence_revision: 9,
      timing: {
        timing_type: 'date',
        timezone: 'Asia/Shanghai',
        planned_date: '2026-07-24',
      },
    })

    expect(requestPath(fetchMock, 0)).toBe(
      '/api/task-occurrences/occurrence-1/schedule/only-this'
    )
    expect(requestInit(fetchMock, 0).method).toBe('PATCH')
    expect(requestBody(fetchMock, 0)).toEqual({
      expected_task_revision: 4,
      expected_schedule_revision: 8,
      expected_occurrence_revision: 9,
      timing: {
        timing_type: 'date',
        timezone: 'Asia/Shanghai',
        planned_date: '2026-07-24',
      },
    })
  })

  it('creates a new immutable schedule version for this-and-following', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse({
        data: { task_revision: 5, schedule_revision: 9, schedule_version: 4 },
      })
    )
    vi.stubGlobal('fetch', fetchMock)

    await rescheduleThisAndFollowing('task-1', {
      expected_task_revision: 4,
      expected_schedule_revision: 8,
      effective_from: '2026-07-24',
      generate_through_exclusive: '2026-08-24',
      schedule: {
        recurrence_type: 'daily',
        timing_type: 'date',
        timezone: 'Asia/Shanghai',
        starts_on: '2026-07-24',
        rule: { interval: 1 },
      },
    })

    expect(requestPath(fetchMock, 0)).toBe(
      '/api/tasks/task-1/schedule/this-and-following'
    )
    expect(requestInit(fetchMock, 0).method).toBe('POST')
    expect(requestBody(fetchMock, 0)).toEqual({
      expected_task_revision: 4,
      expected_schedule_revision: 8,
      effective_from: '2026-07-24',
      generate_through_exclusive: '2026-08-24',
      schedule: {
        recurrence_type: 'daily',
        timing_type: 'date',
        timezone: 'Asia/Shanghai',
        starts_on: '2026-07-24',
        rule: { interval: 1 },
      },
    })
  })

  it('surfaces current revisions on a reschedule conflict', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>().mockResolvedValue(
        jsonResponse(
          {
            error: {
              code: 'revision_conflict',
              message: 'occurrence changed',
              details: {
                current_revisions: {
                  task_revision: 5,
                  schedule_revision: 8,
                  occurrence_revisions: { 'occurrence-1': 10 },
                },
              },
            },
          },
          409
        )
      )
    )
    const expected = {
      expected_task_revision: 4,
      expected_schedule_revision: 8,
      expected_occurrence_revision: 9,
      timing: {
        timing_type: 'date' as const,
        timezone: 'Asia/Shanghai',
        planned_date: '2026-07-24',
      },
    }

    const error = await rescheduleOccurrence('occurrence-1', expected).catch(
      (caught: unknown) => caught
    )

    expect(error).toBeInstanceOf(TaskDomainRevisionConflictError)
    expect(error).toMatchObject({
      expectedRevisions: {
        expected_task_revision: 4,
        expected_schedule_revision: 8,
        expected_occurrence_revisions: { 'occurrence-1': 9 },
      },
      currentRevisions: {
        task_revision: 5,
        schedule_revision: 8,
        occurrence_revisions: { 'occurrence-1': 10 },
      },
    })
  })
})

describe('task definition update client', () => {
  it('patches only mutable fields and preserves decimal sort order', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse({ data: { task: { ...taskFixture(), sort_order: 2.75 } } })
    )
    vi.stubGlobal('fetch', fetchMock)

    const task = await updateTaskDefinition('task-1', {
      expected_task_revision: 4,
      expected_schedule_revision: 8,
      title: 'Updated task',
      description: '',
      priority: 2,
      sort_order: 2.75,
      project_id: 'project-2',
      roadmap_node_id: '',
      task_note_id: '',
      lifecycle_status: 'cancelled',
      schedule: { timing_type: 'unscheduled' },
      execution_status: 'done',
    } as taskDomain.UpdateTaskDefinitionInput & Record<string, unknown>)

    expect(task.sort_order).toBe(2.75)
    expect(requestPath(fetchMock, 0)).toBe('/api/tasks/task-1')
    expect(requestInit(fetchMock, 0).method).toBe('PATCH')
    expect(requestBody(fetchMock, 0)).toEqual({
      expected_task_revision: 4,
      expected_schedule_revision: 8,
      title: 'Updated task',
      description: '',
      priority: 2,
      sort_order: 2.75,
      project_id: 'project-2',
      roadmap_node_id: '',
      task_note_id: '',
    })
  })

  it('omits undefined mutable fields and rejects missing expected revisions before fetch', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValue(jsonResponse({ data: { task: taskFixture() } }))
    vi.stubGlobal('fetch', fetchMock)

    await updateTaskDefinition('task-1', {
      expected_task_revision: 4,
      expected_schedule_revision: 8,
      title: undefined,
      roadmap_node_id: undefined,
      task_note_id: undefined,
    })
    expect(requestBody(fetchMock, 0)).toEqual({
      expected_task_revision: 4,
      expected_schedule_revision: 8,
    })

    await expect(
      updateTaskDefinition('task-1', {
        expected_task_revision: 4,
      } as taskDomain.UpdateTaskDefinitionInput)
    ).rejects.toBeInstanceOf(TypeError)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('preserves task and schedule revisions on a 409', async () => {
    const expected = {
      expected_task_revision: 4,
      expected_schedule_revision: 8,
    }
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>().mockResolvedValue(
        jsonResponse(
          {
            error: {
              code: 'revision_conflict',
              message: 'task changed',
              details: {
                current_revisions: {
                  task_revision: 5,
                  schedule_revision: 9,
                },
              },
            },
          },
          409
        )
      )
    )

    const error = await updateTaskDefinition('task-1', expected).catch(
      (caught: unknown) => caught
    )

    expect(error).toBeInstanceOf(TaskDomainRevisionConflictError)
    expect(error).toMatchObject({
      expectedRevisions: expected,
      currentRevisions: { task_revision: 5, schedule_revision: 9 },
    })
  })

  it('does not expose lifecycle, schedule, or execution fields in the patch type', () => {
    const input: taskDomain.UpdateTaskDefinitionInput = {
      expected_task_revision: 4,
      expected_schedule_revision: 8,
      // @ts-expect-error lifecycle transitions require explicit commands
      lifecycle_status: 'paused',
    }
    expect((input as unknown as Record<string, unknown>).lifecycle_status).toBe(
      'paused'
    )
  })
})

describe('task lifecycle command client', () => {
  const revisions = {
    expected_task_revision: 4,
    expected_schedule_revision: 8,
    expected_occurrence_revisions: { 'occurrence-1': 9 },
  }

  it.each([
    ['publish', publishTaskDefinition],
    ['pause', pauseTaskDefinition],
    ['resume', resumeTaskDefinition],
    ['cancel', cancelTaskDefinition],
    ['restore', restoreTaskDefinition],
    ['archive', archiveTaskDefinition],
  ] as const)(
    'posts the explicit %s command with all expected revisions',
    async (command, invoke) => {
      const fetchMock = vi
        .fn<typeof fetch>()
        .mockResolvedValue(jsonResponse({ data: commandResultFixture() }))
      vi.stubGlobal('fetch', fetchMock)

      const result = await invoke('task-1', revisions)

      expect(requestPath(fetchMock, 0)).toBe(`/api/tasks/task-1/${command}`)
      expect(requestInit(fetchMock, 0).method).toBe('POST')
      expect(requestBody(fetchMock, 0)).toEqual(revisions)
      expect(result.task_revision).toBe(5)
      expect(result.occurrence_revisions['occurrence-1']).toBe(10)
    }
  )

  it('does not expose a lifecycle PATCH escape hatch', () => {
    expect(taskDomain).not.toHaveProperty('patchTaskLifecycle')
    expect(taskDomain).not.toHaveProperty('updateTaskLifecycle')
  })
})

describe('project mutation client', () => {
  const expectedRevision = { expected_project_revision: 3 }

  it('creates planning projects by default and permits explicit active status', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(
        jsonResponse({ data: { project: projectFixture() } })
      )
      .mockResolvedValueOnce(
        jsonResponse({ data: { project: projectFixture() } })
      )
    vi.stubGlobal('fetch', fetchMock)

    await createProject({
      name: 'Project one',
      kind: 'standard',
      horizon: 'short',
    })
    await createProject({
      name: 'Project two',
      kind: 'learning',
      horizon: 'long',
      status: 'active',
    })

    expect(requestPath(fetchMock, 0)).toBe('/api/projects')
    expect(requestInit(fetchMock, 0).method).toBe('POST')
    expect(requestBody(fetchMock, 0)).toEqual({
      name: 'Project one',
      kind: 'standard',
      horizon: 'short',
      status: 'planning',
    })
    expect(requestBody(fetchMock, 1).status).toBe('active')
  })

  it('updates only mutable fields and always carries the project revision', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValue(jsonResponse({ data: { project: projectFixture() } }))
    vi.stubGlobal('fetch', fetchMock)

    await updateProject('project-1', {
      name: 'Updated project',
      kind: 'learning',
      horizon: 'long',
      status: 'paused',
      expected_project_revision: 3,
      id: 'injected-id',
      workspace_id: 'injected-workspace',
      system_role: 'inbox',
    } as taskDomain.UpdateProjectInput & Record<string, unknown>)

    expect(requestPath(fetchMock, 0)).toBe('/api/projects/project-1')
    expect(requestInit(fetchMock, 0).method).toBe('PATCH')
    expect(requestBody(fetchMock, 0)).toEqual({
      name: 'Updated project',
      kind: 'learning',
      horizon: 'long',
      status: 'paused',
      expected_project_revision: 3,
    })
  })

  it('never sends completed or archived through update and exposes no status patch escape hatch', async () => {
    const fetchMock = vi.fn<typeof fetch>()
    vi.stubGlobal('fetch', fetchMock)

    await expect(
      updateProject('project-1', {
        status: 'completed',
        expected_project_revision: 3,
      } as unknown as taskDomain.UpdateProjectInput)
    ).rejects.toBeInstanceOf(TypeError)
    await expect(
      updateProject('project-1', {
        status: 'archived',
        expected_project_revision: 3,
      } as unknown as taskDomain.UpdateProjectInput)
    ).rejects.toBeInstanceOf(TypeError)

    expect(fetchMock).not.toHaveBeenCalled()
    expect(taskDomain).not.toHaveProperty('patchProjectStatus')
    expect(taskDomain).not.toHaveProperty('updateProjectStatus')
  })

  it.each([
    ['complete', completeProject],
    ['archive', archiveProject],
  ] as const)(
    'posts the explicit %s command with an independent project revision',
    async (command, invoke) => {
      const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
        jsonResponse({
          data: {
            project_id: 'project-1',
            project_revision: 4,
            status: command === 'complete' ? 'completed' : 'archived',
          },
        })
      )
      vi.stubGlobal('fetch', fetchMock)

      const response = await invoke('project-1', expectedRevision)

      expect(requestPath(fetchMock, 0)).toBe(
        `/api/projects/project-1/${command}`
      )
      expect(requestInit(fetchMock, 0).method).toBe('POST')
      expect(requestBody(fetchMock, 0)).toEqual(expectedRevision)
      expect(response.project_revision).toBe(4)
    }
  )

  it('deletes with a JSON expected revision body', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse({
        data: {
          project_id: 'project-1',
          project_revision: 4,
          deleted: true,
        },
      })
    )
    vi.stubGlobal('fetch', fetchMock)

    const response = await deleteProject('project-1', expectedRevision)

    expect(requestPath(fetchMock, 0)).toBe('/api/projects/project-1')
    expect(requestInit(fetchMock, 0).method).toBe('DELETE')
    expect(requestBody(fetchMock, 0)).toEqual(expectedRevision)
    expect(response.deleted).toBe(true)
  })

  it('preserves the independent expected project revision on a 409', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse(
        {
          error: {
            code: 'revision_conflict',
            message: 'project changed',
            details: { current_revisions: { project_revision: 4 } },
          },
        },
        409
      )
    )
    vi.stubGlobal('fetch', fetchMock)

    const error = await completeProject('project-1', expectedRevision).catch(
      (caught: unknown) => caught
    )

    expect(error).toBeInstanceOf(TaskDomainRevisionConflictError)
    expect(error).toMatchObject({
      expectedRevisions: expectedRevision,
      currentRevisions: { project_revision: 4 },
    })
  })
})

describe('occurrence command client', () => {
  const revisions = {
    expected_task_revision: 4,
    expected_schedule_revision: 8,
    expected_occurrence_revisions: { 'occurrence-1': 9 },
  }

  it.each([
    ['start', startOccurrence],
    ['unblock', unblockOccurrence],
    ['complete', completeOccurrence],
    ['skip', skipOccurrence],
    ['cancel', cancelOccurrence],
    ['reopen', reopenOccurrence],
  ] as const)(
    'posts the explicit %s command with aggregate revisions',
    async (command, invoke) => {
      const fetchMock = vi
        .fn<typeof fetch>()
        .mockResolvedValue(jsonResponse({ data: commandResultFixture() }))
      vi.stubGlobal('fetch', fetchMock)

      await invoke('occurrence-1', revisions)

      expect(requestPath(fetchMock, 0)).toBe(
        `/api/task-occurrences/occurrence-1/${command}`
      )
      expect(requestBody(fetchMock, 0)).toEqual(revisions)
    }
  )

  it('sends block metadata alongside all aggregate revisions', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValue(jsonResponse({ data: commandResultFixture() }))
    vi.stubGlobal('fetch', fetchMock)

    await blockOccurrence('occurrence-1', {
      ...revisions,
      blocked_reason: '等待评审',
      next_action: '提醒评审人',
    })

    expect(requestPath(fetchMock, 0)).toBe(
      '/api/task-occurrences/occurrence-1/block'
    )
    expect(requestBody(fetchMock, 0)).toEqual({
      ...revisions,
      blocked_reason: '等待评审',
      next_action: '提醒评审人',
    })
  })
})

describe('revision conflict contract', () => {
  it('preserves the expected revisions and server current revisions from a 409', async () => {
    const expected = {
      expected_task_revision: 4,
      expected_schedule_revision: 8,
      expected_occurrence_revisions: { 'occurrence-1': 9 },
    }
    const current = {
      task_revision: 5,
      schedule_revision: 8,
      occurrence_revisions: { 'occurrence-1': 10 },
    }
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>().mockResolvedValue(
        jsonResponse(
          {
            error: {
              code: 'revision_conflict',
              message: 'the resource changed',
              details: { current_revisions: current },
            },
          },
          409
        )
      )
    )

    const error = await publishTaskDefinition('task-1', expected).catch(
      (caught: unknown) => caught
    )

    expect(error).toBeInstanceOf(TaskDomainRevisionConflictError)
    expect(error).toMatchObject({
      status: 409,
      code: 'revision_conflict',
      expectedRevisions: expected,
      currentRevisions: current,
    })
  })
})

function projectFixture() {
  return {
    id: 'project-1',
    name: 'FlowSpace',
    kind: 'standard' as const,
    horizon: 'long' as const,
    status: 'active' as const,
    revision: 3,
  }
}

function taskFixture() {
  return {
    id: 'task-1',
    project_id: 'project-1',
    task_note_id: 'task-note-1',
    title: '每日复盘',
    priority: 1,
    sort_order: 0,
    lifecycle_status: 'active' as const,
    revision: 4,
    schedule_revision: 8,
  }
}

function occurrenceFixture() {
  return {
    id: 'occurrence-1',
    task_id: 'task-1',
    occurrence_key: '2026-07-22',
    task_note_id: 'task-note-1',
    occurrence_note_id: 'occurrence-note-1',
    execution_status: 'open' as const,
    revision: 9,
    generated_schedule_revision: 8,
  }
}

function calendarEntryFixture() {
  return {
    project_id: 'project-1',
    project_revision: 3,
    task_id: 'task-1',
    task_revision: 4,
    task_title: '每日复盘',
    task_note_id: 'task-note-1',
    occurrence_id: 'occurrence-1',
    occurrence_revision: 9,
    generated_schedule_revision: 8,
    occurrence_note_id: 'occurrence-note-1',
    execution_status: 'open' as const,
    timing_type: 'date' as const,
    planned_date: '2026-07-22',
  }
}

function commandResultFixture() {
  return {
    task_revision: 5,
    schedule_revision: 8,
    occurrence_revisions: { 'occurrence-1': 10 },
  }
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function requestPath(
  fetchMock: ReturnType<typeof vi.fn<typeof fetch>>,
  index: number
) {
  const input = fetchMock.mock.calls[index]?.[0]
  const url =
    typeof input === 'string'
      ? input
      : input instanceof URL
        ? input.toString()
        : (input?.url ?? '')
  return (
    new URL(url, window.location.origin).pathname +
    new URL(url, window.location.origin).search
  )
}

function requestInit(
  fetchMock: ReturnType<typeof vi.fn<typeof fetch>>,
  index: number
) {
  return fetchMock.mock.calls[index]?.[1] ?? {}
}

function requestBody(
  fetchMock: ReturnType<typeof vi.fn<typeof fetch>>,
  index: number
) {
  const body = requestInit(fetchMock, index).body
  if (typeof body !== 'string') throw new Error('request body is not JSON text')
  return JSON.parse(body) as Record<string, any>
}
