import {
  type ComponentType,
  type FormEvent,
  useEffect,
  useMemo,
  useState,
} from 'react'
import { useNavigate } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  BookOpen,
  Maximize2,
  Minimize2,
  Plus,
  Save,
  Search,
  Trash2,
  WandSparkles,
} from 'lucide-react'
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
  completeOccurrence,
  createRoadmapNode,
  createTask,
  createTaskProject,
  deleteRoadmapNode,
  deleteRoadmapResource,
  deleteTaskProject,
  generateLearningRoadmap,
  getLearningRoadmap,
  getRecurringTasks,
  getTasks,
  listTaskProjects,
  optimizeRoadmapLayout,
  reopenOccurrence,
  saveRoadmapLayout,
  searchRoadmapNodeResources,
  updateTask,
  updateRoadmapNode,
  type LearningRoadmap,
  type RecurrenceConfig,
  type RoadmapNode,
  type RoadmapResource,
  type Task,
  type TaskProject,
} from '../api/tasks'
import { TaskRow } from '../components/ui/TaskRow'
import { getNotes, createNote } from '../api/notes'
import {
  dateInputToUnix,
  dateToInputValue,
  todayDateInputValue,
} from '../utils/taskForm'
import { taskProjectTypeLabels } from '../utils/taskProjects'
import { getTaskColor } from '../utils/taskColors'

type TaskTab = 'week' | 'long' | 'recurring' | 'roadmap'
type LongTaskStatus = 'active' | 'blocked' | 'open' | 'done'
type TaskDetailDraft = {
  title: string
  projectID: string
  plannedDate: string
  status: LongTaskStatus
  content: string
}
type RoadmapLinkedTaskExecutionType = 'single' | 'recurring'
type RoadmapLinkedTaskInput = {
  title: string
  content: string
  plannedDate: string
  executionType: RoadmapLinkedTaskExecutionType
  recurrence?: RecurrenceConfig
}

interface RoadmapNodeData extends Record<string, unknown> {
  node: RoadmapNode
  stepNumber: number
  targetPosition: Position
  sourcePosition: Position
  onOpen: (id: string) => void
  onCreateAfter: (id: string) => void
  isCreatingNode: boolean
}

const tabLabels: Record<TaskTab, string> = {
  week: '本周',
  long: '长期任务',
  recurring: '重复任务',
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

const longTaskStatusOrder: LongTaskStatus[] = [
  'active',
  'blocked',
  'open',
  'done',
]
const taskDetailStatusOrder: LongTaskStatus[] = [
  'open',
  'active',
  'blocked',
  'done',
]

const longTaskStatusLabels: Record<LongTaskStatus, string> = {
  active: '进行中',
  blocked: '阻塞',
  open: '未开始',
  done: '完成',
}

const emptyTaskDetailDraft: TaskDetailDraft = {
  title: '',
  projectID: '',
  plannedDate: '',
  status: 'open',
  content: '',
}

const taskProjectGroupMeta: Record<
  TaskProject['type'],
  {
    title: string
    description: string
    createLabel: string
    submitLabel: string
    nameLabel: string
    descriptionLabel: string
    placeholder: string
    emptyCopy: string
  }
> = {
  personal: {
    title: '个人短期项目',
    description: '今日 / 本周任务',
    createLabel: '新建短期项目',
    submitLabel: '创建短期项目',
    nameLabel: '个人短期项目名称',
    descriptionLabel: '个人短期项目说明',
    placeholder: '例如：本周冲刺',
    emptyCopy: '暂无短期项目',
  },
  regular: {
    title: '长期项目',
    description: '长期任务',
    createLabel: '新建长期项目',
    submitLabel: '创建长期项目',
    nameLabel: '长期项目名称',
    descriptionLabel: '长期项目说明',
    placeholder: '例如：年度计划',
    emptyCopy: '暂无长期项目',
  },
  learning: {
    title: '学习项目',
    description: '学习 Roadmap',
    createLabel: '新建学习项目',
    submitLabel: '创建学习项目',
    nameLabel: '学习项目名称',
    descriptionLabel: '学习项目说明',
    placeholder: '例如：N2 语法',
    emptyCopy: '暂无学习项目',
  },
}

type ArticleSearchSourceOption = {
  id: string
  label: string
  custom?: boolean
}

const articleSearchSourceOptions: ArticleSearchSourceOption[] = [
  { id: 'google', label: 'Google/通用' },
  { id: 'medium', label: 'Medium' },
  { id: 'reddit', label: 'Reddit' },
  { id: 'devto', label: 'Dev.to' },
  { id: 'hashnode', label: 'Hashnode' },
  { id: 'stackoverflow', label: 'Stack Overflow' },
  { id: 'github', label: 'GitHub' },
  { id: 'official', label: '官方文档' },
  { id: 'technical', label: '技术博客' },
]

const defaultArticleSearchSources = articleSearchSourceOptions.map(
  (source) => source.id
)

const customArticleSearchSourcesStorageKey =
  'flowspace-roadmap-custom-article-search-sources'
const roadmapGenerationPromptsStorageKey =
  'flowspace-roadmap-generation-prompts-v1'
const maxRoadmapGenerationPromptLength = 4000

function buildDefaultRoadmapGenerationPrompt(project?: TaskProject) {
  if (!project) return ''
  const goal = project.description.trim() || `系统掌握 ${project.name}`
  return `请为「${project.name}」生成一条完整、连续且可执行的学习路径。学习目标：${goal}。从基础知识逐步推进到独立实践与最终验收；每个节点都要包含明确的学习任务、交付物和可验证的完成标准。`
}

function loadRoadmapGenerationPrompts(): Record<string, string> {
  if (typeof window === 'undefined') return {}
  try {
    const stored = JSON.parse(
      window.localStorage.getItem(roadmapGenerationPromptsStorageKey) ?? '{}'
    )
    if (!stored || typeof stored !== 'object' || Array.isArray(stored))
      return {}
    return Object.fromEntries(
      Object.entries(stored).filter(
        ([projectID, prompt]) =>
          Boolean(projectID) &&
          typeof prompt === 'string' &&
          prompt.length <= maxRoadmapGenerationPromptLength
      )
    ) as Record<string, string>
  } catch {
    return {}
  }
}

function normalizeCustomArticleSearchSource(
  rawValue: string
): ArticleSearchSourceOption | null {
  const value = rawValue.trim().replace(/^site:/i, '')
  if (!value) return null
  try {
    const parsed = new URL(
      /^[a-z][a-z\d+.-]*:\/\//i.test(value) ? value : `https://${value}`
    )
    if (!['http:', 'https:'].includes(parsed.protocol)) return null
    const hostname = parsed.hostname
      .toLowerCase()
      .replace(/^www\./, '')
      .replace(/\.$/, '')
    if (!hostname || !hostname.includes('.') || /\s/.test(hostname)) return null
    return { id: `site:${hostname}`, label: hostname, custom: true }
  } catch {
    return null
  }
}

function loadCustomArticleSearchSources(): ArticleSearchSourceOption[] {
  if (typeof window === 'undefined') return []
  try {
    const stored = JSON.parse(
      window.localStorage.getItem(customArticleSearchSourcesStorageKey) ?? '[]'
    )
    if (!Array.isArray(stored)) return []
    const sources = stored
      .map((value) =>
        typeof value === 'string'
          ? normalizeCustomArticleSearchSource(value)
          : null
      )
      .filter((source): source is ArticleSearchSourceOption => Boolean(source))
    return sources.filter(
      (source, index) =>
        sources.findIndex((candidate) => candidate.id === source.id) === index
    )
  } catch {
    return []
  }
}

function buildRoadmapNodeSearchPrompt(node?: RoadmapNode) {
  if (!node) return ''
  return [node.article_search_queries?.[0], node.title, node.deliverable]
    .filter(Boolean)
    .join(' · ')
}

function resourceCandidateKey(resource: RoadmapResource) {
  return resource.id || resource.url
}

function parseDateInputValue(value: string) {
  const date = new Date(`${value}T00:00:00`)
  return Number.isNaN(date.getTime()) ? null : date
}

function weekdayFromDateInput(value: string) {
  const date = parseDateInputValue(value)
  if (!date) return 1
  const weekday = date.getDay()
  return weekday === 0 ? 7 : weekday
}

function monthDayFromDateInput(value: string) {
  return parseDateInputValue(value)?.getDate() ?? 1
}

export default function Tasks() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [activeTab, setActiveTab] = useState<TaskTab>('week')
  const [creatingProjectType, setCreatingProjectType] = useState<
    TaskProject['type'] | ''
  >('')
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
  const [resourceSearchQuery, setResourceSearchQuery] = useState('')
  const [resourceCandidates, setResourceCandidates] = useState<
    RoadmapResource[]
  >([])
  const [selectedResourceCandidateIDs, setSelectedResourceCandidateIDs] =
    useState<string[]>([])
  const [articleSearchSources, setArticleSearchSources] = useState<string[]>(
    defaultArticleSearchSources
  )
  const [
    customArticleSearchSourceOptions,
    setCustomArticleSearchSourceOptions,
  ] = useState<ArticleSearchSourceOption[]>(loadCustomArticleSearchSources)
  const [pendingDeleteProjectID, setPendingDeleteProjectID] = useState('')
  const [recurringTitle, setRecurringTitle] = useState('')
  const [recurringFrequency, setRecurringFrequency] =
    useState<RecurrenceConfig['frequency']>('daily')
  const [recurringInterval, setRecurringInterval] = useState(1)
  const [recurringWeekdays, setRecurringWeekdays] = useState<number[]>([])
  const [recurringMonthDays, setRecurringMonthDays] = useState<number[]>([])
  const [recurringStartDate, setRecurringStartDate] = useState(() =>
    todayDateInputValue()
  )
  const [recurringEndDate, setRecurringEndDate] = useState('')
  const [recurringProjectID, setRecurringProjectID] = useState('')
  const [selectedTaskID, setSelectedTaskID] = useState('')
  const [taskDetailDraft, setTaskDetailDraft] =
    useState<TaskDetailDraft>(emptyTaskDetailDraft)

  const availableArticleSearchSourceOptions = useMemo(
    () => [...articleSearchSourceOptions, ...customArticleSearchSourceOptions],
    [customArticleSearchSourceOptions]
  )

  useEffect(() => {
    window.localStorage.setItem(
      customArticleSearchSourcesStorageKey,
      JSON.stringify(
        customArticleSearchSourceOptions.map((source) => source.id)
      )
    )
  }, [customArticleSearchSourceOptions])

  const projectsQuery = useQuery({
    queryKey: ['task-projects'],
    queryFn: listTaskProjects,
  })

  const projects = projectsQuery.data ?? []
  const shortProjects = projects.filter(
    (project) => project.type === 'personal'
  )
  const personalProject =
    shortProjects.find((project) => project.id === 'personal') ??
    shortProjects[0]
  const regularProjects = projects.filter(
    (project) => project.type === 'regular'
  )
  const longProjects = regularProjects
  const learningProjects = projects.filter(
    (project) => project.type === 'learning'
  )
  const weekProjects = shortProjects
  const selectedLearningProject = learningProjects.find(
    (project) => project.id === selectedLearningProjectID
  )
  const activeProjectID =
    activeTab === 'week'
      ? weekProjectID
      : activeTab === 'long'
        ? longProjectID
        : activeTab === 'recurring'
          ? recurringProjectID
          : selectedLearningProjectID

  useEffect(() => {
    if (!projects.length) return
    if (
      !weekProjectID ||
      !weekProjects.some((project) => project.id === weekProjectID)
    ) {
      setWeekProjectID(personalProject?.id ?? weekProjects[0]?.id ?? '')
    }
    if (!longProjectID && longProjects[0]) {
      setLongProjectID(longProjects[0].id)
    } else if (
      longProjectID &&
      !longProjects.some((project) => project.id === longProjectID)
    ) {
      setLongProjectID(longProjects[0]?.id ?? '')
    }
    if (!recurringProjectID && (personalProject || projects[0])) {
      setRecurringProjectID((personalProject ?? projects[0]).id)
    }
    if (!selectedLearningProjectID && learningProjects[0]) {
      setSelectedLearningProjectID(learningProjects[0].id)
    }
  }, [
    learningProjects,
    longProjectID,
    longProjects,
    personalProject,
    projects,
    recurringProjectID,
    selectedLearningProjectID,
    weekProjectID,
    weekProjects,
  ])

  const weekTasksQuery = useQuery({
    queryKey: ['tasks', 'week'],
    queryFn: () => getTasks({ horizon: 'week', status: 'all', page_size: 100 }),
  })

  const longTasksQuery = useQuery({
    queryKey: ['tasks', 'long'],
    queryFn: () => getTasks({ horizon: 'long', status: 'all', page_size: 100 }),
  })

  const recurringTasksQuery = useQuery({
    queryKey: ['tasks', 'recurring'],
    queryFn: () => getRecurringTasks({ page_size: 100 }),
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
      setCreatingProjectType('')
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
    onSuccess: (task) => {
      setSelectedTaskID(task.id)
      queryClient.invalidateQueries({ queryKey: ['tasks'] })
    },
  })

  const updateTaskMutation = useMutation({
    mutationFn: ({ id, body }: { id: string; body: Partial<Task> }) =>
      updateTask(id, body),
    onSuccess: (task) => {
      setSelectedTaskID(task.id)
      queryClient.invalidateQueries({ queryKey: ['tasks'] })
      queryClient.invalidateQueries({ queryKey: ['learning-roadmap'] })
    },
  })

  const updateRoadmapNodeMutation = useMutation({
    mutationFn: ({ id, body }: { id: string; body: Partial<RoadmapNode> }) =>
      updateRoadmapNode(id, body),
    onSuccess: (node) => {
      setSelectedNodeID(node.id)
      queryClient.invalidateQueries({
        queryKey: ['learning-roadmap', selectedLearningProjectID],
      })
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
      queryClient.invalidateQueries({
        queryKey: ['learning-roadmap', selectedLearningProjectID],
      })
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
      queryClient.invalidateQueries({
        queryKey: ['learning-roadmap', selectedLearningProjectID],
      })
    },
  })

  const optimizeRoadmapLayoutMutation = useMutation({
    mutationFn: optimizeRoadmapLayout,
    onSuccess: (optimizedRoadmap) => {
      queryClient.setQueryData(
        ['learning-roadmap', selectedLearningProjectID],
        optimizedRoadmap
      )
      queryClient.invalidateQueries({
        queryKey: ['learning-roadmap', selectedLearningProjectID],
      })
    },
  })

  const generateRoadmapMutation = useMutation({
    mutationFn: ({
      projectID,
      prompt,
    }: {
      projectID: string
      prompt: string
    }) => generateLearningRoadmap(projectID, { prompt }),
    onSuccess: (roadmap) => {
      setSelectedNodeID(roadmap.nodes[0]?.id ?? '')
      queryClient.invalidateQueries({
        queryKey: ['learning-roadmap', selectedLearningProjectID],
      })
    },
  })

  const searchResourcesMutation = useMutation({
    mutationFn: async ({
      nodeID,
      sources,
      query,
    }: {
      nodeID: string
      sources: string[]
      query: string
    }) => searchRoadmapNodeResources(nodeID, { sources, query }),
    onMutate: () => {
      setSelectedResourceCandidateIDs([])
    },
    onSuccess: (result) => {
      setResourcePickerNodeID(result.node_id)
      setResourceSearchQuery(result.query)
      setResourceCandidates(result.resources)
      setSelectedResourceCandidateIDs([])
    },
  })

  const addResourceMutation = useMutation({
    mutationFn: ({
      nodeID,
      title,
      url,
      summary,
    }: {
      nodeID: string
      title: string
      url: string
      summary?: string
    }) => addRoadmapNodeResource(nodeID, { title, url, summary }),
    onSuccess: () => {
      setManualResourceTitle('')
      setManualResourceURL('')
      queryClient.invalidateQueries({
        queryKey: ['learning-roadmap', selectedLearningProjectID],
      })
    },
  })

  const deleteResourceMutation = useMutation({
    mutationFn: (resourceID: string) => deleteRoadmapResource(resourceID),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: ['learning-roadmap', selectedLearningProjectID],
      })
    },
  })

  const addSelectedResourcesMutation = useMutation({
    mutationFn: async () => {
      if (!resourcePickerNodeID) return
      const selectedResources = resourceCandidates.filter((resource) =>
        selectedResourceCandidateIDs.includes(resourceCandidateKey(resource))
      )
      await Promise.all(
        selectedResources.map((resource) =>
          addRoadmapNodeResource(resourcePickerNodeID, {
            title: resource.title,
            url: resource.url,
            summary: resource.summary,
          })
        )
      )
    },
    onSuccess: () => {
      setResourcePickerNodeID('')
      setResourceCandidates([])
      setSelectedResourceCandidateIDs([])
      queryClient.invalidateQueries({
        queryKey: ['learning-roadmap', selectedLearningProjectID],
      })
    },
  })

  function startProjectCreate(type: TaskProject['type']) {
    setCreatingProjectType(type)
    setProjectType(type)
    setProjectName('')
    setProjectDescription('')
    setPendingDeleteProjectID('')
    if (type === 'learning') {
      setActiveTab('roadmap')
    } else if (type === 'regular') {
      setActiveTab('long')
    } else {
      setActiveTab('week')
    }
  }

  function cancelProjectCreate() {
    setCreatingProjectType('')
    setProjectName('')
    setProjectDescription('')
  }

  function selectProject(project: TaskProject) {
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
  }

  async function handleCreateProject(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const name = projectName.trim()
    if (!name) return
    await createProjectMutation.mutateAsync({
      name,
      type: creatingProjectType || projectType,
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
    const projectID = longProjectID || longProjects[0]?.id
    if (!title || !projectID) return
    await createTaskMutation.mutateAsync({
      title,
      project_id: projectID,
      horizon: 'long',
      scope: 'yearly',
    })
    setLongTitle('')
  }

  async function handleToggleTask(task: Task) {
    await updateTaskMutation.mutateAsync({
      id: task.id,
      body: { done: task.done ? 0 : 1 },
    })
  }

  async function handleUpdateLongTaskStatus(
    task: Task,
    status: LongTaskStatus
  ) {
    await updateTaskMutation.mutateAsync({ id: task.id, body: { status } })
  }

  async function handleUpdateTaskContent(task: Task, content: string) {
    await updateTaskMutation.mutateAsync({ id: task.id, body: { content } })
  }

  function handleToggleResourceCandidate(candidateID: string) {
    setSelectedResourceCandidateIDs((current) =>
      current.includes(candidateID)
        ? current.filter((id) => id !== candidateID)
        : [...current, candidateID]
    )
  }

  function handleCloseResourcePicker() {
    setResourcePickerNodeID('')
    setResourceSearchQuery('')
    setResourceCandidates([])
    setSelectedResourceCandidateIDs([])
  }

  function handleToggleArticleSearchSource(sourceID: string) {
    setArticleSearchSources((current) =>
      current.includes(sourceID)
        ? current.filter((id) => id !== sourceID)
        : [...current, sourceID]
    )
  }

  function handleAddArticleSearchSource(rawValue: string) {
    const source = normalizeCustomArticleSearchSource(rawValue)
    if (!source) return false
    setCustomArticleSearchSourceOptions((current) =>
      current.some((item) => item.id === source.id)
        ? current
        : [...current, source]
    )
    setArticleSearchSources((current) =>
      current.includes(source.id) ? current : [...current, source.id]
    )
    return true
  }

  function handleRemoveArticleSearchSource(sourceID: string) {
    setCustomArticleSearchSourceOptions((current) =>
      current.filter((source) => source.id !== sourceID)
    )
    setArticleSearchSources((current) =>
      current.filter((id) => id !== sourceID)
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

  function handleSelectRoadmapNode(nodeID: string) {
    setSelectedNodeID(nodeID)
    setIsNodeDialogOpen(false)
    handleCloseResourcePicker()
  }

  function handleOpenCreateRoadmapNode(parentID: string) {
    if (!parentID) return
    setCreateNodeParentID(parentID)
    setSelectedNodeID(parentID)
    setIsCreateNodeDialogOpen(true)
  }

  async function handleSaveRoadmapNode(
    nodeID: string,
    body: Partial<RoadmapNode>
  ) {
    await updateRoadmapNodeMutation.mutateAsync({ id: nodeID, body })
  }

  async function handleCreateRoadmapNode(
    body: Parameters<typeof createRoadmapNode>[1]
  ) {
    if (!roadmap?.id) return
    await createRoadmapNodeMutation.mutateAsync({ roadmapID: roadmap.id, body })
  }

  async function handleDeleteRoadmapNode(nodeID: string) {
    await deleteRoadmapNodeMutation.mutateAsync(nodeID)
  }

  async function handleAddRecurringTask(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const title = recurringTitle.trim()
    if (!title) return
    const recurrence: RecurrenceConfig = {
      start_date: recurringStartDate,
      frequency: recurringFrequency,
      interval: recurringInterval,
      weekdays: recurringFrequency === 'weekly' ? recurringWeekdays : [],
      month_days: recurringFrequency === 'monthly' ? recurringMonthDays : [],
      timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
    }
    if (recurringEndDate) {
      recurrence.end_date = recurringEndDate
    }
    await createTaskMutation.mutateAsync({
      title,
      project_id: recurringProjectID || activeProjectID || 'personal',
      execution_type: 'recurring',
      recurrence,
      horizon: 'week',
      scope: 'daily',
    })
    setRecurringTitle('')
  }

  async function handleToggleOccurrence(task: Task) {
    if (task.execution_type !== 'recurring' || !task.occurrence_date) return
    if (task.occurrence_status === 'done') {
      await reopenOccurrence(task.id, task.occurrence_date)
    } else {
      await completeOccurrence(task.id, task.occurrence_date)
    }
    queryClient.invalidateQueries({ queryKey: ['tasks', 'recurring'] })
    queryClient.invalidateQueries({ queryKey: ['today'] })
  }

  const toggleRecurringWeekday = (day: number) => {
    setRecurringWeekdays((prev) =>
      prev.includes(day) ? prev.filter((d) => d !== day) : [...prev, day]
    )
  }

  const toggleRecurringMonthDay = (day: number) => {
    setRecurringMonthDays((prev) =>
      prev.includes(day) ? prev.filter((d) => d !== day) : [...prev, day]
    )
  }

  async function handleCreateLinkedRoadmapTask(
    node: RoadmapNode,
    input: RoadmapLinkedTaskInput
  ) {
    if (!selectedLearningProjectID) return
    const plannedDate = input.plannedDate || todayDateInputValue()
    const baseTask = {
      title: input.title,
      content: input.content,
      project_id: selectedLearningProjectID,
      roadmap_node_id: node.id,
      horizon: 'week' as const,
      scope: 'daily' as const,
    }

    if (input.executionType === 'recurring') {
      await createTaskMutation.mutateAsync({
        ...baseTask,
        execution_type: 'recurring',
        recurrence: input.recurrence ?? {
          start_date: plannedDate,
          frequency: 'daily',
          interval: 1,
          weekdays: [],
          month_days: [],
          timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
        },
      })
      setSelectedNodeID(node.id)
      return
    }

    await createTaskMutation.mutateAsync({
      ...baseTask,
      planned_date: plannedDate,
      due: dateInputToUnix(plannedDate),
    })
    setSelectedNodeID(node.id)
  }

  const roadmap = roadmapQuery.data
  const selectedRoadmapNode = roadmap?.nodes.find(
    (node) => node.id === selectedNodeID
  )
  const activeRoadmapNode = selectedRoadmapNode ?? roadmap?.nodes[0]
  const activeRoadmapNodeID = activeRoadmapNode?.id ?? ''
  const createNodeParent = roadmap?.nodes.find(
    (node) => node.id === createNodeParentID
  )
  const selectedNodeTasks = useMemo(() => {
    if (!activeRoadmapNodeID) return []
    const taskByID = new Map<string, Task>()
    for (const task of [
      ...(weekTasksQuery.data?.tasks ?? []),
      ...(longTasksQuery.data?.tasks ?? []),
      ...(recurringTasksQuery.data?.tasks ?? []),
    ]) {
      if (task.roadmap_node_id === activeRoadmapNodeID) {
        taskByID.set(task.id, task)
      }
    }
    return [...taskByID.values()].sort(compareRoadmapLinkedTasks)
  }, [
    activeRoadmapNodeID,
    longTasksQuery.data?.tasks,
    recurringTasksQuery.data?.tasks,
    weekTasksQuery.data?.tasks,
  ])
  const resourcePickerNode = roadmap?.nodes.find(
    (node) => node.id === resourcePickerNodeID
  )
  const activeTaskCandidates = useMemo(() => {
    if (activeTab === 'week') return weekTasksQuery.data?.tasks ?? []
    if (activeTab === 'long') return longTasksQuery.data?.tasks ?? []
    if (activeTab === 'recurring') return recurringTasksQuery.data?.tasks ?? []
    if (activeTab === 'roadmap') return selectedNodeTasks
    return []
  }, [
    activeTab,
    longTasksQuery.data?.tasks,
    recurringTasksQuery.data?.tasks,
    selectedNodeTasks,
    weekTasksQuery.data?.tasks,
  ])
  const inspectorTask =
    activeTaskCandidates.find((task) => task.id === selectedTaskID) ??
    activeTaskCandidates[0]

  useEffect(() => {
    if (activeTaskCandidates.length === 0) {
      setSelectedTaskID('')
      return
    }
    if (!activeTaskCandidates.some((task) => task.id === selectedTaskID)) {
      setSelectedTaskID(activeTaskCandidates[0].id)
    }
  }, [activeTaskCandidates, selectedTaskID])

  useEffect(() => {
    if (!inspectorTask) {
      setTaskDetailDraft(emptyTaskDetailDraft)
      return
    }
    setTaskDetailDraft({
      title: inspectorTask.title,
      projectID: inspectorTask.project_id ?? '',
      plannedDate: getTaskPlannedDate(inspectorTask),
      status: normalizeLongTaskStatus(inspectorTask),
      content: inspectorTask.content ?? '',
    })
  }, [
    inspectorTask?.content,
    inspectorTask?.done,
    inspectorTask?.due,
    inspectorTask?.id,
    inspectorTask?.planned_date,
    inspectorTask?.project_id,
    inspectorTask?.status,
    inspectorTask?.title,
  ])

  async function handleSaveTaskDetail(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!inspectorTask) return
    const title = taskDetailDraft.title.trim()
    if (!title) return

    const body: Partial<Task> = {
      title,
      content: taskDetailDraft.content.trim(),
      project_id: taskDetailDraft.projectID || inspectorTask.project_id,
      status: taskDetailDraft.status,
    }
    if (inspectorTask.execution_type !== 'recurring') {
      body.planned_date = taskDetailDraft.plannedDate
      if (taskDetailDraft.plannedDate) {
        body.due = dateInputToUnix(taskDetailDraft.plannedDate)
      }
    }

    await updateTaskMutation.mutateAsync({ id: inspectorTask.id, body })
  }

  return (
    <div className="task-workspace">
      <aside className="task-project-panel">
        <div className="task-project-panel-title">
          <span>项目</span>
          <strong>{projects.length}</strong>
        </div>
        <div className="task-project-groups">
          {[
            { type: 'personal' as const, projects: weekProjects },
            { type: 'regular' as const, projects: longProjects },
            { type: 'learning' as const, projects: learningProjects },
          ].map(({ type, projects: groupProjects }) => {
            const meta = taskProjectGroupMeta[type]
            const isCreating = creatingProjectType === type
            return (
              <section
                className="task-project-group"
                data-testid={`task-project-group-${type}`}
                key={type}
              >
                <div className="task-project-group-heading">
                  <div className="task-project-group-title">
                    <h3>{meta.title}</h3>
                    <span>{meta.description}</span>
                  </div>
                  <div className="task-project-group-tools">
                    <span className="task-project-count">
                      {groupProjects.length} 个项目
                    </span>
                    <button
                      type="button"
                      aria-label={meta.createLabel}
                      onClick={() => startProjectCreate(type)}
                    >
                      +
                    </button>
                  </div>
                </div>

                {isCreating && (
                  <form
                    className="project-create-card"
                    onSubmit={handleCreateProject}
                  >
                    <label>
                      <span>{meta.nameLabel}</span>
                      <input
                        aria-label={meta.nameLabel}
                        value={projectName}
                        onChange={(event) => setProjectName(event.target.value)}
                        placeholder={meta.placeholder}
                      />
                    </label>
                    <label>
                      <span>{meta.descriptionLabel}</span>
                      <textarea
                        aria-label={meta.descriptionLabel}
                        value={projectDescription}
                        onChange={(event) =>
                          setProjectDescription(event.target.value)
                        }
                        placeholder="目标、背景或交付物"
                      />
                    </label>
                    <div className="project-create-actions">
                      <button
                        type="submit"
                        disabled={
                          !projectName.trim() || createProjectMutation.isPending
                        }
                      >
                        {meta.submitLabel}
                      </button>
                      <button type="button" onClick={cancelProjectCreate}>
                        取消
                      </button>
                    </div>
                  </form>
                )}

                {groupProjects.length === 0 ? (
                  <p className="task-project-empty">{meta.emptyCopy}</p>
                ) : (
                  <div className="task-project-list">
                    {groupProjects.map((project) => (
                      <div className="task-project-item" key={project.id}>
                        <button
                          type="button"
                          aria-label={`选择项目 ${project.name}`}
                          className={
                            project.id === activeProjectID
                              ? 'task-project-select is-active'
                              : 'task-project-select'
                          }
                          onClick={() => selectProject(project)}
                        >
                          <span className="task-project-name">
                            {project.name}
                          </span>
                          <small className="task-project-kind">
                            {taskProjectTypeLabels[project.type]}
                          </small>
                        </button>

                        {project.id !== 'personal' &&
                          (pendingDeleteProjectID === project.id ? (
                            <div className="task-project-delete-confirm">
                              <button
                                type="button"
                                aria-label={`确认删除 ${project.name}`}
                                disabled={deleteProjectMutation.isPending}
                                onClick={() =>
                                  deleteProjectMutation.mutate(project)
                                }
                              >
                                确认
                              </button>
                              <button
                                type="button"
                                aria-label={`取消删除 ${project.name}`}
                                onClick={() => setPendingDeleteProjectID('')}
                              >
                                取消
                              </button>
                            </div>
                          ) : (
                            <button
                              className="task-project-delete"
                              type="button"
                              aria-label={`删除项目 ${project.name}`}
                              title="删除项目"
                              onClick={() =>
                                setPendingDeleteProjectID(project.id)
                              }
                            >
                              ×
                            </button>
                          ))}
                      </div>
                    ))}
                  </div>
                )}
              </section>
            )
          })}
        </div>

        {activeProjectID &&
          projects.some((project) => project.id === activeProjectID) && (
            <section className="task-project-notes">
              <div className="task-project-notes-heading">
                <h4>项目笔记</h4>
                <span>{projectNotes.length}</span>
              </div>
              {projectNotes.length === 0 && (
                <p className="task-project-notes-empty">暂无笔记</p>
              )}
              <div className="task-project-note-list">
                {projectNotes.map((note) => (
                  <button
                    key={note.id}
                    type="button"
                    onClick={() =>
                      navigate(`/editor/${encodeURIComponent(note.id)}`)
                    }
                  >
                    <BookOpen aria-hidden="true" />
                    <span>
                      <strong>{note.title || '未命名笔记'}</strong>
                      <small>
                        {new Date(note.updated_at * 1000).toLocaleDateString(
                          'zh-CN'
                        )}
                      </small>
                    </span>
                  </button>
                ))}
              </div>
              <button
                type="button"
                className="task-project-note-create"
                onClick={async () => {
                  const note = await createNote({
                    title: '未命名笔记',
                    body: '',
                    folder_id: '__uncategorized',
                    tags: '[]',
                    project_ids: activeProjectID
                      ? [activeProjectID]
                      : undefined,
                  })
                  navigate(`/editor/${encodeURIComponent(note.id)}`)
                }}
              >
                <Plus aria-hidden="true" />
                新建项目笔记
              </button>
            </section>
          )}
      </aside>

      <section className="task-main-panel">
        <div className="panel-heading">
          <div>
            <h2>{activeTab === 'week' ? '今天' : tabLabels[activeTab]}</h2>
            <p>
              {activeTab === 'week'
                ? '把短期行动安排到今天和本周'
                : activeTab === 'long'
                  ? '沉淀阶段目标，持续推进长期事项'
                  : activeTab === 'recurring'
                    ? '管理会反复出现的节奏任务'
                    : '从学习项目生成路线图和行动节点'}
            </p>
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
            projects={weekProjects}
            selectedProjectID={weekProjectID}
            selectedTaskID={inspectorTask?.id ?? ''}
            title={weekTitle}
            date={weekDate}
            isPending={createTaskMutation.isPending}
            onProjectChange={setWeekProjectID}
            onTitleChange={setWeekTitle}
            onDateChange={setWeekDate}
            onSubmit={handleAddWeekTask}
            onSelectTask={setSelectedTaskID}
            onToggle={handleToggleTask}
          />
        )}

        {activeTab === 'long' && (
          <LongTaskView
            tasks={longTasksQuery.data?.tasks ?? []}
            projects={longProjects}
            selectedProjectID={longProjectID}
            selectedTaskID={inspectorTask?.id ?? ''}
            title={longTitle}
            isPending={createTaskMutation.isPending}
            onProjectChange={setLongProjectID}
            onTitleChange={setLongTitle}
            onSubmit={handleAddLongTask}
            onStartCreateProject={() => startProjectCreate('regular')}
            isCreatingProject={creatingProjectType === 'regular'}
            onSelectTask={setSelectedTaskID}
            onToggle={handleToggleTask}
            onStatusChange={handleUpdateLongTaskStatus}
            isUpdating={updateTaskMutation.isPending}
          />
        )}

        {activeTab === 'recurring' && (
          <RecurringTaskView
            tasks={recurringTasksQuery.data?.tasks ?? []}
            selectedTaskID={inspectorTask?.id ?? ''}
            title={recurringTitle}
            frequency={recurringFrequency}
            interval={recurringInterval}
            weekdays={recurringWeekdays}
            monthDays={recurringMonthDays}
            startDate={recurringStartDate}
            endDate={recurringEndDate}
            projectID={recurringProjectID}
            projects={projects}
            isPending={createTaskMutation.isPending}
            onTitleChange={setRecurringTitle}
            onFrequencyChange={setRecurringFrequency}
            onIntervalChange={(v) => setRecurringInterval(Number(v))}
            onToggleWeekday={toggleRecurringWeekday}
            onToggleMonthDay={toggleRecurringMonthDay}
            onStartDateChange={setRecurringStartDate}
            onEndDateChange={setRecurringEndDate}
            onProjectChange={setRecurringProjectID}
            onSubmit={handleAddRecurringTask}
            onSelectTask={setSelectedTaskID}
            onToggle={handleToggleOccurrence}
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
            tasks={selectedNodeTasks}
            selectedTaskID={inspectorTask?.id ?? ''}
            manualResourceTitle={manualResourceTitle}
            manualResourceURL={manualResourceURL}
            articleSearchSources={articleSearchSources}
            articleSearchSourceOptions={availableArticleSearchSourceOptions}
            isGenerating={generateRoadmapMutation.isPending}
            isSearching={searchResourcesMutation.isPending}
            isAddingResource={addResourceMutation.isPending}
            deletingResourceID={
              deleteResourceMutation.isPending
                ? deleteResourceMutation.variables
                : ''
            }
            isOptimizingLayout={optimizeRoadmapLayoutMutation.isPending}
            isCreatingNode={createRoadmapNodeMutation.isPending}
            onSelectProject={(projectID) => {
              setSelectedLearningProjectID(projectID)
              setSelectedNodeID('')
              setIsNodeDialogOpen(false)
              setIsCreateNodeDialogOpen(false)
              setCreateNodeParentID('')
            }}
            onSelectNode={handleSelectRoadmapNode}
            onSelectTask={setSelectedTaskID}
            onEditNode={handleOpenRoadmapNode}
            onGenerate={(prompt) =>
              selectedLearningProjectID &&
              generateRoadmapMutation.mutate({
                projectID: selectedLearningProjectID,
                prompt,
              })
            }
            onStartCreateProject={() => startProjectCreate('learning')}
            isCreatingProject={creatingProjectType === 'learning'}
            onOpenCreateNode={handleOpenCreateRoadmapNode}
            onOptimizeLayout={(roadmapID) =>
              optimizeRoadmapLayoutMutation.mutate(roadmapID)
            }
            onSearchResources={(nodeID, query) =>
              searchResourcesMutation.mutate({
                nodeID,
                sources: articleSearchSources,
                query,
              })
            }
            onToggleArticleSearchSource={handleToggleArticleSearchSource}
            onAddArticleSearchSource={handleAddArticleSearchSource}
            onRemoveArticleSearchSource={handleRemoveArticleSearchSource}
            onManualTitleChange={setManualResourceTitle}
            onManualURLChange={setManualResourceURL}
            onAddResource={(nodeID) => {
              if (!manualResourceTitle.trim() || !manualResourceURL.trim())
                return
              addResourceMutation.mutate({
                nodeID,
                title: manualResourceTitle.trim(),
                url: manualResourceURL.trim(),
              })
            }}
            onDeleteResource={(resourceID) =>
              deleteResourceMutation.mutateAsync(resourceID)
            }
          />
        )}
      </section>

      <TaskDetailPanel
        task={inspectorTask}
        projects={projects}
        draft={taskDetailDraft}
        isSaving={updateTaskMutation.isPending}
        onDraftChange={setTaskDetailDraft}
        onSubmit={handleSaveTaskDetail}
        emptyTitle={
          activeTab === 'roadmap' ? '当前节点暂无关联任务' : undefined
        }
        emptyDescription={
          activeTab === 'roadmap'
            ? '创建关联任务后，可在这里编辑标题、日期、状态和备注。'
            : undefined
        }
        emptyActionLabel={
          activeTab === 'roadmap' && activeRoadmapNodeID
            ? '创建当前节点任务'
            : undefined
        }
        onEmptyAction={
          activeTab === 'roadmap' && activeRoadmapNodeID
            ? () => handleOpenRoadmapNode(activeRoadmapNodeID)
            : undefined
        }
      />

      <RoadmapResourcePickerDialog
        nodeTitle={resourcePickerNode?.title ?? ''}
        query={resourceSearchQuery}
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
          onCreateLinkedTask={handleCreateLinkedRoadmapTask}
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
  selectedTaskID,
  title,
  date,
  isPending,
  onProjectChange,
  onTitleChange,
  onDateChange,
  onSubmit,
  onSelectTask,
  onToggle,
}: {
  tasks: Task[]
  projects: TaskProject[]
  selectedProjectID: string
  selectedTaskID: string
  title: string
  date: string
  isPending: boolean
  onProjectChange: (value: string) => void
  onTitleChange: (value: string) => void
  onDateChange: (value: string) => void
  onSubmit: (event: FormEvent<HTMLFormElement>) => void
  onSelectTask: (id: string) => void
  onToggle: (task: Task) => void
}) {
  const tasksByDate = useMemo(() => {
    const grouped = new Map<string, Task[]>()
    for (const task of tasks) {
      const key = task.planned_date || '未安排'
      grouped.set(key, [...(grouped.get(key) ?? []), task])
    }
    return [...grouped.entries()]
      .map(
        ([date, dayTasks]) =>
          [date, [...dayTasks].sort(compareWeekTasks)] as const
      )
      .sort(([a], [b]) => a.localeCompare(b))
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
        <select
          aria-label="任务项目"
          value={selectedProjectID}
          onChange={(event) => onProjectChange(event.target.value)}
        >
          {projects.map((project) => (
            <option key={project.id} value={project.id}>
              {project.name}
            </option>
          ))}
        </select>
        <input
          aria-label="任务日期"
          type="date"
          value={date}
          onChange={(event) => onDateChange(event.target.value)}
        />
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
                <TaskRow
                  key={task.id}
                  task={task}
                  isSelected={task.id === selectedTaskID}
                  onSelect={onSelectTask}
                  onToggle={() => onToggle(task)}
                />
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
  selectedTaskID,
  title,
  isPending,
  onProjectChange,
  onTitleChange,
  onSubmit,
  onStartCreateProject,
  isCreatingProject,
  onSelectTask,
  onToggle,
  onStatusChange,
  isUpdating,
}: {
  tasks: Task[]
  projects: TaskProject[]
  selectedProjectID: string
  selectedTaskID: string
  title: string
  isPending: boolean
  onProjectChange: (value: string) => void
  onTitleChange: (value: string) => void
  onSubmit: (event: FormEvent<HTMLFormElement>) => void
  onStartCreateProject: () => void
  isCreatingProject: boolean
  onSelectTask: (id: string) => void
  onToggle: (task: Task) => void
  onStatusChange: (task: Task, status: LongTaskStatus) => void
  isUpdating: boolean
}) {
  const tasksByStatus = useMemo(() => {
    const grouped = new Map<LongTaskStatus, Task[]>(
      longTaskStatusOrder.map((status) => [status, []])
    )
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
          <select
            aria-label="任务项目"
            value={selectedProjectID}
            onChange={(event) => onProjectChange(event.target.value)}
          >
            {projects.map((project) => (
              <option key={project.id} value={project.id}>
                {project.name}
              </option>
            ))}
          </select>
          <button
            type="submit"
            disabled={!title.trim() || !selectedProjectID || isPending}
          >
            添加长期任务
          </button>
        </form>
      )}

      {projects.length === 0 ? (
        <div className="task-empty-action">
          <strong>还没有长期项目</strong>
          <p>
            长期任务需要先归到一个长期项目里，创建后就能添加年度或阶段目标。
          </p>
          <button
            type="button"
            onClick={onStartCreateProject}
            disabled={isCreatingProject}
          >
            {isCreatingProject ? '正在填写长期项目' : '创建长期项目'}
          </button>
        </div>
      ) : tasksByStatus.length === 0 ? (
        <p className="empty-copy">还没有长期任务</p>
      ) : (
        <div
          className="long-task-status-groups"
          data-testid="long-task-status-groups"
        >
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
                    isSelected={task.id === selectedTaskID}
                    isUpdating={isUpdating}
                    onSelect={() => onSelectTask(task.id)}
                    onToggle={() => onToggle(task)}
                    onStatusChange={(nextStatus) =>
                      onStatusChange(task, nextStatus)
                    }
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
  isSelected,
  isUpdating,
  onSelect,
  onToggle,
  onStatusChange,
}: {
  task: Task
  isSelected: boolean
  isUpdating: boolean
  onSelect: () => void
  onToggle: () => void
  onStatusChange: (status: LongTaskStatus) => void
}) {
  const status = normalizeLongTaskStatus(task)
  const project = task.project || '未命名项目'
  const taskColor = getTaskColor(task.id, task.color)

  return (
    <div
      className={`task-row long-task-row long-task-row-${status} ${isSelected ? 'is-selected' : ''}`}
    >
      <span
        className="task-color-dot"
        style={{ backgroundColor: taskColor }}
        aria-label={`任务颜色：${task.title}`}
      />
      <button
        type="button"
        className="long-task-done-toggle"
        aria-label={task.done ? `重新打开 ${task.title}` : `完成 ${task.title}`}
        onClick={onToggle}
      >
        {status === 'done' ? '✓' : ''}
      </button>
      <button type="button" className="long-task-copy" onClick={onSelect}>
        <strong className={status === 'done' ? 'is-done' : ''}>
          {task.title}
        </strong>
        <small>
          {project} · 最近进展 {formatLongTaskUpdatedAt(task.updated_at)}
        </small>
      </button>
      <select
        className="long-task-status-select"
        aria-label={`更新长期任务状态：${task.title}`}
        value={status}
        disabled={isUpdating}
        onChange={(event) =>
          onStatusChange(event.target.value as LongTaskStatus)
        }
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

function TaskDetailPanel({
  task,
  projects,
  draft,
  isSaving,
  onDraftChange,
  onSubmit,
  emptyTitle,
  emptyDescription,
  emptyActionLabel,
  onEmptyAction,
}: {
  task?: Task
  projects: TaskProject[]
  draft: TaskDetailDraft
  isSaving: boolean
  onDraftChange: (draft: TaskDetailDraft) => void
  onSubmit: (event: FormEvent<HTMLFormElement>) => void
  emptyTitle?: string
  emptyDescription?: string
  emptyActionLabel?: string
  onEmptyAction?: () => void
}) {
  const canEdit = Boolean(task)
  const isRecurring = task?.execution_type === 'recurring'
  const isRoadmapTask = Boolean(task?.roadmap_node_id)

  function patchDraft(patch: Partial<TaskDetailDraft>) {
    onDraftChange({ ...draft, ...patch })
  }

  return (
    <aside className="surface-panel task-detail-panel">
      <div className="panel-heading is-compact task-detail-heading">
        <div>
          <h2>任务详情</h2>
          <p>
            {canEdit
              ? isRoadmapTask
                ? '编辑当前 Roadmap 节点的关联任务'
                : '修改任务内容、状态和下一步动作'
              : '选择或创建一个任务后在这里编辑'}
          </p>
        </div>
        {canEdit && (
          <span
            className={`task-detail-state task-detail-state-${draft.status}`}
          >
            {longTaskStatusLabels[draft.status]}
          </span>
        )}
      </div>

      {!canEdit ? (
        <div className="task-detail-empty">
          <strong>{emptyTitle || '暂无可编辑任务'}</strong>
          <p>
            {emptyDescription ||
              '当前视图还没有任务。先在中间列表添加一条，再回到这里补充详情。'}
          </p>
          {emptyActionLabel && onEmptyAction && (
            <button type="button" onClick={onEmptyAction}>
              {emptyActionLabel}
            </button>
          )}
        </div>
      ) : (
        <form className="task-detail-form" onSubmit={onSubmit}>
          <div className="task-detail-section">
            <span className="task-detail-section-title">基本信息</span>
            <label>
              <span>标题</span>
              <input
                aria-label="任务详情标题"
                value={draft.title}
                onChange={(event) => patchDraft({ title: event.target.value })}
              />
            </label>
            <label>
              <span>项目</span>
              <select
                aria-label="任务详情项目"
                value={draft.projectID}
                onChange={(event) =>
                  patchDraft({ projectID: event.target.value })
                }
              >
                {!draft.projectID && <option value="">未指定项目</option>}
                {projects.map((project) => (
                  <option key={project.id} value={project.id}>
                    {project.name}
                  </option>
                ))}
              </select>
            </label>
            {!isRecurring && (
              <label>
                <span>日期</span>
                <input
                  aria-label="任务详情日期"
                  type="date"
                  value={draft.plannedDate}
                  onChange={(event) =>
                    patchDraft({ plannedDate: event.target.value })
                  }
                />
              </label>
            )}
          </div>
          <div className="task-detail-section">
            <span className="task-detail-section-title">执行状态</span>
            <div
              className="task-detail-status"
              role="group"
              aria-label="任务详情状态"
            >
              {taskDetailStatusOrder.map((status) => (
                <button
                  key={status}
                  type="button"
                  className={draft.status === status ? 'is-active' : ''}
                  onClick={() => patchDraft({ status })}
                >
                  {longTaskStatusLabels[status]}
                </button>
              ))}
            </div>
          </div>
          <div className="task-detail-section">
            <span className="task-detail-section-title">下一步</span>
            <label>
              <span>备注</span>
              <textarea
                aria-label="任务详情备注"
                value={draft.content}
                onChange={(event) =>
                  patchDraft({ content: event.target.value })
                }
                placeholder="写下下一步动作、阻塞原因或补充说明"
              />
            </label>
          </div>
          <div className="task-detail-actions">
            <button
              type="submit"
              className="primary-action"
              disabled={!draft.title.trim() || isSaving}
            >
              {isSaving ? '保存中...' : '保存修改'}
            </button>
          </div>
        </form>
      )}
    </aside>
  )
}

function normalizeLongTaskStatus(task: Task): LongTaskStatus {
  if (task.done === 1 || task.status === 'done') return 'done'
  if (task.status === 'active') return 'active'
  if (task.status === 'blocked') return 'blocked'
  return 'open'
}

function compareWeekTasks(a: Task, b: Task) {
  const aDone = a.done === 1 || a.status === 'done'
  const bDone = b.done === 1 || b.status === 'done'
  if (aDone !== bDone) return aDone ? 1 : -1

  const aTime = a.updated_at || a.created_at || 0
  const bTime = b.updated_at || b.created_at || 0
  if (aTime !== bTime) return bTime - aTime

  return a.title.localeCompare(b.title, 'zh-CN')
}

function formatLongTaskUpdatedAt(updatedAt: number) {
  if (!updatedAt) return '未知'
  return new Date(updatedAt * 1000).toLocaleDateString('zh-CN', {
    month: 'short',
    day: 'numeric',
  })
}

function getTaskPlannedDate(task: Task) {
  if (task.planned_date) return task.planned_date
  if (!task.due) return ''
  return dateToInputValue(new Date(task.due * 1000))
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
  tasks,
  selectedTaskID,
  manualResourceTitle,
  manualResourceURL,
  articleSearchSources,
  articleSearchSourceOptions,
  isGenerating,
  isSearching,
  isAddingResource,
  deletingResourceID,
  isOptimizingLayout,
  isCreatingNode,
  onSelectProject,
  onSelectNode,
  onSelectTask,
  onEditNode,
  onGenerate,
  onStartCreateProject,
  isCreatingProject,
  onOpenCreateNode,
  onOptimizeLayout,
  onSearchResources,
  onToggleArticleSearchSource,
  onAddArticleSearchSource,
  onRemoveArticleSearchSource,
  onManualTitleChange,
  onManualURLChange,
  onAddResource,
  onDeleteResource,
}: {
  projects: TaskProject[]
  selectedProjectID: string
  selectedProject?: TaskProject
  roadmap: LearningRoadmap | null | undefined
  isLoading: boolean
  selectedNodeID: string
  tasks: Task[]
  selectedTaskID: string
  manualResourceTitle: string
  manualResourceURL: string
  articleSearchSources: string[]
  articleSearchSourceOptions: ArticleSearchSourceOption[]
  isGenerating: boolean
  isSearching: boolean
  isAddingResource: boolean
  deletingResourceID: string
  isOptimizingLayout: boolean
  isCreatingNode: boolean
  onSelectProject: (value: string) => void
  onSelectNode: (value: string) => void
  onSelectTask: (taskID: string) => void
  onEditNode: (value: string) => void
  onGenerate: (prompt: string) => void
  onStartCreateProject: () => void
  isCreatingProject: boolean
  onOpenCreateNode: (nodeID: string) => void
  onOptimizeLayout: (roadmapID: string) => void
  onSearchResources: (nodeID: string, query: string) => void
  onToggleArticleSearchSource: (sourceID: string) => void
  onAddArticleSearchSource: (value: string) => boolean
  onRemoveArticleSearchSource: (sourceID: string) => void
  onManualTitleChange: (value: string) => void
  onManualURLChange: (value: string) => void
  onAddResource: (nodeID: string) => void
  onDeleteResource: (resourceID: string) => Promise<void>
}) {
  const selectedNode =
    roadmap?.nodes.find((node) => node.id === selectedNodeID) ??
    roadmap?.nodes[0]
  const selectedStepNumber = selectedNode
    ? (roadmap?.nodes.findIndex((node) => node.id === selectedNode.id) ?? 0) + 1
    : 0
  const completedNodeCount =
    roadmap?.nodes.filter((node) => node.status === 'done').length ?? 0
  const roadmapProgress = roadmap?.nodes.length
    ? Math.round((completedNodeCount / roadmap.nodes.length) * 100)
    : 0
  const [isFullscreen, setIsFullscreen] = useState(false)
  const [isGenerationPromptOpen, setIsGenerationPromptOpen] = useState(false)
  const [generationPrompts, setGenerationPrompts] = useState<
    Record<string, string>
  >(loadRoadmapGenerationPrompts)
  const defaultGenerationPrompt =
    buildDefaultRoadmapGenerationPrompt(selectedProject)
  const generationPrompt = selectedProjectID
    ? (generationPrompts[selectedProjectID] ?? defaultGenerationPrompt)
    : ''

  useEffect(() => {
    window.localStorage.setItem(
      roadmapGenerationPromptsStorageKey,
      JSON.stringify(generationPrompts)
    )
  }, [generationPrompts])

  function handleGenerationPromptChange(value: string) {
    if (!selectedProjectID) return
    setGenerationPrompts((current) => ({
      ...current,
      [selectedProjectID]: value,
    }))
  }

  function handleRestoreGenerationPrompt() {
    if (!selectedProjectID) return
    setGenerationPrompts((current) => {
      const next = { ...current }
      delete next[selectedProjectID]
      return next
    })
  }

  useEffect(() => {
    if (!isFullscreen) return
    const previousOverflow = document.body.style.overflow
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setIsFullscreen(false)
    }
    document.body.style.overflow = 'hidden'
    window.addEventListener('keydown', handleKeyDown)
    return () => {
      document.body.style.overflow = previousOverflow
      window.removeEventListener('keydown', handleKeyDown)
    }
  }, [isFullscreen])

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
        <div className="roadmap-generate-actions">
          <button
            className="roadmap-generation-prompt-toggle"
            type="button"
            aria-expanded={isGenerationPromptOpen}
            onClick={() => setIsGenerationPromptOpen((current) => !current)}
            disabled={!selectedProjectID}
          >
            编辑生成提示词
          </button>
          <button
            className="roadmap-generate-button"
            type="button"
            onClick={() => onGenerate(generationPrompt.trim())}
            disabled={
              !selectedProjectID || isGenerating || !generationPrompt.trim()
            }
          >
            <WandSparkles aria-hidden="true" />
            {isGenerating
              ? '正在生成完整路径...'
              : roadmap
                ? '重新生成完整路径'
                : '生成完整路径'}
          </button>
        </div>
        {selectedProject && (
          <span className="roadmap-goal">
            {selectedProject.description || selectedProject.name}
          </span>
        )}
      </div>

      {selectedProject && isGenerationPromptOpen && (
        <section
          className="roadmap-generation-prompt-editor"
          aria-label="完整路径生成提示词"
        >
          <div>
            <label htmlFor="roadmap-generation-prompt">
              完整路径生成提示词
            </label>
            {generationPrompt !== defaultGenerationPrompt && (
              <button type="button" onClick={handleRestoreGenerationPrompt}>
                恢复默认
              </button>
            )}
          </div>
          <textarea
            id="roadmap-generation-prompt"
            value={generationPrompt}
            onChange={(event) =>
              handleGenerationPromptChange(event.target.value)
            }
            maxLength={maxRoadmapGenerationPromptLength}
            rows={4}
          />
          <small>
            <span>
              这里填写你对完整学习路径的额外要求；系统仍会保留节点结构和输出格式约束。
            </span>
            <span>
              {generationPrompt.length} / {maxRoadmapGenerationPromptLength}
            </span>
          </small>
        </section>
      )}

      {roadmap && (
        <section className="roadmap-overview" aria-label="Roadmap 进度">
          <div>
            <span>完整学习路径</span>
            <strong>{roadmap.title}</strong>
            <p>{roadmap.goal}</p>
          </div>
          <div className="roadmap-overview-progress">
            <strong>
              {completedNodeCount} / {roadmap.nodes.length}
            </strong>
            <span>已完成 {roadmapProgress}%</span>
            <div aria-hidden="true">
              <i style={{ width: `${roadmapProgress}%` }} />
            </div>
          </div>
        </section>
      )}

      {projects.length === 0 ? (
        <div className="task-empty-action">
          <strong>还没有学习项目</strong>
          <p>学习 Roadmap 需要先绑定一个学习项目，创建后就能生成路线图。</p>
          <button
            type="button"
            onClick={onStartCreateProject}
            disabled={isCreatingProject}
          >
            {isCreatingProject ? '正在填写学习项目' : '创建学习项目'}
          </button>
        </div>
      ) : isLoading ? (
        <p className="empty-copy">正在加载 Roadmap</p>
      ) : roadmap ? (
        <div
          className={`roadmap-content${isFullscreen ? ' is-fullscreen' : ''}`}
        >
          <RoadmapCanvas
            roadmap={roadmap}
            selectedNodeID={selectedNode?.id ?? ''}
            isFullscreen={isFullscreen}
            isOptimizingLayout={isOptimizingLayout}
            isCreatingNode={isCreatingNode}
            onSelectNode={onSelectNode}
            onOpenCreateNode={onOpenCreateNode}
            onOptimizeLayout={onOptimizeLayout}
            onToggleFullscreen={() => setIsFullscreen((current) => !current)}
          />
          <RoadmapInspector
            node={selectedNode}
            stepNumber={selectedStepNumber}
            tasks={tasks}
            selectedTaskID={selectedTaskID}
            onEditNode={onEditNode}
            onSelectTask={onSelectTask}
            manualTitle={manualResourceTitle}
            manualURL={manualResourceURL}
            articleSearchSources={articleSearchSources}
            articleSearchSourceOptions={articleSearchSourceOptions}
            isSearching={isSearching}
            isAddingResource={isAddingResource}
            deletingResourceID={deletingResourceID}
            onSearchResources={onSearchResources}
            onToggleArticleSearchSource={onToggleArticleSearchSource}
            onAddArticleSearchSource={onAddArticleSearchSource}
            onRemoveArticleSearchSource={onRemoveArticleSearchSource}
            onManualTitleChange={onManualTitleChange}
            onManualURLChange={onManualURLChange}
            onAddResource={onAddResource}
            onDeleteResource={onDeleteResource}
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
  isFullscreen,
  isOptimizingLayout,
  isCreatingNode,
  onSelectNode,
  onOpenCreateNode,
  onOptimizeLayout,
  onToggleFullscreen,
}: {
  roadmap: LearningRoadmap
  selectedNodeID: string
  isFullscreen: boolean
  isOptimizingLayout: boolean
  isCreatingNode: boolean
  onSelectNode: (id: string) => void
  onOpenCreateNode: (nodeID: string) => void
  onOptimizeLayout: (roadmapID: string) => void
  onToggleFullscreen: () => void
}) {
  const [nodes, setNodes, onNodesChange] = useNodesState<Node<RoadmapNodeData>>(
    []
  )
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([])
  const [isSaving, setIsSaving] = useState(false)
  const nodeTypes = useMemo(
    () => ({ roadmapNode: RoadmapFlowNode as ComponentType<NodeProps> }),
    []
  )

  useEffect(() => {
    setNodes(
      roadmap.nodes.map((node, index) => {
        const row = Math.floor(index / 3)
        const indexInRow = index % 3
        return {
          id: node.id,
          type: 'roadmapNode',
          position: { x: node.x, y: node.y },
          data: {
            node,
            stepNumber: index + 1,
            targetPosition:
              indexInRow === 0
                ? Position.Top
                : row % 2 === 0
                  ? Position.Left
                  : Position.Right,
            sourcePosition:
              indexInRow === 2
                ? Position.Bottom
                : row % 2 === 0
                  ? Position.Right
                  : Position.Left,
            onOpen: onSelectNode,
            onCreateAfter: onOpenCreateNode,
            isCreatingNode,
          },
          selected: node.id === selectedNodeID,
        }
      })
    )
    setEdges(
      roadmap.edges.map((edge) => ({
        id: edge.id,
        source: edge.source_node_id,
        target: edge.target_node_id,
        type: 'smoothstep',
        animated: false,
        markerEnd: { type: MarkerType.ArrowClosed, color: '#bf6b23' },
        style: {
          stroke: '#bf6b23',
          strokeWidth: 2.1,
        },
      }))
    )
  }, [
    isCreatingNode,
    onOpenCreateNode,
    onSelectNode,
    roadmap.edges,
    roadmap.nodes,
    selectedNodeID,
    setEdges,
    setNodes,
  ])

  async function handleSaveLayout() {
    setIsSaving(true)
    try {
      await saveRoadmapLayout(
        roadmap.id,
        nodes.map((node) => ({
          id: node.id,
          x: node.position.x,
          y: node.position.y,
        }))
      )
    } finally {
      setIsSaving(false)
    }
  }

  return (
    <div className="roadmap-canvas-shell" data-testid="roadmap-canvas">
      <div className="roadmap-canvas-toolbar">
        <div>
          <strong>{roadmap.nodes.length} 步完整路径</strong>
          <span>
            已完成{' '}
            {roadmap.nodes.filter((node) => node.status === 'done').length}
            <i aria-hidden="true" />
            进行中{' '}
            {roadmap.nodes.filter((node) => node.status === 'active').length}
          </span>
        </div>
        <div className="roadmap-canvas-actions">
          <button
            type="button"
            onClick={handleSaveLayout}
            disabled={isSaving}
            aria-label="保存布局"
            title="保存布局"
          >
            <Save aria-hidden="true" />
            <span>{isSaving ? '保存中...' : '保存布局'}</span>
          </button>
          <button
            type="button"
            onClick={() => onOptimizeLayout(roadmap.id)}
            disabled={isOptimizingLayout}
            aria-label="自动排列"
            title="自动排列"
          >
            <WandSparkles aria-hidden="true" />
            <span>{isOptimizingLayout ? '排列中...' : '自动排列'}</span>
          </button>
          <button
            type="button"
            onClick={onToggleFullscreen}
            aria-label={isFullscreen ? '退出全屏编辑' : '进入全屏编辑'}
            title={isFullscreen ? '退出全屏编辑' : '进入全屏编辑'}
          >
            {isFullscreen ? (
              <Minimize2 aria-hidden="true" />
            ) : (
              <Maximize2 aria-hidden="true" />
            )}
            <span>{isFullscreen ? '退出全屏' : '全屏编辑'}</span>
          </button>
        </div>
      </div>
      <div className="roadmap-flow-stage">
        <ReactFlow
          nodes={nodes}
          edges={edges}
          nodeTypes={nodeTypes}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          fitView
          fitViewOptions={{ padding: 0.16 }}
          minZoom={0.32}
          maxZoom={1.8}
        >
          <Background gap={28} size={1} color="#dfd4c4" />
          <MiniMap pannable zoomable nodeStrokeWidth={2} />
          <Controls />
        </ReactFlow>
      </div>
    </div>
  )
}

function RoadmapFlowNode(props: NodeProps) {
  const {
    node,
    stepNumber,
    targetPosition,
    sourcePosition,
    onOpen,
    onCreateAfter,
    isCreatingNode,
  } = props.data as RoadmapNodeData

  return (
    <div
      role="button"
      tabIndex={0}
      data-testid="roadmap-node"
      className={`roadmap-node roadmap-node-type-${node.type} roadmap-node-status-${node.status}${props.selected ? ' is-selected' : ''}`}
      onClick={() => onOpen(node.id)}
      onKeyDown={(event) => {
        if (event.key === 'Enter' || event.key === ' ') onOpen(node.id)
      }}
    >
      <Handle type="target" position={targetPosition} />
      <div className="roadmap-node-meta">
        <span>第 {stepNumber} 步</span>
        <small>{nodeTypeLabels[node.type]}</small>
      </div>
      <strong>{node.title}</strong>
      <span className={`roadmap-node-status status-${node.status}`}>
        {nodeStatusLabels[node.status]}
      </span>
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
        <Plus aria-hidden="true" />
      </button>
      <Handle type="source" position={sourcePosition} />
    </div>
  )
}

function RoadmapInspector({
  node,
  stepNumber,
  tasks,
  selectedTaskID,
  onEditNode,
  onSelectTask,
  manualTitle,
  manualURL,
  articleSearchSources,
  articleSearchSourceOptions,
  isSearching,
  isAddingResource,
  deletingResourceID,
  onSearchResources,
  onToggleArticleSearchSource,
  onAddArticleSearchSource,
  onRemoveArticleSearchSource,
  onManualTitleChange,
  onManualURLChange,
  onAddResource,
  onDeleteResource,
}: {
  node?: RoadmapNode
  stepNumber: number
  tasks: Task[]
  selectedTaskID: string
  onEditNode: (nodeID: string) => void
  onSelectTask: (taskID: string) => void
  manualTitle: string
  manualURL: string
  articleSearchSources: string[]
  articleSearchSourceOptions: ArticleSearchSourceOption[]
  isSearching: boolean
  isAddingResource: boolean
  deletingResourceID: string
  onSearchResources: (nodeID: string, query: string) => void
  onToggleArticleSearchSource: (sourceID: string) => void
  onAddArticleSearchSource: (value: string) => boolean
  onRemoveArticleSearchSource: (sourceID: string) => void
  onManualTitleChange: (value: string) => void
  onManualURLChange: (value: string) => void
  onAddResource: (nodeID: string) => void
  onDeleteResource: (resourceID: string) => Promise<void>
}) {
  const [pendingDeleteResourceID, setPendingDeleteResourceID] = useState('')
  const defaultSearchPrompt = buildRoadmapNodeSearchPrompt(node)
  const [searchPrompt, setSearchPrompt] = useState(defaultSearchPrompt)
  const [customSourceDraft, setCustomSourceDraft] = useState('')
  const [customSourceError, setCustomSourceError] = useState('')

  useEffect(() => {
    setPendingDeleteResourceID('')
    setSearchPrompt(defaultSearchPrompt)
    setCustomSourceError('')
  }, [node?.id, defaultSearchPrompt])

  if (!node) {
    return (
      <aside className="roadmap-inspector">
        <p className="empty-copy">选择一个节点查看详情</p>
      </aside>
    )
  }

  return (
    <aside className="roadmap-inspector">
      <div className="roadmap-inspector-heading">
        <div>
          <span>
            第 {stepNumber} 步 · {nodeTypeLabels[node.type]}
          </span>
          <span className={`roadmap-inspector-status status-${node.status}`}>
            {nodeStatusLabels[node.status]}
          </span>
        </div>
        <h2>{node.title}</h2>
        <p>{node.description}</p>
        <button
          className="roadmap-inspector-edit-button"
          type="button"
          onClick={() => onEditNode(node.id)}
        >
          编辑节点与任务
        </button>
      </div>

      <div className="inspector-section">
        <span>交付物</span>
        <p>{node.deliverable || '暂无交付物'}</p>
      </div>
      <div className="inspector-section">
        <span>验收标准</span>
        <p>{node.acceptance_criteria || '暂无验收标准'}</p>
      </div>

      <div className="roadmap-linked-task-section">
        <div className="roadmap-linked-task-heading">
          <strong>关联任务</strong>
          <span>{tasks.length}</span>
        </div>
        {tasks.length === 0 ? (
          <p className="roadmap-linked-task-empty">
            当前节点还没有关联任务，可点击“编辑节点与任务”创建。
          </p>
        ) : (
          <div className="roadmap-linked-task-list">
            {tasks.map((task, index) => {
              const title = formatRoadmapTaskTitle(task.title, index + 1)
              const plannedDate = getTaskPlannedDate(task)

              return (
                <button
                  key={task.id}
                  type="button"
                  className={task.id === selectedTaskID ? 'is-selected' : ''}
                  aria-label={`在任务详情中编辑 ${title}`}
                  onClick={() => onSelectTask(task.id)}
                >
                  <span>
                    <strong>{title}</strong>
                    <small>
                      {plannedDate || '未安排日期'} ·{' '}
                      {formatRoadmapTaskStatus(task)}
                    </small>
                  </span>
                  <em>编辑详情</em>
                </button>
              )
            })}
          </div>
        )}
      </div>

      <div className="roadmap-resource-section-heading">
        <BookOpen aria-hidden="true" />
        <strong>学习资料</strong>
        <span>{node.resources.length}</span>
      </div>

      <div className="roadmap-search-context">
        <label htmlFor={`roadmap-search-prompt-${node.id}`}>
          本节点搜索提示词
        </label>
        <textarea
          id={`roadmap-search-prompt-${node.id}`}
          aria-label="搜索提示词"
          value={searchPrompt}
          onChange={(event) => setSearchPrompt(event.target.value)}
          rows={3}
        />
        {searchPrompt !== defaultSearchPrompt && (
          <button
            type="button"
            onClick={() => setSearchPrompt(defaultSearchPrompt)}
          >
            恢复节点默认提示词
          </button>
        )}
        <small>搜索结果将按当前节点的主题、交付物和验收目标过滤</small>
      </div>

      <div className="roadmap-inspector-actions">
        <button
          type="button"
          onClick={() => onSearchResources(node.id, searchPrompt.trim())}
          disabled={
            isSearching ||
            articleSearchSources.length === 0 ||
            !searchPrompt.trim()
          }
        >
          <Search aria-hidden="true" />
          {isSearching ? '搜索中...' : '搜索文章'}
        </button>
      </div>

      <fieldset className="roadmap-source-settings" aria-label="搜索源">
        <legend>搜索源</legend>
        <div className="roadmap-source-options">
          {articleSearchSourceOptions.map((source) => (
            <div className="roadmap-source-option" key={source.id}>
              <label>
                <input
                  type="checkbox"
                  checked={articleSearchSources.includes(source.id)}
                  onChange={() => onToggleArticleSearchSource(source.id)}
                />
                <span>{source.label}</span>
              </label>
              {source.custom && (
                <button
                  type="button"
                  aria-label={`删除搜索源 ${source.label}`}
                  title="删除自定义搜索源"
                  onClick={() => onRemoveArticleSearchSource(source.id)}
                >
                  ×
                </button>
              )}
            </div>
          ))}
        </div>
        <form
          className="roadmap-source-add-form"
          onSubmit={(event) => {
            event.preventDefault()
            if (onAddArticleSearchSource(customSourceDraft)) {
              setCustomSourceDraft('')
              setCustomSourceError('')
            } else {
              setCustomSourceError('请输入有效的网站域名')
            }
          }}
        >
          <input
            aria-label="自定义搜索源"
            value={customSourceDraft}
            onChange={(event) => {
              setCustomSourceDraft(event.target.value)
              setCustomSourceError('')
            }}
            placeholder="添加网站域名，如 docs.python.org"
          />
          <button type="submit" disabled={!customSourceDraft.trim()}>
            添加来源
          </button>
        </form>
        {customSourceError && (
          <small className="roadmap-source-error" role="alert">
            {customSourceError}
          </small>
        )}
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
        <button
          type="submit"
          disabled={
            !manualTitle.trim() || !manualURL.trim() || isAddingResource
          }
        >
          添加链接
        </button>
      </form>

      <div className="roadmap-resource-list">
        {node.resources.length === 0 ? (
          <p className="empty-copy">暂无文章链接</p>
        ) : (
          node.resources.map((resource) => (
            <article className="roadmap-resource-item" key={resource.id}>
              <a href={resource.url} target="_blank" rel="noreferrer">
                <small className="roadmap-resource-association">
                  {resource.added_by === 'search' ? '系统推荐' : '手动添加'} ·{' '}
                  {node.title}
                </small>
                <strong>{resource.title}</strong>
                {resource.summary && <span>{resource.summary}</span>}
              </a>
              <div className="roadmap-resource-delete-actions">
                {pendingDeleteResourceID === resource.id ? (
                  <>
                    <button
                      type="button"
                      onClick={async () => {
                        await onDeleteResource(resource.id)
                        setPendingDeleteResourceID('')
                      }}
                      disabled={deletingResourceID === resource.id}
                    >
                      {deletingResourceID === resource.id
                        ? '删除中...'
                        : '确认删除'}
                    </button>
                    <button
                      type="button"
                      onClick={() => setPendingDeleteResourceID('')}
                    >
                      取消
                    </button>
                  </>
                ) : (
                  <button
                    type="button"
                    aria-label={`删除文章 ${resource.title}`}
                    onClick={() => setPendingDeleteResourceID(resource.id)}
                  >
                    <Trash2 aria-hidden="true" />
                    删除
                  </button>
                )}
              </div>
            </article>
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

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const trimmedTitle = title.trim()
    if (!trimmedTitle) return
    await onCreate({
      parent_id: parentNode.id,
      title: trimmedTitle,
      type: nodeType,
      description: description.trim(),
      path_type: 'required',
      status: 'todo',
      edge_style: 'solid',
    })
  }

  return (
    <div className="roadmap-node-create-overlay">
      <section
        className="roadmap-node-create-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="新增 Roadmap 节点"
      >
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
            <input
              aria-label="节点标题"
              value={title}
              onChange={(event) => setTitle(event.target.value)}
              autoFocus
            />
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
              onChange={(event) =>
                setNodeType(event.target.value as RoadmapNode['type'])
              }
            >
              {(Object.keys(nodeTypeLabels) as RoadmapNode['type'][])
                .filter((value) => value !== 'choice')
                .map((value) => (
                  <option key={value} value={value}>
                    {nodeTypeLabels[value]}
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
  onCreateLinkedTask,
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
  onCreateLinkedTask: (
    node: RoadmapNode,
    input: RoadmapLinkedTaskInput
  ) => Promise<void>
  onToggleTask: (task: Task) => Promise<void>
  onSaveTaskContent: (task: Task, content: string) => Promise<void>
}) {
  const [title, setTitle] = useState(node.title)
  const [description, setDescription] = useState(node.description)
  const [deliverable, setDeliverable] = useState(node.deliverable)
  const [acceptanceCriteria, setAcceptanceCriteria] = useState(
    node.acceptance_criteria
  )
  const [status, setStatus] = useState<RoadmapNode['status']>(node.status)
  const [linkedTaskTitle, setLinkedTaskTitle] = useState('')
  const [linkedTaskContent, setLinkedTaskContent] = useState('')
  const [linkedTaskDate, setLinkedTaskDate] = useState(() =>
    todayDateInputValue()
  )
  const [linkedTaskExecutionType, setLinkedTaskExecutionType] =
    useState<RoadmapLinkedTaskExecutionType>('single')
  const [linkedTaskFrequency, setLinkedTaskFrequency] =
    useState<RecurrenceConfig['frequency']>('daily')
  const [linkedTaskInterval, setLinkedTaskInterval] = useState('1')
  const [isConfirmingDelete, setIsConfirmingDelete] = useState(false)

  useEffect(() => {
    setTitle(node.title)
    setDescription(node.description)
    setDeliverable(node.deliverable)
    setAcceptanceCriteria(node.acceptance_criteria)
    setStatus(node.status)
    setLinkedTaskTitle('')
    setLinkedTaskContent('')
    setLinkedTaskDate(todayDateInputValue())
    setLinkedTaskExecutionType('single')
    setLinkedTaskFrequency('daily')
    setLinkedTaskInterval('1')
    setIsConfirmingDelete(false)
  }, [
    node.acceptance_criteria,
    node.deliverable,
    node.description,
    node.id,
    node.status,
    node.title,
  ])

  const doneCount = tasks.filter(
    (task) => task.done === 1 || task.status === 'done'
  ).length
  const progressPercent = tasks.length
    ? Math.round((doneCount / tasks.length) * 100)
    : 0
  const linkedTasks = useMemo(
    () => [...tasks].sort(compareRoadmapLinkedTasks),
    [tasks]
  )

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

  async function handleCreateLinkedTask(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const trimmedTitle = linkedTaskTitle.trim()
    if (!trimmedTitle) return
    const plannedDate = linkedTaskDate || todayDateInputValue()
    const recurrence: RecurrenceConfig | undefined =
      linkedTaskExecutionType === 'recurring'
        ? {
            start_date: plannedDate,
            frequency: linkedTaskFrequency,
            interval: Math.max(1, Number(linkedTaskInterval) || 1),
            weekdays:
              linkedTaskFrequency === 'weekly'
                ? [weekdayFromDateInput(plannedDate)]
                : [],
            month_days:
              linkedTaskFrequency === 'monthly'
                ? [monthDayFromDateInput(plannedDate)]
                : [],
            timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
          }
        : undefined
    await onCreateLinkedTask(node, {
      title: trimmedTitle,
      content: linkedTaskContent.trim(),
      plannedDate,
      executionType: linkedTaskExecutionType,
      recurrence,
    })
    setLinkedTaskTitle('')
    setLinkedTaskContent('')
    setLinkedTaskDate(todayDateInputValue())
    setLinkedTaskExecutionType('single')
    setLinkedTaskFrequency('daily')
    setLinkedTaskInterval('1')
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
            <span>
              {nodeTypeLabels[node.type]} · {pathTypeLabels[node.path_type]}
            </span>
            <h2>{title || node.title}</h2>
          </div>
          <button type="button" aria-label="关闭节点详情" onClick={onClose}>
            ×
          </button>
        </div>

        <form className="roadmap-node-edit-form" onSubmit={handleSubmit}>
          <label>
            <span>节点标题</span>
            <input
              aria-label="节点标题"
              value={title}
              onChange={(event) => setTitle(event.target.value)}
            />
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
              onChange={(event) =>
                setStatus(event.target.value as RoadmapNode['status'])
              }
            >
              {(Object.keys(nodeStatusLabels) as RoadmapNode['status'][]).map(
                (value) => (
                  <option key={value} value={value}>
                    {nodeStatusLabels[value]}
                  </option>
                )
              )}
            </select>
          </label>
          <label>
            <span>交付物</span>
            <textarea
              aria-label="交付物"
              value={deliverable}
              onChange={(event) => setDeliverable(event.target.value)}
            />
          </label>
          <label>
            <span>验收标准</span>
            <textarea
              aria-label="验收标准"
              value={acceptanceCriteria}
              onChange={(event) => setAcceptanceCriteria(event.target.value)}
            />
          </label>
          <div className="roadmap-node-form-actions">
            <div className="roadmap-node-dialog-actions">
              <button type="submit" disabled={!title.trim() || isSaving}>
                {isSaving ? '保存中...' : '保存节点'}
              </button>
            </div>
            <div className="roadmap-node-danger-actions">
              {isConfirmingDelete ? (
                <>
                  <button
                    type="button"
                    onClick={() => onDelete(node.id)}
                    disabled={isDeleting}
                  >
                    {isDeleting ? '删除中...' : '确认删除节点'}
                  </button>
                  <button
                    type="button"
                    onClick={() => setIsConfirmingDelete(false)}
                    disabled={isDeleting}
                  >
                    取消
                  </button>
                </>
              ) : (
                <button
                  type="button"
                  onClick={() => setIsConfirmingDelete(true)}
                >
                  删除节点
                </button>
              )}
            </div>
          </div>
        </form>

        <div
          className="roadmap-node-progress"
          data-testid="roadmap-node-progress"
        >
          <div>
            <span>任务进度</span>
            <strong>
              {doneCount} / {tasks.length}
            </strong>
          </div>
          <div className="roadmap-node-progress-track" aria-hidden="true">
            <i style={{ width: `${progressPercent}%` }} />
          </div>
        </div>

        <form
          className="roadmap-linked-task-create-form"
          onSubmit={handleCreateLinkedTask}
        >
          <div>
            <span>新增关联学习任务</span>
          </div>
          <label>
            <span>任务标题</span>
            <input
              data-testid="roadmap-linked-task-title-input"
              aria-label="关联任务标题"
              value={linkedTaskTitle}
              onChange={(event) => setLinkedTaskTitle(event.target.value)}
              placeholder="例如：调研 HNSW 参数"
            />
          </label>
          <label className="roadmap-linked-task-create-content">
            <span>具体任务内容</span>
            <textarea
              data-testid="roadmap-linked-task-content-input"
              aria-label="关联任务内容"
              value={linkedTaskContent}
              onChange={(event) => setLinkedTaskContent(event.target.value)}
              placeholder="写下这次要完成的阅读、实验、输出或验收点"
            />
          </label>
          <label>
            <span>任务类型</span>
            <select
              data-testid="roadmap-linked-task-execution-type"
              aria-label="关联任务类型"
              value={linkedTaskExecutionType}
              onChange={(event) =>
                setLinkedTaskExecutionType(
                  event.target.value as RoadmapLinkedTaskExecutionType
                )
              }
            >
              <option value="single">普通任务</option>
              <option value="recurring">重复任务</option>
            </select>
          </label>
          <label>
            <span>
              {linkedTaskExecutionType === 'recurring'
                ? '开始日期'
                : '计划日期'}
            </span>
            <input
              data-testid="roadmap-linked-task-date-input"
              aria-label="关联任务计划日期"
              type="date"
              value={linkedTaskDate}
              onChange={(event) => setLinkedTaskDate(event.target.value)}
            />
          </label>
          {linkedTaskExecutionType === 'recurring' && (
            <div className="roadmap-linked-task-recurrence-fields">
              <label>
                <span>重复频率</span>
                <select
                  data-testid="roadmap-linked-task-frequency-select"
                  aria-label="关联任务重复频率"
                  value={linkedTaskFrequency}
                  onChange={(event) =>
                    setLinkedTaskFrequency(
                      event.target.value as RecurrenceConfig['frequency']
                    )
                  }
                >
                  <option value="daily">每天</option>
                  <option value="weekly">每周</option>
                  <option value="monthly">每月</option>
                </select>
              </label>
              {linkedTaskFrequency !== 'daily' && (
                <label>
                  <span>间隔</span>
                  <input
                    data-testid="roadmap-linked-task-interval-input"
                    aria-label="关联任务重复间隔"
                    type="number"
                    min={1}
                    value={linkedTaskInterval}
                    onChange={(event) =>
                      setLinkedTaskInterval(event.target.value)
                    }
                  />
                </label>
              )}
            </div>
          )}
          <button
            data-testid="roadmap-linked-task-create-button"
            type="submit"
            disabled={!linkedTaskTitle.trim() || isAddingTask}
          >
            {isAddingTask ? '添加中...' : '添加关联任务'}
          </button>
        </form>

        <div
          className="roadmap-linked-task-list"
          data-testid="roadmap-linked-task-list"
        >
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
  const taskColor = getTaskColor(task.id, task.color)

  useEffect(() => {
    setContent(task.content ?? '')
  }, [task.content, task.id])

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    await onSaveContent(content)
  }

  return (
    <article
      className={`roadmap-linked-task-row${isDone ? ' is-done' : ''}${isUpdating ? ' is-updating' : ''}`}
    >
      <span
        className="task-color-dot"
        style={{ backgroundColor: taskColor }}
        aria-label={`任务颜色：${title}`}
      />
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
        <form
          className="roadmap-linked-task-content-form"
          onSubmit={handleSubmit}
        >
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

function RecurringTaskView({
  tasks,
  selectedTaskID,
  title,
  frequency,
  interval: freqInterval,
  weekdays,
  monthDays,
  startDate,
  endDate,
  projectID,
  projects,
  isPending,
  onTitleChange,
  onFrequencyChange,
  onIntervalChange,
  onToggleWeekday,
  onToggleMonthDay,
  onStartDateChange,
  onEndDateChange,
  onProjectChange,
  onSubmit,
  onSelectTask,
  onToggle,
}: {
  tasks: Task[]
  selectedTaskID: string
  title: string
  frequency: RecurrenceConfig['frequency']
  interval: number
  weekdays: number[]
  monthDays: number[]
  startDate: string
  endDate: string
  projectID: string
  projects: TaskProject[]
  isPending: boolean
  onTitleChange: (v: string) => void
  onFrequencyChange: (v: RecurrenceConfig['frequency']) => void
  onIntervalChange: (v: number) => void
  onToggleWeekday: (d: number) => void
  onToggleMonthDay: (d: number) => void
  onStartDateChange: (v: string) => void
  onEndDateChange: (v: string) => void
  onProjectChange: (v: string) => void
  onSubmit: (e: FormEvent<HTMLFormElement>) => void
  onSelectTask: (id: string) => void
  onToggle: (task: Task) => void
}) {
  const weekdayLabels = ['一', '二', '三', '四', '五', '六', '日']
  const enabledTasks = tasks.filter((t) => {
    const r = (t as any).recurrence
    return r?.enabled !== false
  })
  const disabledTasks = tasks.filter((t) => {
    const r = (t as any).recurrence
    return r?.enabled === false
  })

  return (
    <div className="task-tab-panel">
      <form
        className="inline-create task-create-form recurring-create-form"
        onSubmit={onSubmit}
      >
        <input
          className="task-title-input"
          aria-label="重复任务标题"
          value={title}
          onChange={(e) => onTitleChange(e.target.value)}
          placeholder="例如：每天背单词"
        />
        <select
          value={frequency}
          onChange={(e) =>
            onFrequencyChange(e.target.value as RecurrenceConfig['frequency'])
          }
        >
          <option value="daily">每天</option>
          <option value="weekly">每周</option>
          <option value="monthly">每月</option>
        </select>
        {frequency !== 'daily' && (
          <input
            aria-label="间隔"
            type="number"
            min={1}
            value={freqInterval}
            onChange={(e) => onIntervalChange(Number(e.target.value))}
            style={{ width: 60 }}
          />
        )}
        <input
          type="date"
          aria-label="开始日期"
          value={startDate}
          onChange={(e) => onStartDateChange(e.target.value)}
        />
        <input
          type="date"
          aria-label="结束日期"
          value={endDate}
          onChange={(e) => onEndDateChange(e.target.value)}
          placeholder="结束日期（可选）"
        />
        <select
          aria-label="任务项目"
          value={projectID}
          onChange={(e) => onProjectChange(e.target.value)}
        >
          {projects.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
        <button type="submit" disabled={!title.trim() || isPending}>
          添加
        </button>
      </form>

      {frequency === 'weekly' && (
        <div className="recurring-day-picker">
          <span>每周几：</span>
          {[1, 2, 3, 4, 5, 6, 7].map((d) => (
            <button
              key={d}
              type="button"
              className={
                weekdays.includes(d) ? 'day-chip is-active' : 'day-chip'
              }
              onClick={() => onToggleWeekday(d)}
            >
              {weekdayLabels[d - 1]}
            </button>
          ))}
        </div>
      )}
      {frequency === 'monthly' && (
        <div className="recurring-day-picker">
          <span>每月几号：</span>
          {[1, 5, 10, 15, 20, 25, 28, 31].map((d) => (
            <button
              key={d}
              type="button"
              className={
                monthDays.includes(d) ? 'day-chip is-active' : 'day-chip'
              }
              onClick={() => onToggleMonthDay(d)}
            >
              {d}
            </button>
          ))}
        </div>
      )}

      {tasks.length === 0 ? (
        <p className="empty-copy">还没有重复任务</p>
      ) : (
        <>
          {enabledTasks.length > 0 && (
            <div className="task-section">
              <span>进行中</span>
              <div className="row-stack">
                {enabledTasks.map((task) => (
                  <TaskRow
                    key={task.id}
                    task={task}
                    isSelected={task.id === selectedTaskID}
                    onSelect={onSelectTask}
                    onToggle={() => onToggle(task)}
                  />
                ))}
              </div>
            </div>
          )}
          {disabledTasks.length > 0 && (
            <div className="task-section">
              <span>已暂停</span>
              <div className="row-stack">
                {disabledTasks.map((task) => (
                  <TaskRow
                    key={task.id}
                    task={task}
                    isSelected={task.id === selectedTaskID}
                    onSelect={onSelectTask}
                    onToggle={() => onToggle(task)}
                  />
                ))}
              </div>
            </div>
          )}
        </>
      )}
    </div>
  )
}

function RoadmapResourcePickerDialog({
  nodeTitle,
  query,
  candidates,
  selectedIDs,
  isSaving,
  onToggle,
  onCancel,
  onConfirm,
}: {
  nodeTitle: string
  query: string
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
      <section
        className="roadmap-resource-picker"
        role="dialog"
        aria-modal="true"
        aria-label="选择文章"
      >
        <div className="roadmap-resource-picker-heading">
          <div>
            <span>{nodeTitle}</span>
            <h2>选择要添加的文章</h2>
          </div>
          <button type="button" aria-label="关闭选择文章" onClick={onCancel}>
            ×
          </button>
        </div>

        <div className="roadmap-resource-search-query">
          <span>本次搜索提示词</span>
          <p>{query}</p>
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
          <button
            type="button"
            onClick={onConfirm}
            disabled={selectedIDs.length === 0 || isSaving}
          >
            {isSaving ? '添加中...' : '添加选中文章'}
          </button>
        </div>
      </section>
    </div>
  )
}
