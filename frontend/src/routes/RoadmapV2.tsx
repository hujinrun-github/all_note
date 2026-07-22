import { type FormEvent, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { APIError } from '../api/client'
import type { RoadmapNodeV2 } from '../api/roadmapV2'
import {
  useCreateRoadmapMutation,
  useCreateRoadmapNodeMutation,
  useDeleteRoadmapNodeMutation,
  useRoadmapV2,
  useUpdateRoadmapNodeMutation,
} from '../hooks/useRoadmapV2'
import { useCreateTaskMutation, useProject } from '../hooks/useTaskDomain'

export default function RoadmapV2() {
  const { projectID = '' } = useParams()
  const project = useProject(projectID)
  const roadmap = useRoadmapV2(projectID)
  const createRoadmap = useCreateRoadmapMutation(projectID)
  const createNode = useCreateRoadmapNodeMutation(projectID)
  const updateNode = useUpdateRoadmapNodeMutation(projectID)
  const deleteNode = useDeleteRoadmapNodeMutation(projectID)
  const createTask = useCreateTaskMutation()
  const [newNodeTitle, setNewNodeTitle] = useState('')
  const [taskNode, setTaskNode] = useState<RoadmapNodeV2 | null>(null)
  const [taskTitle, setTaskTitle] = useState('')
  const [editing, setEditing] = useState<RoadmapNodeV2 | null>(null)
  const [editTitle, setEditTitle] = useState('')
  const [error, setError] = useState('')
  if (project.isLoading || roadmap.isLoading)
    return <p className="domain-empty">正在加载学习路线…</p>
  if (project.isError || !project.data)
    return <p className="domain-empty">项目暂时不可用。</p>
  if (project.data.kind !== 'learning')
    return (
      <div className="domain-unavailable" role="alert">
        <strong>只有学习项目可以使用 Roadmap</strong>
      </div>
    )
  const model = roadmap.data
  async function addNode(e: FormEvent) {
    e.preventDefault()
    if (!model || newNodeTitle.trim() === '') return
    await createNode.mutateAsync({
      roadmapID: model.id,
      input: {
        title: newNodeTitle.trim(),
        node_type: 'topic',
        position: model.nodes.length,
      },
    })
    setNewNodeTitle('')
  }
  async function addTask(e: FormEvent) {
    e.preventDefault()
    if (!taskNode || taskTitle.trim() === '') return
    await createTask.mutateAsync({
      project_id: projectID,
      roadmap_node_id: taskNode.id,
      title: taskTitle.trim(),
      priority: 0,
      schedule: {
        recurrence_type: 'none',
        timing_type: 'unscheduled',
        timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
      },
    })
    setTaskTitle('')
    setTaskNode(null)
  }
  async function removeNode(node: RoadmapNodeV2) {
    setError('')
    try {
      await deleteNode.mutateAsync({
        roadmapID: node.roadmap_id,
        nodeID: node.id,
        expectedRevision: node.revision,
      })
    } catch (caught) {
      setError(
        caught instanceof APIError && caught.code === 'roadmap_node_has_tasks'
          ? '该节点仍有关联任务，请先解绑或迁移任务后再删除。'
          : '删除节点失败，请刷新后重试。'
      )
    }
  }
  async function saveEdit(e: FormEvent) {
    e.preventDefault()
    if (!editing || editTitle.trim() === '') return
    await updateNode.mutateAsync({
      roadmapID: editing.roadmap_id,
      nodeID: editing.id,
      input: {
        title: editTitle.trim(),
        description: editing.description,
        node_type: editing.node_type,
        position: editing.position,
        parent_id: editing.parent_id,
        expected_revision: editing.revision,
      },
    })
    setEditing(null)
  }
  if (!model)
    return (
      <section className="domain-page">
        <header className="domain-page-heading">
          <div>
            <Link
              className="domain-back-link"
              to={`/projects/${encodeURIComponent(projectID)}`}
            >
              ← 返回项目
            </Link>
            <h2>{project.data.name} · 学习路线</h2>
            <p>路线节点用于组织任务，节点进度由关联任务的执行实例自动汇总。</p>
          </div>
        </header>
        <div className="roadmap-v2-empty">
          <h3>尚未创建学习路线</h3>
          <button
            className="domain-primary-button"
            disabled={createRoadmap.isPending}
            onClick={() =>
              createRoadmap.mutate({ title: `${project.data.name} 学习路线` })
            }
          >
            创建空白 Roadmap
          </button>
        </div>
      </section>
    )
  return (
    <section className="domain-page">
      <header className="domain-page-heading">
        <div>
          <Link
            className="domain-back-link"
            to={`/projects/${encodeURIComponent(projectID)}`}
          >
            ← 返回项目
          </Link>
          <h2>{model.title}</h2>
          <p>
            节点本身没有“完成”开关；以下进度始终来自同项目、同节点关联任务的执行实例。
          </p>
        </div>
      </header>
      <form className="domain-inline-create" onSubmit={addNode}>
        <label>
          <span>新增路线节点</span>
          <input
            aria-label="节点标题"
            value={newNodeTitle}
            onChange={(e) => setNewNodeTitle(e.target.value)}
            placeholder="例如：掌握基础语法"
          />
        </label>
        <button
          className="domain-primary-button"
          disabled={newNodeTitle.trim() === '' || createNode.isPending}
        >
          添加节点
        </button>
      </form>
      {error !== '' ? (
        <div className="domain-alert" role="alert">
          {error}
        </div>
      ) : null}
      <div className="roadmap-v2-grid" role="list" aria-label="学习路线节点">
        {model.nodes.map((node) => (
          <article className="roadmap-v2-node" role="listitem" key={node.id}>
            <div className="roadmap-v2-node-head">
              <div>
                <span className="roadmap-v2-node-type">
                  {node.node_type === 'stage'
                    ? '阶段'
                    : node.node_type === 'milestone'
                      ? '里程碑'
                      : '主题'}
                </span>
                <h3>{node.title}</h3>
              </div>
              <span className="roadmap-v2-progress">
                完成 {node.progress.done} / {node.progress.total}
              </span>
            </div>
            <div className="roadmap-v2-counts">
              <span>任务 {node.progress.tasks}</span>
              <span>待办 {node.progress.open}</span>
              <span>进行中 {node.progress.active}</span>
              <span className={node.progress.blocked > 0 ? 'is-blocked' : ''}>
                阻塞 {node.progress.blocked}
              </span>
              <span>跳过 {node.progress.skipped}</span>
            </div>
            <div className="domain-form-actions">
              <button
                type="button"
                onClick={() => {
                  setTaskNode(node)
                  setTaskTitle('')
                }}
              >
                在此节点添加任务
              </button>
              <button
                type="button"
                onClick={() => {
                  setEditing(node)
                  setEditTitle(node.title)
                }}
              >
                编辑节点
              </button>
              <button
                type="button"
                className="domain-danger-button"
                onClick={() => void removeNode(node)}
              >
                删除
              </button>
            </div>
          </article>
        ))}
      </div>
      {model.nodes.length === 0 ? (
        <p className="domain-empty">
          添加第一个节点，再把一个或多个任务关联到它。
        </p>
      ) : null}
      {taskNode ? (
        <div
          className="domain-decision-dialog"
          role="dialog"
          aria-modal="true"
          aria-label="创建关联任务"
        >
          <form onSubmit={addTask}>
            <h3>在“{taskNode.title}”下创建任务</h3>
            <input
              aria-label="关联任务标题"
              value={taskTitle}
              onChange={(e) => setTaskTitle(e.target.value)}
              autoFocus
            />
            <div className="domain-form-actions">
              <button type="button" onClick={() => setTaskNode(null)}>
                取消
              </button>
              <button
                className="domain-primary-button"
                disabled={taskTitle.trim() === '' || createTask.isPending}
              >
                创建关联任务
              </button>
            </div>
          </form>
        </div>
      ) : null}
      {editing ? (
        <div
          className="domain-decision-dialog"
          role="dialog"
          aria-modal="true"
          aria-label="编辑路线节点"
        >
          <form onSubmit={saveEdit}>
            <h3>编辑节点</h3>
            <input
              aria-label="编辑节点标题"
              value={editTitle}
              onChange={(e) => setEditTitle(e.target.value)}
              autoFocus
            />
            <div className="domain-form-actions">
              <button type="button" onClick={() => setEditing(null)}>
                取消
              </button>
              <button
                className="domain-primary-button"
                disabled={editTitle.trim() === '' || updateNode.isPending}
              >
                保存节点
              </button>
            </div>
          </form>
        </div>
      ) : null}
    </section>
  )
}
