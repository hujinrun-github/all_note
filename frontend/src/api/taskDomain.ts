export type ProjectKind = 'standard' | 'learning'
export type ProjectHorizon = 'short' | 'long'
export type ProjectStatus =
  | 'planning'
  | 'active'
  | 'paused'
  | 'completed'
  | 'archived'
export type ProjectCreateStatus = 'planning' | 'active'
export type ProjectMutableStatus = ProjectCreateStatus | 'paused'
export type TaskLifecycleStatus =
  | 'draft'
  | 'active'
  | 'paused'
  | 'completed'
  | 'cancelled'
  | 'archived'
export type ExecutionStatus =
  | 'open'
  | 'active'
  | 'blocked'
  | 'done'
  | 'skipped'
  | 'cancelled'
export type RecurrenceType = 'none' | 'daily' | 'weekly' | 'monthly'
export type TimingType = 'unscheduled' | 'date' | 'time_block'
export type OccurrenceListScope =
  | 'all'
  | 'today'
  | 'upcoming'
  | 'overdue'
  | 'unscheduled'
  | 'completed'

export interface TaskDomainCapabilities {
  model_version: 'legacy' | 'v2' | 'unknown'
  available: boolean
  error?: {
    code: string
    message: string
    retryable: boolean
  }
}

export interface ProjectV2 {
  id: string
  name: string
  kind: ProjectKind
  horizon: ProjectHorizon
  status: ProjectStatus
  system_role?: '' | 'inbox' | 'personal'
  revision: number
}

export interface CreateProjectInput {
  name: string
  kind: ProjectKind
  horizon: ProjectHorizon
  status?: ProjectCreateStatus
}

export interface ProjectExpectedRevision {
  expected_project_revision: number
}

export interface UpdateProjectInput extends ProjectExpectedRevision {
  name?: string
  kind?: ProjectKind
  horizon?: ProjectHorizon
  status?: ProjectMutableStatus
}

export interface ProjectCommandResponse {
  project_id: string
  project_revision: number
  status?: ProjectStatus
  deleted?: boolean
}

export interface TaskV2 {
  id: string
  project_id: string
  roadmap_node_id?: string
  task_note_id?: string
  title: string
  description?: string
  priority: number
  sort_order: number
  lifecycle_status: TaskLifecycleStatus
  revision: number
  schedule_revision: number
}

export interface OccurrenceV2 {
  id: string
  task_id: string
  project_id?: string
  title?: string
  occurrence_key: string
  task_note_id?: string
  occurrence_note_id?: string
  execution_status: ExecutionStatus
  revision: number
  generated_schedule_revision: number
  planned_date?: string
  all_day_end_date?: string
  planned_start_at?: string
  planned_end_at?: string
  due_at?: string
  blocked_reason?: string
  next_action?: string
  location?: string
  calendar_kind?: string
  calendar_notes?: string
  recurring?: boolean
  task_revision?: number
  schedule_revision?: number
  timing_type?: TimingType
  timezone?: string
}

export interface CalendarEntryV2 {
  project_id: string
  project_revision: number
  task_id: string
  task_revision: number
  task_title: string
  task_note_id?: string
  occurrence_id: string
  occurrence_key: string
  occurrence_revision: number
  schedule_revision: number
  generated_schedule_revision: number
  occurrence_note_id?: string
  execution_status: ExecutionStatus
  timing_type: TimingType
  timezone: string
  recurring: boolean
  planned_date?: string
  all_day_end_date?: string
  planned_start_at?: string
  planned_end_at?: string
  due_at?: string
  location?: string
  calendar_kind?: string
  calendar_notes?: string
}

export interface ScheduleV2Input {
  recurrence_type: RecurrenceType
  timing_type: TimingType
  timezone: string
  starts_on?: string
  ends_on?: string
  local_start_time?: string
  duration_minutes?: number
  rule?: {
    interval: number
    weekdays?: number[]
    month_days?: number[]
  }
}

export interface CreateTaskDefinitionInput {
  project_id: string
  roadmap_node_id?: string
  task_note_id?: string
  title: string
  description?: string
  priority: number
  sort_order?: number
  schedule: ScheduleV2Input
}

export interface TaskDefinitionExpectedRevisions {
  expected_task_revision: number
  expected_schedule_revision: number
}

export interface UpdateTaskDefinitionInput
  extends TaskDefinitionExpectedRevisions {
  title?: string
  description?: string
  priority?: number
  sort_order?: number
  project_id?: string
  roadmap_node_id?: string
  task_note_id?: string
}

export interface TaskDomainExpectedRevisions
  extends TaskDefinitionExpectedRevisions {
  expected_occurrence_revisions: Record<string, number>
}

export interface TaskDomainCurrentRevisions {
  project_revision?: number
  task_revision?: number
  schedule_revision?: number
  occurrence_revisions?: Record<string, number>
}

export interface TaskAggregateCommandResponse {
  task_revision: number
  schedule_revision?: number
  occurrence_revisions: Record<string, number>
}

export interface BlockOccurrenceInput extends TaskDomainExpectedRevisions {
  blocked_reason: string
  next_action: string
}

export interface OccurrenceTimingInput {
  timing_type: TimingType
  timezone: string
  planned_date?: string
  all_day_end_date?: string
  local_start_time?: string
  duration_minutes?: number
  selected_offset_seconds?: number
}

export interface RescheduleOccurrenceInput {
  expected_task_revision: number
  expected_schedule_revision: number
  expected_occurrence_revision: number
  timing: OccurrenceTimingInput
  selected_offsets?: Record<string, number>
}

export interface RescheduleThisAndFollowingInput {
  expected_task_revision: number
  expected_schedule_revision: number
  effective_from: string
  generate_through_exclusive: string
  schedule: ScheduleV2Input
  selected_offsets?: Record<string, number>
}

export interface ScheduleCommandResponse {
  task_revision: number
  schedule_revision: number
  occurrence_revision?: number
  schedule_version?: number
  offset_candidates?: Array<{ offset_seconds: number; utc: string }>
}

export interface TaskDomainErrorDetails {
  current_revisions?: TaskDomainCurrentRevisions
  offset_candidates?: Array<{ offset_seconds: number; utc: string }>
}

export class TaskDomainAPIError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    message: string,
    public readonly retryable: boolean,
    public readonly details?: TaskDomainErrorDetails
  ) {
    super(message)
    this.name = 'TaskDomainAPIError'
  }
}

export class TaskDomainRevisionConflictError extends TaskDomainAPIError {
  constructor(
    message: string,
    public readonly expectedRevisions:
      | TaskDomainExpectedRevisions
      | TaskDefinitionExpectedRevisions
      | ProjectExpectedRevision,
    public readonly currentRevisions?: TaskDomainCurrentRevisions
  ) {
    super(409, 'revision_conflict', message, false, {
      current_revisions: currentRevisions,
    })
    this.name = 'TaskDomainRevisionConflictError'
  }
}

export async function getTaskDomainCapabilities(): Promise<TaskDomainCapabilities> {
  const response = await fetch(withBasePath('/api/task-domain/capabilities'), {
    credentials: 'include',
  })
  if (response.status === 404) {
    return { model_version: 'legacy', available: true }
  }
  const body = await parseJSONResponse(response)
  if (!response.ok) throwTaskDomainError(response.status, body)
  if (
    !isRecord(body) ||
    (body.model_version !== 'legacy' &&
      body.model_version !== 'v2' &&
      body.model_version !== 'unknown') ||
    typeof body.available !== 'boolean'
  ) {
    throw new TaskDomainAPIError(
      response.status,
      'invalid_response',
      'task-domain capability response is invalid',
      false
    )
  }
  return body as unknown as TaskDomainCapabilities
}

export async function listProjects(
  params: {
    kind?: ProjectKind
    horizon?: ProjectHorizon
    status?: ProjectStatus
  } = {}
) {
  const data = await requestData<{ projects: ProjectV2[] }>(
    withQuery('/api/projects', params)
  )
  return data.projects
}

export async function getProject(projectID: string) {
  const data = await requestData<{ project: ProjectV2 }>(
    `/api/projects/${encodeURIComponent(projectID)}`
  )
  return data.project
}

export async function createProject(input: CreateProjectInput) {
  const status = input.status ?? 'planning'
  assertProjectCreateStatus(status)
  const data = await requestData<{ project: ProjectV2 }>(
    '/api/projects',
    jsonPost({
      name: input.name,
      kind: input.kind,
      horizon: input.horizon,
      status,
    })
  )
  return data.project
}

export async function updateProject(
  projectID: string,
  input: UpdateProjectInput
) {
  assertProjectExpectedRevision(input)
  if (input.status !== undefined) assertProjectMutableStatus(input.status)
  const expectedRevision: ProjectExpectedRevision = {
    expected_project_revision: input.expected_project_revision,
  }
  const body: UpdateProjectInput = { ...expectedRevision }
  if (input.name !== undefined) body.name = input.name
  if (input.kind !== undefined) body.kind = input.kind
  if (input.horizon !== undefined) body.horizon = input.horizon
  if (input.status !== undefined) body.status = input.status

  const data = await requestData<{ project: ProjectV2 }>(
    `/api/projects/${encodeURIComponent(projectID)}`,
    jsonRequest('PATCH', body),
    expectedRevision
  )
  return data.project
}

export function completeProject(
  projectID: string,
  expectedRevision: ProjectExpectedRevision
) {
  return projectCommand(projectID, 'complete', expectedRevision)
}

export function archiveProject(
  projectID: string,
  expectedRevision: ProjectExpectedRevision
) {
  return projectCommand(projectID, 'archive', expectedRevision)
}

export function deleteProject(
  projectID: string,
  expectedRevision: ProjectExpectedRevision
) {
  assertProjectExpectedRevision(expectedRevision)
  return requestData<ProjectCommandResponse>(
    `/api/projects/${encodeURIComponent(projectID)}`,
    jsonRequest('DELETE', expectedRevision),
    expectedRevision
  )
}

export async function listTaskDefinitions(
  params: {
    project_id?: string
    lifecycle_status?: TaskLifecycleStatus
  } = {}
) {
  const data = await requestData<{ tasks: TaskV2[] }>(
    withQuery('/api/tasks', params)
  )
  return data.tasks
}

export async function getTaskDefinition(taskID: string) {
  const data = await requestData<{ task: TaskV2 }>(
    `/api/tasks/${encodeURIComponent(taskID)}`
  )
  return data.task
}

export async function listOccurrences(
  params: {
    task_id?: string
    project_id?: string
    execution_status?: ExecutionStatus
    from?: string
    to?: string
    scope?: OccurrenceListScope
    recurring?: boolean
  } = {}
) {
  const query: Record<string, string | undefined> = {
    task_id: params.task_id,
    project_id: params.project_id,
    execution_status: params.execution_status,
    from: params.from,
    to: params.to,
    scope: params.scope,
    recurring:
      params.recurring === undefined ? undefined : String(params.recurring),
  }
  const data = await requestData<{ occurrences: OccurrenceV2[] }>(
    withQuery('/api/task-occurrences', query)
  )
  return data.occurrences
}

export async function getOccurrence(occurrenceID: string) {
  const data = await requestData<{ occurrence: OccurrenceV2 }>(
    `/api/task-occurrences/${encodeURIComponent(occurrenceID)}`
  )
  return data.occurrence
}

export async function getCalendarEntries(params: {
  from: string
  to: string
  timezone: string
  project_id?: string
}) {
  const data = await requestData<{ entries: CalendarEntryV2[] }>(
    withQuery('/api/calendar/entries', params)
  )
  return data.entries
}

export async function createTaskDefinition(input: CreateTaskDefinitionInput) {
  if (input.project_id.trim() === '') {
    throw new TypeError('project_id is required')
  }
  return requestData<{ task: TaskV2; occurrences: OccurrenceV2[] }>(
    '/api/tasks',
    jsonPost(input)
  )
}

export function rescheduleOccurrence(
  occurrenceID: string,
  input: RescheduleOccurrenceInput
) {
  assertTaskDefinitionExpectedRevisions(input)
  if (
    !Number.isSafeInteger(input.expected_occurrence_revision) ||
    input.expected_occurrence_revision < 1
  ) {
    throw new TypeError('expected_occurrence_revision must be a positive integer')
  }
  const expectedRevisions: TaskDomainExpectedRevisions = {
    expected_task_revision: input.expected_task_revision,
    expected_schedule_revision: input.expected_schedule_revision,
    expected_occurrence_revisions: {
      [occurrenceID]: input.expected_occurrence_revision,
    },
  }
  return requestData<ScheduleCommandResponse>(
    `/api/task-occurrences/${encodeURIComponent(occurrenceID)}/schedule/only-this`,
    jsonRequest('PATCH', input),
    expectedRevisions
  )
}

export function rescheduleThisAndFollowing(
  taskID: string,
  input: RescheduleThisAndFollowingInput
) {
  assertTaskDefinitionExpectedRevisions(input)
  return requestData<ScheduleCommandResponse>(
    `/api/tasks/${encodeURIComponent(taskID)}/schedule/this-and-following`,
    jsonPost(input),
    input
  )
}

export async function updateTaskDefinition(
  taskID: string,
  input: UpdateTaskDefinitionInput
) {
  assertTaskDefinitionExpectedRevisions(input)
  const expectedRevisions: TaskDefinitionExpectedRevisions = {
    expected_task_revision: input.expected_task_revision,
    expected_schedule_revision: input.expected_schedule_revision,
  }
  const body: UpdateTaskDefinitionInput = { ...expectedRevisions }
  if (input.title !== undefined) body.title = input.title
  if (input.description !== undefined) body.description = input.description
  if (input.priority !== undefined) body.priority = input.priority
  if (input.sort_order !== undefined) body.sort_order = input.sort_order
  if (input.project_id !== undefined) body.project_id = input.project_id
  if (input.roadmap_node_id !== undefined)
    body.roadmap_node_id = input.roadmap_node_id
  if (input.task_note_id !== undefined) body.task_note_id = input.task_note_id

  const data = await requestData<{ task: TaskV2 }>(
    `/api/tasks/${encodeURIComponent(taskID)}`,
    jsonRequest('PATCH', body),
    expectedRevisions
  )
  return data.task
}

export function publishTaskDefinition(
  taskID: string,
  revisions: TaskDomainExpectedRevisions
) {
  return taskLifecycleCommand(taskID, 'publish', revisions)
}

export function pauseTaskDefinition(
  taskID: string,
  revisions: TaskDomainExpectedRevisions
) {
  return taskLifecycleCommand(taskID, 'pause', revisions)
}

export function resumeTaskDefinition(
  taskID: string,
  revisions: TaskDomainExpectedRevisions
) {
  return taskLifecycleCommand(taskID, 'resume', revisions)
}

export function cancelTaskDefinition(
  taskID: string,
  revisions: TaskDomainExpectedRevisions
) {
  return taskLifecycleCommand(taskID, 'cancel', revisions)
}

export function restoreTaskDefinition(
  taskID: string,
  revisions: TaskDomainExpectedRevisions
) {
  return taskLifecycleCommand(taskID, 'restore', revisions)
}

export function archiveTaskDefinition(
  taskID: string,
  revisions: TaskDomainExpectedRevisions
) {
  return taskLifecycleCommand(taskID, 'archive', revisions)
}

export function startOccurrence(
  occurrenceID: string,
  revisions: TaskDomainExpectedRevisions
) {
  return occurrenceCommand(occurrenceID, 'start', revisions)
}

export function blockOccurrence(
  occurrenceID: string,
  input: BlockOccurrenceInput
) {
  return occurrenceCommand(occurrenceID, 'block', input)
}

export function unblockOccurrence(
  occurrenceID: string,
  revisions: TaskDomainExpectedRevisions
) {
  return occurrenceCommand(occurrenceID, 'unblock', revisions)
}

export function completeOccurrence(
  occurrenceID: string,
  revisions: TaskDomainExpectedRevisions
) {
  return occurrenceCommand(occurrenceID, 'complete', revisions)
}

export function skipOccurrence(
  occurrenceID: string,
  revisions: TaskDomainExpectedRevisions
) {
  return occurrenceCommand(occurrenceID, 'skip', revisions)
}

export function cancelOccurrence(
  occurrenceID: string,
  revisions: TaskDomainExpectedRevisions
) {
  return occurrenceCommand(occurrenceID, 'cancel', revisions)
}

export function reopenOccurrence(
  occurrenceID: string,
  revisions: TaskDomainExpectedRevisions
) {
  return occurrenceCommand(occurrenceID, 'reopen', revisions)
}

type TaskLifecycleCommand =
  | 'publish'
  | 'pause'
  | 'resume'
  | 'cancel'
  | 'restore'
  | 'archive'

type ProjectCommand = 'complete' | 'archive'

function projectCommand(
  projectID: string,
  command: ProjectCommand,
  expectedRevision: ProjectExpectedRevision
) {
  assertProjectExpectedRevision(expectedRevision)
  return requestData<ProjectCommandResponse>(
    `/api/projects/${encodeURIComponent(projectID)}/${command}`,
    jsonPost(expectedRevision),
    expectedRevision
  )
}

function taskLifecycleCommand(
  taskID: string,
  command: TaskLifecycleCommand,
  revisions: TaskDomainExpectedRevisions
) {
  return requestData<TaskAggregateCommandResponse>(
    `/api/tasks/${encodeURIComponent(taskID)}/${command}`,
    jsonPost(revisions),
    revisions
  )
}

type OccurrenceCommand =
  | 'start'
  | 'block'
  | 'unblock'
  | 'complete'
  | 'skip'
  | 'cancel'
  | 'reopen'

function occurrenceCommand(
  occurrenceID: string,
  command: OccurrenceCommand,
  input: TaskDomainExpectedRevisions | BlockOccurrenceInput
) {
  return requestData<TaskAggregateCommandResponse>(
    `/api/task-occurrences/${encodeURIComponent(occurrenceID)}/${command}`,
    jsonPost(input),
    input
  )
}

function jsonPost(body: unknown): RequestInit {
  return jsonRequest('POST', body)
}

function jsonRequest(
  method: 'POST' | 'PATCH' | 'DELETE',
  body: unknown
): RequestInit {
  return {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  }
}

async function requestData<T>(
  path: string,
  init: RequestInit = {},
  expectedRevisions?:
    | TaskDomainExpectedRevisions
    | TaskDefinitionExpectedRevisions
    | ProjectExpectedRevision
): Promise<T> {
  const response = await fetch(withBasePath(path), {
    ...init,
    credentials: 'include',
  })
  const body = await parseJSONResponse(response)
  if (!response.ok) {
    throwTaskDomainError(response.status, body, expectedRevisions)
  }
  if (!isRecord(body) || !('data' in body)) {
    throw new TaskDomainAPIError(
      response.status,
      'invalid_response',
      'task-domain response did not contain data',
      false
    )
  }
  return body.data as T
}

async function parseJSONResponse(response: Response): Promise<unknown> {
  try {
    return await response.json()
  } catch {
    return undefined
  }
}

function throwTaskDomainError(
  status: number,
  body: unknown,
  expectedRevisions?:
    | TaskDomainExpectedRevisions
    | TaskDefinitionExpectedRevisions
    | ProjectExpectedRevision
): never {
  const error = isRecord(body) && isRecord(body.error) ? body.error : undefined
  const code = typeof error?.code === 'string' ? error.code : 'unknown_error'
  const message =
    typeof error?.message === 'string'
      ? error.message
      : 'task-domain request failed'
  const retryable = error?.retryable === true
  const details = isRecord(error?.details)
    ? (error.details as TaskDomainErrorDetails)
    : undefined

  if (status === 409 && code === 'revision_conflict') {
    throw new TaskDomainRevisionConflictError(
      message,
      expectedRevisions ?? emptyExpectedRevisions(),
      normalizeCurrentRevisions(details?.current_revisions)
    )
  }
  throw new TaskDomainAPIError(status, code, message, retryable, details)
}

function normalizeCurrentRevisions(
  value: TaskDomainCurrentRevisions | undefined
): TaskDomainCurrentRevisions | undefined {
  if (!isRecord(value)) return undefined
  return {
    project_revision: numberOrUndefined(value.project_revision),
    task_revision: numberOrUndefined(value.task_revision),
    schedule_revision: numberOrUndefined(value.schedule_revision),
    occurrence_revisions: numberRecordOrUndefined(value.occurrence_revisions),
  }
}

function numberOrUndefined(value: unknown) {
  return typeof value === 'number' ? value : undefined
}

function numberRecordOrUndefined(value: unknown) {
  if (!isRecord(value)) return undefined
  const entries = Object.entries(value).filter(
    (entry): entry is [string, number] => typeof entry[1] === 'number'
  )
  return Object.fromEntries(entries)
}

function emptyExpectedRevisions(): TaskDomainExpectedRevisions {
  return {
    expected_task_revision: 0,
    expected_schedule_revision: 0,
    expected_occurrence_revisions: {},
  }
}

function assertProjectExpectedRevision(value: ProjectExpectedRevision) {
  if (
    !Number.isSafeInteger(value.expected_project_revision) ||
    value.expected_project_revision < 1
  ) {
    throw new TypeError('expected_project_revision must be a positive integer')
  }
}

function assertTaskDefinitionExpectedRevisions(
  value: TaskDefinitionExpectedRevisions
) {
  if (
    !Number.isSafeInteger(value.expected_task_revision) ||
    value.expected_task_revision < 1
  ) {
    throw new TypeError('expected_task_revision must be a positive integer')
  }
  if (
    !Number.isSafeInteger(value.expected_schedule_revision) ||
    value.expected_schedule_revision < 1
  ) {
    throw new TypeError('expected_schedule_revision must be a positive integer')
  }
}

function assertProjectCreateStatus(
  status: ProjectStatus
): asserts status is ProjectCreateStatus {
  if (status !== 'planning' && status !== 'active') {
    throw new TypeError('project create status must be planning or active')
  }
}

function assertProjectMutableStatus(
  status: ProjectStatus
): asserts status is ProjectMutableStatus {
  if (status !== 'planning' && status !== 'active' && status !== 'paused') {
    throw new TypeError(
      'project update status must be planning, active, or paused'
    )
  }
}

function withQuery(path: string, params: Record<string, string | undefined>) {
  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== '') search.set(key, value)
  }
  const query = search.toString()
  return query === '' ? path : `${path}?${query}`
}

function withBasePath(path: string) {
  const basePath =
    import.meta.env.BASE_URL === '/'
      ? ''
      : import.meta.env.BASE_URL.replace(/\/$/, '')
  return `${basePath}${path}`
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}
