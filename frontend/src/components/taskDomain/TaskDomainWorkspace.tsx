import {
  AlertOctagon,
  Archive,
  Ban,
  CalendarDays,
  Check,
  Circle,
  Folder,
  MoreHorizontal,
  Pause,
  Play,
  Repeat2,
  RotateCcw,
  Send,
  X,
} from 'lucide-react'
import { type KeyboardEvent, type ReactNode, useState } from 'react'

import type {
  ExecutionStatus,
  OccurrenceV2,
  ProjectV2,
  TaskLifecycleStatus,
  TaskV2,
} from '../../api/taskDomain'

export interface OccurrenceRowProps {
  occurrence: OccurrenceV2
  task?: TaskV2
  project?: ProjectV2
  selected?: boolean
  onSelect: () => void
  onComplete?: () => void
  trailingAction?: ReactNode
  showDefinitionState?: boolean
}

export function OccurrenceRow({
  occurrence,
  task,
  project,
  selected = false,
  onSelect,
  onComplete,
  trailingAction,
  showDefinitionState = false,
}: OccurrenceRowProps) {
  const title = occurrenceTitle(occurrence, task)
  const schedule = formatOccurrenceSchedule(occurrence)
  const canComplete =
    onComplete !== undefined && occurrence.execution_status !== 'done'

  function handleKeyboard(event: KeyboardEvent<HTMLElement>) {
    if (event.key !== 'Enter' && event.key !== ' ') return
    event.preventDefault()
    onSelect()
  }

  return (
    <article
      className={`td-occurrence-row ${selected ? 'is-selected' : ''}`}
      role="listitem"
      tabIndex={0}
      aria-current={selected ? 'true' : undefined}
      onClick={onSelect}
      onKeyDown={handleKeyboard}
    >
      <button
        type="button"
        className={`td-completion-control ${
          occurrence.execution_status === 'done' ? 'is-done' : ''
        }`}
        aria-label={`完成${title}`}
        disabled={!canComplete}
        onClick={(event) => {
          event.stopPropagation()
          onComplete?.()
        }}
      >
        {occurrence.execution_status === 'done' ? (
          <Check aria-hidden="true" />
        ) : (
          <Circle aria-hidden="true" />
        )}
      </button>

      <div className="td-occurrence-copy">
        <strong>{title}</strong>
        <div className="td-occurrence-meta">
          <span>
            <Folder aria-hidden="true" />
            {project?.name ?? '未命名项目'}
          </span>
          {occurrence.recurring ? (
            <span>
              <Repeat2 aria-hidden="true" />
              重复
            </span>
          ) : null}
          {showDefinitionState && task ? (
            <span>定义：{taskLifecycleLabel(task.lifecycle_status)}</span>
          ) : null}
        </div>
        {occurrence.execution_status === 'blocked' ? (
          <div className="td-blocked-reason">
            <AlertOctagon aria-hidden="true" />
            <span>原因：{occurrence.blocked_reason || '尚未填写'}</span>
            <span>下一步：{occurrence.next_action || '尚未填写'}</span>
          </div>
        ) : null}
      </div>

      <time className="td-occurrence-schedule">{schedule}</time>
      <ExecutionStatusLabel
        status={occurrence.execution_status}
        ariaLabel={`${title}执行状态`}
      />
      <div className="td-row-action">
        {trailingAction ?? <MoreHorizontal aria-hidden="true" />}
      </div>
    </article>
  )
}

export interface OccurrenceInspectorProps {
  occurrence: OccurrenceV2
  task?: TaskV2
  project?: ProjectV2
  onClose: () => void
  onStart?: () => Promise<unknown> | void
  onComplete?: () => Promise<unknown> | void
  onReopen?: () => Promise<unknown> | void
  onBlock?: (reason: string, nextAction: string) => Promise<unknown> | void
  onUnblock?: () => Promise<unknown> | void
  onReschedule?: () => void
  onViewTask?: () => void
  busy?: boolean
  children?: ReactNode
}

export function OccurrenceInspector({
  occurrence,
  task,
  project,
  onClose,
  onStart,
  onComplete,
  onReopen,
  onBlock,
  onUnblock,
  onReschedule,
  onViewTask,
  busy = false,
  children,
}: OccurrenceInspectorProps) {
  const [blocking, setBlocking] = useState(false)
  const [blockedReason, setBlockedReason] = useState(
    occurrence.blocked_reason ?? ''
  )
  const [nextAction, setNextAction] = useState(occurrence.next_action ?? '')
  const title = occurrenceTitle(occurrence, task)
  const terminal = ['done', 'skipped', 'cancelled'].includes(
    occurrence.execution_status
  )

  return (
    <aside
      className="td-inspector"
      role="complementary"
      aria-label={`执行详情：${title}`}
    >
      <header className="td-inspector-header">
        <div>
          <span>执行详情</span>
          <h2>{title}</h2>
        </div>
        <button type="button" aria-label="关闭执行详情" onClick={onClose}>
          <X aria-hidden="true" />
        </button>
      </header>

      <div className="td-inspector-body">
        <div className="td-command-grid">
          {terminal ? (
            <button
              type="button"
              className="is-primary"
              disabled={!onReopen || busy}
              onClick={() => void onReopen?.()}
            >
              <RotateCcw aria-hidden="true" />
              重新打开
            </button>
          ) : (
            <>
              {occurrence.execution_status === 'blocked' ? (
                <button
                  type="button"
                  className="is-primary"
                  disabled={!onUnblock || busy}
                  onClick={() => void onUnblock?.()}
                >
                  <Play aria-hidden="true" />
                  继续
                </button>
              ) : (
                <button
                  type="button"
                  className="is-primary"
                  disabled={!onStart || occurrence.execution_status === 'active' || busy}
                  onClick={() => void onStart?.()}
                >
                  <Play aria-hidden="true" />
                  开始
                </button>
              )}
              <button
                type="button"
                disabled={!onBlock || busy}
                onClick={() => setBlocking((current) => !current)}
              >
                <AlertOctagon aria-hidden="true" />
                阻塞
              </button>
              <button
                type="button"
                className="is-success"
                disabled={!onComplete || busy}
                onClick={() => void onComplete?.()}
              >
                <Check aria-hidden="true" />
                完成
              </button>
            </>
          )}
        </div>

        {blocking ? (
          <form
            className="td-block-form"
            onSubmit={(event) => {
              event.preventDefault()
              if (blockedReason.trim() === '' || nextAction.trim() === '') return
              void onBlock?.(blockedReason.trim(), nextAction.trim())
              setBlocking(false)
            }}
          >
            <label>
              <span>阻塞原因</span>
              <input
                value={blockedReason}
                onChange={(event) => setBlockedReason(event.target.value)}
              />
            </label>
            <label>
              <span>下一步</span>
              <input
                value={nextAction}
                onChange={(event) => setNextAction(event.target.value)}
              />
            </label>
            <button
              type="submit"
              disabled={
                blockedReason.trim() === '' || nextAction.trim() === '' || busy
              }
            >
              保存阻塞
            </button>
          </form>
        ) : null}

        <dl className="td-field-list">
          <div>
            <dt>执行状态</dt>
            <dd>
              <ExecutionStatusLabel status={occurrence.execution_status} />
            </dd>
          </div>
          <div>
            <dt>所属项目</dt>
            <dd>{project?.name ?? '未命名项目'}</dd>
          </div>
          <div>
            <dt>任务定义</dt>
            <dd>{task?.title ?? title}</dd>
          </div>
          <div>
            <dt>本次安排</dt>
            <dd>{formatOccurrenceSchedule(occurrence)}</dd>
          </div>
          {occurrence.location ? (
            <div>
              <dt>地点</dt>
              <dd>{occurrence.location}</dd>
            </div>
          ) : null}
          <div>
            <dt>重复</dt>
            <dd>{occurrence.recurring ? '重复任务实例' : '不重复'}</dd>
          </div>
        </dl>

        {occurrence.calendar_notes ? (
          <div className="td-inspector-note">
            <strong>本次备注</strong>
            <p>{occurrence.calendar_notes}</p>
          </div>
        ) : null}

        <div className="td-inspector-links">
          {onReschedule ? (
            <button
              type="button"
              aria-label={`改期${title}`}
              onClick={onReschedule}
            >
              <CalendarDays aria-hidden="true" />
              改期
            </button>
          ) : null}
          {onViewTask ? (
            <button type="button" onClick={onViewTask}>
              查看任务定义
              <span aria-hidden="true">→</span>
            </button>
          ) : null}
        </div>
        {children}
      </div>
    </aside>
  )
}

export interface TaskDefinitionInspectorProps {
  task: TaskV2
  project?: ProjectV2
  occurrences: OccurrenceV2[]
  onClose: () => void
  onPublish?: () => Promise<unknown> | void
  onPause?: () => Promise<unknown> | void
  onResume?: () => Promise<unknown> | void
  onCancel?: () => Promise<unknown> | void
  onRestore?: () => Promise<unknown> | void
  onArchive?: () => Promise<unknown> | void
  busy?: boolean
}

export function TaskDefinitionInspector({
  task,
  project,
  occurrences,
  onClose,
  onPublish,
  onPause,
  onResume,
  onCancel,
  onRestore,
  onArchive,
  busy = false,
}: TaskDefinitionInspectorProps) {
  return (
    <aside
      className="td-inspector"
      role="complementary"
      aria-label={`任务定义：${task.title}`}
    >
      <header className="td-inspector-header">
        <div>
          <span>任务定义</span>
          <h2>{task.title}</h2>
        </div>
        <button type="button" aria-label="关闭任务定义" onClick={onClose}>
          <X aria-hidden="true" />
        </button>
      </header>
      <div className="td-inspector-body">
        <TaskLifecycleActions
          status={task.lifecycle_status}
          onPublish={onPublish}
          onPause={onPause}
          onResume={onResume}
          onCancel={onCancel}
          onRestore={onRestore}
          onArchive={onArchive}
          busy={busy}
        />
        <dl className="td-field-list">
          <div>
            <dt>生命周期</dt>
            <dd>
              <TaskLifecycleStatusLabel status={task.lifecycle_status} />
            </dd>
          </div>
          <div>
            <dt>所属项目</dt>
            <dd>{project?.name ?? '未命名项目'}</dd>
          </div>
          <div>
            <dt>优先级</dt>
            <dd>{priorityLabel(task.priority)}</dd>
          </div>
          {task.task_note_id ? (
            <div>
              <dt>关联笔记</dt>
              <dd>{task.task_note_id}</dd>
            </div>
          ) : null}
        </dl>
        <div className="td-schedule-summary">
          <span>当前安排</span>
          <strong>{scheduleSummary(occurrences)}</strong>
          <p>{nextOccurrenceSummary(occurrences)}</p>
        </div>
        <div className="td-inspector-note">
          <strong>最近执行</strong>
          {occurrences.length > 0 ? (
            <ul>
              {occurrences.slice(0, 3).map((occurrence) => (
                <li key={occurrence.id}>
                  {formatOccurrenceSchedule(occurrence)} ·{' '}
                  {executionStatusLabel(occurrence.execution_status)}
                </li>
              ))}
            </ul>
          ) : (
            <p>发布后会在这里显示执行实例。</p>
          )}
        </div>
      </div>
    </aside>
  )
}

function TaskLifecycleActions({
  status,
  onPublish,
  onPause,
  onResume,
  onCancel,
  onRestore,
  onArchive,
  busy,
}: {
  status: TaskLifecycleStatus
  onPublish?: () => Promise<unknown> | void
  onPause?: () => Promise<unknown> | void
  onResume?: () => Promise<unknown> | void
  onCancel?: () => Promise<unknown> | void
  onRestore?: () => Promise<unknown> | void
  onArchive?: () => Promise<unknown> | void
  busy: boolean
}) {
  if (status === 'draft') {
    return (
      <div className="td-command-grid is-two">
        <button
          type="button"
          className="is-primary"
          disabled={!onPublish || busy}
          onClick={() => void onPublish?.()}
        >
          <Send aria-hidden="true" />
          发布
        </button>
        <button
          type="button"
          disabled={!onCancel || busy}
          onClick={() => void onCancel?.()}
        >
          <Ban aria-hidden="true" />
          取消
        </button>
      </div>
    )
  }
  if (status === 'paused') {
    return (
      <div className="td-command-grid is-two">
        <button
          type="button"
          className="is-primary"
          disabled={!onResume || busy}
          onClick={() => void onResume?.()}
        >
          <Play aria-hidden="true" />
          恢复
        </button>
        <button
          type="button"
          disabled={!onCancel || busy}
          onClick={() => void onCancel?.()}
        >
          <Ban aria-hidden="true" />
          取消
        </button>
      </div>
    )
  }
  if (status === 'cancelled') {
    return (
      <div className="td-command-grid is-two">
        <button
          type="button"
          className="is-primary"
          disabled={!onRestore || busy}
          onClick={() => void onRestore?.()}
        >
          <RotateCcw aria-hidden="true" />
          恢复
        </button>
        <button
          type="button"
          disabled={!onArchive || busy}
          onClick={() => void onArchive?.()}
        >
          <Archive aria-hidden="true" />
          归档
        </button>
      </div>
    )
  }
  if (status === 'completed') {
    return (
      <div className="td-command-grid is-one">
        <button
          type="button"
          disabled={!onArchive || busy}
          onClick={() => void onArchive?.()}
        >
          <Archive aria-hidden="true" />
          归档
        </button>
      </div>
    )
  }
  return (
    <div className="td-command-grid is-two">
      <button
        type="button"
        disabled={!onPause || busy}
        onClick={() => void onPause?.()}
      >
        <Pause aria-hidden="true" />
        暂停
      </button>
      <button
        type="button"
        disabled={!onCancel || busy}
        onClick={() => void onCancel?.()}
      >
        <Ban aria-hidden="true" />
        取消
      </button>
    </div>
  )
}

export function ExecutionStatusLabel({
  status,
  ariaLabel,
}: {
  status: ExecutionStatus
  ariaLabel?: string
}) {
  return (
    <span
      className={`td-status td-status-${status}`}
      aria-label={ariaLabel ?? `执行状态：${executionStatusLabel(status)}`}
    >
      {executionStatusIcon(status)}
      {executionStatusLabel(status)}
    </span>
  )
}

export function TaskLifecycleStatusLabel({
  status,
}: {
  status: TaskLifecycleStatus
}) {
  return (
    <span className={`td-status td-lifecycle-${status}`}>
      {taskLifecycleLabel(status)}
    </span>
  )
}

export function occurrenceTitle(occurrence: OccurrenceV2, task?: TaskV2) {
  const baseTitle = occurrence.title ?? task?.title ?? '未命名任务'
  if (!occurrence.recurring || !occurrence.planned_date) return baseTitle
  return `${baseTitle} · ${formatShortDate(occurrence.planned_date)}`
}

export function formatOccurrenceSchedule(occurrence: OccurrenceV2) {
  if (occurrence.planned_start_at) {
    const start = formatDateTime(occurrence.planned_start_at)
    const end = occurrence.planned_end_at
      ? formatTime(occurrence.planned_end_at)
      : ''
    return end ? `${start}–${end}` : start
  }
  if (occurrence.planned_date) return formatShortDate(occurrence.planned_date)
  return '无日期'
}

export function executionStatusLabel(status: ExecutionStatus) {
  return {
    open: '未开始',
    active: '进行中',
    blocked: '阻塞',
    done: '已完成',
    skipped: '已跳过',
    cancelled: '已取消',
  }[status]
}

export function taskLifecycleLabel(status: TaskLifecycleStatus) {
  return {
    draft: '草稿',
    active: '进行中',
    paused: '已暂停',
    completed: '已完成',
    cancelled: '已取消',
    archived: '已归档',
  }[status]
}

function executionStatusIcon(status: ExecutionStatus) {
  const iconProps = { 'aria-hidden': true as const }
  if (status === 'done') return <Check {...iconProps} />
  if (status === 'active') return <Play {...iconProps} />
  if (status === 'blocked') return <AlertOctagon {...iconProps} />
  if (status === 'cancelled') return <Ban {...iconProps} />
  if (status === 'skipped') return <RotateCcw {...iconProps} />
  return <Circle {...iconProps} />
}

function formatShortDate(value: string) {
  const [, month, day] = value.split('-')
  if (!month || !day) return value
  return `${Number(month)}月${Number(day)}日`
}

function formatDateTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return `${date.getMonth() + 1}月${date.getDate()}日 ${formatTime(value)}`
}

function formatTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat('zh-CN', {
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  }).format(date)
}

function priorityLabel(priority: number) {
  if (priority >= 3) return '紧急'
  if (priority === 2) return '高'
  if (priority === 1) return '中'
  return '普通'
}

function scheduleSummary(occurrences: OccurrenceV2[]) {
  if (occurrences.length === 0) return '尚未生成执行实例'
  if (occurrences.some((occurrence) => occurrence.recurring)) return '重复任务'
  if (occurrences.some((occurrence) => occurrence.planned_start_at))
    return '单次 · 固定时间'
  if (occurrences.some((occurrence) => occurrence.planned_date))
    return '单次 · 指定日期'
  return '单次 · 无日期'
}

function nextOccurrenceSummary(occurrences: OccurrenceV2[]) {
  const next = occurrences.find(
    (occurrence) =>
      occurrence.execution_status !== 'done' &&
      occurrence.execution_status !== 'skipped' &&
      occurrence.execution_status !== 'cancelled'
  )
  if (!next) return '没有待执行实例'
  return `下一次：${formatOccurrenceSchedule(next)}`
}
