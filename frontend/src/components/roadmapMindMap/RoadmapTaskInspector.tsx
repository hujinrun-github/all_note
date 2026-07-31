import {
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
  TaskV2,
} from '../../api/taskDomain'
import { useUpdateTaskDefinitionMutation } from '../../hooks/useTaskDomain'

const statusLabels: Record<ExecutionStatus, string> = {
  open: '待办',
  active: '进行中',
  blocked: '被阻塞',
  done: '已完成',
  skipped: '已跳过',
  cancelled: '已取消',
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
}: {
  task?: TaskV2
  status?: ExecutionStatus
  occurrence?: OccurrenceV2
  roadmapNodeTitle: string
  onClose: () => void
  onRename?: () => void
  onAddSibling?: () => void
  onCancel?: () => void
}) {
  const updateTask = useUpdateTaskDefinitionMutation()
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [priority, setPriority] = useState('0')
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    setTitle(task?.title ?? '')
    setDescription(task?.description ?? '')
    setPriority(String(task?.priority ?? 0))
    setSaved(false)
  }, [task?.description, task?.id, task?.priority, task?.title])

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
    Number(priority) !== task.priority

  async function save(event: FormEvent) {
    event.preventDefault()
    if (!task || title.trim() === '' || !isDirty) return
    await updateTask.mutateAsync({
      projectID: task.project_id,
      taskID: task.id,
      input: {
        title: title.trim(),
        description: description.trim(),
        priority: Number(priority),
        expected_task_revision: task.revision,
        expected_schedule_revision: task.schedule_revision,
      },
    })
    setSaved(true)
  }

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
          <label>
            <span>执行状态</span>
            <output className={`is-${status ?? 'open'}`}>
              {statusLabels[status ?? 'open']}
            </output>
          </label>
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

        <section className="mindmap-resources">
          <header>
            <div>
              <FileText aria-hidden="true" />
              <strong>附件与链接</strong>
            </div>
            <span>{task.attachment_links?.length ?? 0}</span>
          </header>
          {task.attachment_links?.length ? (
            <ul>
              {task.attachment_links.map((attachment) => (
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
        </section>

        <p className="mindmap-capability-note">
          当前按路线节点的一层关联任务展示；任务层级与文章库联动将在扩展能力开放后接入。
        </p>

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
