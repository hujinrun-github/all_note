import {
  ArrowRight,
  Check,
  Circle,
  Inbox as InboxIcon,
  Plus,
} from 'lucide-react'
import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'

import type { ExecutionStatus, OccurrenceV2, TaskV2 } from '../api/taskDomain'
import { TaskDomainRevisionConflictError } from '../api/taskDomain'
import {
  ExecutionStatusLabel,
  occurrenceTitle,
} from '../components/taskDomain/TaskDomainWorkspace'
import {
  useCompleteOccurrenceMutation,
  useTaskDefinitions,
  useUpdateTaskDefinitionMutation,
} from '../hooks/useTaskDomain'
import { useTaskInbox } from '../hooks/useTaskInbox'
import { useUIStore } from '../stores/ui'

type InboxFilter = 'all' | 'open' | 'active' | 'blocked'

const filters: Array<{ id: InboxFilter; label: string }> = [
  { id: 'all', label: '全部' },
  { id: 'open', label: '待开始' },
  { id: 'active', label: '进行中' },
  { id: 'blocked', label: '已阻塞' },
]

export default function Inbox() {
  const setCaptureOpen = useUIStore((state) => state.setCaptureOpen)
  const { inboxProject, occurrencesQuery, projectsQuery } = useTaskInbox()
  const definitionsQuery = useTaskDefinitions()
  const updateTask = useUpdateTaskDefinitionMutation()
  const completeOccurrence = useCompleteOccurrenceMutation()
  const [filter, setFilter] = useState<InboxFilter>('all')
  const [selectedOccurrenceID, setSelectedOccurrenceID] = useState('')
  const [targetProjectID, setTargetProjectID] = useState('')
  const [error, setError] = useState('')

  const tasksByID = useMemo(
    () =>
      new Map(
        (definitionsQuery.data ?? []).map((task) => [task.id, task] as const)
      ),
    [definitionsQuery.data]
  )
  const occurrences = occurrencesQuery.data ?? []
  const filteredOccurrences = occurrences.filter((occurrence) =>
    matchesFilter(occurrence.execution_status, filter)
  )
  const selectedOccurrence =
    filteredOccurrences.find(
      (occurrence) => occurrence.id === selectedOccurrenceID
    ) ?? filteredOccurrences[0]
  const selectedTask = selectedOccurrence
    ? tasksByID.get(selectedOccurrence.task_id)
    : undefined
  const organizeTargets = (projectsQuery.data ?? []).filter(
    (project) =>
      !project.system_role &&
      project.status !== 'completed' &&
      project.status !== 'archived'
  )
  const resolvedTargetProjectID =
    organizeTargets.find((project) => project.id === targetProjectID)?.id ??
    organizeTargets[0]?.id ??
    ''
  const selectedTargetProject = organizeTargets.find(
    (project) => project.id === resolvedTargetProjectID
  )
  const isLoading =
    projectsQuery.isLoading ||
    definitionsQuery.isLoading ||
    occurrencesQuery.isLoading
  const hasQueryError =
    projectsQuery.isError ||
    definitionsQuery.isError ||
    occurrencesQuery.isError

  async function organizeSelectedTask() {
    if (!selectedTask || !inboxProject || resolvedTargetProjectID === '') {
      return
    }
    setError('')
    try {
      await updateTask.mutateAsync({
        projectID: inboxProject.id,
        taskID: selectedTask.id,
        input: {
          expected_task_revision: selectedTask.revision,
          expected_schedule_revision: selectedTask.schedule_revision,
          project_id: resolvedTargetProjectID,
        },
      })
      setSelectedOccurrenceID('')
      setTargetProjectID('')
    } catch (caught) {
      setError(
        caught instanceof TaskDomainRevisionConflictError
          ? '任务刚刚被其他操作更新了，请刷新后重试。'
          : '整理失败，请稍后重试。'
      )
    }
  }

  async function completeSelectedOccurrence() {
    if (!selectedTask || !selectedOccurrence) return
    setError('')
    try {
      await completeOccurrence.mutateAsync(
        occurrenceCommandVariables(selectedTask, selectedOccurrence)
      )
      setSelectedOccurrenceID('')
    } catch (caught) {
      setError(
        caught instanceof TaskDomainRevisionConflictError
          ? '任务状态已经变化，请刷新后重试。'
          : '完成任务失败，请稍后重试。'
      )
    }
  }

  return (
    <div className="inbox-v2-page">
      <header className="inbox-v2-hero">
        <div>
          <h1>未整理</h1>
          <p>
            快速捕获且尚未归入具体项目的任务都会来到这里。选定项目后，任务会离开收件箱并进入对应项目。
          </p>
        </div>
        <button
          type="button"
          className="primary-action"
          onClick={() => setCaptureOpen(true)}
        >
          <Plus aria-hidden="true" />
          快速捕获
        </button>
      </header>

      <section className="inbox-v2-summary" aria-label="收件箱概览">
        <div className="inbox-v2-summary-main">
          <span className="inbox-v2-summary-icon">
            <InboxIcon aria-hidden="true" />
          </span>
          <div>
            <strong>{occurrences.length}</strong>
            <span>条任务等待整理</span>
          </div>
        </div>
        <p>
          {occurrences.length === 0
            ? '收件箱已经清空，新的快速捕获会直接出现在这里。'
            : '每次只做一个决定：放进项目、继续执行，或者直接完成。'}
        </p>
      </section>

      <div className="inbox-v2-workspace">
        <section className="inbox-v2-list-panel">
          <div className="inbox-v2-list-heading">
            <div>
              <h2>任务收件箱</h2>
              <span>{filteredOccurrences.length} 条</span>
            </div>
            <div className="inbox-v2-filters" aria-label="收件箱筛选">
              {filters.map((item) => (
                <button
                  key={item.id}
                  type="button"
                  className={filter === item.id ? 'is-active' : ''}
                  aria-pressed={filter === item.id}
                  onClick={() => {
                    setFilter(item.id)
                    setSelectedOccurrenceID('')
                  }}
                >
                  {item.label}
                  <span>{filterCount(occurrences, item.id)}</span>
                </button>
              ))}
            </div>
          </div>

          {isLoading ? (
            <div className="inbox-v2-loading" aria-label="正在加载收件箱">
              <span />
              <span />
              <span />
            </div>
          ) : null}

          {hasQueryError ? (
            <div className="inbox-v2-error" role="alert">
              收件箱暂时不可用，请刷新后重试。
            </div>
          ) : null}

          {!isLoading && !hasQueryError && filteredOccurrences.length === 0 ? (
            <div className="inbox-v2-empty">
              <span>
                <Check aria-hidden="true" />
              </span>
              <strong>
                {occurrences.length === 0
                  ? '这里已经整理干净'
                  : '这个筛选下没有任务'}
              </strong>
              <p>
                {occurrences.length === 0
                  ? '新的快速捕获会默认进入系统收件箱。'
                  : '切换到其他状态继续整理。'}
              </p>
            </div>
          ) : null}

          <div className="inbox-v2-task-list" role="list">
            {filteredOccurrences.map((occurrence) => {
              const task = tasksByID.get(occurrence.task_id)
              const title = occurrenceTitle(occurrence, task)
              const selected = occurrence.id === selectedOccurrence?.id
              return (
                <article
                  key={occurrence.id}
                  role="listitem"
                  className={`inbox-v2-task-row ${
                    selected ? 'is-selected' : ''
                  }`}
                  tabIndex={0}
                  aria-current={selected ? 'true' : undefined}
                  onClick={() => setSelectedOccurrenceID(occurrence.id)}
                  onKeyDown={(event) => {
                    if (event.key !== 'Enter' && event.key !== ' ') return
                    event.preventDefault()
                    setSelectedOccurrenceID(occurrence.id)
                  }}
                >
                  <span className="inbox-v2-task-check">
                    <Circle aria-hidden="true" />
                  </span>
                  <div className="inbox-v2-task-copy">
                    <strong>{title}</strong>
                    <span>{task?.description || '尚未补充任务说明'}</span>
                  </div>
                  <span className="inbox-v2-priority">
                    {priorityLabel(task?.priority)}
                  </span>
                  <ExecutionStatusLabel status={occurrence.execution_status} />
                  <ArrowRight aria-hidden="true" />
                </article>
              )
            })}
          </div>
        </section>

        <aside className="inbox-v2-organizer" aria-label="整理任务">
          {selectedOccurrence && selectedTask ? (
            <>
              <div className="inbox-v2-organizer-heading">
                <span>整理任务</span>
                <h2>{occurrenceTitle(selectedOccurrence, selectedTask)}</h2>
                <p>将任务归入一个明确项目，后续安排和执行会在该项目中继续。</p>
              </div>

              <dl className="inbox-v2-task-details">
                <div>
                  <dt>当前归属</dt>
                  <dd>{inboxProject?.name ?? '系统收件箱'}</dd>
                </div>
                <div>
                  <dt>当前状态</dt>
                  <dd>
                    <ExecutionStatusLabel
                      status={selectedOccurrence.execution_status}
                    />
                  </dd>
                </div>
                <div>
                  <dt>安排</dt>
                  <dd>{scheduleLabel(selectedOccurrence)}</dd>
                </div>
              </dl>

              <label className="inbox-v2-project-field">
                <span>归入项目</span>
                <select
                  aria-label="归入项目"
                  value={resolvedTargetProjectID}
                  disabled={organizeTargets.length === 0}
                  onChange={(event) => setTargetProjectID(event.target.value)}
                >
                  {organizeTargets.length === 0 ? (
                    <option value="">暂无可用项目</option>
                  ) : (
                    organizeTargets.map((project) => (
                      <option key={project.id} value={project.id}>
                        {project.name} ·{' '}
                        {project.kind === 'learning' ? '学习项目' : '标准项目'}
                      </option>
                    ))
                  )}
                </select>
                <small>
                  {selectedTargetProject
                    ? `整理后进入「${selectedTargetProject.name}」`
                    : '请先创建一个可用项目'}
                </small>
              </label>

              {error !== '' ? (
                <div className="inbox-v2-error" role="alert">
                  {error}
                </div>
              ) : null}

              <div className="inbox-v2-organizer-actions">
                <button
                  type="button"
                  className="primary-action"
                  disabled={
                    resolvedTargetProjectID === '' || updateTask.isPending
                  }
                  onClick={() => void organizeSelectedTask()}
                >
                  {updateTask.isPending ? '正在整理…' : '归入项目'}
                  <ArrowRight aria-hidden="true" />
                </button>
                <button
                  type="button"
                  className="secondary-action"
                  disabled={completeOccurrence.isPending}
                  onClick={() => void completeSelectedOccurrence()}
                >
                  <Check aria-hidden="true" />
                  直接完成
                </button>
              </div>

              <Link
                className="inbox-v2-workbench-link"
                to={`/tasks?view=inbox&occurrence_id=${selectedOccurrence.id}`}
              >
                在任务工作台中查看
                <ArrowRight aria-hidden="true" />
              </Link>
            </>
          ) : (
            <div className="inbox-v2-organizer-empty">
              <InboxIcon aria-hidden="true" />
              <strong>选择一条任务</strong>
              <p>在左侧选择任务后，可将它归入项目或直接完成。</p>
            </div>
          )}
        </aside>
      </div>
    </div>
  )
}

function matchesFilter(status: ExecutionStatus, filter: InboxFilter) {
  if (filter === 'all') return true
  return status === filter
}

function filterCount(occurrences: OccurrenceV2[], filter: InboxFilter) {
  return occurrences.filter((occurrence) =>
    matchesFilter(occurrence.execution_status, filter)
  ).length
}

function priorityLabel(priority?: number) {
  if (priority === undefined || priority <= 0) return '普通'
  if (priority >= 3) return '紧急'
  if (priority === 2) return '高'
  return '中'
}

function scheduleLabel(occurrence: OccurrenceV2) {
  if (occurrence.planned_start_at) {
    return new Intl.DateTimeFormat('zh-CN', {
      month: 'numeric',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    }).format(new Date(occurrence.planned_start_at))
  }
  if (occurrence.planned_date) return occurrence.planned_date
  return '无日期'
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
