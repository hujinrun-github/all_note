import {
  ArrowLeft,
  Map as MapIcon,
  Plus,
  Sparkles,
  WandSparkles,
} from 'lucide-react'
import { type FormEvent, useState } from 'react'
import { Link, useParams } from 'react-router-dom'

import { APIError } from '../api/client'
import type { RoadmapNodeV2 } from '../api/roadmapV2'
import { RoadmapProgressLedger } from '../components/roadmapPlan/RoadmapProgressLedger'
import { RoadmapStageRail } from '../components/roadmapPlan/RoadmapStageRail'
import { RoadmapStageWorkspace } from '../components/roadmapPlan/RoadmapStageWorkspace'
import {
  buildTaskScheduleInput,
  createTaskScheduleDraft,
  TaskScheduleFields,
  taskScheduleValidationError,
} from '../components/taskDomain/TaskScheduleFields'
import {
  useCreateRoadmapMutation,
  useCreateRoadmapNodeMutation,
  useDeleteRoadmapNodeMutation,
  useGenerateRoadmapMutation,
  useRoadmapV2,
  useUpdateRoadmapNodeMutation,
} from '../hooks/useRoadmapV2'
import {
  useCreateTaskMutation,
  useProject,
  useTaskDefinitions,
} from '../hooks/useTaskDomain'

export default function RoadmapV2() {
  const { projectID = '' } = useParams()
  const project = useProject(projectID)
  const roadmap = useRoadmapV2(projectID)
  const tasks = useTaskDefinitions({ project_id: projectID })
  const createRoadmap = useCreateRoadmapMutation(projectID)
  const generateRoadmap = useGenerateRoadmapMutation(projectID)
  const createNode = useCreateRoadmapNodeMutation(projectID)
  const updateNode = useUpdateRoadmapNodeMutation(projectID)
  const deleteNode = useDeleteRoadmapNodeMutation(projectID)
  const createTask = useCreateTaskMutation()
  const [selectedNodeID, setSelectedNodeID] = useState('')
  const [newNodeTitle, setNewNodeTitle] = useState('')
  const [showNodeComposer, setShowNodeComposer] = useState(false)
  const [taskNode, setTaskNode] = useState<RoadmapNodeV2 | null>(null)
  const [taskTitle, setTaskTitle] = useState('')
  const [taskSchedule, setTaskSchedule] = useState(createTaskScheduleDraft)
  const [editing, setEditing] = useState<RoadmapNodeV2 | null>(null)
  const [deleting, setDeleting] = useState<RoadmapNodeV2 | null>(null)
  const [editTitle, setEditTitle] = useState('')
  const [generationPrompt, setGenerationPrompt] = useState('')
  const [showGenerator, setShowGenerator] = useState(false)
  const [error, setError] = useState('')

  if (project.isLoading || roadmap.isLoading) {
    return <p className="domain-empty">正在加载学习路线…</p>
  }
  if (project.isError || !project.data) {
    return <p className="domain-empty">项目暂时不可用。</p>
  }
  if (project.data.kind !== 'learning') {
    return (
      <div className="domain-unavailable" role="alert">
        <strong>只有学习项目可以使用 Roadmap</strong>
      </div>
    )
  }

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
  const fallbackNode =
    model?.nodes.find(
      (node) =>
        node.progress.total === 0 ||
        node.progress.done < node.progress.total
    ) ??
    model?.nodes.at(-1) ??
    null
  const selectedNode =
    model?.nodes.find((node) => node.id === selectedNodeID) ?? fallbackNode
  const selectedNodeTasks = selectedNode
    ? (tasks.data ?? []).filter(
        (task) => task.roadmap_node_id === selectedNode.id
      )
    : []

  async function addNode(event: FormEvent) {
    event.preventDefault()
    if (!model || newNodeTitle.trim() === '') return
    const node = await createNode.mutateAsync({
      roadmapID: model.id,
      input: {
        title: newNodeTitle.trim(),
        node_type: 'topic',
        position: model.nodes.length,
      },
    })
    setSelectedNodeID(node.id)
    setNewNodeTitle('')
    setShowNodeComposer(false)
  }

  async function addTask(event: FormEvent) {
    event.preventDefault()
    if (!taskNode || taskTitle.trim() === '') return
    const scheduleError = taskScheduleValidationError(taskSchedule)
    if (scheduleError !== '') {
      setError(scheduleError)
      return
    }
    await createTask.mutateAsync({
      project_id: projectID,
      roadmap_node_id: taskNode.id,
      title: taskTitle.trim(),
      priority: 0,
      schedule: buildTaskScheduleInput(taskSchedule),
    })
    setTaskTitle('')
    setTaskSchedule(createTaskScheduleDraft())
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
      setDeleting(null)
      setSelectedNodeID('')
    } catch (caught) {
      setDeleting(null)
      setError(
        caught instanceof APIError && caught.code === 'roadmap_node_has_tasks'
          ? '该节点仍有关联任务，请先解绑或迁移任务后再删除。'
          : '删除节点失败，请刷新后重试。'
      )
    }
  }

  async function saveEdit(event: FormEvent) {
    event.preventDefault()
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

  async function generate(event: FormEvent) {
    event.preventDefault()
    setError('')
    try {
      await generateRoadmap.mutateAsync({
        prompt: generationPrompt.trim(),
      })
      setShowGenerator(false)
      setGenerationPrompt('')
      setSelectedNodeID('')
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
      className={`plan-generation-panel${model ? ' is-compact' : ''}`}
      onSubmit={generate}
    >
      <div className="plan-generation-intro">
        <span aria-hidden="true">
          <MapIcon />
        </span>
        <div>
          <h2>
            {model ? '重新梳理学习路线' : '先建立一条可执行的学习路线'}
          </h2>
          <p>
            {model
              ? '新路线只会替换没有关联任务的节点；已有执行记录时系统会阻止替换。'
              : '系统会把目标拆成有顺序的阶段，节点进度由关联任务的执行记录自动汇总。'}
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
          placeholder="例如：每个阶段安排一个可以独立验收的实战项目。"
          onChange={(event) => setGenerationPrompt(event.target.value)}
        />
        <small>{generationPrompt.length} / 4000</small>
      </label>
      {error ? (
        <div className="domain-alert" role="alert">
          {error}
        </div>
      ) : null}
      <div className="plan-generation-actions">
        <button
          type="submit"
          className="plan-primary-action"
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
            onClick={() => {
              setShowGenerator(false)
              setError('')
            }}
          >
            取消
          </button>
        ) : (
          <button
            type="button"
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
        )}
      </div>
    </form>
  )

  if (!model) {
    return (
      <section className="plan-page plan-page-empty">
        <header className="plan-page-empty-heading">
          <Link to={`/projects/${encodeURIComponent(projectID)}`}>
            <ArrowLeft aria-hidden="true" />
            返回项目
          </Link>
          <h1>{project.data.name}</h1>
          <p>把学习目标变成一条有顺序、可执行、可持续复盘的路线。</p>
        </header>
        {generationPanel}
        <div className="plan-page-empty-note">
          <strong>路线负责结构，任务负责执行</strong>
          <p>
            节点本身不需要手动勾选；完成度会从节点下的任务执行记录自动汇总。
          </p>
        </div>
      </section>
    )
  }

  return (
    <section className="plan-page plan-page-ready">
      <header className="plan-page-header">
        <div>
          <Link
            className="plan-back-link"
            to={`/projects/${encodeURIComponent(projectID)}`}
          >
            <ArrowLeft aria-hidden="true" />
            学习计划
          </Link>
          <h1>{model.title}</h1>
          <p>
            {model.description ||
              '按阶段组织学习目标，让每一项任务都能回到清晰的路线位置。'}
          </p>
        </div>
        <div className="plan-page-actions">
          <button
            type="button"
            className="plan-outline-action"
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
            重新生成
          </button>
          <button
            type="button"
            className="plan-primary-action"
            onClick={() => setShowNodeComposer((visible) => !visible)}
          >
            <Plus aria-hidden="true" />
            添加节点
          </button>
        </div>
      </header>

      {showNodeComposer ? (
        <form className="plan-node-composer" onSubmit={addNode}>
          <div>
            <strong>添加路线节点</strong>
            <span>新节点会排在路线末尾，创建后可以继续编辑。</span>
          </div>
          <label>
            <span className="sr-only">节点标题</span>
            <input
              aria-label="节点标题"
              value={newNodeTitle}
              onChange={(event) => setNewNodeTitle(event.target.value)}
              placeholder="例如：掌握基础语法"
              autoFocus
            />
          </label>
          <button
            className="plan-primary-action"
            disabled={newNodeTitle.trim() === '' || createNode.isPending}
          >
            保存节点
          </button>
          <button type="button" onClick={() => setShowNodeComposer(false)}>
            取消
          </button>
        </form>
      ) : null}

      {showGenerator ? generationPanel : null}
      {error && !showGenerator ? (
        <div className="domain-alert" role="alert">
          {error}
        </div>
      ) : null}

      {model.nodes.length > 0 && selectedNode ? (
        <>
          <RoadmapStageRail
            nodes={model.nodes}
            selectedNodeID={selectedNode.id}
            onSelect={setSelectedNodeID}
          />

          <div className="plan-page-layout">
            <RoadmapStageWorkspace
              node={selectedNode}
              tasks={selectedNodeTasks}
              projectID={projectID}
              tasksUnavailable={tasks.isError}
              onAddTask={() => {
                setTaskNode(selectedNode)
                setTaskTitle('')
                setTaskSchedule(createTaskScheduleDraft())
                setError('')
              }}
              onEdit={() => {
                setEditing(selectedNode)
                setEditTitle(selectedNode.title)
              }}
              onDelete={() => setDeleting(selectedNode)}
            />
            <RoadmapProgressLedger
              node={selectedNode}
              projectID={projectID}
              progress={progressPercent}
              totalTasks={totalTasks}
              totalOccurrences={totalOccurrences}
              doneOccurrences={doneOccurrences}
            />
          </div>
        </>
      ) : (
        <div className="plan-route-empty">
          <MapIcon aria-hidden="true" />
          <strong>这条路线还没有节点</strong>
          <p>添加第一个节点，或者重新生成一条完整路线。</p>
          <button
            className="plan-primary-action"
            type="button"
            onClick={() => setShowNodeComposer(true)}
          >
            <Plus aria-hidden="true" />
            添加节点
          </button>
        </div>
      )}

      {taskNode ? (
        <div
          className="domain-decision-dialog"
          role="dialog"
          aria-modal="true"
          aria-label="创建关联任务"
        >
          <form className="domain-task-create-form" onSubmit={addTask}>
            <h3>在“{taskNode.title}”下创建任务</h3>
            <input
              aria-label="关联任务标题"
              value={taskTitle}
              onChange={(event) => setTaskTitle(event.target.value)}
              autoFocus
            />
            <details className="task-schedule-disclosure">
              <summary>
                安排与重复
                <span>
                  {taskSchedule.recurrenceType === 'none'
                    ? '不重复'
                    : '已设置重复'}
                </span>
              </summary>
              <TaskScheduleFields
                value={taskSchedule}
                onChange={setTaskSchedule}
                labelPrefix="关联任务"
              />
            </details>
            {error ? (
              <div className="domain-alert" role="alert">
                {error}
              </div>
            ) : null}
            <div className="domain-form-actions">
              <button
                type="button"
                onClick={() => {
                  setTaskNode(null)
                  setTaskSchedule(createTaskScheduleDraft())
                  setError('')
                }}
              >
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
              onChange={(event) => setEditTitle(event.target.value)}
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

      {deleting ? (
        <div
          className="domain-decision-dialog"
          role="dialog"
          aria-modal="true"
          aria-label="删除路线节点"
        >
          <div>
            <h3>删除“{deleting.title}”？</h3>
            <p>有任务关联时服务端会阻止删除，已有执行记录不会被静默移除。</p>
            <div className="domain-form-actions">
              <button type="button" onClick={() => setDeleting(null)}>
                取消
              </button>
              <button
                className="domain-danger-button"
                type="button"
                disabled={deleteNode.isPending}
                onClick={() => void removeNode(deleting)}
              >
                确认删除
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </section>
  )
}
