import {
  CalendarDays,
  CalendarPlus,
  ChevronDown,
  Layers3,
  Plus,
  ArrowUpRight,
  CheckCheck,
  CircleDot,
  CalendarClock,
  NotebookPen,
  CircleAlert,
  ListTodo,
} from 'lucide-react'
import { type ReactNode, useMemo, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'

import {
  getTaskDefinition,
  type OccurrenceV2,
  type TaskV2,
} from '../api/taskDomain'
import {
  createWorkspaceQueryParams,
  type WorkspaceOccurrence,
} from '../api/workspace'
import {
  createWorkspaceDateWindows,
  createWorkspaceOccurrenceParams,
} from '../utils/workspaceDates'
import { useWorkspaceOverview } from '../hooks/useWorkspace'
import { taskCompletionProgress } from '../components/taskDomain/TaskCompletionGate'
import {
  PlanningHeading,
  PlanningLoading,
  PlanningTabs,
} from '../components/planning/PlanningWorkspace'
import { Alert, AlertTitle } from '../components/ui/alert'
import { Button } from '../components/ui/button'
import { Card, CardHeader, CardTitle } from '../components/ui/card'
import {
  RecentNotes,
  WorkspaceProjects,
  WorkspaceShortcuts,
} from '../components/personalWorkspace/WorkspaceOverview'
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '../components/ui/empty'
import {
  OccurrenceInspector,
  OccurrenceRow,
} from '../components/taskDomain/TaskDomainWorkspace'
import { ScheduleCreateDialog } from '../components/taskDomain/ScheduleCreateDialog'
import {
  useBlockOccurrenceMutation,
  useCompleteOccurrenceMutation,
  taskDomainQueryKeys,
  useOccurrence,
  useOccurrences,
  useProject,
  useProjects,
  useReopenOccurrenceMutation,
  useStartOccurrenceMutation,
  useTaskDefinition,
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
  const openPlanCapture = useUIStore((state) => state.openPlanCapture)
  const setCaptureOpen = useUIStore((state) => state.setCaptureOpen)
  const queryClient = useQueryClient()
  const queryParams = createWorkspaceQueryParams()
  const [completionNotice, setCompletionNotice] = useState('')
  const requestedTab = searchParams.get('view')
  const activeTab: TodayTab =
    requestedTab === 'week' ||
    requestedTab === 'month' ||
    requestedTab === 'overdue' ||
    requestedTab === 'done'
      ? requestedTab
      : 'today'
  const selectedOccurrenceID = searchParams.get('occurrence_id') ?? ''

  const overviewQuery = useWorkspaceOverview(queryParams)
  const overview = overviewQuery.isError ? undefined : overviewQuery.data
  const facets = overview?.facets
  // Only the selected non-preview view needs an occurrence collection. Counters
  // always come from the aggregate, independently of the number of visible rows.
  const viewParams = createWorkspaceOccurrenceParams(
    activeTab === 'done' ? 'completed' : activeTab,
    queryParams
  )
  const occurrencesQuery = useOccurrences(viewParams, {
    enabled: activeTab !== 'today',
  })
  // A catalog is needed for the selected view's names or the creation form,
  // never for computing homepage statistics.
  const projectsQuery = useProjects(
    {},
    {
      enabled: activeTab !== 'today' || scheduleCreateOpen,
    }
  )
  const completeOccurrence = useCompleteOccurrenceMutation()
  const startOccurrence = useStartOccurrenceMutation()
  const blockOccurrence = useBlockOccurrenceMutation()
  const unblockOccurrence = useUnblockOccurrenceMutation()
  const reopenOccurrence = useReopenOccurrenceMutation()
  const projects = new Map(
    (projectsQuery.data ?? []).map((project) => [project.id, project])
  )
  const activeOccurrences =
    activeTab === 'today'
      ? (overview?.today ?? emptyOccurrences)
      : occurrencesQuery.isError
        ? emptyOccurrences
        : (occurrencesQuery.data ?? emptyOccurrences)
  const listLoading =
    activeTab === 'today' ? overviewQuery.isLoading : occurrencesQuery.isLoading
  const listError =
    activeTab === 'today' ? overviewQuery.isError : occurrencesQuery.isError
  const tabCounts = {
    today: facets?.today,
    week: facets?.week,
    month: facets?.month,
    overdue: facets?.overdue,
    done: facets?.completed_total,
  }
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
  const selectedOccurrenceQuery = useOccurrence(selectedOccurrenceID)
  const selectedOccurrence = selectedOccurrenceID
    ? (selectedOccurrenceQuery.data ??
      activeOccurrences.find(
        (occurrence) => occurrence.id === selectedOccurrenceID
      ))
    : undefined
  const selectedTaskQuery = useTaskDefinition(selectedOccurrence?.task_id ?? '')
  const selectedTask = selectedTaskQuery.data
  const selectedProjectQuery = useProject(selectedOccurrence?.project_id ?? '')
  const selectedProject = selectedProjectQuery.data
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

  function projectName(occurrence: OccurrenceV2) {
    return (
      (occurrence as Partial<WorkspaceOccurrence>).project_name ??
      (occurrence.project_id
        ? projects.get(occurrence.project_id)?.name
        : undefined)
    )
  }

  async function completeRow(occurrence: OccurrenceV2) {
    // Completion requirements are command preconditions. Read the single task
    // at interaction time instead of downloading every task on page load.
    const task = await queryClient.fetchQuery({
      queryKey: taskDomainQueryKeys.task(occurrence.task_id),
      queryFn: () => getTaskDefinition(occurrence.task_id),
      staleTime: 0,
    })
    if (taskCompletionProgress(task).remaining > 0) {
      setCompletionNotice('请先完成必选项。')
      updateSearchParams({ occurrence_id: occurrence.id, task_id: null })
      return
    }
    setCompletionNotice('')
    await completeOccurrence.mutateAsync(commandVariables(task, occurrence))
  }

  function renderOccurrence(occurrence: OccurrenceV2) {
    const task =
      selectedTask?.id === occurrence.task_id ? selectedTask : undefined
    return (
      <OccurrenceRow
        key={occurrence.id}
        occurrence={occurrence}
        task={task}
        projectName={projectName(occurrence)}
        selected={occurrence.id === selectedOccurrenceID}
        onSelect={() => {
          setCompletionNotice('')
          updateSearchParams({ occurrence_id: occurrence.id, task_id: null })
        }}
        onComplete={() => completeRow(occurrence)}
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

    const task =
      selectedTask?.id === collection.taskID ? selectedTask : undefined
    const containsSelection = collection.occurrences.some(
      (occurrence) => occurrence.id === selectedOccurrenceID
    )
    const expanded = expandedTaskIDs.has(collection.taskID) || containsSelection
    return (
      <OccurrenceCollection
        key={collection.taskID}
        task={task}
        projectName={projectName(collection.occurrences[0])}
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

  function selectTab(tab: TodayTab) {
    setCompletionNotice('')
    updateSearchParams({
      view: tab === 'today' ? null : tab,
      occurrence_id: null,
      task_id: null,
    })
  }

  return (
    <section
      className="planning-page personal-workspace-page"
      aria-labelledby="today-heading"
    >
      <PlanningHeading
        id="today-heading"
        title="我的工作台"
        date={formatToday()}
        description="把想法、计划与行动，放在一个空间。"
      >
        <Button
          type="button"
          variant="outline"
          onClick={() => setScheduleCreateOpen(true)}
        >
          <CalendarPlus aria-hidden="true" data-icon="inline-start" />
          新增日程
        </Button>
        <Button
          type="button"
          variant="outline"
          onClick={() => openPlanCapture()}
        >
          语音计划
        </Button>
        <Button type="button" onClick={() => setCaptureOpen(true)}>
          <Plus aria-hidden="true" data-icon="inline-start" />
          添加任务
        </Button>
      </PlanningHeading>

      <WorkspaceShortcuts />

      <div
        className="workspace-metrics mb-5 grid grid-cols-3 gap-2.5 sm:gap-4"
        role="group"
        aria-label="任务概览"
      >
        {(
          [
            {
              tab: 'today',
              label: '今日待办',
              count: facets?.today,
              icon: CircleDot,
            },
            {
              tab: 'week',
              label: '本周安排',
              count: facets?.week,
              icon: CalendarDays,
            },
            {
              tab: 'done',
              label: '累计完成',
              count: facets?.completed_total,
              icon: CheckCheck,
            },
          ] as const
        ).map(({ tab, label, count, icon: Icon }) => (
          <Card key={tab} className="relative min-w-0 gap-0 py-0 shadow-none">
            <Button
              type="button"
              variant="ghost"
              aria-pressed={activeTab === tab}
              className="planning-metric"
              onClick={() => selectTab(tab)}
            >
              <span className="planning-metric-icon">
                <Icon aria-hidden="true" />
              </span>
              <span className="flex min-w-0 flex-col gap-1 text-left">
                <span className="text-xs font-medium sm:text-sm">{label}</span>
                <strong className="text-2xl font-semibold leading-none tracking-tight tabular-nums sm:text-[1.8rem]">
                  {count ?? '—'}
                  <small className="ml-1 text-xs font-normal text-muted-foreground">
                    项
                  </small>
                </strong>
              </span>
              <ArrowUpRight
                className="planning-metric-arrow"
                aria-hidden="true"
              />
            </Button>
          </Card>
        ))}
      </div>

      <div className={selectedOccurrence ? 'min-w-0' : 'planning-dashboard'}>
        <Card className="planning-agenda min-w-0 gap-0 py-0 shadow-none">
          <CardHeader className="workspace-panel-header">
            <CardTitle>
              <h2>
                <ListTodo aria-hidden="true" />
                行动清单
              </h2>
            </CardTitle>
            <Button asChild variant="ghost" size="sm">
              <Link to="/tasks" aria-label="查看全部任务">
                查看全部
                <ArrowUpRight aria-hidden="true" data-icon="inline-end" />
              </Link>
            </Button>
          </CardHeader>
          <PlanningTabs
            label="今日任务筛选"
            value={activeTab}
            tabs={todayTabs.map((tab) => ({
              ...tab,
              count: tabCounts[tab.id],
            }))}
            onChange={selectTab}
          >
            <div
              className={`td-workspace planning-workspace ${selectedOccurrence ? 'has-inspector' : ''}`}
            >
              <div className="td-list-canvas">
                {listLoading ? (
                  <PlanningLoading label="正在加载今日执行" />
                ) : null}
                {listError ? (
                  <Alert variant="destructive">
                    <CircleAlert aria-hidden="true" />
                    <AlertTitle>
                      {activeTab === 'today'
                        ? '工作台暂时不可用，请刷新后重试。'
                        : '执行清单暂时不可用，请刷新后重试。'}
                    </AlertTitle>
                  </Alert>
                ) : null}

                {activeTab === 'today' &&
                facets &&
                facets.today > activeOccurrences.length ? (
                  <p className="px-4 py-2 text-sm text-muted-foreground">
                    显示前 {activeOccurrences.length} 项，共 {facets.today} 项。{' '}
                    <Link className="underline" to="/tasks?view=today">
                      查看全部今日任务
                    </Link>
                  </p>
                ) : null}
                {selectedOccurrenceID &&
                selectedOccurrenceQuery.isLoading &&
                !selectedOccurrence ? (
                  <PlanningLoading label="正在加载执行详情" />
                ) : null}
                {selectedOccurrenceID && selectedOccurrenceQuery.isError ? (
                  <Alert variant="destructive">
                    <AlertTitle>执行详情暂时不可用，请稍后重试。</AlertTitle>
                  </Alert>
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

                {occurrenceSections.flexibleCount > 0 ? (
                  <>
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
                  </>
                ) : null}

                {activeOccurrences.length === 0 &&
                !listLoading &&
                !listError ? (
                  <Empty className="planning-empty">
                    <EmptyHeader>
                      <EmptyMedia variant="icon">
                        <CalendarDays aria-hidden="true" />
                      </EmptyMedia>
                      <EmptyTitle>
                        {activeTab === 'today'
                          ? '今天的安排，从这里开始'
                          : '这个视图还没有任务'}
                      </EmptyTitle>
                      <EmptyDescription>
                        {emptyStateDescription(activeTab)}
                      </EmptyDescription>
                    </EmptyHeader>
                    {activeTab === 'today' ? (
                      <EmptyContent>
                        <Button
                          type="button"
                          variant="outline"
                          onClick={() => setCaptureOpen(true)}
                        >
                          <Plus aria-hidden="true" data-icon="inline-start" />
                          记录一个任务
                        </Button>
                      </EmptyContent>
                    ) : null}
                  </Empty>
                ) : null}
              </div>

              {selectedOccurrence ? (
                <OccurrenceInspector
                  key={selectedOccurrence.id}
                  occurrence={selectedOccurrence}
                  task={selectedTask}
                  project={selectedProject}
                  projectName={projectName(selectedOccurrence)}
                  busy={busy}
                  onClose={() =>
                    updateSearchParams(
                      { occurrence_id: null, task_id: null },
                      true
                    )
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
                            ...commandVariables(
                              selectedTask,
                              selectedOccurrence
                            ),
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
                >
                  {selectedTaskQuery.isLoading ? (
                    <PlanningLoading label="正在加载任务详情" />
                  ) : null}
                  {selectedTaskQuery.isError ? (
                    <Alert variant="destructive">
                      <AlertTitle>任务详情暂时不可用，请稍后重试。</AlertTitle>
                    </Alert>
                  ) : null}
                  {completionNotice ? (
                    <p role="status">{completionNotice}</p>
                  ) : null}
                </OccurrenceInspector>
              ) : null}
            </div>
          </PlanningTabs>
        </Card>
        {!selectedOccurrence ? (
          <aside className="workspace-overview" aria-label="工作区概览">
            <WorkspaceProjects
              projects={overview?.projects ?? []}
              loading={overviewQuery.isLoading}
              error={overviewQuery.isError}
            />
            <RecentNotes
              section={overview?.notes}
              loading={overviewQuery.isLoading}
              error={overviewQuery.isError}
            />
          </aside>
        ) : null}
      </div>

      <footer className="workspace-home-footer">
        <span>留一点空间，专注重要的事。</span>
        <div>
          <Link to="/tasks?view=unscheduled">
            <CalendarClock aria-hidden="true" />
            还没安排的任务
          </Link>
          <Link to="/notes">
            <NotebookPen aria-hidden="true" />
            打开笔记库
          </Link>
        </div>
      </footer>

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
            {projectName ?? '未命名项目'} ·{' '}
            {collectionStatusSummary(occurrences)}
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
  const { week, month } = createWorkspaceDateWindows(
    createWorkspaceQueryParams(reference, timezone)
  )
  return { week, month }
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
