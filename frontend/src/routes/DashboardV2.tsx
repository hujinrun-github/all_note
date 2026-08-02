import {
  CalendarDays,
  CalendarPlus,
  ChevronDown,
  Layers3,
  Plus,
} from 'lucide-react'
import { type ReactNode, useMemo, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'

import type { OccurrenceV2, TaskV2 } from '../api/taskDomain'
import {
  OccurrenceInspector,
  OccurrenceRow,
} from '../components/taskDomain/TaskDomainWorkspace'
import { ScheduleCreateDialog } from '../components/taskDomain/ScheduleCreateDialog'
import {
  useBlockOccurrenceMutation,
  useCompleteOccurrenceMutation,
  useOccurrences,
  useProjects,
  useReopenOccurrenceMutation,
  useStartOccurrenceMutation,
  useTaskDefinitions,
  useUnblockOccurrenceMutation,
} from '../hooks/useTaskDomain'
import { useUIStore } from '../stores/ui'

type TodayTab = 'today' | 'week' | 'month' | 'overdue' | 'done'

const todayTabs: Array<{ id: TodayTab; label: string }> = [
  { id: 'today', label: '今天' },
  { id: 'week', label: '本周' },
  { id: 'month', label: '本月' },
  { id: 'overdue', label: '已逾期' },
  { id: 'done', label: '已完成' },
]

const emptyOccurrences: OccurrenceV2[] = []

interface OccurrenceCollectionData {
  taskID: string
  occurrences: OccurrenceV2[]
}

export default function DashboardV2() {
  const [searchParams, setSearchParams] = useSearchParams()
  const [scheduleCreateOpen, setScheduleCreateOpen] = useState(false)
  const [expandedTaskIDs, setExpandedTaskIDs] = useState<Set<string>>(
    () => new Set()
  )
  const navigate = useNavigate()
  const setCaptureOpen = useUIStore((state) => state.setCaptureOpen)
  const [dateRanges] = useState(createDashboardDateRanges)
  const requestedTab = searchParams.get('view')
  const activeTab: TodayTab =
    requestedTab === 'week' ||
    requestedTab === 'month' ||
    requestedTab === 'overdue' ||
    requestedTab === 'done'
      ? requestedTab
      : 'today'
  const selectedOccurrenceID = searchParams.get('occurrence_id') ?? ''

  const todayQuery = useOccurrences({ scope: 'today' })
  const weekQuery = useOccurrences({
    scope: 'upcoming',
    ...dateRanges.week,
  })
  const monthQuery = useOccurrences({
    scope: 'upcoming',
    ...dateRanges.month,
  })
  const overdueQuery = useOccurrences({ scope: 'overdue' })
  const completedQuery = useOccurrences({ scope: 'completed' })
  const definitionsQuery = useTaskDefinitions()
  const projectsQuery = useProjects()
  const completeOccurrence = useCompleteOccurrenceMutation()
  const startOccurrence = useStartOccurrenceMutation()
  const blockOccurrence = useBlockOccurrenceMutation()
  const unblockOccurrence = useUnblockOccurrenceMutation()
  const reopenOccurrence = useReopenOccurrenceMutation()

  const definitions = useMemo(
    () =>
      new Map(
        (definitionsQuery.data ?? []).map((definition) => [
          definition.id,
          definition,
        ])
      ),
    [definitionsQuery.data]
  )
  const projects = useMemo(
    () =>
      new Map(
        (projectsQuery.data ?? []).map((project) => [project.id, project])
      ),
    [projectsQuery.data]
  )
  const queries = {
    today: todayQuery,
    week: weekQuery,
    month: monthQuery,
    overdue: overdueQuery,
    done: completedQuery,
  }
  const activeQuery = queries[activeTab]
  const activeOccurrences = activeQuery.data ?? emptyOccurrences
  const groupByTask = activeTab === 'week' || activeTab === 'month'
  const occurrenceSections = useMemo(() => {
    const timed: OccurrenceV2[] = []
    const flexible: OccurrenceV2[] = []
    activeOccurrences.forEach((occurrence) => {
      if (occurrence.planned_start_at) timed.push(occurrence)
      else flexible.push(occurrence)
    })
    return {
      timedCount: timed.length,
      flexibleCount: flexible.length,
      timedCollections: collectOccurrencesByTask(timed, groupByTask),
      flexibleCollections: collectOccurrencesByTask(flexible, groupByTask),
    }
  }, [activeOccurrences, groupByTask])
  const selectedOccurrence = activeOccurrences.find(
    (occurrence) => occurrence.id === selectedOccurrenceID
  )
  const selectedTask = selectedOccurrence
    ? definitions.get(selectedOccurrence.task_id)
    : undefined
  const selectedProject = selectedTask
    ? projects.get(selectedTask.project_id)
    : undefined
  const busy =
    completeOccurrence.isPending ||
    startOccurrence.isPending ||
    blockOccurrence.isPending ||
    unblockOccurrence.isPending ||
    reopenOccurrence.isPending

  function updateSearchParams(
    update: Record<string, string | null>,
    replace = false
  ) {
    const next = new URLSearchParams(searchParams)
    Object.entries(update).forEach(([key, value]) => {
      if (value === null || value === '') next.delete(key)
      else next.set(key, value)
    })
    setSearchParams(next, { replace })
  }

  function expectedRevisions(task: TaskV2, occurrence: OccurrenceV2) {
    return {
      expected_task_revision: occurrence.task_revision ?? task.revision,
      expected_schedule_revision:
        occurrence.schedule_revision ?? task.schedule_revision,
      expected_occurrence_revisions: {
        [occurrence.id]: occurrence.revision,
      },
    }
  }

  function commandVariables(task: TaskV2, occurrence: OccurrenceV2) {
    return {
      projectID: occurrence.project_id ?? task.project_id,
      taskID: task.id,
      occurrenceID: occurrence.id,
      expectedRevisions: expectedRevisions(task, occurrence),
    }
  }

  function renderOccurrence(occurrence: OccurrenceV2) {
    const task = definitions.get(occurrence.task_id)
    const project = task ? projects.get(task.project_id) : undefined
    return (
      <OccurrenceRow
        key={occurrence.id}
        occurrence={occurrence}
        task={task}
        project={project}
        selected={occurrence.id === selectedOccurrenceID}
        onSelect={() =>
          updateSearchParams({ occurrence_id: occurrence.id, task_id: null })
        }
        onComplete={
          task
            ? () =>
                void completeOccurrence.mutateAsync(
                  commandVariables(task, occurrence)
                )
            : undefined
        }
      />
    )
  }

  function renderOccurrenceCollection(collection: OccurrenceCollectionData) {
    if (
      collection.occurrences.length === 1 &&
      !collection.occurrences[0].recurring
    ) {
      return renderOccurrence(collection.occurrences[0])
    }

    const task = definitions.get(collection.taskID)
    const project = task ? projects.get(task.project_id) : undefined
    const containsSelection = collection.occurrences.some(
      (occurrence) => occurrence.id === selectedOccurrenceID
    )
    const expanded = expandedTaskIDs.has(collection.taskID) || containsSelection
    return (
      <OccurrenceCollection
        key={collection.taskID}
        task={task}
        projectName={project?.name}
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
            updateSearchParams({ occurrence_id: null, task_id: null }, true)
          }
        }}
      >
        {expanded ? collection.occurrences.map(renderOccurrence) : null}
      </OccurrenceCollection>
    )
  }

  return (
    <section className="td-page td-today-page" aria-labelledby="today-heading">
      <header className="td-page-header">
        <div>
          <div className="td-title-line">
            <h1 id="today-heading">今天</h1>
            <time>{formatToday()}</time>
          </div>
          <p>先完成今天，逾期事项不会抢占你的注意力。</p>
        </div>
        <div className="td-page-actions">
          <button
            type="button"
            className="td-secondary-action"
            onClick={() => setScheduleCreateOpen(true)}
          >
            <CalendarPlus aria-hidden="true" />
            新增日程
          </button>
          <button
            type="button"
            className="td-primary-action"
            onClick={() => setCaptureOpen(true)}
          >
            <Plus aria-hidden="true" />
            添加任务
          </button>
        </div>
      </header>

      <div className="td-tabs" role="tablist" aria-label="今日任务筛选">
        {todayTabs.map((tab) => {
          const count = queries[tab.id].data?.length ?? 0
          return (
            <button
              type="button"
              role="tab"
              aria-selected={activeTab === tab.id}
              className={activeTab === tab.id ? 'is-active' : ''}
              key={tab.id}
              onClick={() =>
                updateSearchParams({
                  view: tab.id === 'today' ? null : tab.id,
                  occurrence_id: null,
                  task_id: null,
                })
              }
            >
              {tab.label} <span>{count}</span>
            </button>
          )
        })}
      </div>

      <div
        className={`td-workspace ${selectedOccurrence ? 'has-inspector' : ''}`}
      >
        <div className="td-list-canvas">
          {activeQuery.isLoading || definitionsQuery.isLoading ? (
            <div className="td-loading-list" aria-label="正在加载今日执行">
              <span />
              <span />
              <span />
            </div>
          ) : null}
          {activeQuery.isError ? (
            <div className="td-inline-error" role="alert">
              今日执行暂时不可用，请刷新后重试。
            </div>
          ) : null}

          {occurrenceSections.timedCount > 0 ? (
            <>
              <SectionHeading
                title="固定时间"
                count={`${occurrenceSections.timedCount} 个安排`}
              />
              <div
                className="td-occurrence-list is-timeline"
                role="list"
                aria-label="固定时间任务"
              >
                {occurrenceSections.timedCollections.map(
                  renderOccurrenceCollection
                )}
              </div>
            </>
          ) : null}

          <SectionHeading
            title={occurrenceSectionTitle(activeTab)}
            count={`${occurrenceSections.flexibleCount} 个行动`}
          />
          <div
            className="td-occurrence-list"
            role="list"
            aria-label={`${todayTabLabel(activeTab)}执行实例`}
          >
            {occurrenceSections.flexibleCollections.map(
              renderOccurrenceCollection
            )}
          </div>

          {activeOccurrences.length === 0 && !activeQuery.isLoading ? (
            <div className="td-empty-state">
              <CalendarDays aria-hidden="true" />
              <strong>这里还没有执行实例</strong>
              <span>
                {emptyStateDescription(activeTab)}
              </span>
            </div>
          ) : null}
        </div>

        {selectedOccurrence ? (
          <OccurrenceInspector
            key={selectedOccurrence.id}
            occurrence={selectedOccurrence}
            task={selectedTask}
            project={selectedProject}
            busy={busy}
            onClose={() =>
              updateSearchParams({ occurrence_id: null, task_id: null }, true)
            }
            onStart={
              selectedTask
                ? () =>
                    startOccurrence.mutateAsync(
                      commandVariables(selectedTask, selectedOccurrence)
                    )
                : undefined
            }
            onComplete={
              selectedTask
                ? () =>
                    completeOccurrence.mutateAsync(
                      commandVariables(selectedTask, selectedOccurrence)
                    )
                : undefined
            }
            onReopen={
              selectedTask
                ? () =>
                    reopenOccurrence.mutateAsync(
                      commandVariables(selectedTask, selectedOccurrence)
                    )
                : undefined
            }
            onBlock={
              selectedTask
                ? (reason, nextAction) =>
                    blockOccurrence.mutateAsync({
                      ...commandVariables(selectedTask, selectedOccurrence),
                      blockedReason: reason,
                      nextAction,
                    })
                : undefined
            }
            onUnblock={
              selectedTask
                ? () =>
                    unblockOccurrence.mutateAsync(
                      commandVariables(selectedTask, selectedOccurrence)
                    )
                : undefined
            }
            onViewTask={
              selectedTask
                ? () =>
                    navigate(
                      `/tasks?task_id=${encodeURIComponent(selectedTask.id)}`
                    )
                : undefined
            }
          />
        ) : null}
      </div>

      {scheduleCreateOpen ? (
        <ScheduleCreateDialog
          projects={projectsQuery.data ?? []}
          onClose={() => setScheduleCreateOpen(false)}
        />
      ) : null}
    </section>
  )
}

function SectionHeading({ title, count }: { title: string; count: string }) {
  return (
    <header className="td-section-heading">
      <strong>{title}</strong>
      <span>{count}</span>
    </header>
  )
}

function OccurrenceCollection({
  task,
  projectName,
  occurrences,
  expanded,
  onToggle,
  children,
}: {
  task?: TaskV2
  projectName?: string
  occurrences: OccurrenceV2[]
  expanded: boolean
  onToggle: () => void
  children: ReactNode
}) {
  const title = task?.title ?? occurrences[0]?.title ?? '未命名任务'
  const collectionID = `occurrence-collection-${occurrences[0]?.id}`
  return (
    <section
      className={`td-occurrence-collection ${expanded ? 'is-expanded' : ''}`}
      role="listitem"
    >
      <button
        type="button"
        className="td-occurrence-collection-toggle"
        aria-expanded={expanded}
        aria-controls={collectionID}
        aria-label={`${expanded ? '收起' : '展开'}${title}，${occurrences.length} 次执行`}
        onClick={onToggle}
      >
        <span className="td-occurrence-collection-icon" aria-hidden="true">
          <Layers3 />
        </span>
        <span className="td-occurrence-collection-copy">
          <strong>{title}</strong>
          <span>
            {projectName ?? '未命名项目'} · {collectionStatusSummary(occurrences)}
          </span>
        </span>
        <span className="td-occurrence-collection-range">
          {collectionScheduleRange(occurrences)}
        </span>
        <span className="td-occurrence-collection-count">
          {occurrences.length} 次执行
        </span>
        <ChevronDown
          className="td-occurrence-collection-chevron"
          aria-hidden="true"
        />
      </button>
      {expanded ? (
        <div
          id={collectionID}
          className="td-occurrence-collection-items"
          role="list"
          aria-label={`${title}执行实例`}
        >
          {children}
        </div>
      ) : null}
    </section>
  )
}

function collectOccurrencesByTask(
  occurrences: OccurrenceV2[],
  groupByTask: boolean
): OccurrenceCollectionData[] {
  if (!groupByTask) {
    return occurrences.map((occurrence) => ({
      taskID: occurrence.task_id,
      occurrences: [occurrence],
    }))
  }

  const collections = new Map<string, OccurrenceCollectionData>()
  occurrences.forEach((occurrence) => {
    const collection = collections.get(occurrence.task_id)
    if (collection) collection.occurrences.push(occurrence)
    else {
      collections.set(occurrence.task_id, {
        taskID: occurrence.task_id,
        occurrences: [occurrence],
      })
    }
  })
  return Array.from(collections.values())
}

function collectionStatusSummary(occurrences: OccurrenceV2[]) {
  const counts = new Map<string, number>()
  occurrences.forEach((occurrence) => {
    counts.set(
      occurrence.execution_status,
      (counts.get(occurrence.execution_status) ?? 0) + 1
    )
  })
  const labels: Array<[OccurrenceV2['execution_status'], string]> = [
    ['open', '待执行'],
    ['active', '进行中'],
    ['blocked', '阻塞'],
    ['done', '已完成'],
    ['skipped', '已跳过'],
    ['cancelled', '已取消'],
  ]
  return labels
    .flatMap(([status, label]) => {
      const count = counts.get(status) ?? 0
      return count > 0 ? [`${count} ${label}`] : []
    })
    .join(' · ')
}

function collectionScheduleRange(occurrences: OccurrenceV2[]) {
  const first = collectionDateLabel(occurrences[0])
  const last = collectionDateLabel(occurrences.at(-1))
  if (first === last) return first
  return `${first} – ${last}`
}

function collectionDateLabel(occurrence?: OccurrenceV2) {
  if (!occurrence) return '未安排'
  if (occurrence.planned_date) {
    const [, month, day] = occurrence.planned_date.split('-').map(Number)
    return `${month}月${day}日`
  }
  if (!occurrence.planned_start_at) return '未安排'
  return new Intl.DateTimeFormat('zh-CN', {
    month: 'long',
    day: 'numeric',
    timeZone: occurrence.timezone,
  }).format(new Date(occurrence.planned_start_at))
}

function formatToday() {
  const date = new Date()
  return new Intl.DateTimeFormat('zh-CN', {
    month: 'long',
    day: 'numeric',
    weekday: 'long',
  }).format(date)
}

interface DashboardDateRange {
  from: string
  to: string
  timezone: string
}

export function createDashboardDateRanges(
  reference = new Date(),
  timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
): { week: DashboardDateRange; month: DashboardDateRange } {
  const startOfWeek = new Date(reference)
  startOfWeek.setHours(0, 0, 0, 0)
  const daysSinceMonday = (startOfWeek.getDay() + 6) % 7
  startOfWeek.setDate(startOfWeek.getDate() - daysSinceMonday)
  const startOfNextWeek = new Date(startOfWeek)
  startOfNextWeek.setDate(startOfNextWeek.getDate() + 7)

  const startOfMonth = new Date(reference)
  startOfMonth.setHours(0, 0, 0, 0)
  startOfMonth.setDate(1)
  const startOfNextMonth = new Date(startOfMonth)
  startOfNextMonth.setMonth(startOfNextMonth.getMonth() + 1)

  return {
    week: {
      from: startOfWeek.toISOString(),
      to: startOfNextWeek.toISOString(),
      timezone,
    },
    month: {
      from: startOfMonth.toISOString(),
      to: startOfNextMonth.toISOString(),
      timezone,
    },
  }
}

function todayTabLabel(tab: TodayTab) {
  return todayTabs.find((candidate) => candidate.id === tab)?.label ?? '今天'
}

function occurrenceSectionTitle(tab: TodayTab) {
  switch (tab) {
    case 'today':
      return '今天要做'
    case 'week':
      return '本周要做'
    case 'month':
      return '本月要做'
    case 'overdue':
      return '已逾期'
    case 'done':
      return '已完成'
  }
}

function emptyStateDescription(tab: TodayTab) {
  switch (tab) {
    case 'today':
      return '添加一个今天要完成的任务。'
    case 'week':
      return '本周暂时没有待执行任务。'
    case 'month':
      return '本月暂时没有待执行任务。'
    case 'overdue':
    case 'done':
      return '切换视图或调整任务安排。'
  }
}
