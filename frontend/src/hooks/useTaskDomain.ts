import {
  useMutation,
  useQuery,
  useQueryClient,
  type QueryClient,
  type QueryKey,
} from '@tanstack/react-query'
import { useMemo } from 'react'

import * as taskDomainAPI from '../api/taskDomain'

export interface ProjectListParams {
  kind?: taskDomainAPI.ProjectKind
  horizon?: taskDomainAPI.ProjectHorizon
  status?: taskDomainAPI.ProjectStatus
}

export interface TaskListParams {
  project_id?: string
  lifecycle_status?: taskDomainAPI.TaskLifecycleStatus
}

export type OccurrenceListParams = taskDomainAPI.OccurrenceListParams

export interface CalendarEntriesParams {
  from: string
  to: string
  timezone: string
  project_id?: string
}

const taskDomainRootKey = ['task-domain'] as const

export const taskDomainQueryKeys = {
  all: taskDomainRootKey,
  capabilities: () => [...taskDomainRootKey, 'capabilities'] as const,
  projects: () => [...taskDomainRootKey, 'projects'] as const,
  projectLists: () => [...taskDomainRootKey, 'projects', 'list'] as const,
  projectList: (params: ProjectListParams = {}) =>
    [
      ...taskDomainRootKey,
      'projects',
      'list',
      params.kind ?? null,
      params.horizon ?? null,
      params.status ?? null,
    ] as const,
  project: (projectID: string) =>
    [...taskDomainRootKey, 'projects', 'detail', projectID] as const,
  tasks: () => [...taskDomainRootKey, 'tasks'] as const,
  taskLists: () => [...taskDomainRootKey, 'tasks', 'list'] as const,
  taskList: (params: TaskListParams = {}) =>
    [
      ...taskDomainRootKey,
      'tasks',
      'list',
      params.project_id ?? null,
      params.lifecycle_status ?? null,
    ] as const,
  task: (taskID: string) =>
    [...taskDomainRootKey, 'tasks', 'detail', taskID] as const,
  occurrences: () => [...taskDomainRootKey, 'occurrences'] as const,
  occurrenceLists: () => [...taskDomainRootKey, 'occurrences', 'list'] as const,
  occurrenceList: (params: OccurrenceListParams = {}) =>
    [
      ...taskDomainRootKey,
      'occurrences',
      'list',
      params.task_id ?? null,
      params.project_id ?? null,
      params.execution_status ?? null,
      params.from ?? null,
      params.to ?? null,
      params.timezone ?? null,
      params.scope ?? null,
      params.recurring ?? null,
    ] as const,
  occurrence: (occurrenceID: string) =>
    [...taskDomainRootKey, 'occurrences', 'detail', occurrenceID] as const,
  calendar: () => [...taskDomainRootKey, 'calendar'] as const,
  calendarEntries: (params: CalendarEntriesParams) =>
    [
      ...taskDomainRootKey,
      'calendar',
      'entries',
      params.from,
      params.to,
      params.timezone,
      params.project_id ?? null,
    ] as const,
}

export function useTaskDomainCapabilities() {
  return useQuery({
    queryKey: taskDomainQueryKeys.capabilities(),
    queryFn: taskDomainAPI.getTaskDomainCapabilities,
    staleTime: 30_000,
  })
}

export function useProjects(params: ProjectListParams = {}) {
  return useQuery({
    queryKey: taskDomainQueryKeys.projectList(params),
    queryFn: () => taskDomainAPI.listProjects(params),
  })
}

export function useProject(projectID: string) {
  return useQuery({
    queryKey: taskDomainQueryKeys.project(projectID),
    queryFn: () => taskDomainAPI.getProject(projectID),
    enabled: projectID !== '',
  })
}

export function useTaskDefinitions(params: TaskListParams = {}) {
  return useQuery({
    queryKey: taskDomainQueryKeys.taskList(params),
    queryFn: () => taskDomainAPI.listTaskDefinitions(params),
  })
}

export function useTaskDefinition(taskID: string) {
  return useQuery({
    queryKey: taskDomainQueryKeys.task(taskID),
    queryFn: () => taskDomainAPI.getTaskDefinition(taskID),
    enabled: taskID !== '',
  })
}

export function useOccurrences(
  params: OccurrenceListParams = {},
  options: { enabled?: boolean } = {}
) {
  const resolvedParams = useMemo(
    () => taskDomainAPI.resolveOccurrenceListParams(params),
    [
      params.task_id,
      params.project_id,
      params.execution_status,
      params.from,
      params.to,
      params.timezone,
      params.scope,
      params.recurring,
    ]
  )
  return useQuery({
    queryKey: taskDomainQueryKeys.occurrenceList(resolvedParams),
    queryFn: () => taskDomainAPI.listOccurrences(resolvedParams),
    enabled: options.enabled ?? true,
  })
}

export function useOccurrence(occurrenceID: string) {
  return useQuery({
    queryKey: taskDomainQueryKeys.occurrence(occurrenceID),
    queryFn: () => taskDomainAPI.getOccurrence(occurrenceID),
    enabled: occurrenceID !== '',
  })
}

export function useCalendarEntries(params: CalendarEntriesParams) {
  return useQuery({
    queryKey: taskDomainQueryKeys.calendarEntries(params),
    queryFn: () => taskDomainAPI.getCalendarEntries(params),
    enabled: params.from !== '' && params.to !== '',
  })
}

export interface UpdateProjectVariables {
  projectID: string
  input: taskDomainAPI.UpdateProjectInput
}

export interface ProjectCommandVariables {
  projectID: string
  expectedRevision: taskDomainAPI.ProjectExpectedRevision
}

type ProjectCommand = (
  projectID: string,
  expectedRevision: taskDomainAPI.ProjectExpectedRevision
) => Promise<taskDomainAPI.ProjectCommandResponse>

export function useCreateProjectMutation() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (input: taskDomainAPI.CreateProjectInput) =>
      taskDomainAPI.createProject(input),
    onSuccess: (project) => invalidateCreatedProject(queryClient, project.id),
  })
}

export function useUpdateProjectMutation() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (variables: UpdateProjectVariables) =>
      taskDomainAPI.updateProject(variables.projectID, variables.input),
    onSuccess: (_project, variables) =>
      invalidateAffectedProjectDomain(queryClient, variables.projectID),
  })
}

function useProjectCommandMutation(command: ProjectCommand) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (variables: ProjectCommandVariables) =>
      command(variables.projectID, variables.expectedRevision),
    onSuccess: (_response, variables) =>
      invalidateAffectedProjectDomain(queryClient, variables.projectID),
  })
}

export function useCompleteProjectMutation() {
  return useProjectCommandMutation(taskDomainAPI.completeProject)
}

export function useActivateProjectMutation() {
  return useProjectCommandMutation(taskDomainAPI.activateProject)
}

export function useArchiveProjectMutation() {
  return useProjectCommandMutation(taskDomainAPI.archiveProject)
}

export function useDeleteProjectMutation() {
  return useProjectCommandMutation(taskDomainAPI.deleteProject)
}

export interface TaskCommandVariables {
  projectID: string
  taskID: string
  expectedRevisions: taskDomainAPI.TaskDomainExpectedRevisions
}

export interface RescheduleOccurrenceVariables {
  projectID: string
  taskID: string
  occurrenceID: string
  input: taskDomainAPI.RescheduleOccurrenceInput
}

export interface RescheduleThisAndFollowingVariables {
  projectID: string
  taskID: string
  input: taskDomainAPI.RescheduleThisAndFollowingInput
}

export interface UpdateTaskDefinitionVariables {
  projectID: string
  taskID: string
  input: taskDomainAPI.UpdateTaskDefinitionInput
}

export interface OccurrenceCommandVariables extends TaskCommandVariables {
  occurrenceID: string
}

export interface BlockOccurrenceVariables extends OccurrenceCommandVariables {
  blockedReason: string
  nextAction: string
}

type TaskCommand = (
  taskID: string,
  revisions: taskDomainAPI.TaskDomainExpectedRevisions
) => Promise<taskDomainAPI.TaskAggregateCommandResponse>

type OccurrenceCommand = (
  occurrenceID: string,
  revisions: taskDomainAPI.TaskDomainExpectedRevisions
) => Promise<taskDomainAPI.TaskAggregateCommandResponse>

interface OccurrenceCommandContext {
  snapshots: Array<[QueryKey, unknown]>
}

export function useUpdateTaskDefinitionMutation() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (variables: UpdateTaskDefinitionVariables) =>
      taskDomainAPI.updateTaskDefinition(variables.taskID, variables.input),
    onSuccess: (_task, variables) =>
      invalidateUpdatedTaskDefinition(queryClient, variables),
  })
}

export function useCreateTaskMutation() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (input: taskDomainAPI.CreateTaskDefinitionInput) =>
      taskDomainAPI.createTaskDefinition(input),
    onSuccess: async (_response, input) => {
      const queryKeys: ReadonlyArray<readonly unknown[]> = [
        taskDomainQueryKeys.project(input.project_id),
        taskDomainQueryKeys.taskLists(),
        taskDomainQueryKeys.occurrenceLists(),
        taskDomainQueryKeys.calendar(),
      ]
      await Promise.all(
        queryKeys.map((queryKey) => queryClient.invalidateQueries({ queryKey }))
      )
    },
  })
}

export function useRescheduleOccurrenceMutation() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (variables: RescheduleOccurrenceVariables) =>
      taskDomainAPI.rescheduleOccurrence(
        variables.occurrenceID,
        variables.input
      ),
    onSuccess: (response, variables) =>
      invalidateAffectedTaskDomain(
        queryClient,
        {
          projectID: variables.projectID,
          taskID: variables.taskID,
          occurrenceID: variables.occurrenceID,
          expectedRevisions: {
            expected_task_revision: variables.input.expected_task_revision,
            expected_schedule_revision:
              variables.input.expected_schedule_revision,
            expected_occurrence_revisions: {
              [variables.occurrenceID]:
                variables.input.expected_occurrence_revision,
            },
          },
        },
        response
      ),
  })
}

export function useRescheduleThisAndFollowingMutation() {
  const queryClient = useQueryClient()
  const invalidateScheduleProjections = async (
    variables: RescheduleThisAndFollowingVariables
  ) => {
    const queryKeys: ReadonlyArray<readonly unknown[]> = [
      taskDomainQueryKeys.project(variables.projectID),
      taskDomainQueryKeys.task(variables.taskID),
      taskDomainQueryKeys.taskLists(),
      taskDomainQueryKeys.occurrenceLists(),
      taskDomainQueryKeys.calendar(),
    ]
    await Promise.all(
      queryKeys.map((queryKey) => queryClient.invalidateQueries({ queryKey }))
    )
  }
  return useMutation({
    mutationFn: (variables: RescheduleThisAndFollowingVariables) =>
      taskDomainAPI.rescheduleThisAndFollowing(
        variables.taskID,
        variables.input
      ),
    onSuccess: async (_response, variables) => {
      await invalidateScheduleProjections(variables)
    },
    onError: async (error, variables) => {
      if (error instanceof taskDomainAPI.TaskDomainRevisionConflictError) {
        await invalidateScheduleProjections(variables)
      }
    },
  })
}

function useTaskCommandMutation(
  command: TaskCommand,
  lifecycleStatus: taskDomainAPI.TaskLifecycleStatus
) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (variables: TaskCommandVariables) =>
      command(variables.taskID, variables.expectedRevisions),
    onSuccess: (response, variables) => {
      applyTaskLifecycleResult(
        queryClient,
        variables.taskID,
        lifecycleStatus,
        response
      )
      void invalidateAffectedTaskDomain(queryClient, variables, response).catch(
        () => undefined
      )
    },
  })
}

function useOccurrenceCommandMutation(
  command: OccurrenceCommand,
  executionStatus: taskDomainAPI.ExecutionStatus
) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (variables: OccurrenceCommandVariables) =>
      command(variables.occurrenceID, variables.expectedRevisions),
    onMutate: async (variables): Promise<OccurrenceCommandContext> => {
      await Promise.all([
        queryClient.cancelQueries({
          queryKey: taskDomainQueryKeys.occurrences(),
        }),
        queryClient.cancelQueries({ queryKey: taskDomainQueryKeys.calendar() }),
      ])
      const snapshots = [
        ...queryClient.getQueriesData({
          queryKey: taskDomainQueryKeys.occurrences(),
        }),
        ...queryClient.getQueriesData({
          queryKey: taskDomainQueryKeys.calendar(),
        }),
      ]
      applyOccurrenceExecutionStatus(queryClient, variables, executionStatus)
      return { snapshots }
    },
    onError: (_error, _variables, context) => {
      context?.snapshots.forEach(([queryKey, data]) => {
        queryClient.setQueryData(queryKey, data)
      })
    },
    onSuccess: (response, variables) => {
      applyOccurrenceCommandResult(
        queryClient,
        variables,
        executionStatus,
        response
      )
      void invalidateAffectedTaskDomain(queryClient, variables, response).catch(
        () => undefined
      )
    },
  })
}

export function usePublishTaskMutation() {
  return useTaskCommandMutation(taskDomainAPI.publishTaskDefinition, 'active')
}

export function usePauseTaskMutation() {
  return useTaskCommandMutation(taskDomainAPI.pauseTaskDefinition, 'paused')
}

export function useResumeTaskMutation() {
  return useTaskCommandMutation(taskDomainAPI.resumeTaskDefinition, 'active')
}

export function useCancelTaskMutation() {
  return useTaskCommandMutation(taskDomainAPI.cancelTaskDefinition, 'cancelled')
}

export function useRestoreTaskMutation() {
  return useTaskCommandMutation(taskDomainAPI.restoreTaskDefinition, 'active')
}

export function useArchiveTaskMutation() {
  return useTaskCommandMutation(taskDomainAPI.archiveTaskDefinition, 'archived')
}

export function useDeleteTaskMutation() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (variables: TaskCommandVariables) =>
      taskDomainAPI.deleteTaskDefinition(
        variables.taskID,
        variables.expectedRevisions
      ),
    onSuccess: async (_response, variables) => {
      queryClient.removeQueries({
        queryKey: taskDomainQueryKeys.task(variables.taskID),
      })
      const queryKeys: ReadonlyArray<readonly unknown[]> = [
        taskDomainQueryKeys.project(variables.projectID),
        taskDomainQueryKeys.taskLists(),
        taskDomainQueryKeys.occurrenceLists(),
        taskDomainQueryKeys.calendar(),
      ]
      await Promise.all(
        queryKeys.map((queryKey) => queryClient.invalidateQueries({ queryKey }))
      )
    },
  })
}

export function useStartOccurrenceMutation() {
  return useOccurrenceCommandMutation(taskDomainAPI.startOccurrence, 'active')
}

export function useUnblockOccurrenceMutation() {
  return useOccurrenceCommandMutation(taskDomainAPI.unblockOccurrence, 'open')
}

export function useCompleteOccurrenceMutation() {
  return useOccurrenceCommandMutation(taskDomainAPI.completeOccurrence, 'done')
}

export function useSkipOccurrenceMutation() {
  return useOccurrenceCommandMutation(taskDomainAPI.skipOccurrence, 'skipped')
}

export function useCancelOccurrenceMutation() {
  return useOccurrenceCommandMutation(
    taskDomainAPI.cancelOccurrence,
    'cancelled'
  )
}

export function useReopenOccurrenceMutation() {
  return useOccurrenceCommandMutation(taskDomainAPI.reopenOccurrence, 'open')
}

export function useBlockOccurrenceMutation() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (variables: BlockOccurrenceVariables) =>
      taskDomainAPI.blockOccurrence(variables.occurrenceID, {
        ...variables.expectedRevisions,
        blocked_reason: variables.blockedReason,
        next_action: variables.nextAction,
      }),
    onSuccess: (response, variables) =>
      invalidateAffectedTaskDomain(queryClient, variables, response),
  })
}

async function invalidateAffectedTaskDomain(
  queryClient: QueryClient,
  variables: TaskCommandVariables | OccurrenceCommandVariables,
  response:
    | taskDomainAPI.TaskAggregateCommandResponse
    | taskDomainAPI.ScheduleCommandResponse
) {
  const responseOccurrenceRevisions =
    'occurrence_revisions' in response && response.occurrence_revisions
      ? response.occurrence_revisions
      : {}
  const occurrenceIDs = new Set([
    ...Object.keys(variables.expectedRevisions.expected_occurrence_revisions),
    ...Object.keys(responseOccurrenceRevisions),
  ])
  if ('occurrenceID' in variables) occurrenceIDs.add(variables.occurrenceID)

  const queryKeys: ReadonlyArray<readonly unknown[]> = [
    taskDomainQueryKeys.project(variables.projectID),
    taskDomainQueryKeys.task(variables.taskID),
    taskDomainQueryKeys.taskLists(),
    taskDomainQueryKeys.occurrenceLists(),
    ...Array.from(occurrenceIDs)
      .sort()
      .map((occurrenceID) => taskDomainQueryKeys.occurrence(occurrenceID)),
    taskDomainQueryKeys.calendar(),
  ]
  await Promise.all(
    queryKeys.map((queryKey) => queryClient.invalidateQueries({ queryKey }))
  )
}

function applyOccurrenceExecutionStatus(
  queryClient: QueryClient,
  variables: OccurrenceCommandVariables,
  executionStatus: taskDomainAPI.ExecutionStatus
) {
  const source = findCachedOccurrence(queryClient, variables.occurrenceID)
  const optimisticOccurrence = source
    ? {
        ...source,
        project_id: source.project_id ?? variables.projectID,
        execution_status: executionStatus,
      }
    : undefined

  queryClient
    .getQueriesData<taskDomainAPI.OccurrenceV2[]>({
      queryKey: taskDomainQueryKeys.occurrenceLists(),
    })
    .forEach(([queryKey, occurrences]) => {
      if (!occurrences) return
      let found = false
      const next = occurrences.flatMap((occurrence) => {
        if (occurrence.id !== variables.occurrenceID) return [occurrence]
        found = true
        const updated = {
          ...occurrence,
          execution_status: executionStatus,
        }
        return occurrenceBelongsInExecutionProjection(queryKey, updated)
          ? [updated]
          : []
      })
      if (
        !found &&
        optimisticOccurrence &&
        completedProjectionAccepts(queryKey, optimisticOccurrence)
      ) {
        next.unshift(optimisticOccurrence)
      }
      queryClient.setQueryData(queryKey, next)
    })

  queryClient.setQueryData<taskDomainAPI.OccurrenceV2>(
    taskDomainQueryKeys.occurrence(variables.occurrenceID),
    (occurrence) =>
      occurrence
        ? { ...occurrence, execution_status: executionStatus }
        : occurrence
  )
  queryClient.setQueriesData<taskDomainAPI.CalendarEntryV2[]>(
    { queryKey: taskDomainQueryKeys.calendar() },
    (entries) =>
      entries?.map((entry) =>
        entry.occurrence_id === variables.occurrenceID
          ? { ...entry, execution_status: executionStatus }
          : entry
      )
  )
}

function findCachedOccurrence(queryClient: QueryClient, occurrenceID: string) {
  const detail = queryClient.getQueryData<taskDomainAPI.OccurrenceV2>(
    taskDomainQueryKeys.occurrence(occurrenceID)
  )
  if (detail?.id === occurrenceID) return detail
  for (const [, occurrences] of queryClient.getQueriesData<
    taskDomainAPI.OccurrenceV2[]
  >({ queryKey: taskDomainQueryKeys.occurrenceLists() })) {
    const occurrence = occurrences?.find((item) => item.id === occurrenceID)
    if (occurrence) return occurrence
  }
  return undefined
}

function occurrenceBelongsInExecutionProjection(
  queryKey: QueryKey,
  occurrence: taskDomainAPI.OccurrenceV2
) {
  const executionStatus = queryKey[5]
  if (
    typeof executionStatus === 'string' &&
    executionStatus !== occurrence.execution_status
  ) {
    return false
  }
  const scope = queryKey[9]
  if (scope === 'upcoming' || scope === 'overdue') {
    return !['done', 'skipped', 'cancelled'].includes(
      occurrence.execution_status
    )
  }
  if (scope === 'completed') return occurrence.execution_status === 'done'
  return true
}

function completedProjectionAccepts(
  queryKey: QueryKey,
  occurrence: taskDomainAPI.OccurrenceV2,
  completedAt = Date.now()
) {
  if (queryKey[9] !== 'completed' || occurrence.execution_status !== 'done') {
    return false
  }
  const taskID = queryKey[3]
  const projectID = queryKey[4]
  const executionStatus = queryKey[5]
  const from = queryKey[6]
  const to = queryKey[7]
  const recurring = queryKey[10]
  if (typeof taskID === 'string' && taskID !== occurrence.task_id) return false
  if (typeof projectID === 'string' && projectID !== occurrence.project_id) {
    return false
  }
  if (typeof executionStatus === 'string' && executionStatus !== 'done') {
    return false
  }
  if (
    typeof recurring === 'boolean' &&
    recurring !== Boolean(occurrence.recurring)
  ) {
    return false
  }
  if (typeof from === 'string' && completedAt < new Date(from).getTime()) {
    return false
  }
  if (typeof to === 'string' && completedAt >= new Date(to).getTime()) {
    return false
  }
  return true
}

function applyOccurrenceCommandResult(
  queryClient: QueryClient,
  variables: OccurrenceCommandVariables,
  executionStatus: taskDomainAPI.ExecutionStatus,
  response: taskDomainAPI.TaskAggregateCommandResponse
) {
  applyOccurrenceExecutionStatus(queryClient, variables, executionStatus)
  const occurrenceRevision =
    response.occurrence_revisions[variables.occurrenceID]
  const updateOccurrence = (
    occurrence: taskDomainAPI.OccurrenceV2
  ): taskDomainAPI.OccurrenceV2 =>
    occurrence.id === variables.occurrenceID
      ? {
          ...occurrence,
          revision: occurrenceRevision ?? occurrence.revision,
          task_revision: response.task_revision,
          schedule_revision:
            response.schedule_revision ?? occurrence.schedule_revision,
        }
      : occurrence

  queryClient.setQueriesData<taskDomainAPI.OccurrenceV2[]>(
    { queryKey: taskDomainQueryKeys.occurrenceLists() },
    (occurrences) => occurrences?.map(updateOccurrence)
  )
  queryClient.setQueryData<taskDomainAPI.OccurrenceV2>(
    taskDomainQueryKeys.occurrence(variables.occurrenceID),
    (occurrence) => (occurrence ? updateOccurrence(occurrence) : occurrence)
  )
  queryClient.setQueriesData<taskDomainAPI.CalendarEntryV2[]>(
    { queryKey: taskDomainQueryKeys.calendar() },
    (entries) =>
      entries?.map((entry) =>
        entry.occurrence_id === variables.occurrenceID
          ? {
              ...entry,
              occurrence_revision:
                occurrenceRevision ?? entry.occurrence_revision,
              task_revision: response.task_revision,
              schedule_revision:
                response.schedule_revision ?? entry.schedule_revision,
            }
          : entry
      )
  )
  applyTaskRevisionResult(queryClient, variables.taskID, response)
}

function applyTaskRevisionResult(
  queryClient: QueryClient,
  taskID: string,
  response: taskDomainAPI.TaskAggregateCommandResponse
) {
  const apply = (task: taskDomainAPI.TaskV2): taskDomainAPI.TaskV2 =>
    task.id === taskID
      ? {
          ...task,
          revision: response.task_revision,
          schedule_revision:
            response.schedule_revision ?? task.schedule_revision,
        }
      : task
  queryClient.setQueryData<taskDomainAPI.TaskV2>(
    taskDomainQueryKeys.task(taskID),
    (task) => (task ? apply(task) : task)
  )
  queryClient.setQueriesData<taskDomainAPI.TaskV2[]>(
    { queryKey: taskDomainQueryKeys.taskLists() },
    (tasks) => tasks?.map(apply)
  )
}

function applyTaskLifecycleResult(
  queryClient: QueryClient,
  taskID: string,
  lifecycleStatus: taskDomainAPI.TaskLifecycleStatus,
  response: taskDomainAPI.TaskAggregateCommandResponse
) {
  const apply = (task: taskDomainAPI.TaskV2): taskDomainAPI.TaskV2 =>
    task.id === taskID
      ? {
          ...task,
          lifecycle_status: lifecycleStatus,
          revision: response.task_revision,
          schedule_revision:
            response.schedule_revision ?? task.schedule_revision,
        }
      : task

  queryClient.setQueryData<taskDomainAPI.TaskV2>(
    taskDomainQueryKeys.task(taskID),
    (task) => (task ? apply(task) : task)
  )
  queryClient.setQueriesData<taskDomainAPI.TaskV2[]>(
    { queryKey: taskDomainQueryKeys.taskLists() },
    (tasks) => tasks?.map(apply)
  )
}

async function invalidateUpdatedTaskDefinition(
  queryClient: QueryClient,
  variables: UpdateTaskDefinitionVariables
) {
  const projectIDs = new Set([variables.projectID])
  if (
    variables.input.project_id !== undefined &&
    variables.input.project_id !== ''
  ) {
    projectIDs.add(variables.input.project_id)
  }
  const queryKeys: ReadonlyArray<readonly unknown[]> = [
    taskDomainQueryKeys.task(variables.taskID),
    taskDomainQueryKeys.taskLists(),
    ...Array.from(projectIDs).map((projectID) =>
      taskDomainQueryKeys.project(projectID)
    ),
    taskDomainQueryKeys.occurrenceLists(),
    taskDomainQueryKeys.calendar(),
  ]
  await Promise.all(
    queryKeys.map((queryKey) => queryClient.invalidateQueries({ queryKey }))
  )
}

async function invalidateCreatedProject(
  queryClient: QueryClient,
  projectID: string
) {
  await Promise.all([
    queryClient.invalidateQueries({
      queryKey: taskDomainQueryKeys.project(projectID),
    }),
    queryClient.invalidateQueries({
      queryKey: taskDomainQueryKeys.projectLists(),
    }),
  ])
}

async function invalidateAffectedProjectDomain(
  queryClient: QueryClient,
  projectID: string
) {
  const queryKeys: ReadonlyArray<readonly unknown[]> = [
    taskDomainQueryKeys.project(projectID),
    taskDomainQueryKeys.projectLists(),
    taskDomainQueryKeys.taskLists(),
    taskDomainQueryKeys.occurrenceLists(),
    taskDomainQueryKeys.calendar(),
  ]
  await Promise.all(
    queryKeys.map((queryKey) => queryClient.invalidateQueries({ queryKey }))
  )
}
