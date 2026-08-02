import {
  AlertOctagon,
  Ban,
  CalendarDays,
  Check,
  Circle,
  Folder,
  ExternalLink,
  Link2,
  LockKeyhole,
  MoreHorizontal,
  Pause,
  Pencil,
  Play,
  Plus,
  Repeat2,
  RotateCcw,
  Save,
  Send,
  Trash2,
  X,
} from 'lucide-react'
import {
  type FormEvent,
  type KeyboardEvent,
  type ReactNode,
  useState,
} from 'react'
import { Link } from 'react-router-dom'

import {
  TaskDomainAPIError,
  TaskDomainRevisionConflictError,
  type ExecutionStatus,
  type OccurrenceV2,
  type ProjectV2,
  type RecurrenceType,
  type TaskAttachmentLink,
  type TaskLifecycleStatus,
  type TaskV2,
  type TimingType,
} from '../../api/taskDomain'
import {
  TaskCompletionGate,
  taskCompletionProgress,
} from './TaskCompletionGate'
import { TaskRetentionActions } from './TaskRetentionActions'

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
  const completionProgress = taskCompletionProgress(task)
  const canComplete =
    onComplete !== undefined &&
    occurrence.execution_status !== 'done' &&
    completionProgress.remaining === 0

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
        title={
          completionProgress.remaining > 0
            ? '还需完成 ' + completionProgress.remaining + ' 个必选项'
            : undefined
        }
        disabled={!canComplete}
        onClick={(event) => {
          event.stopPropagation()
          onComplete?.()
        }}
      >
        {occurrence.execution_status === 'done' ? (
          <Check aria-hidden="true" />
        ) : completionProgress.remaining > 0 ? (
          <LockKeyhole aria-hidden="true" />
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
          {completionProgress.total > 0 ? (
            <span>
              <LockKeyhole aria-hidden="true" />
              完成门槛 {completionProgress.completed}/{completionProgress.total}
            </span>
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
  onArchive?: () => Promise<unknown> | void
  onDelete?: () => Promise<unknown> | void
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
  onArchive,
  onDelete,
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
  const completionProgress = taskCompletionProgress(task)

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
                  disabled={
                    !onStart || occurrence.execution_status === 'active' || busy
                  }
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
                disabled={
                  !onComplete || busy || completionProgress.remaining > 0
                }
                title={
                  completionProgress.remaining > 0
                    ? '还需完成 ' + completionProgress.remaining + ' 个必选项'
                    : undefined
                }
                onClick={() => void onComplete?.()}
              >
                {completionProgress.remaining > 0 ? (
                  <LockKeyhole aria-hidden="true" />
                ) : (
                  <Check aria-hidden="true" />
                )}
                {completionProgress.remaining > 0
                  ? '还差 ' + completionProgress.remaining + ' 项'
                  : '完成'}
              </button>
            </>
          )}
        </div>

        {task ? <TaskCompletionGate task={task} /> : null}

        {blocking ? (
          <form
            className="td-block-form"
            onSubmit={(event) => {
              event.preventDefault()
              if (blockedReason.trim() === '' || nextAction.trim() === '')
                return
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
        {task && (onArchive || onDelete) ? (
          <TaskRetentionActions
            taskTitle={task.title}
            archived={task.lifecycle_status === 'archived'}
            onArchive={onArchive}
            onDelete={onDelete}
            busy={busy}
          />
        ) : null}
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
  onDelete?: () => Promise<unknown> | void
  onUpdate?: (input: TaskDefinitionEditInput) => Promise<unknown>
  onScheduleUpdate?: (input: TaskScheduleEditInput) => Promise<unknown>
  busy?: boolean
}

export interface TaskDefinitionEditInput {
  title: string
  description: string
  attachment_links: TaskAttachmentLink[]
}

export interface TaskScheduleEditInput {
  recurrence_type: Exclude<RecurrenceType, 'none'>
  timing_type: Exclude<TimingType, 'unscheduled'>
  timezone: string
  starts_on: string
  local_start_time?: string
  duration_minutes?: number
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
  onDelete,
  onUpdate,
  onScheduleUpdate,
  busy = false,
}: TaskDefinitionInspectorProps) {
  const [editing, setEditing] = useState(false)
  const [title, setTitle] = useState(task.title)
  const [description, setDescription] = useState(task.description ?? '')
  const [attachmentLinks, setAttachmentLinks] = useState<TaskAttachmentLink[]>(
    task.attachment_links ?? []
  )
  const [editError, setEditError] = useState('')
  const [editingSchedule, setEditingSchedule] = useState(false)
  const [recurrenceType, setRecurrenceType] = useState<
    '' | Exclude<RecurrenceType, 'none'>
  >('')
  const [scheduleTimingType, setScheduleTimingType] =
    useState<Exclude<TimingType, 'unscheduled'>>('date')
  const [scheduleDate, setScheduleDate] = useState('')
  const [scheduleTime, setScheduleTime] = useState('09:00')
  const [scheduleDuration, setScheduleDuration] = useState(30)
  const [scheduleTimezone, setScheduleTimezone] = useState('UTC')
  const [scheduleError, setScheduleError] = useState('')

  function beginEditing() {
    setTitle(task.title)
    setDescription(task.description ?? '')
    setAttachmentLinks(
      (task.attachment_links ?? []).map((attachment) => ({ ...attachment }))
    )
    setEditError('')
    setEditing(true)
  }

  function cancelEditing() {
    setEditError('')
    setEditing(false)
  }

  function updateAttachment(index: number, patch: Partial<TaskAttachmentLink>) {
    setAttachmentLinks((current) =>
      current.map((attachment, attachmentIndex) =>
        attachmentIndex === index ? { ...attachment, ...patch } : attachment
      )
    )
  }

  function beginScheduleEditing() {
    const next = nextOpenOccurrence(occurrences)
    const timezone =
      next?.timezone ?? Intl.DateTimeFormat().resolvedOptions().timeZone
    setRecurrenceType(
      next?.recurrence_type && next.recurrence_type !== 'none'
        ? next.recurrence_type
        : ''
    )
    setScheduleTimingType(
      next?.timing_type === 'time_block' ? 'time_block' : 'date'
    )
    setScheduleDate(next?.planned_date ?? localDateInputValue())
    setScheduleTime(scheduleTimeInputValue(next, timezone))
    setScheduleDuration(scheduleDurationMinutes(next))
    setScheduleTimezone(timezone)
    setScheduleError('')
    setEditingSchedule(true)
  }

  function cancelScheduleEditing() {
    setScheduleError('')
    setEditingSchedule(false)
  }

  async function saveSchedule(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (recurrenceType === '' || scheduleDate === '') {
      setScheduleError('请选择重复频率和开始日期。')
      return
    }
    if (scheduleTimingType === 'time_block' && scheduleDuration < 1) {
      setScheduleError('时长必须大于 0 分钟。')
      return
    }
    setScheduleError('')
    try {
      await onScheduleUpdate?.({
        recurrence_type: recurrenceType,
        timing_type: scheduleTimingType,
        timezone: scheduleTimezone,
        starts_on: scheduleDate,
        ...(scheduleTimingType === 'time_block'
          ? {
              local_start_time: scheduleTime,
              duration_minutes: scheduleDuration,
            }
          : {}),
      })
      setEditingSchedule(false)
    } catch (caught) {
      setScheduleError(scheduleSaveErrorMessage(caught))
    }
  }

  async function saveTask(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const normalizedTitle = title.trim()
    if (!normalizedTitle) {
      setEditError('任务名不能为空。')
      return
    }
    const normalizedLinks: TaskAttachmentLink[] = []
    for (const attachment of attachmentLinks) {
      const name = attachment.name.trim()
      const url = attachment.url.trim()
      if (!name || !validAttachmentURL(url)) {
        setEditError('请为每个附件填写名称和有效的 http(s) 链接。')
        return
      }
      normalizedLinks.push({ name, url })
    }
    setEditError('')
    try {
      await onUpdate?.({
        title: normalizedTitle,
        description: description.trim(),
        attachment_links: normalizedLinks,
      })
      setEditing(false)
    } catch {
      setEditError('保存失败，请刷新后重试。')
    }
  }

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
        {editing ? (
          <form className="td-task-edit-form" onSubmit={saveTask}>
            <label>
              <span>任务名</span>
              <input
                aria-label="任务名"
                value={title}
                onChange={(event) => setTitle(event.target.value)}
                autoFocus
              />
            </label>
            <label>
              <span>描述</span>
              <textarea
                aria-label="任务描述"
                value={description}
                placeholder="补充背景、目标或完成标准"
                rows={5}
                onChange={(event) => setDescription(event.target.value)}
              />
            </label>
            <fieldset className="td-task-attachments-editor">
              <legend>附件链接</legend>
              {attachmentLinks.map((attachment, index) => (
                <div className="td-task-attachment-row" key={index}>
                  <input
                    aria-label={`附件 ${index + 1} 名称`}
                    value={attachment.name}
                    placeholder="附件名称"
                    onChange={(event) =>
                      updateAttachment(index, { name: event.target.value })
                    }
                  />
                  <input
                    aria-label={`附件 ${index + 1} 链接`}
                    type="url"
                    value={attachment.url}
                    placeholder="https://..."
                    onChange={(event) =>
                      updateAttachment(index, { url: event.target.value })
                    }
                  />
                  <button
                    type="button"
                    aria-label={`删除附件 ${index + 1}`}
                    onClick={() =>
                      setAttachmentLinks((current) =>
                        current.filter(
                          (_, attachmentIndex) => attachmentIndex !== index
                        )
                      )
                    }
                  >
                    <Trash2 aria-hidden="true" />
                  </button>
                </div>
              ))}
              <button
                type="button"
                className="td-add-attachment"
                disabled={attachmentLinks.length >= 20}
                onClick={() =>
                  setAttachmentLinks((current) => [
                    ...current,
                    { name: '', url: '' },
                  ])
                }
              >
                <Plus aria-hidden="true" />
                添加附件链接
              </button>
            </fieldset>
            {editError ? (
              <div className="td-inline-error" role="alert">
                {editError}
              </div>
            ) : null}
            <div className="td-form-actions">
              <button type="button" onClick={cancelEditing}>
                取消编辑
              </button>
              <button
                type="submit"
                className="is-primary"
                disabled={!onUpdate || busy}
              >
                <Save aria-hidden="true" />
                保存任务
              </button>
            </div>
          </form>
        ) : (
          <div className="td-inspector-links">
            <span>任务信息</span>
            <button
              type="button"
              disabled={!onUpdate || busy}
              onClick={beginEditing}
            >
              <Pencil aria-hidden="true" />
              编辑任务
            </button>
          </div>
        )}
        <TaskLifecycleActions
          status={task.lifecycle_status}
          onPublish={onPublish}
          onPause={onPause}
          onResume={onResume}
          onCancel={onCancel}
          onRestore={onRestore}
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
          <div>
            <dt>描述</dt>
            <dd>{task.description || '暂无描述'}</dd>
          </div>
          {task.task_note_id ? (
            <div>
              <dt>关联笔记</dt>
              <dd>
                <Link
                  className="td-linked-note-link"
                  to={`/editor/${encodeURIComponent(task.task_note_id)}`}
                >
                  打开关联笔记
                </Link>
              </dd>
            </div>
          ) : null}
        </dl>
        <TaskCompletionGate task={task} />
        <div className="td-task-attachments">
          <div className="td-task-attachments-heading">
            <span>附件</span>
            <small>{task.attachment_links?.length ?? 0} 个链接</small>
          </div>
          {task.attachment_links?.length ? (
            <ul>
              {task.attachment_links.map((attachment) => (
                <li key={attachment.url}>
                  <Link2 aria-hidden="true" />
                  <a href={attachment.url} target="_blank" rel="noreferrer">
                    {attachment.name}
                    <ExternalLink aria-hidden="true" />
                  </a>
                </li>
              ))}
            </ul>
          ) : (
            <p>暂无附件链接</p>
          )}
        </div>
        {editingSchedule ? (
          <form className="td-schedule-editor" onSubmit={saveSchedule}>
            <div className="td-schedule-editor-heading">
              <div>
                <span>重复安排</span>
                <strong>设置后续执行规则</strong>
              </div>
              <Repeat2 aria-hidden="true" />
            </div>
            <label>
              <span>重复频率</span>
              <select
                aria-label="重复频率"
                required
                value={recurrenceType}
                onChange={(event) =>
                  setRecurrenceType(
                    event.target.value as '' | Exclude<RecurrenceType, 'none'>
                  )
                }
              >
                <option value="">请选择</option>
                <option value="daily">每天</option>
                <option value="weekly">每周</option>
                <option value="monthly">每月</option>
              </select>
            </label>
            <label>
              <span>开始日期</span>
              <input
                type="date"
                aria-label="重复开始日期"
                required
                value={scheduleDate}
                onChange={(event) => setScheduleDate(event.target.value)}
              />
            </label>
            <label>
              <span>安排方式</span>
              <select
                aria-label="重复安排方式"
                value={scheduleTimingType}
                onChange={(event) =>
                  setScheduleTimingType(
                    event.target.value as Exclude<TimingType, 'unscheduled'>
                  )
                }
              >
                <option value="date">全天</option>
                <option value="time_block">指定时间</option>
              </select>
            </label>
            {scheduleTimingType === 'time_block' ? (
              <div className="td-schedule-time-fields">
                <label>
                  <span>开始时间</span>
                  <input
                    type="time"
                    aria-label="重复开始时间"
                    required
                    value={scheduleTime}
                    onChange={(event) => setScheduleTime(event.target.value)}
                  />
                </label>
                <label>
                  <span>时长（分钟）</span>
                  <input
                    type="number"
                    min={1}
                    aria-label="重复时长（分钟）"
                    required
                    value={scheduleDuration}
                    onChange={(event) =>
                      setScheduleDuration(Number(event.target.value))
                    }
                  />
                </label>
              </div>
            ) : null}
            <p className="td-schedule-editor-hint">
              {scheduleRuleHint(recurrenceType, scheduleDate)}
            </p>
            {scheduleError ? (
              <div className="td-inline-error" role="alert">
                {scheduleError}
              </div>
            ) : null}
            <div className="td-form-actions">
              <button type="button" onClick={cancelScheduleEditing}>
                取消
              </button>
              <button
                type="submit"
                className="is-primary"
                disabled={
                  !onScheduleUpdate ||
                  busy ||
                  recurrenceType === '' ||
                  scheduleDate === ''
                }
              >
                <Save aria-hidden="true" />
                保存安排
              </button>
            </div>
          </form>
        ) : (
          <div className="td-schedule-summary">
            <div className="td-schedule-summary-heading">
              <span>当前安排</span>
              {onScheduleUpdate ? (
                <button
                  type="button"
                  disabled={busy}
                  onClick={beginScheduleEditing}
                >
                  <Pencil aria-hidden="true" />
                  设置重复
                </button>
              ) : null}
            </div>
            <strong>{scheduleSummary(occurrences)}</strong>
            <p>{nextOccurrenceSummary(occurrences)}</p>
          </div>
        )}
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
        <TaskRetentionActions
          taskTitle={task.title}
          archived={task.lifecycle_status === 'archived'}
          onArchive={onArchive}
          onDelete={onDelete}
          busy={busy}
        />
      </div>
    </aside>
  )
}

function validAttachmentURL(value: string) {
  try {
    const parsed = new URL(value)
    return parsed.protocol === 'http:' || parsed.protocol === 'https:'
  } catch {
    return false
  }
}

function TaskLifecycleActions({
  status,
  onPublish,
  onPause,
  onResume,
  onCancel,
  onRestore,
  busy,
}: {
  status: TaskLifecycleStatus
  onPublish?: () => Promise<unknown> | void
  onPause?: () => Promise<unknown> | void
  onResume?: () => Promise<unknown> | void
  onCancel?: () => Promise<unknown> | void
  onRestore?: () => Promise<unknown> | void
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
      <div className="td-command-grid is-one">
        <button
          type="button"
          className="is-primary"
          disabled={!onRestore || busy}
          onClick={() => void onRestore?.()}
        >
          <RotateCcw aria-hidden="true" />
          恢复
        </button>
      </div>
    )
  }
  if (status === 'completed' || status === 'archived') return null
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
  const recurring = occurrences.find((occurrence) => occurrence.recurring)
  if (recurring) {
    const recurrence =
      recurring.recurrence_type === 'daily'
        ? '每天'
        : recurring.recurrence_type === 'weekly'
          ? '每周'
          : recurring.recurrence_type === 'monthly'
            ? '每月'
            : '重复任务'
    const timing = recurring.planned_start_at
      ? '固定时间'
      : recurring.planned_date
        ? '全天'
        : ''
    return timing ? `${recurrence} · ${timing}` : recurrence
  }
  if (occurrences.some((occurrence) => occurrence.planned_start_at))
    return '单次 · 固定时间'
  if (occurrences.some((occurrence) => occurrence.planned_date))
    return '单次 · 指定日期'
  return '单次 · 无日期'
}

function scheduleSaveErrorMessage(caught: unknown) {
  if (caught instanceof TaskDomainRevisionConflictError) {
    return '任务已在其他页面更新，数据已自动刷新，请确认后再次保存。'
  }
  if (caught instanceof TaskDomainAPIError) {
    return caught.message || '保存安排失败，请稍后重试。'
  }
  return '保存安排失败，请稍后重试。'
}

function nextOccurrenceSummary(occurrences: OccurrenceV2[]) {
  const next = nextOpenOccurrence(occurrences)
  if (!next) return '没有待执行实例'
  return `下一次：${formatOccurrenceSchedule(next)}`
}

function nextOpenOccurrence(occurrences: OccurrenceV2[]) {
  return occurrences.find(
    (occurrence) =>
      occurrence.execution_status !== 'done' &&
      occurrence.execution_status !== 'skipped' &&
      occurrence.execution_status !== 'cancelled'
  )
}

function localDateInputValue(date = new Date()) {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

function scheduleTimeInputValue(
  occurrence: OccurrenceV2 | undefined,
  timezone: string
) {
  if (!occurrence?.planned_start_at) return '09:00'
  const parts = new Intl.DateTimeFormat('en-GB', {
    timeZone: timezone,
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  }).formatToParts(new Date(occurrence.planned_start_at))
  const hour = parts.find((part) => part.type === 'hour')?.value ?? '09'
  const minute = parts.find((part) => part.type === 'minute')?.value ?? '00'
  return `${hour}:${minute}`
}

function scheduleDurationMinutes(occurrence: OccurrenceV2 | undefined) {
  if (!occurrence?.planned_start_at || !occurrence.planned_end_at) return 30
  return Math.max(
    1,
    Math.round(
      (new Date(occurrence.planned_end_at).getTime() -
        new Date(occurrence.planned_start_at).getTime()) /
        60_000
    )
  )
}

function scheduleRuleHint(
  recurrenceType: '' | Exclude<RecurrenceType, 'none'>,
  startsOn: string
) {
  if (!recurrenceType || !startsOn)
    return '选择频率后，将生成未来 90 天的执行实例。'
  if (recurrenceType === 'daily') return '从开始日期起每天重复。'
  const date = new Date(`${startsOn}T12:00:00`)
  if (recurrenceType === 'weekly') {
    return `从开始日期起，每周${['日', '一', '二', '三', '四', '五', '六'][date.getDay()]}重复。`
  }
  return `从开始日期起，每月 ${date.getDate()} 日重复。`
}
