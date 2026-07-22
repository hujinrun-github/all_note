import { type FormEvent, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'

import type {
  ExecutionStatus,
  OccurrenceListScope,
  OccurrenceV2,
  TaskV2,
} from '../api/taskDomain'
import { TaskDomainRevisionConflictError } from '../api/taskDomain'
import {
  useCompleteOccurrenceMutation,
  useOccurrences,
  useProjects,
  useRescheduleOccurrenceMutation,
  useTaskDefinitions,
} from '../hooks/useTaskDomain'

type TaskTab = 'today' | 'upcoming' | 'overdue' | 'unscheduled' | 'recurring' | 'completed'

const tabDefinitions: Array<{
  id: TaskTab
  label: string
  scope: OccurrenceListScope
  recurring?: boolean
}> = [
  { id: 'today', label: '今天', scope: 'today' },
  { id: 'upcoming', label: '接下来', scope: 'upcoming' },
  { id: 'overdue', label: '已逾期', scope: 'overdue' },
  { id: 'unscheduled', label: '无日期', scope: 'unscheduled' },
  { id: 'recurring', label: '重复', scope: 'all', recurring: true },
  { id: 'completed', label: '已完成', scope: 'completed' },
]

export default function TaskOccurrenceWorkspace() {
  const [activeTab, setActiveTab] = useState<TaskTab>('upcoming')
  const [editingOccurrenceID, setEditingOccurrenceID] = useState('')
  const [rescheduleDate, setRescheduleDate] = useState('')
  const [rescheduleConflict, setRescheduleConflict] =
    useState<TaskDomainRevisionConflictError | null>(null)
  const [showComparison, setShowComparison] = useState(false)
  const projectsQuery = useProjects()
  const definitionsQuery = useTaskDefinitions()
  const todayQuery = useOccurrences({ scope: 'today' })
  const upcomingQuery = useOccurrences({ scope: 'upcoming' })
  const overdueQuery = useOccurrences({ scope: 'overdue' })
  const unscheduledQuery = useOccurrences({ scope: 'unscheduled' })
  const recurringQuery = useOccurrences({ scope: 'all', recurring: true })
  const completedQuery = useOccurrences({ scope: 'completed' })
  const completeOccurrence = useCompleteOccurrenceMutation()
  const rescheduleOccurrence = useRescheduleOccurrenceMutation()

  const queries = {
    today: todayQuery,
    upcoming: upcomingQuery,
    overdue: overdueQuery,
    unscheduled: unscheduledQuery,
    recurring: recurringQuery,
    completed: completedQuery,
  }
  const definitionsByID = useMemo(
    () =>
      new Map(
        (definitionsQuery.data ?? []).map((definition) => [
          definition.id,
          definition,
        ])
      ),
    [definitionsQuery.data]
  )
  const activeQuery = queries[activeTab]
  const activeOccurrences = activeQuery.data ?? []
  const inboxProject = (projectsQuery.data ?? []).find(
    (project) => project.system_role === 'inbox'
  )
  const editingOccurrence = activeOccurrences.find(
    (occurrence) => occurrence.id === editingOccurrenceID
  )
  const editingDefinition = editingOccurrence
    ? definitionsByID.get(editingOccurrence.task_id)
    : undefined

  async function handleReschedule(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!editingOccurrence || !editingDefinition || rescheduleDate === '') return
    setRescheduleConflict(null)
    setShowComparison(false)
    try {
      await rescheduleOccurrence.mutateAsync({
        projectID:
          editingOccurrence.project_id ?? editingDefinition.project_id,
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
            timing_type: 'date',
            timezone:
              editingOccurrence.timezone ??
              Intl.DateTimeFormat().resolvedOptions().timeZone,
            planned_date: rescheduleDate,
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
      throw caught
    }
  }

  return (
    <section className="domain-page task-occurrence-page" aria-labelledby="occurrence-heading">
      <header className="domain-page-heading">
        <div>
          <span className="domain-eyebrow">EXECUTION</span>
          <h2 id="occurrence-heading">任务执行</h2>
          <p>任务定义保持稳定；这里展示每一次真正要完成的执行实例。</p>
        </div>
        <Link className="domain-primary-button" to="/projects">
          按项目查看
        </Link>
      </header>

      <div className="task-v2-capture-note">
        <div>
          <strong>快速捕获</strong>
          <span>
            未选择项目的任务将明确进入「{inboxProject?.name ?? '系统收件箱'}」。
          </span>
        </div>
        <Link to={inboxProject ? `/projects/${inboxProject.id}` : '/projects'}>
          查看收件箱
        </Link>
      </div>

      <div className="task-v2-tabs" role="tablist" aria-label="任务执行筛选">
        {tabDefinitions.map((tab) => {
          const count = queries[tab.id].data?.length ?? 0
          return (
            <button
              type="button"
              role="tab"
              aria-selected={activeTab === tab.id}
              className={activeTab === tab.id ? 'is-active' : ''}
              key={tab.id}
              onClick={() => setActiveTab(tab.id)}
            >
              {tab.label} {count}
            </button>
          )
        })}
      </div>

      {activeQuery.isLoading || definitionsQuery.isLoading ? (
        <p className="domain-empty">正在加载执行实例…</p>
      ) : null}
      {activeQuery.isError ? (
        <p className="domain-empty">执行实例暂时不可用。</p>
      ) : null}

      <div className="task-v2-list" role="list" aria-label="任务执行实例">
        {activeOccurrences.map((occurrence) => {
          const definition = definitionsByID.get(occurrence.task_id)
          return (
            <OccurrenceRow
              key={occurrence.id}
              occurrence={occurrence}
              definition={definition}
              onComplete={() => {
                if (!definition) return
                void completeOccurrence.mutateAsync({
                  projectID:
                    occurrence.project_id ?? definition.project_id,
                  taskID: occurrence.task_id,
                  occurrenceID: occurrence.id,
                  expectedRevisions: {
                    expected_task_revision:
                      occurrence.task_revision ?? definition.revision,
                    expected_schedule_revision:
                      occurrence.schedule_revision ??
                      definition.schedule_revision,
                    expected_occurrence_revisions: {
                      [occurrence.id]: occurrence.revision,
                    },
                  },
                })
              }}
              onReschedule={() => {
                setEditingOccurrenceID(occurrence.id)
                setRescheduleDate(occurrence.planned_date ?? '')
                setRescheduleConflict(null)
                setShowComparison(false)
              }}
            />
          )
        })}
      </div>
      {activeOccurrences.length === 0 && !activeQuery.isLoading ? (
        <p className="domain-empty">这个视图里还没有执行实例。</p>
      ) : null}

      {editingOccurrence && editingDefinition ? (
        <form className="task-v2-reschedule" onSubmit={handleReschedule}>
          <div>
            <strong>改期：{editingOccurrence.title ?? editingDefinition.title}</strong>
            <span>只修改这一次执行，不改变任务定义。</span>
          </div>
          <label>
            <span>新的执行日期</span>
            <input
              type="date"
              aria-label="新的执行日期"
              value={rescheduleDate}
              onChange={(event) => setRescheduleDate(event.target.value)}
            />
          </label>
          <div className="domain-form-actions">
            <button type="button" onClick={() => setEditingOccurrenceID('')}>
              取消
            </button>
            <button
              type="submit"
              className="domain-primary-button"
              disabled={rescheduleDate === '' || rescheduleOccurrence.isPending}
            >
              保存改期
            </button>
          </div>
          {rescheduleConflict ? (
            <div className="domain-alert task-v2-conflict" role="alert">
              <strong>执行实例已在其他窗口更新</strong>
              <p>你的日期仍保留为 {rescheduleDate}，没有覆盖服务器版本。</p>
              <div className="domain-form-actions">
                <button
                  type="button"
                  onClick={() => {
                    setRescheduleConflict(null)
                    void activeQuery.refetch()
                  }}
                >
                  刷新服务器版本
                </button>
                <button
                  type="button"
                  onClick={() => setShowComparison((visible) => !visible)}
                >
                  比较差异
                </button>
              </div>
              {showComparison ? (
                <dl className="task-v2-revision-comparison">
                  <div>
                    <dt>本地 revision</dt>
                    <dd>{editingOccurrence.revision}</dd>
                  </div>
                  <div>
                    <dt>服务器 revision</dt>
                    <dd>
                      {rescheduleConflict.currentRevisions?.occurrence_revisions?.[
                        editingOccurrence.id
                      ] ?? '未知'}
                    </dd>
                  </div>
                </dl>
              ) : null}
            </div>
          ) : null}
        </form>
      ) : null}
    </section>
  )
}

function OccurrenceRow({
  occurrence,
  definition,
  onComplete,
  onReschedule,
}: {
  occurrence: OccurrenceV2
  definition?: TaskV2
  onComplete: () => void
  onReschedule: () => void
}) {
  const baseTitle = occurrence.title ?? definition?.title ?? '未命名任务'
  const title =
    occurrence.recurring && occurrence.planned_date
      ? `${baseTitle} · ${formatShortDate(occurrence.planned_date)}`
      : baseTitle
  return (
    <article className="task-v2-row" role="listitem">
      <button
        type="button"
        className="task-v2-check"
        aria-label={`完成${title}`}
        disabled={occurrence.execution_status === 'done'}
        onClick={onComplete}
      >
        {occurrence.execution_status === 'done' ? '✓' : ''}
      </button>
      <div className="task-v2-row-copy">
        <strong>{title}</strong>
        <div className="task-v2-row-meta">
          <span>定义：{lifecycleLabel(definition?.lifecycle_status)}</span>
          {occurrence.recurring ? <span>重复实例</span> : <span>单次实例</span>}
        </div>
        {occurrence.execution_status === 'blocked' ? (
          <div className="task-v2-blocked-copy">
            <span>原因：{occurrence.blocked_reason}</span>
            <span>下一步：{occurrence.next_action}</span>
          </div>
        ) : null}
      </div>
      <span
        className={`domain-execution-status execution-${occurrence.execution_status}`}
        aria-label={`${title}执行状态`}
      >
        {executionLabel(occurrence.execution_status)}
      </span>
      <button
        type="button"
        className="domain-text-button task-v2-reschedule-button"
        aria-label={`改期${title}`}
        onClick={onReschedule}
      >
        改期
      </button>
    </article>
  )
}

function executionLabel(status: ExecutionStatus) {
  return {
    open: '未开始',
    active: '进行中',
    blocked: '阻塞',
    done: '已完成',
    skipped: '已跳过',
    cancelled: '已取消',
  }[status]
}

function lifecycleLabel(status: TaskV2['lifecycle_status'] | undefined) {
  if (!status) return '未知'
  return {
    draft: '草稿',
    active: '进行中',
    paused: '已暂停',
    completed: '已完成',
    cancelled: '已取消',
    archived: '已归档',
  }[status]
}

function formatShortDate(value: string) {
  const [, month, day] = value.split('-')
  if (!month || !day) return value
  return `${Number(month)}月${Number(day)}日`
}
