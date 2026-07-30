import {
  ArrowLeft,
  CheckCircle2,
  Circle,
  GitBranch,
  LayoutTemplate,
  Plus,
  ShieldAlert,
  Timer,
} from 'lucide-react'
import {
  Background,
  Controls,
  MiniMap,
  ReactFlow,
  type Edge,
  type NodeTypes,
  type ReactFlowInstance,
  useEdgesState,
  useNodesState,
} from '@xyflow/react'
import { type FormEvent, useEffect, useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import '@xyflow/react/dist/style.css'

import type {
  ExecutionStatus,
  OccurrenceV2,
  TaskV2,
} from '../api/taskDomain'
import {
  RoadmapRootNodeView,
  RoadmapTaskNodeView,
  type RoadmapRootFlowNode,
  type RoadmapTaskFlowNode,
} from '../components/roadmapMindMap/RoadmapMindMapNodes'
import { RoadmapTaskInspector } from '../components/roadmapMindMap/RoadmapTaskInspector'
import { roadmapNodeProgress } from '../components/roadmapPlan/RoadmapStageRail'
import { useRoadmapV2 } from '../hooks/useRoadmapV2'
import {
  useCreateTaskMutation,
  useOccurrences,
  useProject,
  useTaskDefinitions,
} from '../hooks/useTaskDomain'

type MindMapNode = RoadmapRootFlowNode | RoadmapTaskFlowNode
type StatusFilter = 'all' | 'open' | 'active' | 'done' | 'blocked'

const nodeTypes: NodeTypes = {
  roadmapRoot: RoadmapRootNodeView,
  roadmapTask: RoadmapTaskNodeView,
}

const filterOptions: Array<{
  value: StatusFilter
  label: string
  icon: typeof Circle
}> = [
  { value: 'all', label: '全部', icon: GitBranch },
  { value: 'open', label: '待办', icon: Circle },
  { value: 'active', label: '进行中', icon: Timer },
  { value: 'done', label: '已完成', icon: CheckCircle2 },
  { value: 'blocked', label: '被阻塞', icon: ShieldAlert },
]

function resolveTaskStatus(
  task: TaskV2,
  occurrences: OccurrenceV2[]
): ExecutionStatus {
  if (task.lifecycle_status === 'completed') return 'done'
  if (task.lifecycle_status === 'cancelled') return 'cancelled'

  const statuses = occurrences.map((occurrence) => occurrence.execution_status)
  if (statuses.includes('blocked')) return 'blocked'
  if (statuses.includes('active')) return 'active'
  if (statuses.includes('open')) return 'open'
  if (statuses.length > 0 && statuses.every((status) => status === 'done')) {
    return 'done'
  }
  if (
    statuses.length > 0 &&
    statuses.every((status) => status === 'skipped')
  ) {
    return 'skipped'
  }
  return 'open'
}

function preferredOccurrence(occurrences: OccurrenceV2[]) {
  return (
    occurrences.find(
      (occurrence) =>
        occurrence.execution_status === 'active' ||
        occurrence.execution_status === 'blocked' ||
        occurrence.execution_status === 'open'
    ) ?? occurrences[0]
  )
}

function buildFlow(
  nodeID: string,
  nodeTitle: string,
  progress: number,
  tasks: TaskV2[],
  statusByTask: Map<string, ExecutionStatus>
): { nodes: MindMapNode[]; edges: Edge[] } {
  const rowGap = 108
  const taskColumnX = 430
  const rootY = Math.max(28, ((tasks.length - 1) * rowGap) / 2)
  const nodes: MindMapNode[] = [
    {
      id: `roadmap-root-${nodeID}`,
      type: 'roadmapRoot',
      position: { x: 48, y: rootY },
      data: {
        title: nodeTitle,
        taskCount: tasks.length,
        progress,
      },
      draggable: false,
    },
    ...tasks.map(
      (task, index): RoadmapTaskFlowNode => ({
        id: task.id,
        type: 'roadmapTask',
        position: { x: taskColumnX, y: index * rowGap },
        data: {
          sequence: index + 1,
          title: task.title,
          priority: task.priority,
          status: statusByTask.get(task.id) ?? 'open',
        },
      })
    ),
  ]
  const edges = tasks.map(
    (task): Edge => ({
      id: `roadmap-task-edge-${task.id}`,
      source: `roadmap-root-${nodeID}`,
      target: task.id,
      type: 'smoothstep',
      style: {
        stroke:
          statusByTask.get(task.id) === 'done'
            ? '#6b9b37'
            : statusByTask.get(task.id) === 'blocked'
              ? '#d4a017'
              : '#9b7658',
        strokeWidth: 1.6,
      },
    })
  )
  return { nodes, edges }
}

export default function RoadmapMindMap() {
  const { projectID = '', roadmapNodeID = '' } = useParams()
  const project = useProject(projectID)
  const roadmap = useRoadmapV2(projectID)
  const tasks = useTaskDefinitions({ project_id: projectID })
  const occurrences = useOccurrences(
    { project_id: projectID },
    { enabled: projectID !== '' }
  )
  const createTask = useCreateTaskMutation()
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all')
  const [selectedTaskID, setSelectedTaskID] = useState('')
  const [showCreateTask, setShowCreateTask] = useState(false)
  const [newTaskTitle, setNewTaskTitle] = useState('')
  const [isCompactViewport, setIsCompactViewport] = useState(
    () =>
      typeof window.matchMedia === 'function' &&
      window.matchMedia('(max-width: 820px)').matches
  )
  const [flowInstance, setFlowInstance] =
    useState<ReactFlowInstance<MindMapNode, Edge> | null>(null)

  const roadmapNode = roadmap.data?.nodes.find(
    (candidate) => candidate.id === roadmapNodeID
  )
  const nodeTasks = useMemo(
    () =>
      [...(tasks.data ?? [])]
        .filter((task: TaskV2) => task.roadmap_node_id === roadmapNodeID)
        .sort(
          (left: TaskV2, right: TaskV2) =>
            left.sort_order - right.sort_order
        ),
    [roadmapNodeID, tasks.data]
  )
  const occurrencesByTask = useMemo(() => {
    const byTask = new Map<string, OccurrenceV2[]>()
    for (const occurrence of occurrences.data ?? []) {
      const current = byTask.get(occurrence.task_id)
      if (current) current.push(occurrence)
      else byTask.set(occurrence.task_id, [occurrence])
    }
    return byTask
  }, [occurrences.data])
  const statusByTask = useMemo(() => {
    const byTask = new Map<string, ExecutionStatus>()
    for (const task of nodeTasks) {
      byTask.set(
        task.id,
        resolveTaskStatus(task, occurrencesByTask.get(task.id) ?? [])
      )
    }
    return byTask
  }, [nodeTasks, occurrencesByTask])
  const visibleTasks = useMemo(
    () =>
      statusFilter === 'all'
        ? nodeTasks
        : nodeTasks.filter((task) => statusByTask.get(task.id) === statusFilter),
    [nodeTasks, statusByTask, statusFilter]
  )
  const selectedTask =
    visibleTasks.find((task) => task.id === selectedTaskID) ??
    nodeTasks.find((task) => task.id === selectedTaskID) ??
    visibleTasks[0]
  const selectedTaskOccurrences = selectedTask
    ? (occurrencesByTask.get(selectedTask.id) ?? [])
    : []
  const progress = roadmapNode ? roadmapNodeProgress(roadmapNode) : 0
  const flowModel = useMemo(
    () =>
      roadmapNode
        ? buildFlow(
            roadmapNode.id,
            roadmapNode.title,
            progress,
            visibleTasks,
            statusByTask
          )
        : { nodes: [], edges: [] },
    [progress, roadmapNode, statusByTask, visibleTasks]
  )
  const [flowNodes, setFlowNodes, onNodesChange] =
    useNodesState<MindMapNode>(flowModel.nodes)
  const [flowEdges, setFlowEdges, onEdgesChange] =
    useEdgesState<Edge>(flowModel.edges)

  useEffect(() => {
    setFlowNodes(flowModel.nodes)
    setFlowEdges(flowModel.edges)
  }, [flowModel.edges, flowModel.nodes, setFlowEdges, setFlowNodes])

  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return
    const media = window.matchMedia('(max-width: 820px)')
    const updateViewportMode = () => setIsCompactViewport(media.matches)
    media.addEventListener('change', updateViewportMode)
    return () => media.removeEventListener('change', updateViewportMode)
  }, [])

  if (project.isLoading || roadmap.isLoading || tasks.isLoading) {
    return <p className="domain-empty">正在打开任务脑图…</p>
  }
  if (
    project.isError ||
    !project.data ||
    roadmap.isError ||
    !roadmap.data ||
    !roadmapNode
  ) {
    return (
      <div className="domain-unavailable" role="alert">
        <strong>这个路线节点暂时不可用</strong>
        <p>返回学习计划，确认节点仍然存在后再试。</p>
      </div>
    )
  }

  function resetLayout() {
    setFlowNodes(flowModel.nodes)
    setFlowEdges(flowModel.edges)
    window.requestAnimationFrame(() => {
      if (isCompactViewport) {
        void flowInstance?.setViewport(
          { x: -16, y: 18, zoom: 0.55 },
          { duration: 240 }
        )
        return
      }
      void flowInstance?.fitView({ padding: 0.18, duration: 240 })
    })
  }

  async function addTask(event: FormEvent) {
    event.preventDefault()
    if (newTaskTitle.trim() === '') return
    const created = await createTask.mutateAsync({
      project_id: projectID,
      roadmap_node_id: roadmapNodeID,
      title: newTaskTitle.trim(),
      priority: 0,
      sort_order: nodeTasks.length,
      schedule: {
        recurrence_type: 'none',
        timing_type: 'unscheduled',
        timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
      },
    })
    setSelectedTaskID(created.task.id)
    setNewTaskTitle('')
    setShowCreateTask(false)
  }

  return (
    <section className="mindmap-page">
      <header className="mindmap-page-header">
        <div>
          <nav aria-label="当前位置">
            <Link
              to={`/projects/${encodeURIComponent(projectID)}/roadmap`}
              aria-label="返回学习计划"
            >
              <ArrowLeft aria-hidden="true" />
            </Link>
            <Link to={`/projects/${encodeURIComponent(projectID)}/roadmap`}>
              {roadmap.data.title}
            </Link>
            <span aria-hidden="true">/</span>
            <strong>{roadmapNode.title}</strong>
          </nav>
          <h1>{roadmapNode.title}</h1>
          <div className="mindmap-page-progress">
            <span>阶段进度 {progress}%</span>
            <i aria-hidden="true">
              <b style={{ width: `${progress}%` }} />
            </i>
            <span>
              已完成 {roadmapNode.progress.done} / {roadmapNode.progress.total}
            </span>
          </div>
        </div>
        <div className="mindmap-page-actions">
          <button type="button" onClick={resetLayout}>
            <LayoutTemplate aria-hidden="true" />
            自动布局
          </button>
          <button
            className="plan-primary-action"
            type="button"
            onClick={() => setShowCreateTask(true)}
          >
            <Plus aria-hidden="true" />
            新建任务
          </button>
        </div>
      </header>

      <div className="mindmap-filter-bar" aria-label="按状态筛选任务">
        {filterOptions.map(({ value, label, icon: Icon }) => {
          const count =
            value === 'all'
              ? nodeTasks.length
              : nodeTasks.filter(
                  (task) => statusByTask.get(task.id) === value
                ).length
          return (
            <button
              type="button"
              className={`is-${value}${
                statusFilter === value ? ' is-active' : ''
              }`}
              aria-pressed={statusFilter === value}
              key={value}
              onClick={() => {
                setStatusFilter(value)
                setSelectedTaskID('')
              }}
            >
              <Icon aria-hidden="true" />
              {label}
              <span>{count}</span>
            </button>
          )
        })}
      </div>

      <div className="mindmap-workspace">
        <main className="mindmap-canvas" aria-label={`${roadmapNode.title} 任务脑图`}>
          <ReactFlow
            nodes={flowNodes}
            edges={flowEdges}
            nodeTypes={nodeTypes}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            onInit={setFlowInstance}
            onNodeClick={(_event, node) => {
              if (node.type === 'roadmapTask') setSelectedTaskID(node.id)
            }}
            nodesConnectable={false}
            elementsSelectable
            fitView={!isCompactViewport}
            fitViewOptions={{ padding: 0.18 }}
            defaultViewport={
              isCompactViewport
                ? { x: -16, y: 18, zoom: 0.55 }
                : undefined
            }
            minZoom={0.32}
            maxZoom={1.8}
          >
            <Background gap={24} size={1} color="#dfd4c4" />
            <MiniMap
              className="mindmap-minimap"
              pannable
              zoomable
              nodeStrokeWidth={2}
              nodeColor={(node) =>
                node.type === 'roadmapRoot' ? '#b87333' : '#fefdf8'
              }
            />
            <Controls showInteractive={false} />
          </ReactFlow>

          {visibleTasks.length === 0 ? (
            <div className="mindmap-canvas-empty">
              <GitBranch aria-hidden="true" />
              <strong>
                {nodeTasks.length === 0
                  ? '这个节点还没有任务'
                  : '当前筛选下没有任务'}
              </strong>
              <p>
                {nodeTasks.length === 0
                  ? '新建第一项任务，脑图会自动把它连接到路线节点。'
                  : '切换状态筛选，或者清除当前筛选。'}
              </p>
              {nodeTasks.length === 0 ? (
                <button
                  className="plan-primary-action"
                  type="button"
                  onClick={() => setShowCreateTask(true)}
                >
                  <Plus aria-hidden="true" />
                  新建任务
                </button>
              ) : (
                <button type="button" onClick={() => setStatusFilter('all')}>
                  查看全部任务
                </button>
              )}
            </div>
          ) : null}
        </main>

        <RoadmapTaskInspector
          task={selectedTask}
          status={
            selectedTask ? statusByTask.get(selectedTask.id) : undefined
          }
          occurrence={preferredOccurrence(selectedTaskOccurrences)}
          roadmapNodeTitle={roadmapNode.title}
          onClose={() => setSelectedTaskID('')}
        />
      </div>

      {showCreateTask ? (
        <div
          className="domain-decision-dialog"
          role="dialog"
          aria-modal="true"
          aria-label="在脑图中新建任务"
        >
          <form onSubmit={addTask}>
            <h3>在“{roadmapNode.title}”下新建任务</h3>
            <p>创建后会直接出现在当前脑图中，默认保持未安排状态。</p>
            <input
              aria-label="新任务标题"
              value={newTaskTitle}
              onChange={(event) => setNewTaskTitle(event.target.value)}
              autoFocus
            />
            <div className="domain-form-actions">
              <button type="button" onClick={() => setShowCreateTask(false)}>
                取消
              </button>
              <button
                className="domain-primary-button"
                disabled={
                  newTaskTitle.trim() === '' || createTask.isPending
                }
              >
                创建任务
              </button>
            </div>
          </form>
        </div>
      ) : null}
    </section>
  )
}
