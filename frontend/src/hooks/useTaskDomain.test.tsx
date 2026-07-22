import { act, renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { PropsWithChildren } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as taskDomainAPI from '../api/taskDomain'
import {
  taskDomainQueryKeys,
  useArchiveProjectMutation,
  useBlockOccurrenceMutation,
  useCalendarEntries,
  useCompleteProjectMutation,
  useCompleteOccurrenceMutation,
  useCreateTaskMutation,
  useCreateProjectMutation,
  useDeleteProjectMutation,
  useOccurrence,
  useOccurrences,
  useProject,
  useProjects,
  useTaskDefinition,
  useTaskDomainCapabilities,
  useTaskDefinitions,
  useRescheduleOccurrenceMutation,
  useRescheduleThisAndFollowingMutation,
  useUnblockOccurrenceMutation,
  useUpdateProjectMutation,
  useUpdateTaskDefinitionMutation,
} from './useTaskDomain'

vi.mock('../api/taskDomain', async () => {
  const actual =
    await vi.importActual<typeof import('../api/taskDomain')>(
      '../api/taskDomain'
    )
  return {
    ...actual,
    listProjects: vi.fn(),
    getProject: vi.fn(),
    createProject: vi.fn(),
    updateProject: vi.fn(),
    updateTaskDefinition: vi.fn(),
    createTaskDefinition: vi.fn(),
    rescheduleOccurrence: vi.fn(),
    rescheduleThisAndFollowing: vi.fn(),
    completeProject: vi.fn(),
    archiveProject: vi.fn(),
    deleteProject: vi.fn(),
    listTaskDefinitions: vi.fn(),
    getTaskDefinition: vi.fn(),
    listOccurrences: vi.fn(),
    getOccurrence: vi.fn(),
    getCalendarEntries: vi.fn(),
    getTaskDomainCapabilities: vi.fn(),
    publishTaskDefinition: vi.fn(),
    pauseTaskDefinition: vi.fn(),
    resumeTaskDefinition: vi.fn(),
    cancelTaskDefinition: vi.fn(),
    restoreTaskDefinition: vi.fn(),
    archiveTaskDefinition: vi.fn(),
    startOccurrence: vi.fn(),
    blockOccurrence: vi.fn(),
    unblockOccurrence: vi.fn(),
    completeOccurrence: vi.fn(),
    skipOccurrence: vi.fn(),
    cancelOccurrence: vi.fn(),
    reopenOccurrence: vi.fn(),
  }
})

describe('task-domain query hooks', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(taskDomainAPI.listProjects).mockResolvedValue([])
    vi.mocked(taskDomainAPI.getProject).mockResolvedValue(projectFixture)
    vi.mocked(taskDomainAPI.listTaskDefinitions).mockResolvedValue([])
    vi.mocked(taskDomainAPI.getTaskDefinition).mockResolvedValue(taskFixture)
    vi.mocked(taskDomainAPI.listOccurrences).mockResolvedValue([])
    vi.mocked(taskDomainAPI.getOccurrence).mockResolvedValue(occurrenceFixture)
    vi.mocked(taskDomainAPI.getCalendarEntries).mockResolvedValue([])
    vi.mocked(taskDomainAPI.getTaskDomainCapabilities).mockResolvedValue({
      model_version: 'v2',
      available: true,
    })
  })

  it('uses stable scalar query keys instead of caller object identity', () => {
    expect(
      taskDomainQueryKeys.projectList({
        status: 'active',
        kind: 'standard',
        horizon: 'short',
      })
    ).toEqual(
      taskDomainQueryKeys.projectList({
        horizon: 'short',
        kind: 'standard',
        status: 'active',
      })
    )
    expect(
      taskDomainQueryKeys.occurrenceList({
        to: '2026-08-01',
        from: '2026-07-01',
        task_id: 'task-1',
        execution_status: 'open',
      })
    ).toEqual([
      'task-domain',
      'occurrences',
      'list',
      'task-1',
      null,
      'open',
      '2026-07-01',
      '2026-08-01',
      null,
      null,
    ])
  })

  it('connects project, task, occurrence and calendar hooks to their API reads', async () => {
    const { wrapper } = createQueryWrapper()
    const { result } = renderHook(
      () => ({
        projects: useProjects({ kind: 'standard' }),
        project: useProject('project-1'),
        tasks: useTaskDefinitions({ project_id: 'project-1' }),
        task: useTaskDefinition('task-1'),
        occurrences: useOccurrences({ task_id: 'task-1' }),
        occurrence: useOccurrence('occurrence-1'),
        calendar: useCalendarEntries({
          from: '2026-07-01',
          to: '2026-08-01',
          timezone: 'Asia/Shanghai',
          project_id: 'project-1',
        }),
      }),
      { wrapper }
    )

    await waitFor(() => {
      expect(result.current.projects.isSuccess).toBe(true)
      expect(result.current.project.isSuccess).toBe(true)
      expect(result.current.tasks.isSuccess).toBe(true)
      expect(result.current.task.isSuccess).toBe(true)
      expect(result.current.occurrences.isSuccess).toBe(true)
      expect(result.current.occurrence.isSuccess).toBe(true)
      expect(result.current.calendar.isSuccess).toBe(true)
    })
    expect(taskDomainAPI.listProjects).toHaveBeenCalledWith({
      kind: 'standard',
    })
    expect(taskDomainAPI.getProject).toHaveBeenCalledWith('project-1')
    expect(taskDomainAPI.listTaskDefinitions).toHaveBeenCalledWith({
      project_id: 'project-1',
    })
    expect(taskDomainAPI.getTaskDefinition).toHaveBeenCalledWith('task-1')
    expect(taskDomainAPI.listOccurrences).toHaveBeenCalledWith({
      task_id: 'task-1',
    })
    expect(taskDomainAPI.getOccurrence).toHaveBeenCalledWith('occurrence-1')
    expect(taskDomainAPI.getCalendarEntries).toHaveBeenCalledWith({
      from: '2026-07-01',
      to: '2026-08-01',
      timezone: 'Asia/Shanghai',
      project_id: 'project-1',
    })
  })

  it('includes timezone in calendar cache identity', () => {
    const range = { from: '2026-07-01', to: '2026-08-01' }
    expect(
      taskDomainQueryKeys.calendarEntries({
        ...range,
        timezone: 'Asia/Shanghai',
      })
    ).not.toEqual(
      taskDomainQueryKeys.calendarEntries({
        ...range,
        timezone: 'America/New_York',
      })
    )
  })

  it('deduplicates the workspace capability query across consumers', async () => {
    const { wrapper } = createQueryWrapper()
    const { result } = renderHook(
      () => ({ first: useTaskDomainCapabilities(), second: useTaskDomainCapabilities() }),
      { wrapper }
    )

    await waitFor(() => expect(result.current.first.isSuccess).toBe(true))
    expect(result.current.second.data).toEqual({
      model_version: 'v2',
      available: true,
    })
    expect(taskDomainAPI.getTaskDomainCapabilities).toHaveBeenCalledTimes(1)
  })
})

describe('task-domain mutation hooks', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('passes expected revisions and invalidates only affected domain keys', async () => {
    const response: taskDomainAPI.TaskAggregateCommandResponse = {
      task_revision: 8,
      schedule_revision: 5,
      occurrence_revisions: { 'occurrence-1': 12 },
    }
    vi.mocked(taskDomainAPI.completeOccurrence).mockResolvedValue(response)
    const { client, wrapper } = createQueryWrapper()
    const invalidate = vi.spyOn(client, 'invalidateQueries')
    const { result } = renderHook(() => useCompleteOccurrenceMutation(), {
      wrapper,
    })
    const expectedRevisions = revisionsFixture()

    await act(async () => {
      await result.current.mutateAsync({
        projectID: 'project-1',
        taskID: 'task-1',
        occurrenceID: 'occurrence-1',
        expectedRevisions,
      })
    })

    expect(taskDomainAPI.completeOccurrence).toHaveBeenCalledWith(
      'occurrence-1',
      expectedRevisions
    )
    expect(invalidate.mock.calls.map(([filters]) => filters?.queryKey)).toEqual(
      [
        taskDomainQueryKeys.project('project-1'),
        taskDomainQueryKeys.task('task-1'),
        taskDomainQueryKeys.taskLists(),
        taskDomainQueryKeys.occurrenceLists(),
        taskDomainQueryKeys.occurrence('occurrence-1'),
        taskDomainQueryKeys.calendar(),
      ]
    )
  })

  it('updates a task definition and invalidates precise affected projections', async () => {
    const input: taskDomainAPI.UpdateTaskDefinitionInput = {
      expected_task_revision: 7,
      expected_schedule_revision: 5,
      title: 'Updated',
      sort_order: 1.25,
      project_id: 'project-2',
      roadmap_node_id: '',
      task_note_id: '',
    }
    vi.mocked(taskDomainAPI.updateTaskDefinition).mockResolvedValue({
      ...taskFixture,
      project_id: 'project-2',
      title: 'Updated',
      sort_order: 1.25,
      revision: 8,
    })
    const { client, wrapper } = createQueryWrapper()
    const invalidate = vi.spyOn(client, 'invalidateQueries')
    const { result } = renderHook(() => useUpdateTaskDefinitionMutation(), {
      wrapper,
    })

    await act(async () => {
      await result.current.mutateAsync({
        projectID: 'project-1',
        taskID: 'task-1',
        input,
      })
    })

    expect(taskDomainAPI.updateTaskDefinition).toHaveBeenCalledWith(
      'task-1',
      input
    )
    expect(invalidate.mock.calls.map(([filters]) => filters?.queryKey)).toEqual(
      [
        taskDomainQueryKeys.task('task-1'),
        taskDomainQueryKeys.taskLists(),
        taskDomainQueryKeys.project('project-1'),
        taskDomainQueryKeys.project('project-2'),
        taskDomainQueryKeys.occurrenceLists(),
        taskDomainQueryKeys.calendar(),
      ]
    )
  })

  it('combines block details with the same expected revisions', async () => {
    vi.mocked(taskDomainAPI.blockOccurrence).mockResolvedValue({
      task_revision: 8,
      occurrence_revisions: { 'occurrence-1': 12 },
    })
    const { wrapper } = createQueryWrapper()
    const { result } = renderHook(() => useBlockOccurrenceMutation(), {
      wrapper,
    })
    const expectedRevisions = revisionsFixture()

    await act(async () => {
      await result.current.mutateAsync({
        projectID: 'project-1',
        taskID: 'task-1',
        occurrenceID: 'occurrence-1',
        expectedRevisions,
        blockedReason: '等待评审',
        nextAction: '周五跟进',
      })
    })

    expect(taskDomainAPI.blockOccurrence).toHaveBeenCalledWith('occurrence-1', {
      ...expectedRevisions,
      blocked_reason: '等待评审',
      next_action: '周五跟进',
    })
  })

  it('creates a task in an explicit project and invalidates only affected projections', async () => {
    vi.mocked(taskDomainAPI.createTaskDefinition).mockResolvedValue({
      task: taskFixture,
      occurrences: [occurrenceFixture],
    })
    const { client, wrapper } = createQueryWrapper()
    const invalidate = vi.spyOn(client, 'invalidateQueries')
    const { result } = renderHook(() => useCreateTaskMutation(), { wrapper })
    const input: taskDomainAPI.CreateTaskDefinitionInput = {
      project_id: 'project-1',
      title: '整理方案',
      priority: 1,
      schedule: {
        recurrence_type: 'none',
        timing_type: 'unscheduled',
        timezone: 'Asia/Shanghai',
      },
    }

    await act(async () => {
      await result.current.mutateAsync(input)
    })

    expect(taskDomainAPI.createTaskDefinition).toHaveBeenCalledWith(input)
    expect(invalidate.mock.calls.map(([filters]) => filters?.queryKey)).toEqual([
      taskDomainQueryKeys.project('project-1'),
      taskDomainQueryKeys.taskLists(),
      taskDomainQueryKeys.occurrenceLists(),
      taskDomainQueryKeys.calendar(),
    ])
  })

  it('reschedules an occurrence without replacing cached local editor state on conflict', async () => {
    const conflict = new taskDomainAPI.TaskDomainRevisionConflictError(
      'occurrence changed',
      revisionsFixture(),
      { occurrence_revisions: { 'occurrence-1': 12 } }
    )
    vi.mocked(taskDomainAPI.rescheduleOccurrence).mockRejectedValue(conflict)
    const { client, wrapper } = createQueryWrapper()
    const localDraft = { planned_date: '2026-07-24' }
    client.setQueryData(
      taskDomainQueryKeys.occurrence('occurrence-1'),
      localDraft
    )
    const invalidate = vi.spyOn(client, 'invalidateQueries')
    const { result } = renderHook(() => useRescheduleOccurrenceMutation(), {
      wrapper,
    })

    let thrown: unknown
    await act(async () => {
      try {
        await result.current.mutateAsync({
          projectID: 'project-1',
          taskID: 'task-1',
          occurrenceID: 'occurrence-1',
          input: {
            expected_task_revision: 7,
            expected_schedule_revision: 5,
            expected_occurrence_revision: 11,
            timing: {
              timing_type: 'date',
              timezone: 'Asia/Shanghai',
              planned_date: '2026-07-24',
            },
          },
        })
      } catch (error) {
        thrown = error
      }
    })

    expect(thrown).toBe(conflict)
    expect(
      client.getQueryData(taskDomainQueryKeys.occurrence('occurrence-1'))
    ).toBe(localDraft)
    expect(invalidate).not.toHaveBeenCalled()
  })

  it('invalidates task, occurrence, and calendar projections after this-and-following', async () => {
    vi.mocked(taskDomainAPI.rescheduleThisAndFollowing).mockResolvedValue({
      task_revision: 8,
      schedule_revision: 6,
      schedule_version: 4,
    })
    const { client, wrapper } = createQueryWrapper()
    const invalidate = vi.spyOn(client, 'invalidateQueries')
    const { result } = renderHook(
      () => useRescheduleThisAndFollowingMutation(),
      { wrapper }
    )

    await act(async () => {
      await result.current.mutateAsync({
        projectID: 'project-1',
        taskID: 'task-1',
        input: {
          expected_task_revision: 7,
          expected_schedule_revision: 5,
          effective_from: '2026-07-24',
          generate_through_exclusive: '2026-08-24',
          schedule: {
            recurrence_type: 'daily',
            timing_type: 'date',
            timezone: 'Asia/Shanghai',
            starts_on: '2026-07-24',
            rule: { interval: 1 },
          },
        },
      })
    })

    expect(taskDomainAPI.rescheduleThisAndFollowing).toHaveBeenCalledWith(
      'task-1',
      expect.objectContaining({ expected_schedule_revision: 5 })
    )
    expect(invalidate.mock.calls.map(([filters]) => filters?.queryKey)).toEqual([
      taskDomainQueryKeys.project('project-1'),
      taskDomainQueryKeys.task('task-1'),
      taskDomainQueryKeys.taskLists(),
      taskDomainQueryKeys.occurrenceLists(),
      taskDomainQueryKeys.calendar(),
    ])
  })

  it('exposes unblock as an explicit occurrence command', async () => {
    vi.mocked(taskDomainAPI.unblockOccurrence).mockResolvedValue({
      task_revision: 8,
      occurrence_revisions: { 'occurrence-1': 12 },
    })
    const { wrapper } = createQueryWrapper()
    const { result } = renderHook(() => useUnblockOccurrenceMutation(), {
      wrapper,
    })
    const expectedRevisions = revisionsFixture()

    await act(async () => {
      await result.current.mutateAsync({
        projectID: 'project-1',
        taskID: 'task-1',
        occurrenceID: 'occurrence-1',
        expectedRevisions,
      })
    })

    expect(taskDomainAPI.unblockOccurrence).toHaveBeenCalledWith(
      'occurrence-1',
      expectedRevisions
    )
  })

  it('rethrows the exact 409 conflict and leaves cached editor data untouched', async () => {
    const expectedRevisions = revisionsFixture()
    const conflict = new taskDomainAPI.TaskDomainRevisionConflictError(
      '任务已被其他会话修改',
      expectedRevisions,
      { task_revision: 9, occurrence_revisions: { 'occurrence-1': 13 } }
    )
    vi.mocked(taskDomainAPI.completeOccurrence).mockRejectedValue(conflict)
    const { client, wrapper } = createQueryWrapper()
    const invalidate = vi.spyOn(client, 'invalidateQueries')
    const editorDraft = { title: '调用方尚未保存的标题' }
    client.setQueryData(taskDomainQueryKeys.task('task-1'), editorDraft)
    const { result } = renderHook(() => useCompleteOccurrenceMutation(), {
      wrapper,
    })

    let thrown: unknown
    await act(async () => {
      try {
        await result.current.mutateAsync({
          projectID: 'project-1',
          taskID: 'task-1',
          occurrenceID: 'occurrence-1',
          expectedRevisions,
        })
      } catch (error) {
        thrown = error
      }
    })

    expect(thrown).toBe(conflict)
    expect(thrown).toBeInstanceOf(taskDomainAPI.TaskDomainRevisionConflictError)
    expect(client.getQueryData(taskDomainQueryKeys.task('task-1'))).toBe(
      editorDraft
    )
    expect(invalidate).not.toHaveBeenCalled()
  })

  it('connects create, update, complete, archive, and delete project mutations', async () => {
    const expectedRevision = { expected_project_revision: 1 }
    const commandResponse: taskDomainAPI.ProjectCommandResponse = {
      project_id: 'project-1',
      project_revision: 2,
    }
    vi.mocked(taskDomainAPI.createProject).mockResolvedValue(projectFixture)
    vi.mocked(taskDomainAPI.updateProject).mockResolvedValue({
      ...projectFixture,
      name: 'Updated',
      revision: 2,
    })
    vi.mocked(taskDomainAPI.completeProject).mockResolvedValue({
      ...commandResponse,
      status: 'completed',
    })
    vi.mocked(taskDomainAPI.archiveProject).mockResolvedValue({
      ...commandResponse,
      status: 'archived',
    })
    vi.mocked(taskDomainAPI.deleteProject).mockResolvedValue({
      ...commandResponse,
      deleted: true,
    })
    const { wrapper } = createQueryWrapper()
    const create = renderHook(() => useCreateProjectMutation(), { wrapper })
    const update = renderHook(() => useUpdateProjectMutation(), { wrapper })
    const complete = renderHook(() => useCompleteProjectMutation(), {
      wrapper,
    })
    const archive = renderHook(() => useArchiveProjectMutation(), { wrapper })
    const remove = renderHook(() => useDeleteProjectMutation(), { wrapper })

    await act(async () => {
      await create.result.current.mutateAsync({
        name: 'Project',
        kind: 'standard',
        horizon: 'short',
      })
      await update.result.current.mutateAsync({
        projectID: 'project-1',
        input: {
          name: 'Updated',
          status: 'paused',
          expected_project_revision: 1,
        },
      })
      await complete.result.current.mutateAsync({
        projectID: 'project-1',
        expectedRevision,
      })
      await archive.result.current.mutateAsync({
        projectID: 'project-1',
        expectedRevision,
      })
      await remove.result.current.mutateAsync({
        projectID: 'project-1',
        expectedRevision,
      })
    })

    expect(taskDomainAPI.createProject).toHaveBeenCalledWith({
      name: 'Project',
      kind: 'standard',
      horizon: 'short',
    })
    expect(taskDomainAPI.updateProject).toHaveBeenCalledWith('project-1', {
      name: 'Updated',
      status: 'paused',
      expected_project_revision: 1,
    })
    expect(taskDomainAPI.completeProject).toHaveBeenCalledWith(
      'project-1',
      expectedRevision
    )
    expect(taskDomainAPI.archiveProject).toHaveBeenCalledWith(
      'project-1',
      expectedRevision
    )
    expect(taskDomainAPI.deleteProject).toHaveBeenCalledWith(
      'project-1',
      expectedRevision
    )
  })

  it('invalidates only project detail/list and affected list projections after project update', async () => {
    vi.mocked(taskDomainAPI.updateProject).mockResolvedValue({
      ...projectFixture,
      status: 'paused',
      revision: 2,
    })
    const { client, wrapper } = createQueryWrapper()
    const invalidate = vi.spyOn(client, 'invalidateQueries')
    const { result } = renderHook(() => useUpdateProjectMutation(), {
      wrapper,
    })

    await act(async () => {
      await result.current.mutateAsync({
        projectID: 'project-1',
        input: {
          status: 'paused',
          expected_project_revision: 1,
        },
      })
    })

    expect(invalidate.mock.calls.map(([filters]) => filters?.queryKey)).toEqual(
      [
        taskDomainQueryKeys.project('project-1'),
        taskDomainQueryKeys.projectLists(),
        taskDomainQueryKeys.taskLists(),
        taskDomainQueryKeys.occurrenceLists(),
        taskDomainQueryKeys.calendar(),
      ]
    )
    expect(invalidate).not.toHaveBeenCalledWith({
      queryKey: taskDomainQueryKeys.all,
    })
  })

  it('passes through the exact project 409 without optimistic overwrite or cache clearing', async () => {
    const expectedRevision = { expected_project_revision: 1 }
    const conflict = new taskDomainAPI.TaskDomainRevisionConflictError(
      'project changed',
      expectedRevision,
      { project_revision: 2 }
    )
    vi.mocked(taskDomainAPI.updateProject).mockRejectedValue(conflict)
    const { client, wrapper } = createQueryWrapper()
    const invalidate = vi.spyOn(client, 'invalidateQueries')
    const editorDraft = { ...projectFixture, name: 'Unsaved local name' }
    client.setQueryData(taskDomainQueryKeys.project('project-1'), editorDraft)
    const { result } = renderHook(() => useUpdateProjectMutation(), {
      wrapper,
    })

    let thrown: unknown
    await act(async () => {
      try {
        await result.current.mutateAsync({
          projectID: 'project-1',
          input: {
            name: 'Server update',
            expected_project_revision: 1,
          },
        })
      } catch (error) {
        thrown = error
      }
    })

    expect(thrown).toBe(conflict)
    expect(client.getQueryData(taskDomainQueryKeys.project('project-1'))).toBe(
      editorDraft
    )
    expect(invalidate).not.toHaveBeenCalled()
  })
})

function createQueryWrapper() {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
  return {
    client,
    wrapper: ({ children }: PropsWithChildren) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    ),
  }
}

function revisionsFixture(): taskDomainAPI.TaskDomainExpectedRevisions {
  return {
    expected_task_revision: 7,
    expected_schedule_revision: 5,
    expected_occurrence_revisions: { 'occurrence-1': 11 },
  }
}

const projectFixture: taskDomainAPI.ProjectV2 = {
  id: 'project-1',
  name: '项目',
  kind: 'standard',
  horizon: 'short',
  status: 'active',
  revision: 1,
}

const taskFixture: taskDomainAPI.TaskV2 = {
  id: 'task-1',
  project_id: 'project-1',
  title: '任务',
  priority: 1,
  sort_order: 1,
  lifecycle_status: 'active',
  revision: 1,
  schedule_revision: 1,
}

const occurrenceFixture: taskDomainAPI.OccurrenceV2 = {
  id: 'occurrence-1',
  task_id: 'task-1',
  occurrence_key: 'once',
  execution_status: 'open',
  revision: 1,
  generated_schedule_revision: 1,
}
