import {
  ArrowLeft,
  CheckCircle2,
  Circle,
  CircleHelp,
  GitBranch,
  LayoutTemplate,
  Maximize2,
  Minus,
  PanelRight,
  Pencil,
  Plus,
  ShieldAlert,
  Timer,
  Trash2,
  X,
} from 'lucide-react'
import {
  Background,
  MiniMap,
  ReactFlow,
  type Edge,
  type NodeTypes,
  type ReactFlowInstance,
  useEdgesState,
  useNodesState,
} from '@xyflow/react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import '@xyflow/react/dist/style.css'

import type { ExecutionStatus, OccurrenceV2, TaskV2 } from '../api/taskDomain'
import {
  RoadmapRootNodeView,
  RoadmapTaskNodeView,
  type RoadmapRootFlowNode,
  type RoadmapTaskFlowNode,
} from '../components/roadmapMindMap/RoadmapMindMapNodes'
import {
  RoadmapTaskInspector,
  type RoadmapExecutionStatusChange,
} from '../components/roadmapMindMap/RoadmapTaskInspector'
import { roadmapNodeProgress } from '../components/roadmapPlan/RoadmapStageRail'
import { useRoadmapV2 } from '../hooks/useRoadmapV2'
import {
  useBlockOccurrenceMutation,
  useCancelOccurrenceMutation,
  useCancelTaskMutation,
  useCompleteOccurrenceMutation,
  useCreateTaskMutation,
  useOccurrences,
  useProject,
  useReopenOccurrenceMutation,
  useSkipOccurrenceMutation,
  useStartOccurrenceMutation,
  useTaskDefinitions,
  useUnblockOccurrenceMutation,
  useUpdateTaskDefinitionMutation,
} from '../hooks/useTaskDomain'

type MindMapNode = RoadmapRootFlowNode | RoadmapTaskFlowNode
type StatusFilter = 'all' | 'open' | 'active' | 'done' | 'blocked'

interface FlowCallbacks {
  onAddTask: () => void
  onToggleCollapse: () => void
  onAddSibling: (taskID: string) => void
  onCancelDraft: () => void
  onCreateDraft: (title: string) => Promise<void>
  onRename: (taskID: string, title: string) => Promise<void>
}

interface FlowOptions extends FlowCallbacks {
  collapsed: boolean
  draftAfterTaskID?: string
  editRequests: Record<string, number>
  selectedNodeID: string
  totalTaskCount: number
}

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
  if (statuses.length > 0 && statuses.every((status) => status === 'skipped')) {
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

function occurrenceCommandVariables(task: TaskV2, occurrence: OccurrenceV2) {
  return {
    projectID: occurrence.project_id ?? task.project_id,
    taskID: task.id,
    occurrenceID: occurrence.id,
    expectedRevisions: {
      expected_task_revision: occurrence.task_revision ?? task.revision,
      expected_schedule_revision:
        occurrence.schedule_revision ?? task.schedule_revision,
      expected_occurrence_revisions: {
        [occurrence.id]: occurrence.revision,
      },
    },
  }
}

function buildFlow(
  nodeID: string,
  nodeTitle: string,
  progress: number,
  tasks: TaskV2[],
  statusByTask: Map<string, ExecutionStatus>,
  options: FlowOptions
): { nodes: MindMapNode[]; edges: Edge[] } {
  const rootID = `roadmap-root-${nodeID}`
  const draftID = 'mindmap-draft-task'
  const taskEntries: Array<{ task?: TaskV2; id: string }> = tasks.map(
    (task) => ({ task, id: task.id })
  )

  if (options.draftAfterTaskID !== undefined) {
    const selectedIndex = taskEntries.findIndex(
      (entry) => entry.id === options.draftAfterTaskID
    )
    taskEntries.splice(
      selectedIndex >= 0 ? selectedIndex + 1 : taskEntries.length,
      0,
      {
        id: draftID,
      }
    )
  }

  const visibleEntries = options.collapsed ? [] : taskEntries
  const rowGap = 98
  const taskColumnX = 470
  const rootY = Math.max(38, ((visibleEntries.length - 1) * rowGap) / 2)
  const nodes: MindMapNode[] = [
    {
      id: rootID,
      type: 'roadmapRoot',
      position: { x: 54, y: rootY },
      selected: options.selectedNodeID === rootID,
      data: {
        title: nodeTitle,
        taskCount: options.totalTaskCount,
        progress,
        collapsed: options.collapsed,
        onAddTask: options.onAddTask,
        onToggleCollapse: options.onToggleCollapse,
      },
    },
    ...visibleEntries.map((entry, index): RoadmapTaskFlowNode => {
      const task = entry.task
      return {
        id: entry.id,
        type: 'roadmapTask',
        position: { x: taskColumnX, y: index * rowGap },
        selected: options.selectedNodeID === entry.id,
        data: {
          taskID: task?.id ?? draftID,
          sequence: task
            ? tasks.findIndex((candidate) => candidate.id === task.id) + 1
            : tasks.length + 1,
          title: task?.title ?? '',
          priority: task?.priority ?? 0,
          status: task ? (statusByTask.get(task.id) ?? 'open') : 'open',
          editRequest: task ? (options.editRequests[task.id] ?? 0) : 0,
          isDraft: task === undefined,
          onAddSibling: options.onAddSibling,
          onCancelDraft: options.onCancelDraft,
          onCreateDraft: options.onCreateDraft,
          onRename: options.onRename,
        },
      }
    }),
  ]
  const edges = visibleEntries.map(
    (entry): Edge => ({
      id: `roadmap-task-edge-${entry.id}`,
      source: rootID,
      target: entry.id,
      type: 'smoothstep',
      animated: entry.id === draftID,
      style: {
        stroke:
          entry.task && statusByTask.get(entry.task.id) === 'done'
            ? '#6f8f4b'
            : entry.task && statusByTask.get(entry.task.id) === 'blocked'
              ? '#c79637'
              : '#ad8b6d',
        strokeWidth: 1.6,
      },
    })
  )
  return { nodes, edges }
}

export default function RoadmapMindMap() {
  const { projectID = '', roadmapNodeID = '' } = useParams()
  const rootNodeID = `roadmap-root-${roadmapNodeID}`
  const project = useProject(projectID)
  const roadmap = useRoadmapV2(projectID)
  const tasks = useTaskDefinitions({ project_id: projectID })
  const occurrences = useOccurrences(
    { project_id: projectID },
    { enabled: projectID !== '' }
  )
  const createTask = useCreateTaskMutation()
  const updateTask = useUpdateTaskDefinitionMutation()
  const cancelTask = useCancelTaskMutation()
  const startOccurrence = useStartOccurrenceMutation()
  const blockOccurrence = useBlockOccurrenceMutation()
  const unblockOccurrence = useUnblockOccurrenceMutation()
  const completeOccurrence = useCompleteOccurrenceMutation()
  const skipOccurrence = useSkipOccurrenceMutation()
  const cancelOccurrence = useCancelOccurrenceMutation()
  const reopenOccurrence = useReopenOccurrenceMutation()
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all')
  const [selectedNodeID, setSelectedNodeID] = useState('')
  const [inspectorOpen, setInspectorOpen] = useState(false)
  const [branchCollapsed, setBranchCollapsed] = useState(false)
  const [draftAfterTaskID, setDraftAfterTaskID] = useState<string>()
  const [editRequests, setEditRequests] = useState<Record<string, number>>({})
  const [contextMenu, setContextMenu] = useState<{
    taskID: string
    x: number
    y: number
  } | null>(null)
  const [cancelTaskID, setCancelTaskID] = useState('')
  const [showShortcutHelp, setShowShortcutHelp] = useState(false)
  const [interactionError, setInteractionError] = useState('')
  const [zoomPercent, setZoomPercent] = useState(100)
  const [isCompactViewport, setIsCompactViewport] = useState(
    () =>
      typeof window.matchMedia === 'function' &&
      window.matchMedia('(max-width: 820px)').matches
  )
  const [flowInstance, setFlowInstance] = useState<ReactFlowInstance<
    MindMapNode,
    Edge
  > | null>(null)

  const roadmapNode = roadmap.data?.nodes.find(
    (candidate) => candidate.id === roadmapNodeID
  )
  const nodeTasks = useMemo(
    () =>
      [...(tasks.data ?? [])]
        .filter((task: TaskV2) => task.roadmap_node_id === roadmapNodeID)
        .sort(
          (left: TaskV2, right: TaskV2) => left.sort_order - right.sort_order
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
        : nodeTasks.filter(
            (task) => statusByTask.get(task.id) === statusFilter
          ),
    [nodeTasks, statusByTask, statusFilter]
  )
  const selectedTaskID = selectedNodeID !== rootNodeID ? selectedNodeID : ''
  const selectedTask = nodeTasks.find((task) => task.id === selectedTaskID)
  const selectedTaskOccurrences = selectedTask
    ? (occurrencesByTask.get(selectedTask.id) ?? [])
    : []
  const statusCommandBusy =
    startOccurrence.isPending ||
    blockOccurrence.isPending ||
    unblockOccurrence.isPending ||
    completeOccurrence.isPending ||
    skipOccurrence.isPending ||
    cancelOccurrence.isPending ||
    reopenOccurrence.isPending
  const progress = roadmapNode ? roadmapNodeProgress(roadmapNode) : 0

  const beginDraftTask = useCallback((afterTaskID?: string) => {
    setBranchCollapsed(false)
    setDraftAfterTaskID(afterTaskID ?? '')
    setSelectedNodeID('mindmap-draft-task')
    setInspectorOpen(false)
    setContextMenu(null)
    setInteractionError('')
  }, [])

  const cancelDraftTask = useCallback(() => {
    setDraftAfterTaskID(undefined)
    setSelectedNodeID('')
  }, [])

  const createDraftTask = useCallback(
    async (title: string) => {
      setInteractionError('')
      try {
        const created = await createTask.mutateAsync({
          project_id: projectID,
          roadmap_node_id: roadmapNodeID,
          title,
          priority: 0,
          sort_order: nodeTasks.length,
          schedule: {
            recurrence_type: 'none',
            timing_type: 'unscheduled',
            timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
          },
        })
        setDraftAfterTaskID(undefined)
        setSelectedNodeID(created.task.id)
        setInspectorOpen(true)
      } catch (caught) {
        setInteractionError(
          caught instanceof Error
            ? caught.message
            : '创建任务失败，请稍后重试。'
        )
        throw caught
      }
    },
    [createTask.mutateAsync, nodeTasks.length, projectID, roadmapNodeID]
  )

  const renameTask = useCallback(
    async (taskID: string, title: string) => {
      const task = nodeTasks.find((candidate) => candidate.id === taskID)
      if (!task) return
      setInteractionError('')
      try {
        await updateTask.mutateAsync({
          projectID,
          taskID,
          input: {
            title,
            expected_task_revision: task.revision,
            expected_schedule_revision: task.schedule_revision,
          },
        })
      } catch (caught) {
        setInteractionError(
          caught instanceof Error ? caught.message : '重命名失败，请稍后重试。'
        )
        throw caught
      }
    },
    [nodeTasks, projectID, updateTask.mutateAsync]
  )

  const toggleBranch = useCallback(() => {
    setBranchCollapsed((current) => !current)
    setDraftAfterTaskID(undefined)
    setSelectedNodeID(rootNodeID)
    setInspectorOpen(false)
  }, [rootNodeID])

  const flowModel = useMemo(
    () =>
      roadmapNode
        ? buildFlow(
            roadmapNode.id,
            roadmapNode.title,
            progress,
            visibleTasks,
            statusByTask,
            {
              collapsed: branchCollapsed,
              draftAfterTaskID,
              editRequests,
              selectedNodeID,
              totalTaskCount: nodeTasks.length,
              onAddTask: () => beginDraftTask(),
              onToggleCollapse: toggleBranch,
              onAddSibling: beginDraftTask,
              onCancelDraft: cancelDraftTask,
              onCreateDraft: createDraftTask,
              onRename: renameTask,
            }
          )
        : { nodes: [], edges: [] },
    [
      beginDraftTask,
      branchCollapsed,
      cancelDraftTask,
      createDraftTask,
      draftAfterTaskID,
      editRequests,
      nodeTasks.length,
      progress,
      renameTask,
      roadmapNode,
      selectedNodeID,
      statusByTask,
      toggleBranch,
      visibleTasks,
    ]
  )
  const [flowNodes, setFlowNodes, onNodesChange] = useNodesState<MindMapNode>(
    flowModel.nodes
  )
  const [flowEdges, setFlowEdges, onEdgesChange] = useEdgesState<Edge>(
    flowModel.edges
  )

  useEffect(() => {
    setFlowNodes((currentNodes) => {
      const positions = new Map(
        currentNodes.map((node) => [node.id, node.position])
      )
      return flowModel.nodes.map((node) => ({
        ...node,
        position: positions.get(node.id) ?? node.position,
      }))
    })
    setFlowEdges(flowModel.edges)
  }, [flowModel.edges, flowModel.nodes, setFlowEdges, setFlowNodes])

  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return
    const media = window.matchMedia('(max-width: 820px)')
    const updateViewportMode = () => setIsCompactViewport(media.matches)
    media.addEventListener('change', updateViewportMode)
    return () => media.removeEventListener('change', updateViewportMode)
  }, [])

  useEffect(() => {
    function handleKeyboard(event: KeyboardEvent) {
      const target = event.target as HTMLElement | null
      if (
        target?.closest('input, textarea, select, [contenteditable="true"]')
      ) {
        return
      }
      if (event.key === 'Escape') {
        setContextMenu(null)
        setShowShortcutHelp(false)
        setInspectorOpen(false)
        if (draftAfterTaskID !== undefined) cancelDraftTask()
        return
      }
      if (cancelTaskID || draftAfterTaskID !== undefined) return
      if (event.key === 'Tab' && selectedNodeID === rootNodeID) {
        event.preventDefault()
        beginDraftTask()
      } else if (event.key === 'Enter' && selectedTask) {
        event.preventDefault()
        beginDraftTask(selectedTask.id)
      } else if (event.key === ' ' && selectedTask) {
        event.preventDefault()
        setEditRequests((current) => ({
          ...current,
          [selectedTask.id]: (current[selectedTask.id] ?? 0) + 1,
        }))
      } else if ((event.metaKey || event.ctrlKey) && event.key === '/') {
        event.preventDefault()
        toggleBranch()
      } else if (event.key === '?') {
        setShowShortcutHelp((current) => !current)
      }
    }

    window.addEventListener('keydown', handleKeyboard)
    return () => window.removeEventListener('keydown', handleKeyboard)
  }, [
    beginDraftTask,
    cancelDraftTask,
    cancelTaskID,
    draftAfterTaskID,
    rootNodeID,
    selectedNodeID,
    selectedTask,
    toggleBranch,
  ])

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
          { x: -20, y: 28, zoom: 0.62 },
          { duration: 240 }
        )
        return
      }
      void flowInstance?.fitView({ padding: 0.2, duration: 240 })
    })
  }

  function requestRename(taskID: string) {
    setContextMenu(null)
    setSelectedNodeID(taskID)
    setEditRequests((current) => ({
      ...current,
      [taskID]: (current[taskID] ?? 0) + 1,
    }))
  }

  async function changeSelectedOccurrenceStatus(
    change: RoadmapExecutionStatusChange
  ) {
    if (!selectedTask) return
    const occurrence = preferredOccurrence(selectedTaskOccurrences)
    if (!occurrence || occurrence.execution_status === change.status) return

    const variables = occurrenceCommandVariables(selectedTask, occurrence)
    setInteractionError('')
    try {
      if (change.status === 'active') {
        if (occurrence.execution_status === 'open') {
          await startOccurrence.mutateAsync(variables)
          return
        }
        if (occurrence.execution_status === 'blocked') {
          await unblockOccurrence.mutateAsync(variables)
          return
        }
      }
      if (
        change.status === 'blocked' &&
        occurrence.execution_status === 'active'
      ) {
        await blockOccurrence.mutateAsync({
          ...variables,
          blockedReason: change.blockedReason ?? '',
          nextAction: change.nextAction ?? '',
        })
        return
      }
      if (change.status === 'done') {
        await completeOccurrence.mutateAsync(variables)
        return
      }
      if (change.status === 'skipped') {
        await skipOccurrence.mutateAsync(variables)
        return
      }
      if (change.status === 'cancelled') {
        await cancelOccurrence.mutateAsync(variables)
        return
      }
      if (change.status === 'open') {
        await reopenOccurrence.mutateAsync(variables)
        return
      }
      throw new Error('当前状态不能执行这个操作。')
    } catch (caught) {
      setInteractionError(
        caught instanceof Error ? caught.message : '状态更新失败，请稍后重试。'
      )
      throw caught
    }
  }

  async function confirmCancelTask() {
    const task = nodeTasks.find((candidate) => candidate.id === cancelTaskID)
    if (!task) return
    const taskOccurrences = occurrencesByTask.get(task.id) ?? []
    setInteractionError('')
    try {
      await cancelTask.mutateAsync({
        projectID,
        taskID: task.id,
        expectedRevisions: {
          expected_task_revision: task.revision,
          expected_schedule_revision: task.schedule_revision,
          expected_occurrence_revisions: Object.fromEntries(
            taskOccurrences.map((occurrence) => [
              occurrence.id,
              occurrence.revision,
            ])
          ),
        },
      })
      setCancelTaskID('')
      setSelectedNodeID('')
      setInspectorOpen(false)
    } catch (caught) {
      setInteractionError(
        caught instanceof Error ? caught.message : '取消任务失败，请稍后重试。'
      )
    }
  }

  return (
    <section className="mindmap-page">
      <header className="mindmap-page-header">
        <div className="mindmap-title-group">
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
          <div>
            <h1>{roadmapNode.title}</h1>
            <span>{progress}% 完成</span>
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
            onClick={() => beginDraftTask(selectedTask?.id)}
          >
            <Plus aria-hidden="true" />
            添加任务
          </button>
          <button
            className="mindmap-icon-action"
            type="button"
            aria-label="查看脑图快捷键"
            aria-expanded={showShortcutHelp}
            onClick={() => setShowShortcutHelp((current) => !current)}
          >
            <CircleHelp aria-hidden="true" />
          </button>
        </div>
      </header>

      <div className="mindmap-filter-bar" aria-label="按状态筛选任务">
        {filterOptions.map(({ value, label, icon: Icon }) => {
          const count =
            value === 'all'
              ? nodeTasks.length
              : nodeTasks.filter((task) => statusByTask.get(task.id) === value)
                  .length
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
                setSelectedNodeID('')
                setInspectorOpen(false)
                setDraftAfterTaskID(undefined)
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
        <main
          className="mindmap-canvas"
          aria-label={`${roadmapNode.title} 任务脑图`}
        >
          <ReactFlow
            nodes={flowNodes}
            edges={flowEdges}
            nodeTypes={nodeTypes}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            onInit={setFlowInstance}
            onMove={(_event, viewport) =>
              setZoomPercent(Math.round(viewport.zoom * 100))
            }
            onNodeClick={(_event, node) => {
              setContextMenu(null)
              setSelectedNodeID(node.id)
              if (
                node.type === 'roadmapTask' &&
                node.id !== 'mindmap-draft-task'
              ) {
                setInspectorOpen(true)
              } else {
                setInspectorOpen(false)
              }
            }}
            onNodeContextMenu={(event, node) => {
              event.preventDefault()
              if (
                node.type !== 'roadmapTask' ||
                node.id === 'mindmap-draft-task'
              )
                return
              setSelectedNodeID(node.id)
              setInspectorOpen(false)
              setContextMenu({
                taskID: node.id,
                x: Math.min(event.clientX, window.innerWidth - 228),
                y: Math.min(event.clientY, window.innerHeight - 220),
              })
            }}
            onPaneClick={() => {
              setContextMenu(null)
              setSelectedNodeID('')
              setInspectorOpen(false)
            }}
            nodesConnectable={false}
            elementsSelectable
            zoomOnDoubleClick={false}
            fitView={!isCompactViewport}
            fitViewOptions={{ padding: 0.2 }}
            defaultViewport={
              isCompactViewport ? { x: -20, y: 28, zoom: 0.62 } : undefined
            }
            minZoom={0.32}
            maxZoom={1.8}
          >
            <Background gap={24} size={1} color="#ded5c8" />
            <MiniMap
              className="mindmap-minimap"
              pannable
              zoomable
              nodeStrokeWidth={2}
              nodeColor={(node) =>
                node.type === 'roadmapRoot' ? '#b87333' : '#fffdf8'
              }
            />
          </ReactFlow>

          {!branchCollapsed &&
          visibleTasks.length === 0 &&
          draftAfterTaskID === undefined ? (
            <div className="mindmap-canvas-empty">
              <GitBranch aria-hidden="true" />
              <strong>
                {nodeTasks.length === 0
                  ? '从第一个任务开始展开'
                  : '当前筛选下没有任务'}
              </strong>
              <p>
                {nodeTasks.length === 0
                  ? '选择中心主题后按 Tab，或者直接添加任务。'
                  : '切换状态筛选，或者查看全部任务。'}
              </p>
              {nodeTasks.length === 0 ? (
                <button
                  className="plan-primary-action"
                  type="button"
                  onClick={() => beginDraftTask()}
                >
                  <Plus aria-hidden="true" />
                  添加第一个任务
                </button>
              ) : (
                <button type="button" onClick={() => setStatusFilter('all')}>
                  查看全部任务
                </button>
              )}
            </div>
          ) : null}

          <div className="mindmap-zoom-controls" aria-label="脑图缩放">
            <button
              type="button"
              aria-label="缩小"
              onClick={() => void flowInstance?.zoomOut({ duration: 160 })}
            >
              <Minus aria-hidden="true" />
            </button>
            <output>{zoomPercent}%</output>
            <button
              type="button"
              aria-label="放大"
              onClick={() => void flowInstance?.zoomIn({ duration: 160 })}
            >
              <Plus aria-hidden="true" />
            </button>
            <button type="button" aria-label="适应画布" onClick={resetLayout}>
              <Maximize2 aria-hidden="true" />
            </button>
          </div>

          <div className="mindmap-shortcut-strip" aria-hidden="true">
            <span>
              <kbd>Tab</kbd> 添加任务
            </span>
            <span>
              <kbd>Enter</kbd> 同级任务
            </span>
            <span>
              <kbd>Space</kbd> 编辑标题
            </span>
            <span>滚轮缩放 · 拖动画布</span>
          </div>

          {showShortcutHelp ? (
            <aside className="mindmap-shortcut-help" aria-label="脑图快捷键">
              <header>
                <strong>脑图快捷键</strong>
                <button
                  type="button"
                  aria-label="关闭快捷键说明"
                  onClick={() => setShowShortcutHelp(false)}
                >
                  <X aria-hidden="true" />
                </button>
              </header>
              <dl>
                <div>
                  <dt>Tab</dt>
                  <dd>为中心主题添加任务</dd>
                </div>
                <div>
                  <dt>Enter</dt>
                  <dd>新建同级任务</dd>
                </div>
                <div>
                  <dt>Space / 双击</dt>
                  <dd>编辑任务标题</dd>
                </div>
                <div>
                  <dt>⌘ / Ctrl + /</dt>
                  <dd>折叠或展开分支</dd>
                </div>
                <div>
                  <dt>Esc</dt>
                  <dd>退出当前操作</dd>
                </div>
              </dl>
            </aside>
          ) : null}

          {interactionError ? (
            <div className="mindmap-interaction-error" role="alert">
              {interactionError}
              <button
                type="button"
                aria-label="关闭错误提示"
                onClick={() => setInteractionError('')}
              >
                <X aria-hidden="true" />
              </button>
            </div>
          ) : null}
        </main>

        {inspectorOpen && selectedTask ? (
          <RoadmapTaskInspector
            task={selectedTask}
            status={statusByTask.get(selectedTask.id)}
            occurrence={preferredOccurrence(selectedTaskOccurrences)}
            roadmapNodeTitle={roadmapNode.title}
            onClose={() => {
              setInspectorOpen(false)
              setSelectedNodeID('')
            }}
            onRename={() => requestRename(selectedTask.id)}
            onAddSibling={() => beginDraftTask(selectedTask.id)}
            onCancel={() => setCancelTaskID(selectedTask.id)}
            isCompleting={completeOccurrence.isPending}
            isStatusChanging={statusCommandBusy}
            onStatusChange={changeSelectedOccurrenceStatus}
            onComplete={async () => {
              try {
                await changeSelectedOccurrenceStatus({
                  status: 'done',
                })
              } catch {
                // The shared status handler renders the visible error.
              }
            }}
          />
        ) : null}
      </div>

      {contextMenu ? (
        <div
          className="mindmap-context-menu"
          role="menu"
          style={{ left: contextMenu.x, top: contextMenu.y }}
        >
          <button
            type="button"
            role="menuitem"
            onClick={() => requestRename(contextMenu.taskID)}
          >
            <Pencil aria-hidden="true" />
            <span>编辑标题</span>
            <kbd>Space</kbd>
          </button>
          <button
            type="button"
            role="menuitem"
            onClick={() => beginDraftTask(contextMenu.taskID)}
          >
            <Plus aria-hidden="true" />
            <span>新建同级任务</span>
            <kbd>Enter</kbd>
          </button>
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              setContextMenu(null)
              setSelectedNodeID(contextMenu.taskID)
              setInspectorOpen(true)
            }}
          >
            <PanelRight aria-hidden="true" />
            <span>打开任务详情</span>
          </button>
          <hr />
          <button
            className="is-danger"
            type="button"
            role="menuitem"
            onClick={() => {
              setCancelTaskID(contextMenu.taskID)
              setContextMenu(null)
            }}
          >
            <Trash2 aria-hidden="true" />
            <span>取消任务</span>
          </button>
        </div>
      ) : null}

      {cancelTaskID ? (
        <div className="mindmap-cancel-overlay">
          <section
            role="dialog"
            aria-modal="true"
            aria-labelledby="mindmap-cancel-title"
          >
            <header>
              <span>
                <Trash2 aria-hidden="true" />
              </span>
              <div>
                <h2 id="mindmap-cancel-title">取消这个任务？</h2>
                <p>任务及其执行记录会标记为已取消，不会从历史中删除。</p>
              </div>
            </header>
            <div>
              <button
                type="button"
                disabled={cancelTask.isPending}
                onClick={() => setCancelTaskID('')}
              >
                保留任务
              </button>
              <button
                className="is-danger"
                type="button"
                disabled={cancelTask.isPending}
                onClick={() => void confirmCancelTask()}
              >
                {cancelTask.isPending ? '正在取消…' : '确认取消'}
              </button>
            </div>
          </section>
        </div>
      ) : null}
    </section>
  )
}
