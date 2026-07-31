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
  OccurrenceV2,
  TaskAttachmentLink,
  TaskV2,
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
  const value =
    occurrence?.planned_start_at ??
    occurrence?.planned_date ??
    occurrence?.due_at
  if (!value) return '未安排'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat('zh-CN', {
    month: 'short',
    day: 'numeric',
    hour:
      occurrence?.planned_start_at || occurrence?.due_at
        ? '2-digit'
        : undefined,
    minute:
      occurrence?.planned_start_at || occurrence?.due_at
        ? '2-digit'
        : undefined,
  }).format(date)
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
  isCompleting = false,
  isStatusChanging = false,
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
  isCompleting?: boolean
  isStatusChanging?: boolean
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

        <div className="mindmap-inspector-facts">
          <div>
            <CalendarDays aria-hidden="true" />
            <span>计划时间</span>
            <strong>{formatOccurrenceTime(occurrence)}</strong>
          </div>
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
