import {
  CalendarClock,
  CircleAlert,
  Inbox,
  ListChecks,
  Plus,
} from 'lucide-react'
import { type FormEvent, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useQueries } from '@tanstack/react-query'

import type { OccurrenceV2, TaskV2, TimingType } from '../api/taskDomain'
import {
  getTaskDefinition,
  TaskDomainRevisionConflictError,
} from '../api/taskDomain'
import { createWorkspaceQueryParams } from '../api/workspace'
import { useTaskWorkbenchFacets } from '../hooks/useWorkspace'
import {
  createWorkspaceOccurrenceParams,
  matchesWorkspaceDateFilter,
} from '../utils/workspaceDates'
import {
  PlanningHeading,
  PlanningLoading,
  PlanningTabs,
} from '../components/planning/PlanningWorkspace'
import { Alert, AlertTitle } from '../components/ui/alert'
import { Button } from '../components/ui/button'
import { Card } from '../components/ui/card'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '../components/ui/empty'
import {
  NativeSelect,
  NativeSelectOption,
} from '../components/ui/native-select'
import {
  OccurrenceInspector,
  OccurrenceRow,
  TaskDefinitionInspector,
  occurrenceTitle,
} from '../components/taskDomain/TaskDomainWorkspace'
import {
  useArchiveTaskMutation,
  useBlockOccurrenceMutation,
  useCancelTaskMutation,
  useCompleteOccurrenceMutation,
  useDeleteTaskMutation,
  taskDomainQueryKeys,
  useOccurrence,
  useOccurrences,
  usePauseTaskMutation,
  useProjects,
  usePublishTaskMutation,
  useReopenOccurrenceMutation,
  useRescheduleOccurrenceMutation,
  useRestoreTaskMutation,
  useResumeTaskMutation,
  useStartOccurrenceMutation,
  useTaskDefinition,
  useTaskDefinitions,
  useUnblockOccurrenceMutation,
  useUpdateTaskDefinitionMutation,
} from '../hooks/useTaskDomain'
import { OccurrenceCollection } from '../components/taskDomain/OccurrenceCollection'
import {
  collectOccurrencesByTask,
  type OccurrenceCollectionData,
} from '../utils/occurrenceCollections'
import { useUIStore } from '../stores/ui'

type TaskTab =
  | 'inbox'
  | 'today'
  | 'upcoming'
  | 'month'
  | 'overdue'
  | 'unscheduled'
  | 'recurring'
  | 'completed'
  | 'draft'

type DateFilter = 'current' | 'today' | 'next-7-days' | 'unscheduled'

const emptyOccurrences: OccurrenceV2[] = []

const tabDefinitions: Array<{ id: TaskTab; label: string }> = [
  { id: 'inbox', label: '任务收件箱' },
  { id: 'today', label: '今天' },
  { id: 'upcoming', label: '本周' },
  { id: 'month', label: '本月' },
  { id: 'overdue', label: '已逾期' },
  { id: 'unscheduled', label: '无日期' },
  { id: 'recurring', label: '重复' },
  { id: 'completed', label: '已完成' },
  { id: 'draft', label: '草稿' },
]

export default function TaskOccurrenceWorkspace() {
  const [searchParams, setSearchParams] = useSearchParams()
  const setCaptureOpen = useUIStore((state) => state.setCaptureOpen)
  const requestedView = searchParams.get('view')
  const requestedTab = (
    requestedView === 'week' ? 'upcoming' : requestedView
  ) as TaskTab | null
  const activeTab = tabDefinitions.some((tab) => tab.id === requestedTab)
    ? requestedTab!
    : 'upcoming'
  const selectedOccurrenceID = searchParams.get('occurrence_id') ?? ''
  const selectedTaskID = searchParams.get('task_id') ?? ''
  const projectFilter = searchParams.get('project') ?? ''
  const priorityFilter = searchParams.get('priority') ?? ''
  const statusFilter = searchParams.get('status') ?? ''
  const requestedDateFilter = searchParams.get('date')
  const dateFilter: DateFilter =
    requestedDateFilter === 'today' ||
    requestedDateFilter === 'next-7-days' ||
    requestedDateFilter === 'unscheduled'
      ? requestedDateFilter
      : 'current'
  const queryParams = createWorkspaceQueryParams()

  const projectFilterRef = useRef<HTMLSelectElement>(null)

  const [expandedTaskIDs, setExpandedTaskIDs] = useState<Set<string>>(
    () => new Set()
  )
  const [editingOccurrenceID, setEditingOccurrenceID] = useState('')
  const [rescheduleDate, setRescheduleDate] = useState('')
  const [rescheduleTimingType, setRescheduleTimingType] =
    useState<Exclude<TimingType, 'unscheduled'>>('date')
  const [rescheduleTimeRange, setRescheduleTimeRange] = useState({
    start: '09:00',
    end: '10:00',
  })
  const [rescheduleConflict, setRescheduleConflict] =
    useState<TaskDomainRevisionConflictError | null>(null)
  const [rescheduleError, setRescheduleError] = useState('')
  const [showComparison, setShowComparison] = useState(false)
  const [taskCommandError, setTaskCommandError] = useState('')

  const projectsQuery = useProjects()
  const inboxProject = (projectsQuery.data ?? []).find(
    (project) => project.system_role === 'inbox'
  )
  const facetsQuery = useTaskWorkbenchFacets({
    ...queryParams,
    project_id: projectFilter || undefined,
  })
  const facets = facetsQuery.isError ? undefined : facetsQuery.data?.facets
  const viewCounts = {
    inbox: facets?.inbox,
    today: facets?.today,
    upcoming: facets?.week,
    month: facets?.month,
    overdue: facets?.overdue,
    unscheduled: facets?.unscheduled,
    recurring: facets?.recurring,
    completed: facets?.completed_total,
    draft: facets?.draft,
  }
  const inboxMatchesProject =
    !projectFilter || projectFilter === inboxProject?.id
  const inboxReady = activeTab !== 'inbox' || Boolean(inboxProject)
  const occurrenceQuery = useOccurrences(
    createWorkspaceOccurrenceParams(
      activeTab === 'upcoming' || activeTab === 'draft' ? 'week' : activeTab,
      queryParams,
      activeTab === 'inbox'
        ? (inboxProject?.id ?? '__missing_system_inbox__')
        : projectFilter || undefined
    ),
    {
      enabled:
        activeTab !== 'draft' &&
        inboxReady &&
        (activeTab !== 'inbox' || inboxMatchesProject),
    }
  )
  const draftsQuery = useTaskDefinitions(
    { lifecycle_status: 'draft', project_id: projectFilter || undefined },
    { enabled: activeTab === 'draft' }
  )
  const occurrenceSource =
    activeTab === 'draft' ||
    occurrenceQuery.isError ||
    (activeTab === 'inbox' && !inboxMatchesProject)
      ? emptyOccurrences
      : (occurrenceQuery.data ?? emptyOccurrences)
  const visibleTaskIDs = [
    ...new Set(
      occurrenceSource
        .filter((item) =>
          activeTab === 'completed'
            ? item.execution_status === 'done'
            : ['open', 'active', 'blocked'].includes(item.execution_status)
        )
        .map((item) => item.task_id)
    ),
  ]
  // Read metadata for the current collection only. Other view counts do not
  // need task definitions or occurrence collections.
  const taskMetadataQueries = useQueries({
    queries: visibleTaskIDs.map((taskID) => ({
      queryKey: taskDomainQueryKeys.task(taskID),
      queryFn: () => getTaskDefinition(taskID),
    })),
  })
  const definitionsByID = new Map(
    taskMetadataQueries.flatMap((query) =>
      !query.isError && query.data ? [[query.data.id, query.data] as const] : []
    )
  )
  const metadataLoading = taskMetadataQueries.some(
    (query) => query.isPending || query.isLoading
  )
  const metadataError = taskMetadataQueries.some((query) => query.isError)

  const completeOccurrence = useCompleteOccurrenceMutation()
  const startOccurrence = useStartOccurrenceMutation()
  const blockOccurrence = useBlockOccurrenceMutation()
  const unblockOccurrence = useUnblockOccurrenceMutation()
  const reopenOccurrence = useReopenOccurrenceMutation()
  const rescheduleOccurrence = useRescheduleOccurrenceMutation()
  const publishTask = usePublishTaskMutation()
  const pauseTask = usePauseTaskMutation()
  const resumeTask = useResumeTaskMutation()
  const cancelTask = useCancelTaskMutation()
  const restoreTask = useRestoreTaskMutation()
  const archiveTask = useArchiveTaskMutation()
  const deleteTask = useDeleteTaskMutation()
  const updateTask = useUpdateTaskDefinitionMutation()

  const projectsByID = useMemo(
    () =>
      new Map(
        (projectsQuery.data ?? []).map((project) => [project.id, project])
      ),
    [projectsQuery.data]
  )
  const activeQuery = activeTab === 'draft' ? null : occurrenceQuery
  const activeOccurrences = occurrenceSource.filter((occurrence) => {
    const task = definitionsByID.get(occurrence.task_id)
    const projectID = occurrence.project_id ?? task?.project_id
    const project = projectID ? projectsByID.get(projectID) : undefined
    if (!task) return false
    if (activeTab === 'completed') {
      if (occurrence.execution_status !== 'done') return false
    } else if (
      !['open', 'active', 'blocked'].includes(occurrence.execution_status) ||
      !['active', 'paused'].includes(task.lifecycle_status) ||
      !project ||
      !['planning', 'active', 'paused'].includes(project.status)
    )
      return false
    if (projectFilter && projectID !== projectFilter) return false
    if (priorityFilter && task.priority !== Number(priorityFilter)) return false
    if (statusFilter && occurrence.execution_status !== statusFilter)
      return false
    return matchesWorkspaceDateFilter(occurrence, dateFilter, queryParams)
  })
  const activeDrafts =
    draftsQuery.isError || activeTab !== 'draft'
      ? []
      : (draftsQuery.data ?? []).filter(
          (task) =>
            task.lifecycle_status === 'draft' &&
            (!projectFilter || task.project_id === projectFilter) &&
            (!priorityFilter || task.priority === Number(priorityFilter))
        )
  const selectedOccurrenceQuery = useOccurrence(selectedOccurrenceID)
  const selectedOccurrence = selectedOccurrenceID
    ? (selectedOccurrenceQuery.data ??
      occurrenceSource.find((item) => item.id === selectedOccurrenceID))
    : undefined
  const selectedTaskQuery = useTaskDefinition(
    selectedOccurrence?.task_id ?? selectedTaskID
  )
  const selectedTask =
    selectedTaskQuery.data ??
    definitionsByID.get(selectedOccurrence?.task_id ?? selectedTaskID) ??
    activeDrafts.find((task) => task.id === selectedTaskID)
  const selectedReadError = Boolean(
    (selectedOccurrenceID && selectedOccurrenceQuery.isError) ||
    selectedTaskQuery.isError
  )
  const selectedTaskOccurrencesQuery = useOccurrences(
    {
      scope: 'all',
      task_id: selectedTask?.id ?? '',
    },
    { enabled: selectedTask !== undefined }
  )
  const selectedProject = selectedTask
    ? projectsByID.get(selectedTask.project_id)
    : undefined
  const taskCommandsReady =
    selectedTaskOccurrencesQuery.data !== undefined &&
    !selectedTaskOccurrencesQuery.isError &&
    !selectedTaskOccurrencesQuery.isLoading
  const selectedTaskOccurrences = taskCommandsReady
    ? (selectedTaskOccurrencesQuery.data ?? emptyOccurrences)
    : emptyOccurrences
  const archiveSelectedTask =
    selectedTask && taskCommandsReady
      ? () =>
          archiveTask
            .mutateAsync(taskCommandVariables(selectedTask))
            .then(() =>
              updateSearchParams({ occurrence_id: null, task_id: null })
            )
      : undefined
  const deleteSelectedTask =
    selectedTask && taskCommandsReady
      ? () =>
          deleteTask
            .mutateAsync(taskCommandVariables(selectedTask))
            .then(() =>
              updateSearchParams({ occurrence_id: null, task_id: null })
            )
      : undefined
  const editingOccurrence =
    selectedOccurrence?.id === editingOccurrenceID
      ? selectedOccurrence
      : occurrenceSource.find(
          (occurrence) => occurrence.id === editingOccurrenceID
        )
  const editingDefinition = editingOccurrence
    ? selectedTask?.id === editingOccurrence.task_id
      ? selectedTask
      : definitionsByID.get(editingOccurrence.task_id)
    : undefined
  const commandBusy = [
    updateTask,
    completeOccurrence,
    startOccurrence,
    blockOccurrence,
    unblockOccurrence,
    reopenOccurrence,
    publishTask,
    pauseTask,
    resumeTask,
    cancelTask,
    restoreTask,
    archiveTask,
    deleteTask,
  ].some((mutation) => mutation.isPending)

  function updateSearchParams(update: Record<string, string | null>) {
    const next = new URLSearchParams(searchParams)
    Object.entries(update).forEach(([key, value]) => {
      if (!value) next.delete(key)
      else next.set(key, value)
    })
    setSearchParams(next)
  }

  function occurrenceCommandVariables(task: TaskV2, occurrence: OccurrenceV2) {
    return {
      projectID: occurrence.project_id ?? task.project_id,
      taskID: task.id,
      occurrenceID: occurrence.id,
      expectedRevisions: {
        expected_task_revision: occurrence.task_revision ?? task.revision,
        expected_schedule_revision:
          occurrence.schedule_revision ?? task.schedule_revision,
        expected_occurrence_revisions: {
          [occurrence.id]: occurrence.revision,
        },
      },
    }
  }

  function taskCommandVariables(task: TaskV2) {
    if (!taskCommandsReady)
      throw new Error('Task execution history is unavailable')
    return {
      projectID: task.project_id,
      taskID: task.id,
      expectedRevisions: {
        expected_task_revision: task.revision,
        expected_schedule_revision: task.schedule_revision,
        expected_occurrence_revisions: Object.fromEntries(
          selectedTaskOccurrences.map((occurrence) => [
            occurrence.id,
            occurrence.revision,
          ])
        ),
      },
    }
  }

  async function handleTaskCommand(command: () => Promise<unknown>) {
    setTaskCommandError('')
    try {
      await command()
    } catch (caught) {
      setTaskCommandError(
        caught instanceof TaskDomainRevisionConflictError
          ? '任务已在其他窗口更新，请刷新后重试。'
          : '任务操作失败，请稍后重试。'
      )
    }
  }

  function beginReschedule(occurrence: OccurrenceV2) {
    updateSearchParams({
      occurrence_id: occurrence.id,
      task_id: null,
    })
    setEditingOccurrenceID(occurrence.id)
    setRescheduleDate(occurrence.planned_date ?? '')
    setRescheduleTimingType(
      occurrence.timing_type === 'time_block' || occurrence.planned_start_at
        ? 'time_block'
        : 'date'
    )
    setRescheduleTimeRange(timeRangeForOccurrence(occurrence))
    setRescheduleConflict(null)
    setRescheduleError('')
    setShowComparison(false)
  }

  async function handleReschedule(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!editingOccurrence || !editingDefinition || rescheduleDate === '')
      return
    const durationMinutes = timeRangeMinutes(
      rescheduleTimeRange.start,
      rescheduleTimeRange.end
    )
    if (rescheduleTimingType === 'time_block' && durationMinutes <= 0) {
      setRescheduleError('结束时间必须晚于开始时间。')
      return
    }
    setRescheduleConflict(null)
    setRescheduleError('')
    setShowComparison(false)
    try {
      await rescheduleOccurrence.mutateAsync({
        projectID: editingOccurrence.project_id ?? editingDefinition.project_id,
        taskID: editingDefinition.id,
        occurrenceID: editingOccurrence.id,
        input: {
          expected_task_revision:
            editingOccurrence.task_revision ?? editingDefinition.revision,
          expected_schedule_revision:
            editingOccurrence.schedule_revision ??
            editingDefinition.schedule_revision,
          expected_occurrence_revision: editingOccurrence.revision,
          timing: {
            timing_type: rescheduleTimingType,
            timezone:
              editingOccurrence.timezone ??
              Intl.DateTimeFormat().resolvedOptions().timeZone,
            planned_date: rescheduleDate,
            ...(rescheduleTimingType === 'time_block'
              ? {
                  local_start_time: rescheduleTimeRange.start,
                  duration_minutes: durationMinutes,
                }
              : {}),
          },
        },
      })
      setEditingOccurrenceID('')
      setRescheduleDate('')
    } catch (caught) {
      if (caught instanceof TaskDomainRevisionConflictError) {
        setRescheduleConflict(caught)
        return
      }
      setRescheduleError('保存改期失败，请稍后重试。')
    }
  }

  const occurrenceCollections = collectOccurrencesByTask(activeOccurrences)
  const hasCollections = occurrenceCollections.some(
    (collection) => collection.occurrences.length > 1
  )

  function renderOccurrence(occurrence: OccurrenceV2) {
    const task = definitionsByID.get(occurrence.task_id)
    const project = task ? projectsByID.get(task.project_id) : undefined
    const title = occurrenceTitle(occurrence, task)
    return (
      <OccurrenceRow
        key={occurrence.id}
        occurrence={occurrence}
        task={task}
        project={project}
        selected={occurrence.id === selectedOccurrenceID}
        onSelect={() =>
          updateSearchParams({
            occurrence_id: occurrence.id,
            task_id: null,
          })
        }
        onComplete={
          task
            ? () =>
                completeOccurrence.mutateAsync(
                  occurrenceCommandVariables(task, occurrence)
                )
            : undefined
        }
        trailingAction={
          <Button
            type="button"
            size="icon-sm"
            variant="ghost"
            aria-label={`改期${title}`}
            onClick={(event) => {
              event.stopPropagation()
              beginReschedule(occurrence)
            }}
          >
            <CalendarClock aria-hidden="true" />
          </Button>
        }
      />
    )
  }

  function renderOccurrenceCollection(collection: OccurrenceCollectionData) {
    if (collection.occurrences.length === 1)
      return renderOccurrence(collection.occurrences[0])
    const task = definitionsByID.get(collection.taskID)
    const projectID = collection.occurrences[0].project_id ?? task?.project_id
    const containsSelection = collection.occurrences.some(
      (occurrence) => occurrence.id === selectedOccurrenceID
    )
    const expanded = expandedTaskIDs.has(collection.taskID) || containsSelection
    return (
      <OccurrenceCollection
        key={collection.taskID}
        task={task}
        projectName={projectID ? projectsByID.get(projectID)?.name : undefined}
        occurrences={collection.occurrences}
        expanded={expanded}
        onToggle={() => {
          setExpandedTaskIDs((current) => {
            const next = new Set(current)
            if (expanded) next.delete(collection.taskID)
            else next.add(collection.taskID)
            return next
          })
          if (expanded && containsSelection) {
            updateSearchParams({ occurrence_id: null, task_id: null })
            setEditingOccurrenceID('')
          }
        }}
      >
        {expanded ? collection.occurrences.map(renderOccurrence) : null}
      </OccurrenceCollection>
    )
  }

  const activeCount =
    activeTab === 'draft' ? activeDrafts.length : activeOccurrences.length
  const listQuery = activeQuery ?? draftsQuery
  const listLoading =
    listQuery.isLoading ||
    (activeTab !== 'draft' && (metadataLoading || projectsQuery.isLoading))
  const inboxUnavailable =
    activeTab === 'inbox' && !projectsQuery.isLoading && !inboxProject
  const listError =
    listQuery.isError ||
    (activeTab !== 'draft' &&
      (metadataError || projectsQuery.isError || inboxUnavailable))
  const hasFilters = Boolean(
    projectFilter || priorityFilter || statusFilter || dateFilter !== 'current'
  )

  return (
    <section className="planning-page" aria-labelledby="tasks-heading">
      <PlanningHeading
        id="tasks-heading"
        title="任务"
        description="按项目、优先级和时间查看任务，安排下一步。"
      >
        <Button type="button" onClick={() => setCaptureOpen(true)}>
          <Plus aria-hidden="true" data-icon="inline-start" />
          创建任务
        </Button>
      </PlanningHeading>

      <Card className="min-w-0 gap-0 py-0 shadow-none">
        <PlanningTabs
          label="任务执行筛选"
          value={activeTab}
          tabs={tabDefinitions.map((tab) => ({
            ...tab,
            count: viewCounts[tab.id],
          }))}
          onChange={(tab) =>
            updateSearchParams({
              view: tab === 'upcoming' ? null : tab,
              occurrence_id: null,
              task_id: null,
            })
          }
        >
          <div className="planning-filters" role="group" aria-label="任务筛选">
            <label>
              <span>项目</span>
              <NativeSelect
                ref={projectFilterRef}
                aria-label="按项目筛选"
                value={projectFilter}
                onChange={(event) =>
                  updateSearchParams({
                    project: event.target.value || null,
                    occurrence_id: null,
                    task_id: null,
                  })
                }
              >
                <NativeSelectOption value="">全部</NativeSelectOption>
                {(projectsQuery.data ?? []).map((project) => (
                  <NativeSelectOption key={project.id} value={project.id}>
                    {project.name}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </label>
            <label>
              <span>优先级</span>
              <NativeSelect
                aria-label="按优先级筛选"
                value={priorityFilter}
                onChange={(event) =>
                  updateSearchParams({
                    priority: event.target.value || null,
                    occurrence_id: null,
                    task_id: null,
                  })
                }
              >
                <NativeSelectOption value="">全部</NativeSelectOption>
                <NativeSelectOption value="3">紧急</NativeSelectOption>
                <NativeSelectOption value="2">高</NativeSelectOption>
                <NativeSelectOption value="1">中</NativeSelectOption>
                <NativeSelectOption value="0">普通</NativeSelectOption>
              </NativeSelect>
            </label>
            <label>
              <span>状态</span>
              <NativeSelect
                aria-label="按状态筛选"
                value={statusFilter}
                disabled={activeTab === 'draft'}
                onChange={(event) =>
                  updateSearchParams({
                    status: event.target.value || null,
                    occurrence_id: null,
                    task_id: null,
                  })
                }
              >
                <NativeSelectOption value="">全部</NativeSelectOption>
                <NativeSelectOption value="open">未开始</NativeSelectOption>
                <NativeSelectOption value="active">进行中</NativeSelectOption>
                <NativeSelectOption value="blocked">阻塞</NativeSelectOption>
                <NativeSelectOption value="done">已完成</NativeSelectOption>
                <NativeSelectOption value="skipped">已跳过</NativeSelectOption>
                <NativeSelectOption value="cancelled">
                  已取消
                </NativeSelectOption>
              </NativeSelect>
            </label>
            <label>
              <span>日期范围</span>
              <NativeSelect
                aria-label="按日期范围筛选"
                value={dateFilter}
                disabled={activeTab === 'draft'}
                onChange={(event) =>
                  updateSearchParams({
                    date:
                      event.target.value === 'current'
                        ? null
                        : event.target.value,
                    occurrence_id: null,
                    task_id: null,
                  })
                }
              >
                <NativeSelectOption value="current">
                  当前视图
                </NativeSelectOption>
                <NativeSelectOption value="today">今天</NativeSelectOption>
                <NativeSelectOption value="next-7-days">
                  未来 7 天
                </NativeSelectOption>
                <NativeSelectOption value="unscheduled">
                  无日期
                </NativeSelectOption>
              </NativeSelect>
            </label>
            <div className="planning-filter-summary">
              <span role="status">
                {listLoading || listError
                  ? '当前显示 — 项'
                  : activeTab !== 'draft' && hasCollections
                    ? `当前显示 ${occurrenceCollections.length} 个任务 · ${activeCount} 次执行`
                    : `当前显示 ${activeCount} 项`}
              </span>
              {hasFilters ? (
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  onClick={() => {
                    updateSearchParams({
                      project: null,
                      priority: null,
                      status: null,
                      date: null,
                      occurrence_id: null,
                      task_id: null,
                    })
                    projectFilterRef.current?.focus()
                  }}
                >
                  清除筛选
                </Button>
              ) : null}
            </div>
          </div>

          <p className="px-4 pb-3 text-xs text-muted-foreground">
            视图数量按所选项目统计，其他筛选仅影响下方列表。
          </p>
          {facetsQuery.isError ? (
            <Alert variant="destructive">
              <CircleAlert aria-hidden="true" />
              <AlertTitle>视图计数暂时不可用，请稍后重试。</AlertTitle>
            </Alert>
          ) : null}
          {(selectedOccurrenceID &&
            selectedOccurrenceQuery.isLoading &&
            !selectedOccurrence) ||
          ((selectedTaskID || selectedOccurrenceID) &&
            selectedTaskQuery.isLoading &&
            !selectedTask) ? (
            <PlanningLoading label="正在加载任务详情" />
          ) : null}
          {(selectedOccurrenceID && selectedOccurrenceQuery.isError) ||
          ((selectedTaskID || selectedOccurrenceID) &&
            selectedTaskQuery.isError) ? (
            <Alert variant="destructive">
              <AlertTitle>任务详情暂时不可用，请稍后重试。</AlertTitle>
            </Alert>
          ) : null}
          {selectedTask && selectedTaskOccurrencesQuery.isError ? (
            <Alert variant="destructive">
              <AlertTitle>任务执行历史暂时不可用，请稍后重试。</AlertTitle>
            </Alert>
          ) : null}
          <div
            className={`td-workspace planning-workspace ${
              selectedOccurrence || selectedTask ? 'has-inspector' : ''
            }`}
          >
            <div className="td-list-canvas">
              {activeTab === 'inbox' && inboxProject ? (
                <div className="td-context-note">
                  <Inbox aria-hidden="true" />
                  <div>
                    <strong>{inboxProject.name}</strong>
                    <span>这里收纳尚未整理到具体项目的任务。</span>
                  </div>
                </div>
              ) : null}

              {activeTab === 'draft' ? (
                <div
                  className="td-definition-list"
                  role="list"
                  aria-label="任务草稿"
                >
                  {activeDrafts.map((task) => (
                    <article
                      className={`td-definition-row ${
                        selectedTaskID === task.id ? 'is-selected' : ''
                      }`}
                      role="listitem"
                      tabIndex={0}
                      key={task.id}
                      onClick={() =>
                        updateSearchParams({
                          task_id: task.id,
                          occurrence_id: null,
                        })
                      }
                      onKeyDown={(event) => {
                        if (event.key === 'Enter') {
                          updateSearchParams({
                            task_id: task.id,
                            occurrence_id: null,
                          })
                        }
                      }}
                    >
                      <div>
                        <strong>{task.title}</strong>
                        <span>
                          {projectsByID.get(task.project_id)?.name ??
                            '未命名项目'}
                        </span>
                      </div>
                      <span>草稿</span>
                    </article>
                  ))}
                </div>
              ) : (
                <>
                  {activeCount > 0 ? (
                    <div className="td-list-columns" aria-hidden="true">
                      <span />
                      <span>任务 / 项目</span>
                      <span>安排</span>
                      <span>状态</span>
                      <span />
                    </div>
                  ) : null}
                  <div
                    className="td-occurrence-list"
                    role="list"
                    aria-label="任务执行实例"
                  >
                    {occurrenceCollections.map(renderOccurrenceCollection)}
                  </div>
                </>
              )}

              {listLoading ? <PlanningLoading label="正在加载任务" /> : null}
              {listError ? (
                <Alert variant="destructive">
                  <CircleAlert aria-hidden="true" />
                  <AlertTitle>
                    {activeTab === 'draft'
                      ? '任务草稿暂时不可用，请刷新后重试。'
                      : '执行实例暂时不可用，请刷新后重试。'}
                  </AlertTitle>
                </Alert>
              ) : null}
              {taskCommandError && !selectedTask ? (
                <Alert variant="destructive">
                  <CircleAlert aria-hidden="true" />
                  <AlertTitle>{taskCommandError}</AlertTitle>
                </Alert>
              ) : null}
              {activeCount === 0 && !listLoading && !listError ? (
                <Empty className="planning-empty">
                  <EmptyHeader>
                    <EmptyMedia variant="icon">
                      <ListChecks aria-hidden="true" />
                    </EmptyMedia>
                    <EmptyTitle>
                      {hasFilters
                        ? '没有符合筛选条件的任务'
                        : '这个视图还没有任务'}
                    </EmptyTitle>
                    <EmptyDescription>
                      {hasFilters
                        ? '调整上方筛选条件后再试。'
                        : '创建任务或切换到其他视图。'}
                    </EmptyDescription>
                  </EmptyHeader>
                </Empty>
              ) : null}
            </div>

            {selectedOccurrence && selectedTask ? (
              <OccurrenceInspector
                key={selectedOccurrence.id}
                occurrence={selectedOccurrence}
                task={selectedTask}
                project={selectedProject}
                busy={commandBusy || selectedReadError}
                onClose={() => {
                  updateSearchParams({ occurrence_id: null, task_id: null })
                  setEditingOccurrenceID('')
                }}
                onStart={() =>
                  startOccurrence.mutateAsync(
                    occurrenceCommandVariables(selectedTask, selectedOccurrence)
                  )
                }
                onComplete={() =>
                  completeOccurrence.mutateAsync(
                    occurrenceCommandVariables(selectedTask, selectedOccurrence)
                  )
                }
                onReopen={() =>
                  reopenOccurrence.mutateAsync(
                    occurrenceCommandVariables(selectedTask, selectedOccurrence)
                  )
                }
                onBlock={(reason, nextAction) =>
                  blockOccurrence.mutateAsync({
                    ...occurrenceCommandVariables(
                      selectedTask,
                      selectedOccurrence
                    ),
                    blockedReason: reason,
                    nextAction,
                  })
                }
                onUnblock={() =>
                  unblockOccurrence.mutateAsync(
                    occurrenceCommandVariables(selectedTask, selectedOccurrence)
                  )
                }
                onReschedule={() => beginReschedule(selectedOccurrence)}
                onViewTask={() =>
                  updateSearchParams({
                    task_id: selectedTask.id,
                    occurrence_id: null,
                  })
                }
                onArchive={archiveSelectedTask}
                onDelete={deleteSelectedTask}
              >
                {editingOccurrence?.id === selectedOccurrence.id ? (
                  <form
                    className="td-reschedule-form"
                    onSubmit={handleReschedule}
                  >
                    <div>
                      <strong>调整本次安排</strong>
                      <span>可设置全天日期或具体时间，只影响这一次执行。</span>
                    </div>
                    <div className="td-reschedule-grid">
                      <label>
                        <span>安排方式</span>
                        <select
                          aria-label="新的安排方式"
                          value={rescheduleTimingType}
                          onChange={(event) =>
                            setRescheduleTimingType(
                              event.target.value as Exclude<
                                TimingType,
                                'unscheduled'
                              >
                            )
                          }
                        >
                          <option value="date">全天</option>
                          <option value="time_block">具体时间</option>
                        </select>
                      </label>
                      <label>
                        <span>新的执行日期</span>
                        <input
                          type="date"
                          aria-label="新的执行日期"
                          value={rescheduleDate}
                          onChange={(event) =>
                            setRescheduleDate(event.target.value)
                          }
                        />
                      </label>
                    </div>
                    {rescheduleTimingType === 'time_block' ? (
                      <div className="td-reschedule-time-row">
                        <label>
                          <span>开始时间</span>
                          <input
                            type="time"
                            aria-label="新的开始时间"
                            value={rescheduleTimeRange.start}
                            onChange={(event) => {
                              const start = event.target.value
                              setRescheduleTimeRange((current) => ({
                                start,
                                end:
                                  timeRangeMinutes(start, current.end) > 0
                                    ? current.end
                                    : addMinutes(start, 60),
                              }))
                            }}
                          />
                        </label>
                        <span aria-hidden="true">至</span>
                        <label>
                          <span>结束时间</span>
                          <input
                            type="time"
                            aria-label="新的结束时间"
                            value={rescheduleTimeRange.end}
                            onChange={(event) =>
                              setRescheduleTimeRange((current) => ({
                                ...current,
                                end: event.target.value,
                              }))
                            }
                          />
                        </label>
                      </div>
                    ) : null}
                    <div className="td-form-actions">
                      <button
                        type="button"
                        onClick={() => setEditingOccurrenceID('')}
                      >
                        取消
                      </button>
                      <button
                        type="submit"
                        className="is-primary"
                        disabled={
                          rescheduleDate === '' ||
                          (rescheduleTimingType === 'time_block' &&
                            timeRangeMinutes(
                              rescheduleTimeRange.start,
                              rescheduleTimeRange.end
                            ) <= 0) ||
                          rescheduleOccurrence.isPending ||
                          selectedReadError
                        }
                      >
                        保存改期
                      </button>
                    </div>
                    {rescheduleConflict ? (
                      <div className="td-conflict" role="alert">
                        <strong>执行实例已在其他窗口更新</strong>
                        <p>
                          你的安排仍保留为{' '}
                          {formatRescheduleDraft(
                            rescheduleDate,
                            rescheduleTimingType,
                            rescheduleTimeRange
                          )}
                          ，没有覆盖服务器版本。
                        </p>
                        <div className="td-form-actions">
                          <button
                            type="button"
                            onClick={async () => {
                              setRescheduleError('')
                              const results = await Promise.all([
                                activeQuery?.refetch(),
                                selectedOccurrenceQuery.refetch(),
                                selectedTaskQuery.refetch(),
                                selectedTaskOccurrencesQuery.refetch(),
                                facetsQuery.refetch(),
                              ])
                              if (results.some((result) => result?.isError)) {
                                setRescheduleError(
                                  '刷新服务器版本失败，请稍后重试。'
                                )
                                return
                              }
                              setRescheduleConflict(null)
                            }}
                          >
                            刷新服务器版本
                          </button>
                          <button
                            type="button"
                            onClick={() =>
                              setShowComparison((visible) => !visible)
                            }
                          >
                            比较差异
                          </button>
                        </div>
                        {showComparison ? (
                          <dl className="td-revision-comparison">
                            <div>
                              <dt>本地 revision</dt>
                              <dd>{editingOccurrence.revision}</dd>
                            </div>
                            <div>
                              <dt>服务器 revision</dt>
                              <dd>
                                {rescheduleConflict.currentRevisions
                                  ?.occurrence_revisions?.[
                                  editingOccurrence.id
                                ] ?? '未知'}
                              </dd>
                            </div>
                          </dl>
                        ) : null}
                      </div>
                    ) : null}
                    {rescheduleError ? (
                      <div className="td-inline-error" role="alert">
                        {rescheduleError}
                      </div>
                    ) : null}
                  </form>
                ) : null}
              </OccurrenceInspector>
            ) : selectedTask ? (
              <TaskDefinitionInspector
                commandError={taskCommandError}
                task={selectedTask}
                project={selectedProject}
                occurrences={selectedTaskOccurrences}
                busy={commandBusy || !taskCommandsReady || selectedReadError}
                onClose={() =>
                  updateSearchParams({ occurrence_id: null, task_id: null })
                }
                onUpdate={(input) =>
                  updateTask.mutateAsync({
                    projectID: selectedTask.project_id,
                    taskID: selectedTask.id,
                    input: {
                      ...input,
                      expected_task_revision: selectedTask.revision,
                      expected_schedule_revision:
                        selectedTask.schedule_revision,
                    },
                  })
                }
                onPublish={() =>
                  handleTaskCommand(() =>
                    publishTask.mutateAsync(taskCommandVariables(selectedTask))
                  )
                }
                onPause={() =>
                  handleTaskCommand(() =>
                    pauseTask.mutateAsync(taskCommandVariables(selectedTask))
                  )
                }
                onResume={() =>
                  handleTaskCommand(() =>
                    resumeTask.mutateAsync(taskCommandVariables(selectedTask))
                  )
                }
                onCancel={() =>
                  handleTaskCommand(() =>
                    cancelTask.mutateAsync(taskCommandVariables(selectedTask))
                  )
                }
                onRestore={() =>
                  handleTaskCommand(() =>
                    restoreTask.mutateAsync(taskCommandVariables(selectedTask))
                  )
                }
                onArchive={archiveSelectedTask}
                onDelete={deleteSelectedTask}
              />
            ) : null}
          </div>
        </PlanningTabs>
      </Card>
    </section>
  )
}

function timeRangeForOccurrence(occurrence: OccurrenceV2) {
  const timezone =
    occurrence.timezone ?? Intl.DateTimeFormat().resolvedOptions().timeZone
  const start = occurrence.planned_start_at
    ? formatLocalTime(occurrence.planned_start_at, timezone)
    : '09:00'
  const duration = occurrenceDurationMinutes(occurrence)
  return { start, end: addMinutes(start, duration) }
}

function occurrenceDurationMinutes(occurrence: OccurrenceV2) {
  if (!occurrence.planned_start_at || !occurrence.planned_end_at) return 60
  const start = new Date(occurrence.planned_start_at).getTime()
  const end = new Date(occurrence.planned_end_at).getTime()
  const duration = Math.round((end - start) / 60_000)
  return duration > 0 ? duration : 60
}

function formatLocalTime(value: string, timezone: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '09:00'
  return new Intl.DateTimeFormat('en-GB', {
    timeZone: timezone,
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  }).format(date)
}

function timeRangeMinutes(start: string, end: string) {
  return timeToMinutes(end) - timeToMinutes(start)
}

function timeToMinutes(value: string) {
  const [hours, minutes] = value.split(':').map(Number)
  if (!Number.isFinite(hours) || !Number.isFinite(minutes)) return 0
  return hours * 60 + minutes
}

function addMinutes(value: string, minutes: number) {
  const total = Math.min(timeToMinutes(value) + minutes, 23 * 60 + 59)
  const hours = Math.floor(total / 60)
  const remainingMinutes = total % 60
  return `${String(hours).padStart(2, '0')}:${String(remainingMinutes).padStart(2, '0')}`
}

function formatRescheduleDraft(
  date: string,
  timingType: Exclude<TimingType, 'unscheduled'>,
  timeRange: { start: string; end: string }
) {
  return timingType === 'date'
    ? `${date} 全天`
    : `${date} ${timeRange.start}–${timeRange.end}`
}
