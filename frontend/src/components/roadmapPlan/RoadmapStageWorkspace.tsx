import {
  ArrowRight,
  CheckCircle2,
  ChevronRight,
  Circle,
  GitBranch,
  MoreHorizontal,
  Plus,
} from 'lucide-react'
import { useState } from 'react'
import { Link } from 'react-router-dom'

import type { RoadmapNodeV2 } from '../../api/roadmapV2'
import type { TaskV2 } from '../../api/taskDomain'
import { roadmapNodeProgress } from './RoadmapStageRail'

const lifecycleLabels: Record<TaskV2['lifecycle_status'], string> = {
  draft: '待发布',
  active: '进行中',
  paused: '已暂停',
  completed: '已完成',
  cancelled: '已取消',
  archived: '已归档',
}

function priorityLabel(priority: number) {
  if (priority >= 2) return '高优先级'
  if (priority === 1) return '普通'
  return '低优先级'
}

export function RoadmapStageWorkspace({
  node,
  tasks,
  projectID,
  tasksUnavailable,
  onAddTask,
  onEdit,
  onDelete,
}: {
  node: RoadmapNodeV2
  tasks: TaskV2[]
  projectID: string
  tasksUnavailable: boolean
  onAddTask: () => void
  onEdit: () => void
  onDelete: () => void
}) {
  const [actionsOpen, setActionsOpen] = useState(false)
  const progress = roadmapNodeProgress(node)

  return (
    <section className="plan-stage-workspace" aria-label={`${node.title} 详情`}>
      <header className="plan-stage-workspace-heading">
        <div>
          <span>
            当前阶段
            <i aria-hidden="true">/</i>
            {node.node_type === 'milestone' ? '里程碑' : '学习节点'}
          </span>
          <h2>{node.title}</h2>
          <p>
            {node.description ||
              '围绕这个节点建立任务，并用实际执行记录推进路线进度。'}
          </p>
        </div>
        <div className="plan-stage-workspace-actions">
          <Link
            className="plan-outline-action"
            to={`/projects/${encodeURIComponent(projectID)}/roadmap/nodes/${encodeURIComponent(node.id)}/mind-map`}
          >
            <GitBranch aria-hidden="true" />
            进入任务脑图
          </Link>
          <button
            className="plan-primary-action"
            type="button"
            onClick={onAddTask}
          >
            <Plus aria-hidden="true" />
            添加任务
          </button>
          <div className="plan-more-menu">
            <button
              type="button"
              aria-label="更多节点操作"
              aria-expanded={actionsOpen}
              onClick={() => setActionsOpen((open) => !open)}
            >
              <MoreHorizontal aria-hidden="true" />
            </button>
            {actionsOpen ? (
              <div role="menu">
                <button
                  type="button"
                  role="menuitem"
                  onClick={() => {
                    setActionsOpen(false)
                    onEdit()
                  }}
                >
                  编辑节点
                </button>
                <button
                  className="is-danger"
                  type="button"
                  role="menuitem"
                  onClick={() => {
                    setActionsOpen(false)
                    onDelete()
                  }}
                >
                  删除节点
                </button>
              </div>
            ) : null}
          </div>
        </div>
      </header>

      <div className="plan-stage-metrics" aria-label="当前阶段进度">
        <div>
          <span>阶段进度</span>
          <strong>{progress}%</strong>
        </div>
        <div>
          <span>任务定义</span>
          <strong>{node.progress.tasks}</strong>
        </div>
        <div>
          <span>进行中</span>
          <strong>{node.progress.active}</strong>
        </div>
        <div>
          <span>被阻塞</span>
          <strong>{node.progress.blocked}</strong>
        </div>
        <span className="plan-stage-metrics-track" aria-hidden="true">
          <i style={{ width: `${progress}%` }} />
        </span>
      </div>

      <div className="plan-task-table">
        <div className="plan-task-table-heading">
          <div>
            <strong>关联任务</strong>
            <span>按当前任务定义展示；进度仍以执行记录为准</span>
          </div>
          <Link to="/tasks">
            打开任务工作台
            <ArrowRight aria-hidden="true" />
          </Link>
        </div>

        {tasksUnavailable ? (
          <p className="plan-task-table-state">
            任务明细暂时不可用，阶段聚合进度仍可查看。
          </p>
        ) : tasks.length === 0 ? (
          <div className="plan-task-table-empty">
            <GitBranch aria-hidden="true" />
            <div>
              <strong>这个阶段还没有关联任务</strong>
              <p>添加第一项可执行工作，或者进入脑图集中组织任务。</p>
            </div>
            <button type="button" onClick={onAddTask}>
              创建任务
              <ChevronRight aria-hidden="true" />
            </button>
          </div>
        ) : (
          <ol aria-label={`${node.title} 的关联任务`}>
            {tasks.map((task, index) => {
              const isComplete = task.lifecycle_status === 'completed'
              return (
                <li key={task.id}>
                  <span className="plan-task-grip" aria-hidden="true">
                    {String(index + 1).padStart(2, '0')}
                  </span>
                  <span
                    className={`plan-task-state${
                      isComplete ? ' is-complete' : ''
                    }`}
                    aria-hidden="true"
                  >
                    {isComplete ? <CheckCircle2 /> : <Circle />}
                  </span>
                  <div>
                    <strong>{task.title}</strong>
                    <small>
                      {lifecycleLabels[task.lifecycle_status]}
                      <i aria-hidden="true">·</i>
                      {priorityLabel(task.priority)}
                    </small>
                  </div>
                  <ChevronRight aria-hidden="true" />
                </li>
              )
            })}
          </ol>
        )}
      </div>
    </section>
  )
}
