import {
  AlertOctagon,
  CalendarDays,
  ExternalLink,
  FileText,
  GitBranch,
  Link2,
  Pencil,
  Plus,
  Save,
  Trash2,
  X,
} from 'lucide-react'
import { type FormEvent, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'

import type {
  ExecutionStatus,
  OccurrenceTimingInput,
  OccurrenceV2,
  TaskAttachmentLink,
  TaskV2,
  TimingType,
} from '../../api/taskDomain'
import {
  TaskCompletionGate,
  taskCompletionProgress,
} from '../taskDomain/TaskCompletionGate'
import { TaskRetentionActions } from '../taskDomain/TaskRetentionActions'
import { useUpdateTaskDefinitionMutation } from '../../hooks/useTaskDomain'

const statusLabels: Record<ExecutionStatus, string> = {
  open: '待办',
  active: '进行中',
  blocked: '被阻塞',
  done: '已完成',
  skipped: '已跳过',
  cancelled: '已取消',
}

export interface RoadmapExecutionStatusChange {
  status: ExecutionStatus
  blockedReason?: string
  nextAction?: string
}

export function roadmapExecutionStatusTargets(
  status: ExecutionStatus,
  recurring = false
): ExecutionStatus[] {
  if (status === 'open') {
    return [
      'active',
      'done',
      ...(recurring ? (['skipped'] as ExecutionStatus[]) : []),
      'cancelled',
    ]
  }
  if (status === 'active') return ['blocked', 'done', 'cancelled']
  if (status === 'blocked') return ['active', 'cancelled']
  return ['open']
}

function formatOccurrenceTime(occurrence?: OccurrenceV2) {
  if (occurrence?.planned_date && !occurrence.planned_start_at) {
    const [year, month, day] = occurrence.planned_date.split('-').map(Number)
    if (year && month && day) return `${month}月${day}日 · 全天`
    return occurrence.planned_date
  }
  const value = occurrence?.planned_start_at ?? occurrence?.due_at
  if (!value) return '未安排'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat('zh-CN', {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    timeZone: occurrence?.timezone,
  }).format(date)
}

function localISODate(date = new Date()) {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

function timeRangeForOccurrence(occurrence?: OccurrenceV2) {
  if (!occurrence) return { start: '09:00', end: '10:00' }
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

export function RoadmapTaskInspector({
  task,
  status,
  occurrence,
  roadmapNodeTitle,
  onClose,
  onRename,
  onAddSibling,
  onCancel,
  onArchive,
  onDelete,
  onComplete,
  onStatusChange,
  onScheduleChange,
  isCompleting = false,
  isStatusChanging = false,
  isScheduleChanging = false,
  isRetentionChanging = false,
}: {
  task?: TaskV2
  status?: ExecutionStatus
  occurrence?: OccurrenceV2
  roadmapNodeTitle: string
  onClose: () => void
  onRename?: () => void
  onAddSibling?: () => void
  onCancel?: () => void
  onArchive?: () => Promise<unknown> | void
  onDelete?: () => Promise<unknown> | void
  onComplete?: () => Promise<void>
  onStatusChange?: (change: RoadmapExecutionStatusChange) => Promise<void>
  onScheduleChange?: (timing: OccurrenceTimingInput) => Promise<void>
  isCompleting?: boolean
  isStatusChanging?: boolean
  isScheduleChanging?: boolean
  isRetentionChanging?: boolean
}) {
  const updateTask = useUpdateTaskDefinitionMutation()
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [priority, setPriority] = useState('0')
  const [saved, setSaved] = useState(false)
  const [blocking, setBlocking] = useState(false)
  const [blockedReason, setBlockedReason] = useState('')
  const [nextAction, setNextAction] = useState('')
  const [attachmentLinks, setAttachmentLinks] = useState<TaskAttachmentLink[]>(
    []
  )
  const [editingResources, setEditingResources] = useState(false)
  const [resourceError, setResourceError] = useState('')
  const [editingSchedule, setEditingSchedule] = useState(false)
  const [scheduleTimingType, setScheduleTimingType] =
    useState<Exclude<TimingType, 'unscheduled'>>('date')
  const [scheduleDate, setScheduleDate] = useState(localISODate)
  const [scheduleTimeRange, setScheduleTimeRange] = useState({
    start: '09:00',
    end: '10:00',
  })
  const [scheduleSaved, setScheduleSaved] = useState(false)
  const [scheduleError, setScheduleError] = useState('')

  useEffect(() => {
    setTitle(task?.title ?? '')
    setDescription(task?.description ?? '')
    setPriority(String(task?.priority ?? 0))
    setAttachmentLinks(
      (task?.attachment_links ?? []).map((attachment) => ({ ...attachment }))
    )
    setEditingResources(false)
    setResourceError('')
    setSaved(false)
  }, [
    task?.attachment_links,
    task?.description,
    task?.id,
    task?.priority,
    task?.title,
  ])

  useEffect(() => {
    setBlocking(false)
    setBlockedReason('')
    setNextAction('')
  }, [status, task?.id])

  useEffect(() => {
    setScheduleTimingType(
      occurrence?.timing_type === 'time_block' || occurrence?.planned_start_at
        ? 'time_block'
        : 'date'
    )
    setScheduleDate(
      occurrence?.planned_date ??
        occurrence?.planned_start_at?.slice(0, 10) ??
        localISODate()
    )
    setScheduleTimeRange(timeRangeForOccurrence(occurrence))
    setEditingSchedule(false)
    setScheduleSaved(false)
    setScheduleError('')
  }, [occurrence?.id, task?.id])

  if (!task) {
    return (
      <aside className="mindmap-inspector is-empty">
        <GitBranch aria-hidden="true" />
        <strong>选择一个任务</strong>
        <p>点击画布中的任务节点，在这里查看与编辑现有任务定义。</p>
      </aside>
    )
  }

  const isDirty =
    title.trim() !== task.title ||
    description.trim() !== (task.description ?? '') ||
    Number(priority) !== task.priority ||
    !attachmentLinksEqual(attachmentLinks, task.attachment_links ?? [])

  async function save(event: FormEvent) {
    event.preventDefault()
    if (!task || title.trim() === '' || !isDirty) return
    const normalizedLinks: TaskAttachmentLink[] = []
    for (const attachment of attachmentLinks) {
      const name = attachment.name.trim()
      const url = attachment.url.trim()
      if (!name || !validAttachmentURL(url)) {
        setResourceError('请填写资料名称和有效的 http(s) 链接。')
        setEditingResources(true)
        return
      }
      normalizedLinks.push({ name, url })
    }
    setResourceError('')
    try {
      await updateTask.mutateAsync({
        projectID: task.project_id,
        taskID: task.id,
        input: {
          title: title.trim(),
          description: description.trim(),
          priority: Number(priority),
          attachment_links: normalizedLinks,
          expected_task_revision: task.revision,
          expected_schedule_revision: task.schedule_revision,
        },
      })
      setAttachmentLinks(normalizedLinks)
      setEditingResources(false)
      setSaved(true)
    } catch {
      setResourceError('保存失败，请刷新任务后重试。')
    }
  }

  function addAttachmentLink() {
    if (attachmentLinks.length >= 20) return
    setResourceError('')
    setEditingResources(true)
    setSaved(false)
    setAttachmentLinks((current) => [...current, { name: '', url: '' }])
  }

  function updateAttachmentLink(
    index: number,
    patch: Partial<TaskAttachmentLink>
  ) {
    setSaved(false)
    setAttachmentLinks((current) =>
      current.map((attachment, attachmentIndex) =>
        attachmentIndex === index ? { ...attachment, ...patch } : attachment
      )
    )
  }

  function cancelResourceEditing() {
    setAttachmentLinks(
      (task?.attachment_links ?? []).map((attachment) => ({ ...attachment }))
    )
    setEditingResources(false)
    setResourceError('')
  }

  async function selectStatus(nextStatus: ExecutionStatus) {
    if (!onStatusChange || nextStatus === status) return
    if (nextStatus === 'blocked') {
      setBlockedReason(occurrence?.blocked_reason ?? '')
      setNextAction(occurrence?.next_action ?? '')
      setBlocking(true)
      return
    }
    try {
      await onStatusChange({ status: nextStatus })
    } catch {
      // The route owns the visible command error; keep the current selection.
    }
  }

  async function confirmBlocked() {
    const reason = blockedReason.trim()
    const action = nextAction.trim()
    if (!onStatusChange || reason === '' || action === '') return
    try {
      await onStatusChange({
        status: 'blocked',
        blockedReason: reason,
        nextAction: action,
      })
      setBlocking(false)
    } catch {
      // Preserve the entered details so the user can retry.
    }
  }

  async function saveSchedule() {
    if (!occurrence || !onScheduleChange || scheduleDate === '') return
    const durationMinutes =
      timeToMinutes(scheduleTimeRange.end) -
      timeToMinutes(scheduleTimeRange.start)
    if (scheduleTimingType === 'time_block' && durationMinutes <= 0) {
      setScheduleError('结束时间必须晚于开始时间。')
      return
    }

    setScheduleError('')
    setScheduleSaved(false)
    try {
      await onScheduleChange({
        timing_type: scheduleTimingType,
        timezone:
          occurrence.timezone ??
          Intl.DateTimeFormat().resolvedOptions().timeZone,
        planned_date: scheduleDate,
        ...(scheduleTimingType === 'time_block'
          ? {
              local_start_time: scheduleTimeRange.start,
              duration_minutes: durationMinutes,
            }
          : {}),
      })
      setEditingSchedule(false)
      setScheduleSaved(true)
    } catch {
      setScheduleError('保存失败，请刷新任务后重试。')
    }
  }

  const currentStatus = status ?? 'open'
  const statusTargets = roadmapExecutionStatusTargets(
    currentStatus,
    occurrence?.recurring
  )
  const completionProgress = taskCompletionProgress(task)

  return (
    <aside className="mindmap-inspector" aria-label="任务详情">
      <header>
        <div>
          <span>任务详情</span>
          <strong>{task.title}</strong>
        </div>
        <button type="button" aria-label="关闭任务详情" onClick={onClose}>
          <X aria-hidden="true" />
        </button>
      </header>

      <div className="mindmap-inspector-quick-actions">
        <button type="button" onClick={onRename}>
          <Pencil aria-hidden="true" />
          重命名
        </button>
        <button type="button" onClick={onAddSibling}>
          <Plus aria-hidden="true" />
          同级任务
        </button>
        <button className="is-danger" type="button" onClick={onCancel}>
          <Trash2 aria-hidden="true" />
          取消
        </button>
      </div>

      <form onSubmit={save}>
        <label>
          <span>标题</span>
          <input
            aria-label="任务标题"
            value={title}
            onChange={(event) => {
              setSaved(false)
              setTitle(event.target.value)
            }}
          />
        </label>

        <div className="mindmap-inspector-grid">
          <div className="mindmap-status-field">
            <label htmlFor="mindmap-execution-status">执行状态</label>
            <select
              id="mindmap-execution-status"
              aria-label="执行状态"
              className={'is-' + currentStatus}
              value={currentStatus}
              disabled={!occurrence || !onStatusChange || isStatusChanging}
              onChange={(event) =>
                void selectStatus(event.target.value as ExecutionStatus)
              }
            >
              <option value={currentStatus}>
                {statusLabels[currentStatus]}
              </option>
              {statusTargets.map((target) => {
                const completionLocked =
                  target === 'done' && completionProgress.remaining > 0
                return (
                  <option
                    key={target}
                    value={target}
                    disabled={completionLocked}
                  >
                    {statusLabels[target]}
                    {completionLocked
                      ? '（还差 ' + completionProgress.remaining + ' 项）'
                      : ''}
                  </option>
                )
              })}
            </select>
            <small>
              {!occurrence
                ? '尚未生成执行实例'
                : isStatusChanging
                  ? '正在更新状态…'
                  : completionProgress.remaining > 0
                    ? '完成前还需满足 ' +
                      completionProgress.remaining +
                      ' 个必选项'
                    : '选择后立即更新当前执行实例'}
            </small>
          </div>
          <label>
            <span>优先级</span>
            <select
              aria-label="任务优先级"
              value={priority}
              onChange={(event) => {
                setSaved(false)
                setPriority(event.target.value)
              }}
            >
              <option value="0">低</option>
              <option value="1">普通</option>
              <option value="2">高</option>
              <option value="3">紧急</option>
            </select>
          </label>
          {blocking ? (
            <div className="mindmap-status-block-editor">
              <header>
                <AlertOctagon aria-hidden="true" />
                <div>
                  <strong>标记为被阻塞</strong>
                  <span>记录原因和可继续推进的下一步。</span>
                </div>
              </header>
              <label>
                <span>阻塞原因</span>
                <input
                  aria-label="阻塞原因"
                  value={blockedReason}
                  placeholder="例如：等待数据权限"
                  onChange={(event) => setBlockedReason(event.target.value)}
                />
              </label>
              <label>
                <span>下一步</span>
                <input
                  aria-label="阻塞后的下一步"
                  value={nextAction}
                  placeholder="例如：联系管理员开通权限"
                  onChange={(event) => setNextAction(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter') {
                      event.preventDefault()
                      void confirmBlocked()
                    }
                    if (event.key === 'Escape') setBlocking(false)
                  }}
                />
              </label>
              <div>
                <button type="button" onClick={() => setBlocking(false)}>
                  取消
                </button>
                <button
                  type="button"
                  className="is-primary"
                  disabled={
                    blockedReason.trim() === '' ||
                    nextAction.trim() === '' ||
                    isStatusChanging
                  }
                  onClick={() => void confirmBlocked()}
                >
                  {isStatusChanging ? '正在保存…' : '确认阻塞'}
                </button>
              </div>
            </div>
          ) : null}
        </div>

        <label>
          <span>任务说明</span>
          <textarea
            aria-label="任务说明"
            rows={4}
            value={description}
            placeholder="补充输出、验收方式或需要注意的上下文"
            onChange={(event) => {
              setSaved(false)
              setDescription(event.target.value)
            }}
          />
        </label>

        <section className="mindmap-schedule-card">
          <header>
            <div>
              <CalendarDays aria-hidden="true" />
              <div>
                <span>计划时间</span>
                <strong>{formatOccurrenceTime(occurrence)}</strong>
              </div>
            </div>
            <button
              type="button"
              disabled={!occurrence || !onScheduleChange}
              onClick={() => {
                setScheduleError('')
                setEditingSchedule((current) => !current)
              }}
            >
              {editingSchedule ? '收起' : '安排时间'}
            </button>
          </header>

          {scheduleSaved ? (
            <p className="mindmap-schedule-success" role="status">
              已保存，并同步到日历
            </p>
          ) : null}

          {!occurrence ? (
            <p>尚未生成执行实例，请稍后再试。</p>
          ) : editingSchedule ? (
            <div className="mindmap-schedule-editor">
              <label>
                <span>安排方式</span>
                <select
                  aria-label="学习任务安排方式"
                  value={scheduleTimingType}
                  onChange={(event) =>
                    setScheduleTimingType(
                      event.target.value as Exclude<TimingType, 'unscheduled'>
                    )
                  }
                >
                  <option value="date">全天任务</option>
                  <option value="time_block">具体时间</option>
                </select>
              </label>
              <label>
                <span>执行日期</span>
                <input
                  aria-label="学习任务执行日期"
                  type="date"
                  value={scheduleDate}
                  onChange={(event) => setScheduleDate(event.target.value)}
                />
              </label>
              {scheduleTimingType === 'time_block' ? (
                <div className="mindmap-schedule-time-range">
                  <label>
                    <span>开始</span>
                    <input
                      aria-label="学习任务开始时间"
                      type="time"
                      value={scheduleTimeRange.start}
                      onChange={(event) =>
                        setScheduleTimeRange((current) => ({
                          ...current,
                          start: event.target.value,
                        }))
                      }
                    />
                  </label>
                  <label>
                    <span>结束</span>
                    <input
                      aria-label="学习任务结束时间"
                      type="time"
                      value={scheduleTimeRange.end}
                      onChange={(event) =>
                        setScheduleTimeRange((current) => ({
                          ...current,
                          end: event.target.value,
                        }))
                      }
                    />
                  </label>
                </div>
              ) : null}
              {scheduleError ? (
                <p className="mindmap-schedule-error" role="alert">
                  {scheduleError}
                </p>
              ) : null}
              <div className="mindmap-schedule-actions">
                <button type="button" onClick={() => setEditingSchedule(false)}>
                  取消
                </button>
                <button
                  className="is-primary"
                  type="button"
                  disabled={scheduleDate === '' || isScheduleChanging}
                  onClick={() => void saveSchedule()}
                >
                  {isScheduleChanging ? '正在同步…' : '保存并同步日历'}
                </button>
              </div>
            </div>
          ) : (
            <p>
              {formatOccurrenceTime(occurrence) === '未安排'
                ? '安排后，任务会出现在对应日期的日历中。'
                : '修改后，任务工作台和日历会同步更新。'}
            </p>
          )}
        </section>

        <div className="mindmap-inspector-facts">
          <div>
            <GitBranch aria-hidden="true" />
            <span>所属节点</span>
            <strong>{roadmapNodeTitle}</strong>
          </div>
        </div>

        <TaskCompletionGate
          task={task}
          occurrence={occurrence}
          status={status}
          onComplete={onComplete}
          isCompleting={isCompleting}
          showCompleteAction
        />

        <section className="mindmap-resources">
          <header>
            <div>
              <FileText aria-hidden="true" />
              <strong>附件与链接</strong>
            </div>
            <div className="mindmap-resource-header-actions">
              <span>{attachmentLinks.length}</span>
              {attachmentLinks.length > 0 && !editingResources ? (
                <button
                  type="button"
                  aria-label="管理附件与链接"
                  onClick={() => {
                    setResourceError('')
                    setEditingResources(true)
                  }}
                >
                  <Pencil aria-hidden="true" />
                </button>
              ) : null}
              <button
                type="button"
                aria-label="添加附件链接"
                title="添加附件链接"
                disabled={attachmentLinks.length >= 20}
                onClick={addAttachmentLink}
              >
                <Plus aria-hidden="true" />
              </button>
            </div>
          </header>
          {editingResources ? (
            <div className="mindmap-resource-editor">
              {attachmentLinks.length > 0 ? (
                attachmentLinks.map((attachment, index) => (
                  <div className="mindmap-resource-edit-row" key={index}>
                    <input
                      aria-label={`附件 ${index + 1} 名称`}
                      value={attachment.name}
                      placeholder="资料名称"
                      autoFocus={
                        index === attachmentLinks.length - 1 &&
                        attachment.name === ''
                      }
                      onChange={(event) =>
                        updateAttachmentLink(index, {
                          name: event.target.value,
                        })
                      }
                    />
                    <input
                      aria-label={`附件 ${index + 1} 链接`}
                      type="url"
                      value={attachment.url}
                      placeholder="https://..."
                      onChange={(event) =>
                        updateAttachmentLink(index, {
                          url: event.target.value,
                        })
                      }
                    />
                    <button
                      type="button"
                      aria-label={`删除附件 ${index + 1}`}
                      onClick={() => {
                        setSaved(false)
                        setAttachmentLinks((current) =>
                          current.filter(
                            (_, attachmentIndex) => attachmentIndex !== index
                          )
                        )
                      }}
                    >
                      <Trash2 aria-hidden="true" />
                    </button>
                  </div>
                ))
              ) : (
                <p>暂无资料。点击右上角的加号继续添加。</p>
              )}
              {resourceError ? (
                <div className="mindmap-resource-error" role="alert">
                  {resourceError}
                </div>
              ) : null}
              <div className="mindmap-resource-editor-footer">
                <small>编辑完成后，点击下方“保存修改”生效。</small>
                <button type="button" onClick={cancelResourceEditing}>
                  取消编辑
                </button>
              </div>
            </div>
          ) : attachmentLinks.length > 0 ? (
            <ul>
              {attachmentLinks.map((attachment) => (
                <li key={`${attachment.name}-${attachment.url}`}>
                  <Link2 aria-hidden="true" />
                  <a href={attachment.url} target="_blank" rel="noreferrer">
                    <span>{attachment.name}</span>
                    <ExternalLink aria-hidden="true" />
                  </a>
                </li>
              ))}
            </ul>
          ) : (
            <p>这个任务尚未关联附件或外部资料。</p>
          )}
          {!editingResources && resourceError ? (
            <div className="mindmap-resource-error" role="alert">
              {resourceError}
            </div>
          ) : null}
        </section>

        <TaskRetentionActions
          taskTitle={task.title}
          archived={task.lifecycle_status === 'archived'}
          onArchive={onArchive}
          onDelete={onDelete}
          busy={isRetentionChanging}
        />

        <div className="mindmap-inspector-actions">
          <button
            className="plan-primary-action"
            type="submit"
            disabled={!isDirty || title.trim() === '' || updateTask.isPending}
          >
            <Save aria-hidden="true" />
            {updateTask.isPending ? '正在保存…' : saved ? '已保存' : '保存修改'}
          </button>
          <Link to="/tasks">
            打开任务工作台
            <ExternalLink aria-hidden="true" />
          </Link>
        </div>
      </form>
    </aside>
  )
}

function attachmentLinksEqual(
  left: TaskAttachmentLink[],
  right: TaskAttachmentLink[]
) {
  return (
    left.length === right.length &&
    left.every(
      (attachment, index) =>
        attachment.name === right[index]?.name &&
        attachment.url === right[index]?.url
    )
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
