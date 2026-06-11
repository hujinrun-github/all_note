import { type ComponentType, type FormEvent, useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Background,
  Controls,
  Handle,
  MarkerType,
  MiniMap,
  Position,
  ReactFlow,
  type Edge,
  type Node,
  type NodeProps,
  useEdgesState,
  useNodesState,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import {
  addRoadmapNodeResource,
  createTask,
  createTaskProject,
  generateLearningRoadmap,
  getLearningRoadmap,
  getTasks,
  listTaskProjects,
  saveRoadmapLayout,
  searchRoadmapNodeResources,
  updateTask,
  type LearningRoadmap,
  type RoadmapNode,
  type RoadmapResource,
  type Task,
  type TaskProject,
} from '../api/tasks'
import { TaskRow } from '../components/ui/TaskRow'
import { dateInputToUnix, todayDateInputValue } from '../utils/taskForm'

type TaskTab = 'week' | 'long' | 'roadmap'

interface RoadmapNodeData extends Record<string, unknown> {
  node: RoadmapNode
  onOpen: (id: string) => void
}

const tabLabels: Record<TaskTab, string> = {
  week: '本周',
  long: '长期项目',
  roadmap: '学习 Roadmap',
}

const projectTypeLabels: Record<TaskProject['type'], string> = {
  personal: '个人',
  regular: '普通项目',
  learning: '学习项目',
}

const nodeTypeLabels: Record<RoadmapNode['type'], string> = {
  phase: '阶段',
  module: '模块',
  task: '任务',
  choice: '分支',
}

const pathTypeLabels: Record<RoadmapNode['path_type'], string> = {
  required: '主路径',
  recommended: '推荐',
  optional: '可选',
  alternative: '替代',
}

const articleSearchSourceOptions = [
  { id: 'google', label: 'Google/通用' },
  { id: 'medium', label: 'Medium' },
  { id: 'reddit', label: 'Reddit' },
  { id: 'devto', label: 'Dev.to' },
  { id: 'hashnode', label: 'Hashnode' },
  { id: 'stackoverflow', label: 'Stack Overflow' },
  { id: 'github', label: 'GitHub' },
  { id: 'official', label: '官方文档' },
  { id: 'technical', label: '技术博客' },
] as const

const defaultArticleSearchSources = articleSearchSourceOptions.map((source) => source.id)

function resourceCandidateKey(resource: RoadmapResource) {
  return resource.id || resource.url
}

export default function Tasks() {
  const queryClient = useQueryClient()
  const [activeTab, setActiveTab] = useState<TaskTab>('week')
  const [projectName, setProjectName] = useState('')
  const [projectType, setProjectType] = useState<TaskProject['type']>('regular')
  const [projectDescription, setProjectDescription] = useState('')
  const [weekTitle, setWeekTitle] = useState('')
  const [weekDate, setWeekDate] = useState(() => todayDateInputValue())
  const [weekProjectID, setWeekProjectID] = useState('personal')
  const [longTitle, setLongTitle] = useState('')
  const [longProjectID, setLongProjectID] = useState('')
  const [selectedLearningProjectID, setSelectedLearningProjectID] = useState('')
  const [selectedNodeID, setSelectedNodeID] = useState('')
  const [manualResourceTitle, setManualResourceTitle] = useState('')
  const [manualResourceURL, setManualResourceURL] = useState('')
  const [resourcePickerNodeID, setResourcePickerNodeID] = useState('')
  const [resourceCandidates, setResourceCandidates] = useState<RoadmapResource[]>([])
  const [selectedResourceCandidateIDs, setSelectedResourceCandidateIDs] = useState<string[]>([])
  const [articleSearchSources, setArticleSearchSources] = useState<string[]>(defaultArticleSearchSources)

  const projectsQuery = useQuery({
    queryKey: ['task-projects'],
    queryFn: listTaskProjects,
  })

  const projects = projectsQuery.data ?? []
  const personalProject = projects.find((project) => project.type === 'personal') ?? projects[0]
  const regularProjects = projects.filter((project) => project.type === 'regular')
  const learningProjects = projects.filter((project) => project.type === 'learning')
  const weekProjects = projects.filter((project) => project.type !== 'learning')
  const selectedLearningProject = learningProjects.find((project) => project.id === selectedLearningProjectID)

  useEffect(() => {
    if (!projects.length) return
    if (!projects.some((project) => project.id === weekProjectID)) {
      setWeekProjectID(personalProject?.id ?? projects[0].id)
    }
    if (!longProjectID && regularProjects[0]) {
      setLongProjectID(regularProjects[0].id)
    }
    if (!selectedLearningProjectID && learningProjects[0]) {
      setSelectedLearningProjectID(learningProjects[0].id)
    }
  }, [learningProjects, longProjectID, personalProject?.id, projects, regularProjects, selectedLearningProjectID, weekProjectID])

  const weekTasksQuery = useQuery({
    queryKey: ['tasks', 'week'],
    queryFn: () => getTasks({ horizon: 'week', status: 'all', page_size: 300 }),
  })

  const longTasksQuery = useQuery({
    queryKey: ['tasks', 'long'],
    queryFn: () => getTasks({ horizon: 'long', status: 'all', page_size: 300 }),
  })

  const roadmapQuery = useQuery({
    queryKey: ['learning-roadmap', selectedLearningProjectID],
    queryFn: () => getLearningRoadmap(selectedLearningProjectID),
    enabled: Boolean(selectedLearningProjectID),
  })

  const createProjectMutation = useMutation({
    mutationFn: createTaskProject,
    onSuccess: (project) => {
      queryClient.invalidateQueries({ queryKey: ['task-projects'] })
      setProjectName('')
      setProjectDescription('')
      if (project.type === 'learning') {
        setSelectedLearningProjectID(project.id)
        setActiveTab('roadmap')
      } else if (project.type === 'regular') {
        setLongProjectID(project.id)
        setActiveTab('long')
      }
    },
  })

  const createTaskMutation = useMutation({
    mutationFn: createTask,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['tasks'] })
      queryClient.invalidateQueries({ queryKey: ['learning-roadmap'] })
    },
  })

  const updateTaskMutation = useMutation({
    mutationFn: ({ id, done }: { id: string; done: number }) => updateTask(id, { done }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['tasks'] })
      queryClient.invalidateQueries({ queryKey: ['learning-roadmap'] })
    },
  })

  const generateRoadmapMutation = useMutation({
    mutationFn: generateLearningRoadmap,
    onSuccess: (roadmap) => {
      setSelectedNodeID(roadmap.nodes[0]?.id ?? '')
      queryClient.invalidateQueries({ queryKey: ['learning-roadmap', selectedLearningProjectID] })
    },
  })

  const searchResourcesMutation = useMutation({
    mutationFn: async ({ nodeID, sources }: { nodeID: string; sources: string[] }) => ({
      nodeID,
      resources: await searchRoadmapNodeResources(nodeID, { sources }),
    }),
    onSuccess: ({ nodeID, resources }) => {
      setResourcePickerNodeID(nodeID)
      setResourceCandidates(resources)
      setSelectedResourceCandidateIDs(resources.map(resourceCandidateKey))
    },
  })

  const addResourceMutation = useMutation({
    mutationFn: ({ nodeID, title, url, summary }: { nodeID: string; title: string; url: string; summary?: string }) =>
      addRoadmapNodeResource(nodeID, { title, url, summary }),
    onSuccess: () => {
      setManualResourceTitle('')
      setManualResourceURL('')
      queryClient.invalidateQueries({ queryKey: ['learning-roadmap', selectedLearningProjectID] })
    },
  })

  const addSelectedResourcesMutation = useMutation({
    mutationFn: async () => {
      if (!resourcePickerNodeID) return
      const selectedResources = resourceCandidates.filter((resource) =>
        selectedResourceCandidateIDs.includes(resourceCandidateKey(resource)),
      )
      await Promise.all(
        selectedResources.map((resource) =>
          addRoadmapNodeResource(resourcePickerNodeID, {
            title: resource.title,
            url: resource.url,
            summary: resource.summary,
          }),
        ),
      )
    },
    onSuccess: () => {
      setResourcePickerNodeID('')
      setResourceCandidates([])
      setSelectedResourceCandidateIDs([])
      queryClient.invalidateQueries({ queryKey: ['learning-roadmap', selectedLearningProjectID] })
    },
  })

  async function handleCreateProject(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const name = projectName.trim()
    if (!name) return
    await createProjectMutation.mutateAsync({
      name,
      type: projectType,
      description: projectDescription.trim(),
    })
  }

  async function handleAddWeekTask(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const title = weekTitle.trim()
    if (!title) return
    await createTaskMutation.mutateAsync({
      title,
      project_id: weekProjectID || 'personal',
      planned_date: weekDate,
      due: dateInputToUnix(weekDate),
      horizon: 'week',
      scope: 'daily',
    })
    setWeekTitle('')
  }

  async function handleAddLongTask(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const title = longTitle.trim()
    if (!title || !longProjectID) return
    await createTaskMutation.mutateAsync({
      title,
      project_id: longProjectID,
      horizon: 'long',
      scope: 'yearly',
    })
    setLongTitle('')
  }

  async function handleToggleTask(task: Task) {
    await updateTaskMutation.mutateAsync({ id: task.id, done: task.done ? 0 : 1 })
  }

  function handleToggleResourceCandidate(candidateID: string) {
    setSelectedResourceCandidateIDs((current) =>
      current.includes(candidateID) ? current.filter((id) => id !== candidateID) : [...current, candidateID],
    )
  }

  function handleCloseResourcePicker() {
    setResourcePickerNodeID('')
    setResourceCandidates([])
    setSelectedResourceCandidateIDs([])
  }

  function handleToggleArticleSearchSource(sourceID: string) {
    setArticleSearchSources((current) =>
      current.includes(sourceID) ? current.filter((id) => id !== sourceID) : [...current, sourceID],
    )
  }

  const roadmap = roadmapQuery.data
  const resourcePickerNode = roadmap?.nodes.find((node) => node.id === resourcePickerNodeID)

  return (
    <div className="task-workspace">
      <aside className="task-project-panel">
        <div className="filter-title">项目</div>
        <form className="project-create-card" onSubmit={handleCreateProject}>
          <label>
            <span>项目名称</span>
            <input
              aria-label="项目名称"
              value={projectName}
              onChange={(event) => setProjectName(event.target.value)}
              placeholder="新的项目"
            />
          </label>
          <label>
            <span>项目类型</span>
            <select
              aria-label="项目类型"
              value={projectType}
              onChange={(event) => setProjectType(event.target.value as TaskProject['type'])}
            >
              <option value="regular">普通项目</option>
              <option value="learning">学习项目</option>
            </select>
          </label>
          <label>
            <span>说明</span>
            <textarea
              value={projectDescription}
              onChange={(event) => setProjectDescription(event.target.value)}
              placeholder="目标、背景或交付物"
            />
          </label>
          <button type="submit" disabled={!projectName.trim() || createProjectMutation.isPending}>
            新增项目
          </button>
        </form>

        <div className="task-project-list">
          {projects.map((project) => (
            <button
              key={project.id}
              type="button"
              className={
                project.id === weekProjectID || project.id === longProjectID || project.id === selectedLearningProjectID
                  ? 'is-active'
                  : ''
              }
              onClick={() => {
                if (project.type === 'learning') {
                  setSelectedLearningProjectID(project.id)
                  setActiveTab('roadmap')
                } else if (project.type === 'regular') {
                  setLongProjectID(project.id)
                  setActiveTab('long')
                } else {
                  setWeekProjectID(project.id)
                  setActiveTab('week')
                }
              }}
            >
              <span>{project.name}</span>
              <small>{projectTypeLabels[project.type]}</small>
            </button>
          ))}
        </div>
      </aside>

      <section className="task-main-panel">
        <div className="panel-heading">
          <div>
            <span>任务工作台</span>
            <h2>短期推进、长期项目和学习路线</h2>
          </div>
          <div className="segmented-tabs" role="tablist" aria-label="任务视图">
            {(Object.keys(tabLabels) as TaskTab[]).map((tab) => (
              <button
                key={tab}
                type="button"
                role="tab"
                aria-selected={activeTab === tab}
                className={activeTab === tab ? 'is-active' : ''}
                onClick={() => setActiveTab(tab)}
              >
                {tabLabels[tab]}
              </button>
            ))}
          </div>
        </div>

        {activeTab === 'week' && (
          <WeekTaskView
            tasks={weekTasksQuery.data?.tasks ?? []}
            projects={weekProjects.length ? weekProjects : projects}
            selectedProjectID={weekProjectID}
            title={weekTitle}
            date={weekDate}
            isPending={createTaskMutation.isPending}
            onProjectChange={setWeekProjectID}
            onTitleChange={setWeekTitle}
            onDateChange={setWeekDate}
            onSubmit={handleAddWeekTask}
            onToggle={handleToggleTask}
          />
        )}

        {activeTab === 'long' && (
          <LongTaskView
            tasks={longTasksQuery.data?.tasks ?? []}
            projects={regularProjects}
            selectedProjectID={longProjectID}
            title={longTitle}
            isPending={createTaskMutation.isPending}
            onProjectChange={setLongProjectID}
            onTitleChange={setLongTitle}
            onSubmit={handleAddLongTask}
            onToggle={handleToggleTask}
          />
        )}

        {activeTab === 'roadmap' && (
          <RoadmapTaskView
            projects={learningProjects}
            selectedProjectID={selectedLearningProjectID}
            selectedProject={selectedLearningProject}
            roadmap={roadmap}
            isLoading={roadmapQuery.isLoading}
            selectedNodeID={selectedNodeID}
            manualResourceTitle={manualResourceTitle}
            manualResourceURL={manualResourceURL}
            articleSearchSources={articleSearchSources}
            isGenerating={generateRoadmapMutation.isPending}
            isSearching={searchResourcesMutation.isPending}
            isAddingResource={addResourceMutation.isPending}
            onSelectProject={setSelectedLearningProjectID}
            onSelectNode={setSelectedNodeID}
            onGenerate={() => selectedLearningProjectID && generateRoadmapMutation.mutate(selectedLearningProjectID)}
            onSearchResources={(nodeID) => searchResourcesMutation.mutate({ nodeID, sources: articleSearchSources })}
            onToggleArticleSearchSource={handleToggleArticleSearchSource}
            onManualTitleChange={setManualResourceTitle}
            onManualURLChange={setManualResourceURL}
            onAddResource={(nodeID) => {
              if (!manualResourceTitle.trim() || !manualResourceURL.trim()) return
              addResourceMutation.mutate({ nodeID, title: manualResourceTitle.trim(), url: manualResourceURL.trim() })
            }}
            onAddNodeToWeek={async (node) => {
              if (!selectedLearningProjectID) return
              await createTaskMutation.mutateAsync({
                title: node.title,
                project_id: selectedLearningProjectID,
                roadmap_node_id: node.id,
                planned_date: todayDateInputValue(),
                due: dateInputToUnix(todayDateInputValue()),
                horizon: 'week',
                scope: 'daily',
              })
              setActiveTab('week')
            }}
          />
        )}
      </section>

      <RoadmapResourcePickerDialog
        nodeTitle={resourcePickerNode?.title ?? ''}
        candidates={resourceCandidates}
        selectedIDs={selectedResourceCandidateIDs}
        isSaving={addSelectedResourcesMutation.isPending}
        onToggle={handleToggleResourceCandidate}
        onCancel={handleCloseResourcePicker}
        onConfirm={() => addSelectedResourcesMutation.mutate()}
      />
    </div>
  )
}

function WeekTaskView({
  tasks,
  projects,
  selectedProjectID,
  title,
  date,
  isPending,
  onProjectChange,
  onTitleChange,
  onDateChange,
  onSubmit,
  onToggle,
}: {
  tasks: Task[]
  projects: TaskProject[]
  selectedProjectID: string
  title: string
  date: string
  isPending: boolean
  onProjectChange: (value: string) => void
  onTitleChange: (value: string) => void
  onDateChange: (value: string) => void
  onSubmit: (event: FormEvent<HTMLFormElement>) => void
  onToggle: (task: Task) => void
}) {
  const tasksByDate = useMemo(() => {
    const grouped = new Map<string, Task[]>()
    for (const task of tasks) {
      const key = task.planned_date || '未安排'
      grouped.set(key, [...(grouped.get(key) ?? []), task])
    }
    return [...grouped.entries()].sort(([a], [b]) => a.localeCompare(b))
  }, [tasks])

  return (
    <div className="task-tab-panel">
      <form className="inline-create task-create-form" onSubmit={onSubmit}>
        <input
          className="task-title-input"
          aria-label="任务内容"
          value={title}
          onChange={(event) => onTitleChange(event.target.value)}
          placeholder="添加本周任务"
        />
        <select aria-label="任务项目" value={selectedProjectID} onChange={(event) => onProjectChange(event.target.value)}>
          {projects.map((project) => (
            <option key={project.id} value={project.id}>
              {project.name}
            </option>
          ))}
        </select>
        <input aria-label="任务日期" type="date" value={date} onChange={(event) => onDateChange(event.target.value)} />
        <button type="submit" disabled={!title.trim() || isPending}>
          添加任务
        </button>
      </form>

      {tasksByDate.length === 0 ? (
        <p className="empty-copy">本周还没有任务</p>
      ) : (
        tasksByDate.map(([plannedDate, dayTasks]) => (
          <div key={plannedDate} className="task-section">
            <span>{plannedDate}</span>
            <div className="row-stack">
              {dayTasks.map((task) => (
                <TaskRow key={task.id} task={task} onToggle={() => onToggle(task)} />
              ))}
            </div>
          </div>
        ))
      )}
    </div>
  )
}

function LongTaskView({
  tasks,
  projects,
  selectedProjectID,
  title,
  isPending,
  onProjectChange,
  onTitleChange,
  onSubmit,
  onToggle,
}: {
  tasks: Task[]
  projects: TaskProject[]
  selectedProjectID: string
  title: string
  isPending: boolean
  onProjectChange: (value: string) => void
  onTitleChange: (value: string) => void
  onSubmit: (event: FormEvent<HTMLFormElement>) => void
  onToggle: (task: Task) => void
}) {
  const tasksByProject = useMemo(() => {
    const grouped = new Map<string, Task[]>()
    for (const task of tasks) {
      const key = task.project || '未命名项目'
      grouped.set(key, [...(grouped.get(key) ?? []), task])
    }
    return [...grouped.entries()]
  }, [tasks])

  return (
    <div className="task-tab-panel">
      <form className="inline-create task-create-form" onSubmit={onSubmit}>
        <input
          className="task-title-input"
          aria-label="长期任务内容"
          value={title}
          onChange={(event) => onTitleChange(event.target.value)}
          placeholder="添加长期任务"
        />
        <select aria-label="长期项目" value={selectedProjectID} onChange={(event) => onProjectChange(event.target.value)}>
          {projects.map((project) => (
            <option key={project.id} value={project.id}>
              {project.name}
            </option>
          ))}
        </select>
        <button type="submit" disabled={!title.trim() || !selectedProjectID || isPending}>
          添加长期任务
        </button>
      </form>

      {projects.length === 0 ? (
        <p className="empty-copy">先在左侧新增一个普通项目</p>
      ) : tasksByProject.length === 0 ? (
        <p className="empty-copy">长期项目还没有任务</p>
      ) : (
        tasksByProject.map(([project, projectTasks]) => (
          <div key={project} className="task-section">
            <span>{project}</span>
            <div className="row-stack">
              {projectTasks.map((task) => (
                <TaskRow key={task.id} task={task} onToggle={() => onToggle(task)} />
              ))}
            </div>
          </div>
        ))
      )}
    </div>
  )
}

function RoadmapTaskView({
  projects,
  selectedProjectID,
  selectedProject,
  roadmap,
  isLoading,
  selectedNodeID,
  manualResourceTitle,
  manualResourceURL,
  articleSearchSources,
  isGenerating,
  isSearching,
  isAddingResource,
  onSelectProject,
  onSelectNode,
  onGenerate,
  onSearchResources,
  onToggleArticleSearchSource,
  onManualTitleChange,
  onManualURLChange,
  onAddResource,
  onAddNodeToWeek,
}: {
  projects: TaskProject[]
  selectedProjectID: string
  selectedProject?: TaskProject
  roadmap: LearningRoadmap | null | undefined
  isLoading: boolean
  selectedNodeID: string
  manualResourceTitle: string
  manualResourceURL: string
  articleSearchSources: string[]
  isGenerating: boolean
  isSearching: boolean
  isAddingResource: boolean
  onSelectProject: (value: string) => void
  onSelectNode: (value: string) => void
  onGenerate: () => void
  onSearchResources: (nodeID: string) => void
  onToggleArticleSearchSource: (sourceID: string) => void
  onManualTitleChange: (value: string) => void
  onManualURLChange: (value: string) => void
  onAddResource: (nodeID: string) => void
  onAddNodeToWeek: (node: RoadmapNode) => Promise<void>
}) {
  const selectedNode = roadmap?.nodes.find((node) => node.id === selectedNodeID) ?? roadmap?.nodes[0]

  return (
    <div className="roadmap-workspace">
      <div className="roadmap-toolbar">
        <label>
          <span>学习项目</span>
          <select
            aria-label="学习项目"
            value={selectedProjectID}
            onChange={(event) => {
              onSelectProject(event.target.value)
              onSelectNode('')
            }}
          >
            <option value="">选择学习项目</option>
            {projects.map((project) => (
              <option key={project.id} value={project.id}>
                {project.name}
              </option>
            ))}
          </select>
        </label>
        <button type="button" onClick={onGenerate} disabled={!selectedProjectID || isGenerating}>
          {isGenerating ? '生成中...' : '生成 Roadmap'}
        </button>
        {selectedProject && <span className="roadmap-goal">{selectedProject.description || selectedProject.name}</span>}
      </div>

      {projects.length === 0 ? (
        <p className="empty-copy">先在左侧新增一个学习项目</p>
      ) : isLoading ? (
        <p className="empty-copy">正在加载 Roadmap</p>
      ) : roadmap ? (
        <div className="roadmap-content">
          <RoadmapCanvas roadmap={roadmap} selectedNodeID={selectedNode?.id ?? ''} onSelectNode={onSelectNode} />
          <RoadmapInspector
            node={selectedNode}
            manualTitle={manualResourceTitle}
            manualURL={manualResourceURL}
            articleSearchSources={articleSearchSources}
            isSearching={isSearching}
            isAddingResource={isAddingResource}
            onSearchResources={onSearchResources}
            onToggleArticleSearchSource={onToggleArticleSearchSource}
            onManualTitleChange={onManualTitleChange}
            onManualURLChange={onManualURLChange}
            onAddResource={onAddResource}
            onAddNodeToWeek={onAddNodeToWeek}
          />
        </div>
      ) : (
        <p className="empty-copy">这个学习项目还没有 Roadmap</p>
      )}
    </div>
  )
}

function RoadmapCanvas({
  roadmap,
  selectedNodeID,
  onSelectNode,
}: {
  roadmap: LearningRoadmap
  selectedNodeID: string
  onSelectNode: (id: string) => void
}) {
  const [nodes, setNodes, onNodesChange] = useNodesState<Node<RoadmapNodeData>>([])
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([])
  const [isSaving, setIsSaving] = useState(false)
  const nodeTypes = useMemo(() => ({ roadmapNode: RoadmapFlowNode as ComponentType<NodeProps> }), [])

  useEffect(() => {
    setNodes(
      roadmap.nodes.map((node) => ({
        id: node.id,
        type: 'roadmapNode',
        position: { x: node.x, y: node.y },
        data: { node, onOpen: onSelectNode },
        selected: node.id === selectedNodeID,
      })),
    )
    setEdges(
      roadmap.edges.map((edge) => ({
        id: edge.id,
        source: edge.source_node_id,
        target: edge.target_node_id,
        type: 'smoothstep',
        animated: edge.style === 'dotted',
        markerEnd: { type: MarkerType.ArrowClosed, color: '#2f80ed' },
        style: {
          stroke: '#2f80ed',
          strokeWidth: 2.4,
          strokeDasharray: edge.style === 'dotted' ? '3 7' : undefined,
        },
      })),
    )
  }, [onSelectNode, roadmap.edges, roadmap.nodes, selectedNodeID, setEdges, setNodes])

  async function handleSaveLayout() {
    setIsSaving(true)
    try {
      await saveRoadmapLayout(
        roadmap.id,
        nodes.map((node) => ({ id: node.id, x: node.position.x, y: node.position.y })),
      )
    } finally {
      setIsSaving(false)
    }
  }

  return (
    <div className="roadmap-canvas-shell" data-testid="roadmap-canvas">
      <div className="roadmap-legend">
        <span>
          <i className="status-dot status-active" /> 当前学习
        </span>
        <span>
          <i className="status-dot status-done" /> 已完成
        </span>
        <span>
          <i className="status-dot status-todo" /> 未开始
        </span>
      </div>
      <button className="roadmap-save-layout" type="button" onClick={handleSaveLayout} disabled={isSaving}>
        {isSaving ? '保存中' : '保存布局'}
      </button>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        fitView
        minZoom={0.35}
        maxZoom={1.4}
      >
        <Background gap={26} color="#d7e4ff" />
        <MiniMap pannable zoomable />
        <Controls />
      </ReactFlow>
    </div>
  )
}

function RoadmapFlowNode(props: NodeProps) {
  const { node, onOpen } = props.data as RoadmapNodeData

  return (
    <div
      role="button"
      tabIndex={0}
      data-testid="roadmap-node"
      className={`roadmap-node roadmap-node-${node.path_type} roadmap-node-status-${node.status}`}
      onClick={() => onOpen(node.id)}
      onKeyDown={(event) => {
        if (event.key === 'Enter' || event.key === ' ') onOpen(node.id)
      }}
    >
      <Handle type="target" position={Position.Top} />
      <Handle type="target" position={Position.Left} />
      <span className={`status-dot status-${node.status}`} />
      <strong>{node.title}</strong>
      <small>{nodeTypeLabels[node.type]} · {pathTypeLabels[node.path_type]}</small>
      <Handle type="source" position={Position.Bottom} />
      <Handle type="source" position={Position.Right} />
    </div>
  )
}

function RoadmapInspector({
  node,
  manualTitle,
  manualURL,
  articleSearchSources,
  isSearching,
  isAddingResource,
  onSearchResources,
  onToggleArticleSearchSource,
  onManualTitleChange,
  onManualURLChange,
  onAddResource,
  onAddNodeToWeek,
}: {
  node?: RoadmapNode
  manualTitle: string
  manualURL: string
  articleSearchSources: string[]
  isSearching: boolean
  isAddingResource: boolean
  onSearchResources: (nodeID: string) => void
  onToggleArticleSearchSource: (sourceID: string) => void
  onManualTitleChange: (value: string) => void
  onManualURLChange: (value: string) => void
  onAddResource: (nodeID: string) => void
  onAddNodeToWeek: (node: RoadmapNode) => Promise<void>
}) {
  if (!node) {
    return <aside className="roadmap-inspector"><p className="empty-copy">选择一个节点查看详情</p></aside>
  }

  return (
    <aside className="roadmap-inspector">
      <div className="roadmap-inspector-heading">
        <span>{nodeTypeLabels[node.type]} · {pathTypeLabels[node.path_type]}</span>
        <h2>{node.title}</h2>
        <p>{node.description}</p>
      </div>

      <div className="inspector-section">
        <span>交付物</span>
        <p>{node.deliverable || '暂无交付物'}</p>
      </div>
      <div className="inspector-section">
        <span>验收标准</span>
        <p>{node.acceptance_criteria || '暂无验收标准'}</p>
      </div>

      <div className="roadmap-inspector-actions">
        <button type="button" onClick={() => onAddNodeToWeek(node)}>
          加入本周
        </button>
        <button type="button" onClick={() => onSearchResources(node.id)} disabled={isSearching || articleSearchSources.length === 0}>
          {isSearching ? '搜索中...' : '搜索文章'}
        </button>
      </div>

      <fieldset className="roadmap-source-settings" aria-label="搜索源">
        <legend>搜索源</legend>
        <div>
          {articleSearchSourceOptions.map((source) => (
            <label key={source.id}>
              <input
                type="checkbox"
                checked={articleSearchSources.includes(source.id)}
                onChange={() => onToggleArticleSearchSource(source.id)}
              />
              <span>{source.label}</span>
            </label>
          ))}
        </div>
      </fieldset>

      <form
        className="roadmap-resource-form"
        onSubmit={(event) => {
          event.preventDefault()
          onAddResource(node.id)
        }}
      >
        <input
          aria-label="文章标题"
          value={manualTitle}
          onChange={(event) => onManualTitleChange(event.target.value)}
          placeholder="文章标题"
        />
        <input
          aria-label="文章链接"
          value={manualURL}
          onChange={(event) => onManualURLChange(event.target.value)}
          placeholder="https://"
        />
        <button type="submit" disabled={!manualTitle.trim() || !manualURL.trim() || isAddingResource}>
          添加链接
        </button>
      </form>

      <div className="roadmap-resource-list">
        {node.resources.length === 0 ? (
          <p className="empty-copy">暂无文章链接</p>
        ) : (
          node.resources.map((resource) => (
            <a key={resource.id} href={resource.url} target="_blank" rel="noreferrer">
              <strong>{resource.title}</strong>
              {resource.summary && <span>{resource.summary}</span>}
            </a>
          ))
        )}
      </div>
    </aside>
  )
}

function RoadmapResourcePickerDialog({
  nodeTitle,
  candidates,
  selectedIDs,
  isSaving,
  onToggle,
  onCancel,
  onConfirm,
}: {
  nodeTitle: string
  candidates: RoadmapResource[]
  selectedIDs: string[]
  isSaving: boolean
  onToggle: (candidateID: string) => void
  onCancel: () => void
  onConfirm: () => void
}) {
  if (!nodeTitle && candidates.length === 0) return null

  return (
    <div className="roadmap-resource-picker-overlay">
      <section className="roadmap-resource-picker" role="dialog" aria-modal="true" aria-label="选择文章">
        <div className="roadmap-resource-picker-heading">
          <div>
            <span>{nodeTitle}</span>
            <h2>选择要添加的文章</h2>
          </div>
          <button type="button" aria-label="关闭选择文章" onClick={onCancel}>
            ×
          </button>
        </div>

        {candidates.length === 0 ? (
          <p className="empty-copy">没有找到可添加的文章</p>
        ) : (
          <div className="roadmap-resource-candidates">
            {candidates.map((resource) => {
              const candidateID = resourceCandidateKey(resource)
              return (
                <div className="roadmap-resource-candidate" key={candidateID}>
                  <label>
                    <input
                      type="checkbox"
                      checked={selectedIDs.includes(candidateID)}
                      onChange={() => onToggle(candidateID)}
                      aria-label={`选择文章 ${resource.title}`}
                    />
                    <span>
                      <strong>{resource.title}</strong>
                      {resource.summary && <small>{resource.summary}</small>}
                    </span>
                  </label>
                  <a href={resource.url} target="_blank" rel="noreferrer">
                    打开
                  </a>
                </div>
              )
            })}
          </div>
        )}

        <div className="roadmap-resource-picker-actions">
          <button type="button" onClick={onCancel}>
            取消
          </button>
          <button type="button" onClick={onConfirm} disabled={selectedIDs.length === 0 || isSaving}>
            {isSaving ? '添加中...' : '添加选中文章'}
          </button>
        </div>
      </section>
    </div>
  )
}
