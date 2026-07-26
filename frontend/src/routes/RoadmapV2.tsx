import {
  ArrowLeft,
  ArrowRight,
  CheckCircle2,
  CircleDot,
  Flag,
  Map as MapIcon,
  Plus,
  Sparkles,
  WandSparkles,
} from 'lucide-react'
import { type FormEvent, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { APIError } from '../api/client'
import type { RoadmapNodeV2 } from '../api/roadmapV2'
import {
  useCreateRoadmapMutation,
  useCreateRoadmapNodeMutation,
  useDeleteRoadmapNodeMutation,
  useGenerateRoadmapMutation,
  useRoadmapV2,
  useUpdateRoadmapNodeMutation,
} from '../hooks/useRoadmapV2'
import { useCreateTaskMutation, useProject } from '../hooks/useTaskDomain'

const nodeTypeLabel = {
  stage: '阶段',
  topic: '主题',
  milestone: '里程碑',
} as const

export default function RoadmapV2() {
  const { projectID = '' } = useParams()
  const project = useProject(projectID)
  const roadmap = useRoadmapV2(projectID)
  const createRoadmap = useCreateRoadmapMutation(projectID)
  const generateRoadmap = useGenerateRoadmapMutation(projectID)
  const createNode = useCreateRoadmapNodeMutation(projectID)
  const updateNode = useUpdateRoadmapNodeMutation(projectID)
  const deleteNode = useDeleteRoadmapNodeMutation(projectID)
  const createTask = useCreateTaskMutation()
  const [newNodeTitle, setNewNodeTitle] = useState('')
  const [taskNode, setTaskNode] = useState<RoadmapNodeV2 | null>(null)
  const [taskTitle, setTaskTitle] = useState('')
  const [editing, setEditing] = useState<RoadmapNodeV2 | null>(null)
  const [editTitle, setEditTitle] = useState('')
  const [generationPrompt, setGenerationPrompt] = useState('')
  const [showGenerator, setShowGenerator] = useState(false)
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
  const totalTasks =
    model?.nodes.reduce((sum, node) => sum + node.progress.tasks, 0) ?? 0
  const totalOccurrences =
    model?.nodes.reduce((sum, node) => sum + node.progress.total, 0) ?? 0
  const doneOccurrences =
    model?.nodes.reduce((sum, node) => sum + node.progress.done, 0) ?? 0
  const progressPercent =
    totalOccurrences === 0
      ? 0
      : Math.round((doneOccurrences / totalOccurrences) * 100)

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

  async function generate(e: FormEvent) {
    e.preventDefault()
    setError('')
    try {
      await generateRoadmap.mutateAsync({
        prompt: generationPrompt.trim(),
      })
      setShowGenerator(false)
      setGenerationPrompt('')
    } catch (caught) {
      setError(
        caught instanceof APIError &&
          caught.code === 'roadmap_node_has_tasks'
          ? '当前路线已有任务。为保护执行记录，请先迁移或解绑任务，再重新生成路线。'
          : caught instanceof Error
            ? caught.message
            : '学习路线生成失败，请稍后重试。'
      )
    }
  }

  const generationPanel = (
    <form
      className={`roadmap-v2-generation-panel${model ? ' is-compact' : ''}`}
      onSubmit={generate}
    >
      <div className="roadmap-v2-generation-intro">
        <span className="roadmap-v2-generation-icon" aria-hidden="true">
          <MapIcon />
        </span>
        <div>
          <span>AI LEARNING ROADMAP</span>
          <h2>
            {model
              ? '重新梳理这条学习路线'
              : '先生成一条完整路径，再逐步转成任务'}
          </h2>
          <p>
            {model
              ? '新路线会替换当前未关联任务的节点；已有执行任务时会自动阻止替换，避免学习记录丢失。'
              : '将目标拆成诊断、基础知识、专项练习、综合实践与最终验收，并给每一步定义清晰产出。'}
          </p>
        </div>
      </div>
      <label>
        <span>补充生成要求（可选）</span>
        <textarea
          aria-label="补充生成要求"
          value={generationPrompt}
          rows={model ? 3 : 5}
          maxLength={4000}
          placeholder="例如：优先练习听力，每个阶段安排一个可以独立完成的实战项目。"
          onChange={(event) => setGenerationPrompt(event.target.value)}
        />
        <small>{generationPrompt.length} / 4000</small>
      </label>
      {error ? (
        <div className="domain-alert" role="alert">
          {error}
        </div>
      ) : null}
      <div className="roadmap-v2-generation-actions">
        <button
          type="submit"
          className="domain-primary-button"
          disabled={generateRoadmap.isPending}
        >
          <WandSparkles aria-hidden="true" />
          {generateRoadmap.isPending
            ? '正在生成完整路径…'
            : model
              ? '重新生成路线'
              : '生成学习 Roadmap'}
        </button>
        {model ? (
          <button
            type="button"
            className="roadmap-v2-blank-button"
            onClick={() => {
              setShowGenerator(false)
              setError('')
            }}
          >
            取消
          </button>
        ) : (
          <>
            <span>或</span>
            <button
              type="button"
              className="roadmap-v2-blank-button"
              disabled={createRoadmap.isPending}
              onClick={() =>
                createRoadmap.mutate({
                  title: `${project.data.name} 学习路线`,
                })
              }
            >
              <Plus aria-hidden="true" />
              创建空白路线
            </button>
          </>
        )}
      </div>
    </form>
  )

  if (!model)
    return (
      <section className="domain-page roadmap-v2-page roadmap-v2-page-empty">
        <header className="domain-page-heading roadmap-v2-page-heading">
          <div>
            <Link
              className="domain-back-link"
              to={`/projects/${encodeURIComponent(projectID)}`}
            >
              <ArrowLeft aria-hidden="true" />
              返回项目
            </Link>
            <span className="roadmap-v2-eyebrow">LEARNING ROADMAP</span>
            <h1>{project.data.name}</h1>
            <p>把学习目标变成一条有顺序、可执行、可持续复盘的路线。</p>
          </div>
        </header>
        {generationPanel}
        <div className="roadmap-v2-generation-note">
          <strong>路线负责结构，任务负责执行</strong>
          <p>
            Roadmap
            节点本身不需要手动勾选；完成度会从节点下的任务执行记录自动汇总。
          </p>
        </div>
      </section>
    )

  return (
    <section className="domain-page roadmap-v2-page roadmap-v2-page-ready">
      <header className="roadmap-v2-ready-header">
        <div>
          <Link
            className="domain-back-link"
            to={`/projects/${encodeURIComponent(projectID)}`}
          >
            <ArrowLeft aria-hidden="true" />
            返回项目
          </Link>
          <span className="roadmap-v2-eyebrow">LEARNING ROADMAP</span>
          <h1>{model.title}</h1>
          <p>
            按路线建立任务并执行，节点进度会根据关联任务的实际记录自动更新。
          </p>
        </div>
        <div className="roadmap-v2-ready-actions">
          <button
            type="button"
            className="roadmap-v2-regenerate-button"
            disabled={totalTasks > 0}
            title={
              totalTasks > 0
                ? '路线已有任务，请先迁移或解绑任务'
                : '根据项目目标重新生成节点'
            }
            onClick={() => {
              setShowGenerator((current) => !current)
              setError('')
            }}
          >
            <Sparkles aria-hidden="true" />
            重新生成路线
          </button>
        </div>
      </header>

      <div className="roadmap-v2-summary" aria-label="学习路线概览">
        <div>
          <span>路线节点</span>
          <strong>{model.nodes.length}</strong>
        </div>
        <div>
          <span>关联任务</span>
          <strong>{totalTasks}</strong>
        </div>
        <div>
          <span>执行进度</span>
          <strong>{progressPercent}%</strong>
        </div>
        <div className="roadmap-v2-summary-progress">
          <span
            style={{ width: `${progressPercent}%` }}
            aria-hidden="true"
          />
        </div>
      </div>

      {showGenerator ? generationPanel : null}
      {error && !showGenerator ? (
        <div className="domain-alert" role="alert">
          {error}
        </div>
      ) : null}

      <div className="roadmap-v2-layout">
        <main className="roadmap-v2-route">
          <div className="roadmap-v2-section-heading">
            <div>
              <span>PATH</span>
              <h2>学习路径</h2>
            </div>
            <p>{model.nodes.length} 个节点，按建议顺序推进</p>
          </div>

          {model.nodes.length > 0 ? (
            <ol className="roadmap-v2-timeline" aria-label="学习路线节点">
              {model.nodes.map((node, index) => {
                const nodePercent =
                  node.progress.total === 0
                    ? 0
                    : Math.round(
                        (node.progress.done / node.progress.total) * 100
                      )
                return (
                  <li className="roadmap-v2-node" key={node.id}>
                    <div className="roadmap-v2-node-marker" aria-hidden="true">
                      {node.node_type === 'milestone' ? (
                        <Flag />
                      ) : node.progress.total > 0 &&
                        node.progress.done === node.progress.total ? (
                        <CheckCircle2 />
                      ) : (
                        <CircleDot />
                      )}
                    </div>
                    <article>
                      <div className="roadmap-v2-node-head">
                        <div>
                          <span className="roadmap-v2-node-type">
                            {String(index + 1).padStart(2, '0')} ·{' '}
                            {nodeTypeLabel[node.node_type]}
                          </span>
                          <h3>{node.title}</h3>
                        </div>
                        <span className="roadmap-v2-progress">
                          {nodePercent}%
                        </span>
                      </div>
                      {node.description ? (
                        <p className="roadmap-v2-node-description">
                          {node.description}
                        </p>
                      ) : null}
                      <div className="roadmap-v2-node-progress">
                        <span style={{ width: `${nodePercent}%` }} />
                      </div>
                      <div className="roadmap-v2-node-footer">
                        <div className="roadmap-v2-counts">
                          <span>任务 {node.progress.tasks}</span>
                          <span>待办 {node.progress.open}</span>
                          <span>进行中 {node.progress.active}</span>
                          {node.progress.blocked > 0 ? (
                            <span className="is-blocked">
                              阻塞 {node.progress.blocked}
                            </span>
                          ) : null}
                        </div>
                        <div className="roadmap-v2-node-actions">
                          <button
                            type="button"
                            className="is-primary"
                            onClick={() => {
                              setTaskNode(node)
                              setTaskTitle('')
                            }}
                          >
                            添加任务
                            <ArrowRight aria-hidden="true" />
                          </button>
                          <button
                            type="button"
                            onClick={() => {
                              setEditing(node)
                              setEditTitle(node.title)
                            }}
                          >
                            编辑
                          </button>
                          <button
                            type="button"
                            className="domain-danger-button"
                            onClick={() => void removeNode(node)}
                          >
                            删除
                          </button>
                        </div>
                      </div>
                    </article>
                  </li>
                )
              })}
            </ol>
          ) : (
            <div className="roadmap-v2-route-empty">
              <MapIcon aria-hidden="true" />
              <strong>这条路线还没有节点</strong>
              <p>用右侧表单添加第一个节点，或重新生成一条完整路线。</p>
            </div>
          )}
        </main>

        <aside className="roadmap-v2-side">
          <form className="roadmap-v2-add-panel" onSubmit={addNode}>
            <span className="roadmap-v2-side-label">ADD A STEP</span>
            <h2>添加路线节点</h2>
            <p>手动补充一个主题、练习阶段或验收里程碑。</p>
            <label>
              <span>节点标题</span>
              <input
                aria-label="节点标题"
                value={newNodeTitle}
                onChange={(event) => setNewNodeTitle(event.target.value)}
                placeholder="例如：掌握基础语法"
              />
            </label>
            <button
              className="domain-primary-button"
              disabled={
                newNodeTitle.trim() === '' || createNode.isPending
              }
            >
              <Plus aria-hidden="true" />
              添加节点
            </button>
          </form>
          <div className="roadmap-v2-guide">
            <strong>如何使用这条路线</strong>
            <ol>
              <li>先浏览所有节点，确认顺序和最终产出。</li>
              <li>从当前节点创建可执行任务并安排时间。</li>
              <li>完成任务后，节点进度会自动汇总。</li>
            </ol>
          </div>
        </aside>
      </div>

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
              onChange={(event) => setTaskTitle(event.target.value)}
              autoFocus
            />
            <div className="domain-form-actions">
              <button type="button" onClick={() => setTaskNode(null)}>
                取消
              </button>
              <button
                className="domain-primary-button"
                disabled={
                  taskTitle.trim() === '' || createTask.isPending
                }
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
              onChange={(event) => setEditTitle(event.target.value)}
              autoFocus
            />
            <div className="domain-form-actions">
              <button type="button" onClick={() => setEditing(null)}>
                取消
              </button>
              <button
                className="domain-primary-button"
                disabled={
                  editTitle.trim() === '' || updateNode.isPending
                }
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
