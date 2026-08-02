import {
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Circle,
  Flag,
  GitBranch,
  LoaderCircle,
  PauseCircle,
  Plus,
  ShieldAlert,
} from 'lucide-react'
import { Handle, Position, type Node, type NodeProps } from '@xyflow/react'
import {
  type KeyboardEvent,
  type MouseEvent,
  useEffect,
  useRef,
  useState,
} from 'react'

import type { ExecutionStatus } from '../../api/taskDomain'
import {
  createTaskScheduleDraft,
  TaskRecurrenceField,
  type TaskScheduleDraft,
} from '../taskDomain/TaskScheduleFields'

export interface RoadmapRootNodeData extends Record<string, unknown> {
  title: string
  taskCount: number
  progress: number
  collapsed: boolean
  onAddTask: () => void
  onToggleCollapse: () => void
}

export interface RoadmapTaskNodeData extends Record<string, unknown> {
  taskID: string
  sequence: number
  title: string
  priority: number
  status: ExecutionStatus
  editRequest: number
  isDraft?: boolean
  onAddSibling: (taskID: string) => void
  onCancelDraft?: () => void
  onCreateDraft?: (
    title: string,
    schedule: TaskScheduleDraft
  ) => Promise<void>
  onRename: (taskID: string, title: string) => Promise<void>
}

export type RoadmapRootFlowNode = Node<RoadmapRootNodeData, 'roadmapRoot'>
export type RoadmapTaskFlowNode = Node<RoadmapTaskNodeData, 'roadmapTask'>

const statusLabels: Record<ExecutionStatus, string> = {
  open: '待办',
  active: '进行中',
  blocked: '被阻塞',
  done: '已完成',
  skipped: '已跳过',
  cancelled: '已取消',
}

function StatusIcon({ status }: { status: ExecutionStatus }) {
  if (status === 'done') return <CheckCircle2 />
  if (status === 'blocked') return <ShieldAlert />
  if (status === 'active') return <PauseCircle />
  return <Circle />
}

export function RoadmapRootNodeView({
  data,
  selected,
}: NodeProps<RoadmapRootFlowNode>) {
  return (
    <article className={`mindmap-root-node${selected ? ' is-selected' : ''}`}>
      <GitBranch aria-hidden="true" />
      <div>
        <strong>{data.title}</strong>
        <small>
          {data.taskCount} 个任务 · {data.progress}%
        </small>
      </div>
      <button
        className="mindmap-root-collapse nodrag nopan"
        type="button"
        aria-label={data.collapsed ? '展开任务分支' : '折叠任务分支'}
        title={data.collapsed ? '展开任务分支' : '折叠任务分支'}
        onClick={(event) => {
          event.stopPropagation()
          data.onToggleCollapse()
        }}
      >
        {data.collapsed ? <ChevronRight /> : <ChevronDown />}
        {data.collapsed ? <span>{data.taskCount}</span> : null}
      </button>
      {selected && !data.collapsed ? (
        <button
          className="mindmap-root-add nodrag nopan"
          type="button"
          aria-label="添加任务"
          title="添加任务（Tab）"
          onClick={(event) => {
            event.stopPropagation()
            data.onAddTask()
          }}
        >
          <Plus />
        </button>
      ) : null}
      <Handle
        className="mindmap-node-handle"
        type="source"
        position={Position.Right}
      />
    </article>
  )
}

export function RoadmapTaskNodeView({
  data,
  selected,
}: NodeProps<RoadmapTaskFlowNode>) {
  const [editing, setEditing] = useState(Boolean(data.isDraft))
  const [draftTitle, setDraftTitle] = useState(data.title)
  const [draftSchedule, setDraftSchedule] = useState(createTaskScheduleDraft)
  const [saving, setSaving] = useState(false)
  const latestEditRequest = useRef(data.editRequest)
  const committing = useRef(false)

  useEffect(() => {
    if (editing || data.isDraft) return
    setDraftTitle(data.title)
  }, [data.isDraft, data.title, editing])

  useEffect(() => {
    if (latestEditRequest.current === data.editRequest) return
    latestEditRequest.current = data.editRequest
    if (!selected || data.isDraft) return
    setDraftTitle(data.title)
    setEditing(true)
  }, [data.editRequest, data.isDraft, data.title, selected])

  async function commitTitle() {
    if (committing.current) return
    const title = draftTitle.trim()
    if (title === '') {
      if (data.isDraft) data.onCancelDraft?.()
      else {
        setDraftTitle(data.title)
        setEditing(false)
      }
      return
    }
    if (!data.isDraft && title === data.title) {
      setEditing(false)
      return
    }

    committing.current = true
    setSaving(true)
    try {
      try {
        if (data.isDraft) await data.onCreateDraft?.(title, draftSchedule)
        else await data.onRename(data.taskID, title)
        setEditing(false)
      } catch {
        // Keep the input open so the title can be retried without retyping it.
      }
    } finally {
      committing.current = false
      setSaving(false)
    }
  }

  function cancelEditing() {
    if (saving) return
    if (data.isDraft) data.onCancelDraft?.()
    else {
      setDraftTitle(data.title)
      setEditing(false)
    }
  }

  function startEditing(event: MouseEvent<HTMLElement>) {
    event.preventDefault()
    if (data.isDraft) return
    setDraftTitle(data.title)
    setEditing(true)
  }

  function handleEditKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    event.stopPropagation()
    if (event.key === 'Enter') {
      event.preventDefault()
      void commitTitle()
    } else if (event.key === 'Escape') {
      event.preventDefault()
      cancelEditing()
    }
  }

  return (
    <article
      className={`mindmap-task-node is-${data.status}${
        selected ? ' is-selected' : ''
      }${data.isDraft ? ' is-draft' : ''}`}
      onDoubleClick={startEditing}
      onBlur={(event) => {
        if (editing && !event.currentTarget.contains(event.relatedTarget)) {
          void commitTitle()
        }
      }}
    >
      <Handle
        className="mindmap-node-handle"
        type="target"
        position={Position.Left}
      />
      <span
        className="mindmap-task-status"
        title={statusLabels[data.status]}
        aria-label={statusLabels[data.status]}
      >
        {saving ? (
          <LoaderCircle className="is-spinning" />
        ) : (
          <StatusIcon status={data.status} />
        )}
      </span>
      <div className="mindmap-task-copy">
        {editing ? (
          <input
            className="nodrag nopan"
            aria-label={data.isDraft ? '新任务标题' : '编辑任务标题'}
            value={draftTitle}
            placeholder="输入任务名称"
            disabled={saving}
            onChange={(event) => setDraftTitle(event.target.value)}
            onKeyDown={handleEditKeyDown}
            onMouseDown={(event) => event.stopPropagation()}
            autoFocus
          />
        ) : (
          <strong>{data.title}</strong>
        )}
      </div>
      {editing && data.isDraft ? (
        <div className="mindmap-draft-schedule nodrag nopan">
          <TaskRecurrenceField
            value={draftSchedule}
            onChange={(next) =>
              setDraftSchedule({
                ...next,
                timingType:
                  next.recurrenceType === 'none'
                    ? 'unscheduled'
                    : next.timingType,
              })
            }
            labelPrefix="新任务"
            showStartDate
          />
        </div>
      ) : null}
      {data.priority >= 2 ? (
        <Flag className="mindmap-task-priority" aria-label="高优先级" />
      ) : null}
      <span className="mindmap-task-sequence" aria-hidden="true">
        {String(data.sequence).padStart(2, '0')}
      </span>
      {selected && !editing && !data.isDraft ? (
        <button
          className="mindmap-task-add nodrag nopan"
          type="button"
          aria-label="新建同级任务"
          title="新建同级任务（Enter）"
          onClick={(event) => {
            event.stopPropagation()
            data.onAddSibling(data.taskID)
          }}
        >
          <Plus />
        </button>
      ) : null}
    </article>
  )
}
