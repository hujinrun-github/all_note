import { type ComponentType, type FormEvent, useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
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
  createRoadmapNode,
  createTask,
  createTaskProject,
  deleteRoadmapNode,
  deleteTaskProject,
  generateLearningRoadmap,
  getLearningRoadmap,
  getTasks,
  listTaskProjects,
  optimizeRoadmapLayout,
  saveRoadmapLayout,
  searchRoadmapNodeResources,
  updateTask,
  updateRoadmapNode,
  type LearningRoadmap,
  type RoadmapNode,
  type RoadmapResource,
  type Task,
  type TaskProject,
} from '../api/tasks'
import { TaskRow } from '../components/ui/TaskRow'
import { getNotes, createNote } from '../api/notes'
import { dateInputToUnix, dateToInputValue, todayDateInputValue } from '../utils/taskForm'
import { formatTaskProjectOption, taskProjectTypeLabels } from '../utils/taskProjects'

type TaskTab = 'week' | 'long' | 'roadmap'
type LongTaskStatus = 'active' | 'blocked' | 'open' | 'done'

interface RoadmapNodeData extends Record<string, unknown> {
  node: RoadmapNode
  onOpen: (id: string) => void
  onCreateAfter: (id: string) => void
  isCreatingNode: boolean
}

const tabLabels: Record<TaskTab, string> = {
  week: '本周',
  long: '长期任务',
  roadmap: '学习 Roadmap',
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

const nodeStatusLabels: Record<RoadmapNode['status'], string> = {
  todo: '未开始',
  active: '学习中',
  done: '已完成',
  skipped: '已跳过',
}

const longTaskStatusOrder: LongTaskStatus[] = ['active', 'blocked', 'open', 'done']

const longTaskStatusLabels: Record<LongTaskStatus, string> = {
  active: '进行中',
  blocked: '阻塞',
  open: '未开始',
  done: '完成',
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
  const navigate = useNavigate()
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
  const [isNodeDialogOpen, setIsNodeDialogOpen] = useState(false)
  const [isCreateNodeDialogOpen, setIsCreateNodeDialogOpen] = useState(false)
  const [createNodeParentID, setCreateNodeParentID] = useState('')
  const [manualResourceTitle, setManualResourceTitle] = useState('')
  const [manualResourceURL, setManualResourceURL] = useState('')
  const [resourcePickerNodeID, setResourcePickerNodeID] = useState('')
  const [resourceCandidates, setResourceCandidates] = useState<RoadmapResource[]>([])
  const [selectedResourceCandidateIDs, setSelectedResourceCandidateIDs] = useState<string[]>([])
  const [articleSearchSources, setArticleSearchSources] = useState<string[]>(defaultArticleSearchSources)
  const [pendingDeleteProjectID, setPendingDeleteProjectID] = useState('')

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
  const activeProjectID =
    activeTab === 'week' ? weekProjectID : activeTab === 'long' ? longProjectID : selectedLearningProjectID

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
    queryFn: () => getTasks({ horizon: 'week', status: 'all', page_size: 100 }),
  })

  const longTasksQuery = useQuery({
    queryKey: ['tasks', 'long'],
    queryFn: () => getTasks({ horizon: 'long', status: 'all', page_size: 100 }),
  })

  const roadmapQuery = useQuery({
    queryKey: ['learning-roadmap', selectedLearningProjectID],
    queryFn: () => getLearningRoadmap(selectedLearningProjectID),
    enabled: Boolean(selectedLearningProjectID),
  })

  const { data: projectNotesData } = useQuery({
    queryKey: ['notes', { project_id: activeProjectID }],
    queryFn: () => getNotes({ project_id: activeProjectID!, page_size: 6 }),
    enabled: !!activeProjectID,
  })
  const projectNotes = projectNotesData?.notes || []

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

  const deleteProjectMutation = useMutation({
    mutationFn: async (project: TaskProject) => {
      await deleteTaskProject(project.id)
      return project
    },
    onSuccess: (project) => {
      setPendingDeleteProjectID('')
      if (weekProjectID === project.id) {
        setWeekProjectID(personalProject?.id ?? 'personal')
        setActiveTab('week')
      }
      if (longProjectID === project.id) {
        setLongProjectID('')
      }
      if (selectedLearningProjectID === project.id) {
        setSelectedLearningProjectID('')
        setSelectedNodeID('')
        setIsNodeDialogOpen(false)
        setIsCreateNodeDialogOpen(false)
        setCreateNodeParentID('')
        setActiveTab('week')
      }
      queryClient.invalidateQueries({ queryKey: ['task-projects'] })
      queryClient.invalidateQueries({ queryKey: ['tasks'] })
      queryClient.invalidateQueries({ queryKey: ['learning-roadmap'] })
    },
  })

  const createTaskMutation = useMutation({
    mutationFn: createTask,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['tasks'] })
    },
  })

  const updateTaskMutation = useMutation({
    mutationFn: ({ id, body }: { id: string; body: Partial<Task> }) => updateTask(id, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['tasks'] })
      queryClient.invalidateQueries({ queryKey: ['learning-roadmap'] })
    },
  })

  const updateRoadmapNodeMutation = useMutation({
    mutationFn: ({ id, body }: { id: string; body: Partial<RoadmapNode> }) => updateRoadmapNode(id, body),
    onSuccess: (node) => {
      setSelectedNodeID(node.id)
      queryClient.invalidateQueries({ queryKey: ['learning-roadmap', selectedLearningProjectID] })
    },
  })

  const createRoadmapNodeMutation = useMutation({
    mutationFn: ({
      roadmapID,
      body,
    }: {
      roadmapID: string
      body: Parameters<typeof createRoadmapNode>[1]
    }) => createRoadmapNode(roadmapID, body),
    onSuccess: (node) => {
      setSelectedNodeID(node.id)
      setIsCreateNodeDialogOpen(false)
      setCreateNodeParentID('')
      queryClient.invalidateQueries({ queryKey: ['learning-roadmap', selectedLearningProjectID] })
    },
  })

  const deleteRoadmapNodeMutation = useMutation({
    mutationFn: deleteRoadmapNode,
    onSuccess: (_unused, nodeID) => {
      if (selectedNodeID === nodeID) {
        setSelectedNodeID('')
        setIsNodeDialogOpen(false)
      }
      queryClient.invalidateQueries({ queryKey: ['tasks'] })
      queryClient.invalidateQueries({ queryKey: ['learning-roadmap', selectedLearningProjectID] })
    },
  })

  const optimizeRoadmapLayoutMutation = useMutation({
    mutationFn: optimizeRoadmapLayout,
    onSuccess: (optimizedRoadmap) => {
      queryClient.setQueryData(['learning-roadmap', selectedLearningProjectID], optimizedRoadmap)
      queryClient.invalidateQueries({ queryKey: ['learning-roadmap', selectedLearningProjectID] })
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
    await updateTaskMutation.mutateAsync({ id: task.id, body: { done: task.done ? 0 : 1 } })
  }

  async function handleUpdateLongTaskStatus(task: Task, status: LongTaskStatus) {
    await updateTaskMutation.mutateAsync({ id: task.id, body: { status } })
  }

  async function handleUpdateTaskContent(task: Task, content: string) {
    await updateTaskMutation.mutateAsync({ id: task.id, body: { content } })
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

  function handleOpenRoadmapNode(nodeID: string) {
    if (!nodeID) {
      setSelectedNodeID('')
      setIsNodeDialogOpen(false)
      return
    }
    setSelectedNodeID(nodeID)
    setIsNodeDialogOpen(true)
  }

  function handleOpenCreateRoadmapNode(parentID: string) {
    if (!parentID) return
    setCreateNodeParentID(parentID)
    setSelectedNodeID(parentID)
    setIsCreateNodeDialogOpen(true)
  }

  async function handleSaveRoadmapNode(nodeID: string, body: Partial<RoadmapNode>) {
    await updateRoadmapNodeMutation.mutateAsync({ id: nodeID, body })
  }

  async function handleCreateRoadmapNode(body: Parameters<typeof createRoadmapNode>[1]) {
    if (!roadmap?.id) return
    await createRoadmapNodeMutation.mutateAsync({ roadmapID: roadmap.id, body })
  }

  async function handleDeleteRoadmapNode(nodeID: string) {
    await deleteRoadmapNodeMutation.mutateAsync(nodeID)
  }

  async function handleAddNodeToWeek(node: RoadmapNode) {
    if (!selectedLearningProjectID) return
    const plannedDate = todayDateInputValue()
    const linkedNodeTasks = (weekTasksQuery.data?.tasks ?? []).filter(
      (task) =>
        task.roadmap_node_id === node.id &&
        (!task.project_id || task.project_id === selectedLearningProjectID),
    )
    const hasExistingTaskToday = linkedNodeTasks.some(
      (task) =>
        isTaskPlannedOnDate(task, plannedDate),
    )
    if (hasExistingTaskToday) {
      setSelectedNodeID(node.id)
      return
    }
    await createTaskMutation.mutateAsync({
      title: formatRoadmapTaskTitle(node.title, linkedNodeTasks.length + 1),
      project_id: selectedLearningProjectID,
      roadmap_node_id: node.id,
      planned_date: plannedDate,
      due: dateInputToUnix(plannedDate),
      horizon: 'week',
      scope: 'daily',
    })
    setSelectedNodeID(node.id)
  }

  const roadmap = roadmapQuery.data
  const selectedRoadmapNode = roadmap?.nodes.find((node) => node.id === selectedNodeID)
  const createNodeParent = roadmap?.nodes.find((node) => node.id === createNodeParentID)
  const selectedNodeTasks = useMemo(() => {
    if (!selectedNodeID) return []
    const taskByID = new Map<string, Task>()
    for (const task of [...(weekTasksQuery.data?.tasks ?? []), ...(longTasksQuery.data?.tasks ?? [])]) {
      if (task.roadmap_node_id === selectedNodeID) {
        taskByID.set(task.id, task)
      }
    }
    return [...taskByID.values()]
  }, [longTasksQuery.data?.tasks, selectedNodeID, weekTasksQuery.data?.tasks])
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
              <option value="regular">任务项目</option>
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
            <div className="task-project-item" key={project.id}>
              <button
                type="button"
                className={project.id === activeProjectID ? 'task-project-select is-active' : 'task-project-select'}
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
                <small>{taskProjectTypeLabels[project.type]}</small>
              </button>

              {project.id !== 'personal' && (
                pendingDeleteProjectID === project.id ? (
                  <div className="task-project-delete-confirm">
                    <button
                      type="button"
                      aria-label={`确认删除 ${project.name}`}
                      disabled={deleteProjectMutation.isPending}
                      onClick={() => deleteProjectMutation.mutate(project)}
                    >
                      确认
                    </button>
                    <button type="button" aria-label={`取消删除 ${project.name}`} onClick={() => setPendingDeleteProjectID('')}>
                      取消
                    </button>
                  </div>
                ) : (
                  <button
                    className="task-project-delete"
                    type="button"
                    aria-label={`删除项目 ${project.name}`}
                    title="删除项目"
                    onClick={() => setPendingDeleteProjectID(project.id)}
                  >
                    ×
                  </button>
                )
              )}
            </div>
          ))}
        </div>

        {activeProjectID && (
          <div className="mt-4">
            <h4 className="text-xs font-semibold text-fs-text-muted mb-2">项目笔记</h4>
            {projectNotes.length === 0 && (
              <p className="text-xs text-fs-text-muted">暂无笔记</p>
            )}
            {projectNotes.map((note) => (
              <button
                key={note.id}
                type="button"
                className="block w-full text-left px-2 py-1 rounded hover:bg-fs-accent/5 text-sm"
                onClick={() => navigate(`/editor/${encodeURIComponent(note.id)}`)}
              >
                <div className="truncate">{note.title || '未命名笔记'}</div>
                <div className="text-xs text-fs-text-muted">
                  {new Date(note.updated_at * 1000).toLocaleDateString('zh-CN')}
                </div>
              </button>
            ))}
            <button
              type="button"
              className="mt-2 text-sm text-fs-accent hover:underline"
              onClick={async () => {
                const note = await createNote({
                  title: '未命名笔记',
                  body: '',
                  folder_id: '__uncategorized',
                  tags: '[]',
                  project_ids: activeProjectID ? [activeProjectID] : undefined,
                })
                navigate(`/editor/${encodeURIComponent(note.id)}`)
              }}
            >
              + 新建项目笔记
            </button>
          </div>
        )}
      </aside>

      <section className="task-main-panel">
        <div className="panel-heading">
          <div>
            <span>任务工作台</span>
            <h2>短期推进、长期任务和学习路线</h2>
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
            onStatusChange={handleUpdateLongTaskStatus}
            isUpdating={updateTaskMutation.isPending}
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
            isOptimizingLayout={optimizeRoadmapLayoutMutation.isPending}
            isCreatingNode={createRoadmapNodeMutation.isPending}
            onSelectProject={(projectID) => {
              setSelectedLearningProjectID(projectID)
              setSelectedNodeID('')
              setIsNodeDialogOpen(false)
              setIsCreateNodeDialogOpen(false)
              setCreateNodeParentID('')
            }}
            onSelectNode={handleOpenRoadmapNode}
            onGenerate={() => selectedLearningProjectID && generateRoadmapMutation.mutate(selectedLearningProjectID)}
            onOpenCreateNode={handleOpenCreateRoadmapNode}
            onOptimizeLayout={(roadmapID) => optimizeRoadmapLayoutMutation.mutate(roadmapID)}
            onSearchResources={(nodeID) => searchResourcesMutation.mutate({ nodeID, sources: articleSearchSources })}
            onToggleArticleSearchSource={handleToggleArticleSearchSource}
            onManualTitleChange={setManualResourceTitle}
            onManualURLChange={setManualResourceURL}
            onAddResource={(nodeID) => {
              if (!manualResourceTitle.trim() || !manualResourceURL.trim()) return
              addResourceMutation.mutate({ nodeID, title: manualResourceTitle.trim(), url: manualResourceURL.trim() })
            }}
            onAddNodeToWeek={handleAddNodeToWeek}
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
      {roadmap && isCreateNodeDialogOpen && createNodeParent && (
        <RoadmapNodeCreateDialog
          parentNode={createNodeParent}
          isSaving={createRoadmapNodeMutation.isPending}
          onClose={() => {
            setIsCreateNodeDialogOpen(false)
            setCreateNodeParentID('')
          }}
          onCreate={handleCreateRoadmapNode}
        />
      )}
      {selectedRoadmapNode && isNodeDialogOpen && (
        <RoadmapNodeDialog
          node={selectedRoadmapNode}
          tasks={selectedNodeTasks}
          isSaving={updateRoadmapNodeMutation.isPending}
          isAddingTask={createTaskMutation.isPending}
          isUpdatingTask={updateTaskMutation.isPending}
          isDeleting={deleteRoadmapNodeMutation.isPending}
          onClose={() => setIsNodeDialogOpen(false)}
          onSave={handleSaveRoadmapNode}
          onDelete={handleDeleteRoadmapNode}
          onAddNodeToWeek={handleAddNodeToWeek}
          onToggleTask={handleToggleTask}
          onSaveTaskContent={handleUpdateTaskContent}
        />
      )}
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
  onStatusChange,
  isUpdating,
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
  onStatusChange: (task: Task, status: LongTaskStatus) => void
  isUpdating: boolean
}) {
  const tasksByStatus = useMemo(() => {
    const grouped = new Map<LongTaskStatus, Task[]>(longTaskStatusOrder.map((status) => [status, []]))
    for (const task of tasks) {
      const status = normalizeLongTaskStatus(task)
      grouped.set(status, [...(grouped.get(status) ?? []), task])
    }
    return longTaskStatusOrder
      .map((status) => ({ status, tasks: grouped.get(status) ?? [] }))
      .filter((group) => group.tasks.length > 0)
  }, [tasks])

  return (
    <div className="task-tab-panel">
      {projects.length > 0 && (
        <form className="inline-create task-create-form" onSubmit={onSubmit}>
          <input
            className="task-title-input"
            aria-label="长期任务内容"
            value={title}
            onChange={(event) => onTitleChange(event.target.value)}
            placeholder="添加长期任务"
          />
          <select aria-label="任务项目" value={selectedProjectID} onChange={(event) => onProjectChange(event.target.value)}>
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
      )}

      {projects.length === 0 ? (
        <p className="empty-copy">先在左侧新增一个任务项目</p>
      ) : tasksByStatus.length === 0 ? (
        <p className="empty-copy">还没有长期任务</p>
      ) : (
        <div className="long-task-status-groups" data-testid="long-task-status-groups">
          {tasksByStatus.map(({ status, tasks: statusTasks }) => (
            <section
              key={status}
              className="task-section long-task-section"
              data-testid={`long-task-status-${status}`}
            >
              <div className="long-task-section-heading">
                <h3>{longTaskStatusLabels[status]}</h3>
                <span>{statusTasks.length}</span>
              </div>
              <div className="row-stack">
                {statusTasks.map((task) => (
                  <LongTaskRow
                    key={task.id}
                    task={task}
                    isUpdating={isUpdating}
                    onToggle={() => onToggle(task)}
                    onStatusChange={(nextStatus) => onStatusChange(task, nextStatus)}
                  />
                ))}
              </div>
            </section>
          ))}
        </div>
      )}
    </div>
  )
}

function LongTaskRow({
  task,
  isUpdating,
  onToggle,
  onStatusChange,
}: {
  task: Task
  isUpdating: boolean
  onToggle: () => void
  onStatusChange: (status: LongTaskStatus) => void
}) {
  const status = normalizeLongTaskStatus(task)
  const project = task.project || '未命名项目'

  return (
    <div className={`task-row long-task-row long-task-row-${status}`}>
      <button
        type="button"
        className="long-task-done-toggle"
        aria-label={task.done ? `重新打开 ${task.title}` : `完成 ${task.title}`}
        onClick={onToggle}
      >
        {status === 'done' ? '✓' : ''}
      </button>
      <div className="long-task-copy">
        <strong className={status === 'done' ? 'is-done' : ''}>{task.title}</strong>
        <small>
          {project} · 最近进展 {formatLongTaskUpdatedAt(task.updated_at)}
        </small>
      </div>
      <select
        className="long-task-status-select"
        aria-label={`更新长期任务状态：${task.title}`}
        value={status}
        disabled={isUpdating}
        onChange={(event) => onStatusChange(event.target.value as LongTaskStatus)}
      >
        {longTaskStatusOrder.map((value) => (
          <option key={value} value={value}>
            {longTaskStatusLabels[value]}
          </option>
        ))}
      </select>
    </div>
  )
}

function normalizeLongTaskStatus(task: Task): LongTaskStatus {
  if (task.done === 1 || task.status === 'done') return 'done'
  if (task.status === 'active') return 'active'
  if (task.status === 'blocked') return 'blocked'
  return 'open'
}

function formatLongTaskUpdatedAt(updatedAt: number) {
  if (!updatedAt) return '未知'
  return new Date(updatedAt * 1000).toLocaleDateString('zh-CN', { month: 'short', day: 'numeric' })
}

function getTaskPlannedDate(task: Task) {
  if (task.planned_date) return task.planned_date
  if (!task.due) return ''
  return dateToInputValue(new Date(task.due * 1000))
}

function isTaskPlannedOnDate(task: Task, plannedDate: string) {
  return getTaskPlannedDate(task) === plannedDate
}

function compareRoadmapLinkedTasks(a: Task, b: Task) {
  const dateCompare = getTaskPlannedDate(a).localeCompare(getTaskPlannedDate(b))
  if (dateCompare !== 0) return dateCompare
  if (a.created_at !== b.created_at) return a.created_at - b.created_at
  return a.id.localeCompare(b.id)
}

function formatRoadmapTaskStatus(task: Task) {
  if (task.done === 1 || task.status === 'done') return '已完成'
  if (task.status === 'active') return '进行中'
  if (task.status === 'blocked') return '阻塞'
  return '未完成'
}

function formatRoadmapTaskCreatedAt(createdAt: number) {
  if (!createdAt) return '未知时间'
  return new Intl.DateTimeFormat('zh-CN', {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(createdAt * 1000))
}

function formatRoadmapTaskTitle(baseTitle: string, sequence: number) {
  if (/第\s*\d+\s*次推进/.test(baseTitle)) return baseTitle
  return `${baseTitle} · 第 ${sequence} 次推进`
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
  isOptimizingLayout,
  isCreatingNode,
  onSelectProject,
  onSelectNode,
  onGenerate,
  onOpenCreateNode,
  onOptimizeLayout,
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
  isOptimizingLayout: boolean
  isCreatingNode: boolean
  onSelectProject: (value: string) => void
  onSelectNode: (value: string) => void
  onGenerate: () => void
  onOpenCreateNode: (nodeID: string) => void
  onOptimizeLayout: (roadmapID: string) => void
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
          <RoadmapCanvas
            roadmap={roadmap}
            selectedNodeID={selectedNode?.id ?? ''}
            isOptimizingLayout={isOptimizingLayout}
            isCreatingNode={isCreatingNode}
            onSelectNode={onSelectNode}
            onOpenCreateNode={onOpenCreateNode}
            onOptimizeLayout={onOptimizeLayout}
          />
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
  isOptimizingLayout,
  isCreatingNode,
  onSelectNode,
  onOpenCreateNode,
  onOptimizeLayout,
}: {
  roadmap: LearningRoadmap
  selectedNodeID: string
  isOptimizingLayout: boolean
  isCreatingNode: boolean
  onSelectNode: (id: string) => void
  onOpenCreateNode: (nodeID: string) => void
  onOptimizeLayout: (roadmapID: string) => void
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
        data: { node, onOpen: onSelectNode, onCreateAfter: onOpenCreateNode, isCreatingNode },
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
  }, [isCreatingNode, onOpenCreateNode, onSelectNode, roadmap.edges, roadmap.nodes, selectedNodeID, setEdges, setNodes])

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
      <button
        className="roadmap-optimize-layout"
        type="button"
        onClick={() => onOptimizeLayout(roadmap.id)}
        disabled={isOptimizingLayout}
      >
        {isOptimizingLayout ? '优化中...' : '自动优化布局'}
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
  const { node, onOpen, onCreateAfter, isCreatingNode } = props.data as RoadmapNodeData

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
      <button
        className="roadmap-node-add-after nodrag nopan"
        type="button"
        aria-label="添加后续节点"
        title="添加后续节点"
        disabled={isCreatingNode}
        onClick={(event) => {
          event.stopPropagation()
          onCreateAfter(node.id)
        }}
        onPointerDown={(event) => event.stopPropagation()}
        onKeyDown={(event) => event.stopPropagation()}
      >
        +
      </button>
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

function RoadmapNodeCreateDialog({
  parentNode,
  isSaving,
  onClose,
  onCreate,
}: {
  parentNode: RoadmapNode
  isSaving: boolean
  onClose: () => void
  onCreate: (body: Parameters<typeof createRoadmapNode>[1]) => Promise<void>
}) {
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [nodeType, setNodeType] = useState<RoadmapNode['type']>('task')
  const [pathType, setPathType] = useState<RoadmapNode['path_type']>('required')

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const trimmedTitle = title.trim()
    if (!trimmedTitle) return
    await onCreate({
      parent_id: parentNode.id,
      title: trimmedTitle,
      type: nodeType,
      description: description.trim(),
      path_type: pathType,
      status: 'todo',
      edge_style: pathType === 'required' ? 'solid' : 'dotted',
    })
  }

  return (
    <div className="roadmap-node-create-overlay">
      <section className="roadmap-node-create-dialog" role="dialog" aria-modal="true" aria-label="新增 Roadmap 节点">
        <div className="roadmap-node-dialog-heading">
          <div>
            <span>Roadmap</span>
            <h2>新增节点</h2>
          </div>
          <button type="button" aria-label="关闭新增节点" onClick={onClose}>
            ×
          </button>
        </div>

        <form className="roadmap-node-create-form" onSubmit={handleSubmit}>
          <label>
            <span>节点标题</span>
            <input aria-label="节点标题" value={title} onChange={(event) => setTitle(event.target.value)} autoFocus />
          </label>
          <div className="roadmap-node-parent-context">
            <span>接在</span>
            <strong>{parentNode.title}</strong>
            <small>之后创建后续节点</small>
          </div>
          <label>
            <span>节点类型</span>
            <select
              aria-label="节点类型"
              value={nodeType}
              onChange={(event) => setNodeType(event.target.value as RoadmapNode['type'])}
            >
              {(Object.keys(nodeTypeLabels) as RoadmapNode['type'][]).map((value) => (
                <option key={value} value={value}>
                  {nodeTypeLabels[value]}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span>路径类型</span>
            <select
              aria-label="路径类型"
              value={pathType}
              onChange={(event) => setPathType(event.target.value as RoadmapNode['path_type'])}
            >
              {(Object.keys(pathTypeLabels) as RoadmapNode['path_type'][]).map((value) => (
                <option key={value} value={value}>
                  {pathTypeLabels[value]}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span>节点说明</span>
            <textarea
              aria-label="节点说明"
              value={description}
              onChange={(event) => setDescription(event.target.value)}
            />
          </label>
          <div className="roadmap-node-create-actions">
            <button type="button" onClick={onClose}>
              取消
            </button>
            <button type="submit" disabled={!title.trim() || isSaving}>
              {isSaving ? '创建中...' : '创建节点'}
            </button>
          </div>
        </form>
      </section>
    </div>
  )
}

function RoadmapNodeDialog({
  node,
  tasks,
  isSaving,
  isAddingTask,
  isUpdatingTask,
  isDeleting,
  onClose,
  onSave,
  onDelete,
  onAddNodeToWeek,
  onToggleTask,
  onSaveTaskContent,
}: {
  node: RoadmapNode
  tasks: Task[]
  isSaving: boolean
  isAddingTask: boolean
  isUpdatingTask: boolean
  isDeleting: boolean
  onClose: () => void
  onSave: (nodeID: string, body: Partial<RoadmapNode>) => Promise<void>
  onDelete: (nodeID: string) => Promise<void>
  onAddNodeToWeek: (node: RoadmapNode) => Promise<void>
  onToggleTask: (task: Task) => Promise<void>
  onSaveTaskContent: (task: Task, content: string) => Promise<void>
}) {
  const [title, setTitle] = useState(node.title)
  const [description, setDescription] = useState(node.description)
  const [deliverable, setDeliverable] = useState(node.deliverable)
  const [acceptanceCriteria, setAcceptanceCriteria] = useState(node.acceptance_criteria)
  const [status, setStatus] = useState<RoadmapNode['status']>(node.status)
  const [isConfirmingDelete, setIsConfirmingDelete] = useState(false)

  useEffect(() => {
    setTitle(node.title)
    setDescription(node.description)
    setDeliverable(node.deliverable)
    setAcceptanceCriteria(node.acceptance_criteria)
    setStatus(node.status)
    setIsConfirmingDelete(false)
  }, [node.acceptance_criteria, node.deliverable, node.description, node.id, node.status, node.title])

  const doneCount = tasks.filter((task) => task.done === 1 || task.status === 'done').length
  const progressPercent = tasks.length ? Math.round((doneCount / tasks.length) * 100) : 0
  const linkedTasks = useMemo(() => [...tasks].sort(compareRoadmapLinkedTasks), [tasks])

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const trimmedTitle = title.trim()
    if (!trimmedTitle) return
    await onSave(node.id, {
      title: trimmedTitle,
      description: description.trim(),
      deliverable: deliverable.trim(),
      acceptance_criteria: acceptanceCriteria.trim(),
      status,
    })
  }

  return (
    <div className="roadmap-node-dialog-overlay">
      <section
        className="roadmap-node-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="节点详情"
        data-testid="roadmap-node-dialog"
      >
        <div className="roadmap-node-dialog-heading">
          <div>
            <span>{nodeTypeLabels[node.type]} · {pathTypeLabels[node.path_type]}</span>
            <h2>{title || node.title}</h2>
          </div>
          <button type="button" aria-label="关闭节点详情" onClick={onClose}>
            ×
          </button>
        </div>

        <form className="roadmap-node-edit-form" onSubmit={handleSubmit}>
          <label>
            <span>节点标题</span>
            <input aria-label="节点标题" value={title} onChange={(event) => setTitle(event.target.value)} />
          </label>
          <label>
            <span>节点说明</span>
            <textarea
              aria-label="节点说明"
              value={description}
              onChange={(event) => setDescription(event.target.value)}
            />
          </label>
          <label>
            <span>学习状态</span>
            <select
              aria-label="学习状态"
              value={status}
              onChange={(event) => setStatus(event.target.value as RoadmapNode['status'])}
            >
              {(Object.keys(nodeStatusLabels) as RoadmapNode['status'][]).map((value) => (
                <option key={value} value={value}>
                  {nodeStatusLabels[value]}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span>交付物</span>
            <textarea aria-label="交付物" value={deliverable} onChange={(event) => setDeliverable(event.target.value)} />
          </label>
          <label>
            <span>验收标准</span>
            <textarea
              aria-label="验收标准"
              value={acceptanceCriteria}
              onChange={(event) => setAcceptanceCriteria(event.target.value)}
            />
          </label>
          <div className="roadmap-node-dialog-actions">
            <button type="submit" disabled={!title.trim() || isSaving}>
              {isSaving ? '保存中...' : '保存节点'}
            </button>
            <button
              type="button"
              onClick={() => onAddNodeToWeek({ ...node, title: title.trim() || node.title })}
              disabled={isAddingTask}
            >
              {isAddingTask ? '添加中...' : '加入本周'}
            </button>
          </div>
          <div className="roadmap-node-danger-actions">
            {isConfirmingDelete ? (
              <>
                <button type="button" onClick={() => onDelete(node.id)} disabled={isDeleting}>
                  {isDeleting ? '删除中...' : '确认删除节点'}
                </button>
                <button type="button" onClick={() => setIsConfirmingDelete(false)} disabled={isDeleting}>
                  取消
                </button>
              </>
            ) : (
              <button type="button" onClick={() => setIsConfirmingDelete(true)}>
                删除节点
              </button>
            )}
          </div>
        </form>

        <div className="roadmap-node-progress" data-testid="roadmap-node-progress">
          <div>
            <span>任务进度</span>
            <strong>{doneCount} / {tasks.length}</strong>
          </div>
          <div className="roadmap-node-progress-track" aria-hidden="true">
            <i style={{ width: `${progressPercent}%` }} />
          </div>
        </div>

        <div className="roadmap-linked-task-list" data-testid="roadmap-linked-task-list">
          <span>关联学习任务</span>
          {tasks.length === 0 ? (
            <p className="empty-copy">暂无关联任务</p>
          ) : (
            linkedTasks.map((task, index) => (
              <RoadmapLinkedTaskRow
                key={task.id}
                task={task}
                sequence={index + 1}
                isUpdating={isUpdatingTask}
                onToggle={() => onToggleTask(task)}
                onSaveContent={(content) => onSaveTaskContent(task, content)}
              />
            ))
          )}
        </div>
      </section>
    </div>
  )
}

function RoadmapLinkedTaskRow({
  task,
  sequence,
  isUpdating,
  onToggle,
  onSaveContent,
}: {
  task: Task
  sequence: number
  isUpdating: boolean
  onToggle: () => void
  onSaveContent: (content: string) => Promise<void>
}) {
  const [content, setContent] = useState(task.content ?? '')
  const isDone = task.done === 1 || task.status === 'done'
  const plannedDate = getTaskPlannedDate(task)
  const title = formatRoadmapTaskTitle(task.title, sequence)
  const actionLabel = isDone ? `重新打开 ${title}` : `完成 ${title}`
  const isDirty = content.trim() !== (task.content ?? '').trim()

  useEffect(() => {
    setContent(task.content ?? '')
  }, [task.content, task.id])

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    await onSaveContent(content)
  }

  return (
    <article className={`roadmap-linked-task-row${isDone ? ' is-done' : ''}${isUpdating ? ' is-updating' : ''}`}>
      <button
        type="button"
        className="roadmap-linked-task-check"
        aria-label={actionLabel}
        disabled={isUpdating}
        onClick={onToggle}
      >
        {isDone ? '✓' : ''}
      </button>
      <div className="roadmap-linked-task-copy">
        <strong>{title}</strong>
        <small className="roadmap-linked-task-meta">
          <span>第 {sequence} 次</span>
          {plannedDate && <span>{plannedDate}</span>}
          <span>{formatRoadmapTaskStatus(task)}</span>
          <span>创建 {formatRoadmapTaskCreatedAt(task.created_at)}</span>
          {task.project && <span>{task.project}</span>}
        </small>
        <form className="roadmap-linked-task-content-form" onSubmit={handleSubmit}>
          <label>
            <span>具体任务内容</span>
            <textarea
              aria-label={`任务内容：${title}`}
              value={content}
              onChange={(event) => setContent(event.target.value)}
              placeholder="写下这次推进要完成的具体步骤、材料或验收点"
            />
          </label>
          <button type="submit" disabled={isUpdating || !isDirty}>
            保存任务内容
          </button>
        </form>
      </div>
    </article>
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
