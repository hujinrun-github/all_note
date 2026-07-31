import {
  useMutation,
  useQuery,
  useQueryClient,
  type QueryClient,
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
  return useMutation({
    mutationFn: (variables: RescheduleThisAndFollowingVariables) =>
      taskDomainAPI.rescheduleThisAndFollowing(
        variables.taskID,
        variables.input
      ),
    onSuccess: async (_response, variables) => {
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

function useOccurrenceCommandMutation(command: OccurrenceCommand) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (variables: OccurrenceCommandVariables) =>
      command(variables.occurrenceID, variables.expectedRevisions),
    onSuccess: (response, variables) =>
      invalidateAffectedTaskDomain(queryClient, variables, response),
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
  return useOccurrenceCommandMutation(taskDomainAPI.startOccurrence)
}

export function useUnblockOccurrenceMutation() {
  return useOccurrenceCommandMutation(taskDomainAPI.unblockOccurrence)
}

export function useCompleteOccurrenceMutation() {
  return useOccurrenceCommandMutation(taskDomainAPI.completeOccurrence)
}

export function useSkipOccurrenceMutation() {
  return useOccurrenceCommandMutation(taskDomainAPI.skipOccurrence)
}

export function useCancelOccurrenceMutation() {
  return useOccurrenceCommandMutation(taskDomainAPI.cancelOccurrence)
}

export function useReopenOccurrenceMutation() {
  return useOccurrenceCommandMutation(taskDomainAPI.reopenOccurrence)
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
