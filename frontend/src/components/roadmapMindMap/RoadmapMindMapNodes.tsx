import {
  CheckCircle2,
  Circle,
  GitBranch,
  PauseCircle,
  ShieldAlert,
} from 'lucide-react'
import {
  Handle,
  Position,
  type Node,
  type NodeProps,
} from '@xyflow/react'

import type { ExecutionStatus } from '../../api/taskDomain'

export interface RoadmapRootNodeData extends Record<string, unknown> {
  title: string
  taskCount: number
  progress: number
}

export interface RoadmapTaskNodeData extends Record<string, unknown> {
  sequence: number
  title: string
  priority: number
  status: ExecutionStatus
}

export type RoadmapRootFlowNode = Node<
  RoadmapRootNodeData,
  'roadmapRoot'
>
export type RoadmapTaskFlowNode = Node<
  RoadmapTaskNodeData,
  'roadmapTask'
>

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
}: NodeProps<RoadmapRootFlowNode>) {
  return (
    <article className="mindmap-root-node">
      <GitBranch aria-hidden="true" />
      <div>
        <strong>{data.title}</strong>
        <small>
          {data.taskCount} 个任务 · {data.progress}%
        </small>
      </div>
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
  return (
    <article
      className={`mindmap-task-node is-${data.status}${
        selected ? ' is-selected' : ''
      }`}
    >
      <Handle
        className="mindmap-node-handle"
        type="target"
        position={Position.Left}
      />
      <span className="mindmap-task-sequence" aria-hidden="true">
        {String(data.sequence).padStart(2, '0')}
      </span>
      <div>
        <strong>{data.title}</strong>
        <small>
          {data.priority >= 2
            ? '高优先级'
            : data.priority === 1
              ? '普通优先级'
              : '低优先级'}
        </small>
      </div>
      <span className="mindmap-task-status">
        <StatusIcon status={data.status} />
        {statusLabels[data.status]}
      </span>
    </article>
  )
}
