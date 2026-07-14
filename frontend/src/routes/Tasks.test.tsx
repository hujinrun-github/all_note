import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import Tasks from './Tasks'
import * as tasksApi from '../api/tasks'
import type { LearningRoadmap, Task, TaskProject } from '../api/tasks'
import { dateInputToUnix, todayDateInputValue } from '../utils/taskForm'

vi.mock('../api/tasks')

type MockFlowNode = {
  id: string
  data?: {
    node?: { id: string; title: string }
    onOpen?: (id: string) => void
  }
}

vi.mock('@xyflow/react', async () => {
  const React = await import('react')

  return {
    Background: () => null,
    Controls: () => null,
    Handle: () => null,
    MarkerType: { ArrowClosed: 'arrowclosed' },
    MiniMap: () => null,
    Position: { Bottom: 'bottom', Left: 'left', Right: 'right', Top: 'top' },
    ReactFlow: ({ children, nodes = [] }: { children?: React.ReactNode; nodes?: MockFlowNode[] }) => (
      <div data-testid="react-flow">
        {nodes.map((node) => (
          <button
            key={node.id}
            type="button"
            data-testid="roadmap-node"
            onClick={() => node.data?.onOpen?.(node.data.node?.id ?? node.id)}
          >
            {node.data?.node?.title ?? node.id}
          </button>
        ))}
        {children}
      </div>
    ),
    useEdgesState: (initial: unknown[]) => {
      const [edges, setEdges] = React.useState(initial)
      return [edges, setEdges, vi.fn()]
    },
    useNodesState: (initial: MockFlowNode[]) => {
      const [nodes, setNodes] = React.useState(initial)
      return [nodes, setNodes, vi.fn()]
    },
  }
})

function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
}

function renderTasks(queryClient = createTestQueryClient()) {

  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <Tasks />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

function task(overrides: Partial<Task>): Task {
  return {
    id: 'task-1',
    title: '任务',
    content: '',
    project: 'AI Infra',
    project_id: 'project-1',
    project_type: 'regular',
    priority: 0,
    done: 0,
    status: 'open',
    horizon: 'long',
    scope: 'yearly',
    sort_order: 0,
    created_at: 1800000000,
    updated_at: 1800000000,
    ...overrides,
  }
}

const projects: TaskProject[] = [
  {
    id: 'personal',
    name: '个人',
    type: 'personal',
    description: '',
    created_at: 1,
    updated_at: 1,
  },
  {
    id: 'project-1',
    name: 'AI Infra',
    type: 'regular',
    description: '',
    created_at: 1,
    updated_at: 1,
  },
]

const pagination = { page: 1, page_size: 100, total: 0, total_pages: 1 }

const learningProject: TaskProject = {
  id: 'learning-1',
  name: 'AI Infra工程师',
  type: 'learning',
  description: '学习路线',
  created_at: 1,
  updated_at: 1,
}

const roadmap: LearningRoadmap = {
  id: 'roadmap-1',
  project_id: learningProject.id,
  title: 'AI Infra Roadmap',
  goal: '掌握 AI Infra',
  status: 'ready',
  nodes: [
    {
      id: 'node-1',
      roadmap_id: 'roadmap-1',
      type: 'module',
      title: 'AI Infra概述与系统设计基础',
      description: '理解核心概念',
      path_type: 'required',
      status: 'active',
      deliverable: '完成学习笔记',
      acceptance_criteria: '能解释端到端链路',
      x: 0,
      y: 0,
      order_index: 0,
      resources: [],
      created_at: 1,
      updated_at: 1,
    },
  ],
  edges: [],
  created_at: 1,
  updated_at: 1,
}

describe('Tasks long task tracking', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(tasksApi.listTaskProjects).mockResolvedValue(projects)
    vi.mocked(tasksApi.getLearningRoadmap).mockResolvedValue(null)
    vi.mocked(tasksApi.getRecurringTasks).mockResolvedValue({ tasks: [], pagination })
    vi.mocked(tasksApi.getTasks).mockImplementation(async (params) => {
      if (params.horizon === 'long') {
        return {
          tasks: [
            task({ id: 'long-active', title: '搭 AI Infra 原型', status: 'active', updated_at: 1800000300 }),
            task({ id: 'long-blocked', title: '等待 Notion 权限', status: 'blocked', updated_at: 1800000200 }),
            task({ id: 'long-open', title: '整理同步边界', status: 'open', updated_at: 1800000100 }),
            task({ id: 'long-done', title: '完成方案设计', status: 'done', done: 1, updated_at: 1800000000 }),
          ],
          pagination,
        }
      }
      return { tasks: [], pagination }
    })
    vi.mocked(tasksApi.updateTask).mockResolvedValue(task({ id: 'long-blocked', status: 'active' }))
  })

  it('shows only the active tab project as selected in the project list', async () => {
    vi.mocked(tasksApi.listTaskProjects).mockResolvedValue([...projects, learningProject])
    vi.mocked(tasksApi.getLearningRoadmap).mockResolvedValue(roadmap)
    const { container } = renderTasks()
    const user = userEvent.setup()

    await screen.findByText('AI Infra工程师')
    const projectButtons = Array.from(container.querySelectorAll<HTMLButtonElement>('.task-project-select'))
    expect(projectButtons).toHaveLength(3)
    const [personalProjectButton, regularProjectButton, learningProjectButton] = projectButtons

    expect(personalProjectButton).toHaveClass('is-active')
    expect(regularProjectButton).not.toHaveClass('is-active')
    expect(learningProjectButton).not.toHaveClass('is-active')

    await user.click(screen.getByRole('tab', { name: '长期任务' }))

    expect(personalProjectButton).not.toHaveClass('is-active')
    expect(regularProjectButton).toHaveClass('is-active')
    expect(learningProjectButton).not.toHaveClass('is-active')

    await user.click(screen.getByRole('tab', { name: '学习 Roadmap' }))

    expect(personalProjectButton).not.toHaveClass('is-active')
    expect(regularProjectButton).not.toHaveClass('is-active')
    expect(learningProjectButton).toHaveClass('is-active')
  })

  it('groups long tasks by tracking status and updates status from the row select', async () => {
    renderTasks()
    const user = userEvent.setup()

    await user.click(await screen.findByRole('tab', { name: '长期任务' }))

    const groups = await screen.findByTestId('long-task-status-groups')
    const groupText = groups.textContent ?? ''
    expect(groupText.indexOf('进行中')).toBeLessThan(groupText.indexOf('阻塞'))
    expect(groupText.indexOf('阻塞')).toBeLessThan(groupText.indexOf('未开始'))
    expect(groupText.indexOf('未开始')).toBeLessThan(groupText.indexOf('完成'))
    expect(within(screen.getByTestId('long-task-status-active')).getByText('搭 AI Infra 原型')).toBeVisible()
    expect(within(screen.getByTestId('long-task-status-blocked')).getByText('等待 Notion 权限')).toBeVisible()
    expect(within(screen.getByTestId('long-task-status-open')).getByText('整理同步边界')).toBeVisible()
    expect(within(screen.getByTestId('long-task-status-done')).getByText('完成方案设计')).toBeVisible()

    await user.selectOptions(screen.getByLabelText('更新长期任务状态：等待 Notion 权限'), 'active')

    await waitFor(() => expect(tasksApi.updateTask).toHaveBeenCalledWith('long-blocked', { status: 'active' }))
  })

  it('loads structured projects even when project name cache already exists', async () => {
    const queryClient = createTestQueryClient()
    queryClient.setQueryData(['task-projects'], ['个人', 'AI Infra'])
    renderTasks(queryClient)
    const user = userEvent.setup()

    await user.click(await screen.findByRole('tab', { name: '长期任务' }))

    await waitFor(() => expect(screen.getAllByRole('option', { name: 'AI Infra' }).length).toBeGreaterThan(0))
    expect(screen.queryByText('先在左侧新增一个任务项目')).not.toBeInTheDocument()
  })

  it('selects a weekly task from the row content without completing it', async () => {
    vi.mocked(tasksApi.getTasks).mockImplementation(async (params) => {
      if (params.horizon === 'long') return { tasks: [], pagination }
      return {
        tasks: [
          task({
            id: 'week-first',
            title: '第一条任务',
            project: '个人',
            project_id: 'personal',
            project_type: 'personal',
            horizon: 'week',
            scope: 'weekly',
            planned_date: todayDateInputValue(),
          }),
          task({
            id: 'week-second',
            title: '第二条任务',
            project: '个人',
            project_id: 'personal',
            project_type: 'personal',
            horizon: 'week',
            scope: 'weekly',
            planned_date: todayDateInputValue(),
          }),
        ],
        pagination,
      }
    })
    renderTasks()
    const user = userEvent.setup()

    await user.click(await screen.findByText('第二条任务'))

    expect(tasksApi.updateTask).not.toHaveBeenCalled()
    expect(screen.getByLabelText('任务详情标题')).toHaveValue('第二条任务')
  })

  it('places completed weekly tasks after active tasks from the same day', async () => {
    vi.mocked(tasksApi.getTasks).mockImplementation(async (params) => {
      if (params.horizon === 'long') return { tasks: [], pagination }
      return {
        tasks: [
          task({
            id: 'week-done',
            title: '已完成任务',
            project: '个人',
            project_id: 'personal',
            project_type: 'personal',
            done: 1,
            status: 'done',
            horizon: 'week',
            scope: 'weekly',
            planned_date: todayDateInputValue(),
            updated_at: 1800000300,
          }),
          task({
            id: 'week-open',
            title: '待处理任务',
            project: '个人',
            project_id: 'personal',
            project_type: 'personal',
            done: 0,
            status: 'open',
            horizon: 'week',
            scope: 'weekly',
            planned_date: todayDateInputValue(),
            updated_at: 1800000100,
          }),
        ],
        pagination,
      }
    })
    const { container } = renderTasks()

    await screen.findByText('待处理任务')

    const titles = Array.from(container.querySelectorAll('.task-section .task-row-title')).map((title) =>
      title.textContent?.trim(),
    )
    expect(titles).toEqual(['待处理任务', '已完成任务'])
  })

  it('groups projects by matching task views and creates typed projects from group controls', async () => {
    vi.mocked(tasksApi.listTaskProjects).mockResolvedValue([...projects, learningProject])
    vi.mocked(tasksApi.createTaskProject).mockResolvedValue({
      id: 'learning-n2',
      name: 'N2 语法',
      type: 'learning',
      description: '',
      created_at: 1,
      updated_at: 1,
    })
    renderTasks()
    const user = userEvent.setup()

    const shortGroup = await screen.findByTestId('task-project-group-personal')
    const longGroup = screen.getByTestId('task-project-group-regular')
    const learningGroup = screen.getByTestId('task-project-group-learning')
    expect(within(shortGroup).getByText('个人短期项目')).toBeVisible()
    expect(await within(shortGroup).findByText('1 个项目')).toBeVisible()
    expect(await within(shortGroup).findByRole('button', { name: '选择项目 个人' })).toBeVisible()
    expect(within(longGroup).getByText('长期项目')).toBeVisible()
    expect(await within(longGroup).findByText('1 个项目')).toBeVisible()
    expect(await within(longGroup).findByText('AI Infra')).toBeVisible()
    expect(within(learningGroup).getByRole('heading', { name: '学习项目' })).toBeVisible()
    expect(await within(learningGroup).findByText('1 个项目')).toBeVisible()
    expect(await within(learningGroup).findByText('AI Infra工程师')).toBeVisible()

    await user.click(within(learningGroup).getByRole('button', { name: '新建学习项目' }))
    await user.type(screen.getByLabelText('学习项目名称'), 'N2 语法')
    await user.click(screen.getByRole('button', { name: '创建学习项目' }))

    await waitFor(() => expect(tasksApi.createTaskProject).toHaveBeenCalled())
    expect(vi.mocked(tasksApi.createTaskProject).mock.calls[0]?.[0]).toEqual({
      name: 'N2 语法',
      type: 'learning',
      description: '',
    })
  })

  it('creates a regular long project before adding long tasks when no long project exists', async () => {
    vi.mocked(tasksApi.listTaskProjects).mockResolvedValue([projects[0]])
    vi.mocked(tasksApi.getTasks).mockResolvedValue({ tasks: [], pagination })
    vi.mocked(tasksApi.createTaskProject).mockResolvedValue({
      id: 'long-plan',
      name: '年度计划',
      type: 'regular',
      description: '',
      created_at: 1,
      updated_at: 1,
    })
    renderTasks()
    const user = userEvent.setup()

    await user.click(await screen.findByRole('tab', { name: '长期任务' }))

    expect(screen.queryByLabelText('长期任务内容')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '创建长期项目' }))
    await user.type(screen.getByLabelText('长期项目名称'), '年度计划')
    await user.click(screen.getByRole('button', { name: '创建长期项目' }))

    await waitFor(() => expect(tasksApi.createTaskProject).toHaveBeenCalled())
    expect(vi.mocked(tasksApi.createTaskProject).mock.calls[0]?.[0]).toEqual({
      name: '年度计划',
      type: 'regular',
      description: '',
    })
  })

  it('opens the learning project creator from the roadmap empty state', async () => {
    vi.mocked(tasksApi.listTaskProjects).mockResolvedValue([projects[0]])
    vi.mocked(tasksApi.getTasks).mockResolvedValue({ tasks: [], pagination })
    vi.mocked(tasksApi.createTaskProject).mockResolvedValue({
      id: 'learning-n2',
      name: 'N2 语法',
      type: 'learning',
      description: '',
      created_at: 1,
      updated_at: 1,
    })
    renderTasks()
    const user = userEvent.setup()

    await user.click(await screen.findByRole('tab', { name: '学习 Roadmap' }))

    await user.click(screen.getByRole('button', { name: '创建学习项目' }))
    await user.type(screen.getByLabelText('学习项目名称'), 'N2 语法')
    await user.click(screen.getByRole('button', { name: '创建学习项目' }))

    await waitFor(() => expect(tasksApi.createTaskProject).toHaveBeenCalled())
    expect(vi.mocked(tasksApi.createTaskProject).mock.calls[0]?.[0]).toEqual({
      name: 'N2 语法',
      type: 'learning',
      description: '',
    })
  })

  it('saves editable task details from the inspector panel', async () => {
    renderTasks()
    const user = userEvent.setup()

    await user.click(await screen.findByRole('tab', { name: '长期任务' }))

    const titleInput = await screen.findByLabelText('任务详情标题')
    await user.clear(titleInput)
    await user.type(titleInput, '更新后的长期目标')
    await user.selectOptions(screen.getByLabelText('任务详情项目'), 'personal')
    await user.click(screen.getByRole('button', { name: '进行中' }))
    await user.type(screen.getByLabelText('任务详情备注'), '下一步先整理任务边界')
    await user.click(screen.getByRole('button', { name: '保存修改' }))

    await waitFor(() =>
      expect(tasksApi.updateTask).toHaveBeenCalledWith('long-active', {
        title: '更新后的长期目标',
        content: '下一步先整理任务边界',
        project_id: 'personal',
        status: 'active',
        planned_date: '',
      }),
    )
  })
})

describe('Tasks recurring task project selector', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(tasksApi.listTaskProjects).mockResolvedValue([...projects, learningProject])
    vi.mocked(tasksApi.getLearningRoadmap).mockResolvedValue(null)
    vi.mocked(tasksApi.getTasks).mockResolvedValue({ tasks: [], pagination })
    vi.mocked(tasksApi.getRecurringTasks).mockResolvedValue({ tasks: [], pagination })
  })

  it('uses official task projects when creating recurring tasks', async () => {
    const { container } = renderTasks()
    const user = userEvent.setup()

    await user.click(await screen.findByRole('tab', { name: '重复任务' }))

    const form = container.querySelector<HTMLElement>('.recurring-create-form')
    expect(form).not.toBeNull()
    const selects = within(form!).getAllByRole('combobox')
    const projectSelect = selects[selects.length - 1]
    const options = within(projectSelect).getAllByRole('option')

    expect(options.map((option) => option.textContent)).toEqual(['个人', 'AI Infra', 'AI Infra工程师'])
    expect(options.map((option) => option.getAttribute('value'))).toEqual(['personal', 'project-1', 'learning-1'])
  })
})

describe('Tasks learning roadmap weekly linking', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(tasksApi.listTaskProjects).mockResolvedValue([...projects, learningProject])
    vi.mocked(tasksApi.getLearningRoadmap).mockResolvedValue(roadmap)
    vi.mocked(tasksApi.getTasks).mockResolvedValue({ tasks: [], pagination })
    vi.mocked(tasksApi.getRecurringTasks).mockResolvedValue({ tasks: [], pagination })
    vi.mocked(tasksApi.createTask).mockResolvedValue(
      task({
        id: 'week-roadmap-task',
        title: 'AI Infra概述与系统设计基础',
        project_id: learningProject.id,
        project_type: 'learning',
        roadmap_node_id: 'node-1',
        horizon: 'week',
        scope: 'daily',
      }),
    )
  })

  it('opens the roadmap graph and inspector in full-screen edit mode', async () => {
    const { container } = renderTasks()
    const user = userEvent.setup()

    await user.click(await screen.findByRole('tab', { name: '学习 Roadmap' }))
    await user.click(await screen.findByRole('button', { name: '进入全屏编辑' }))

    expect(container.querySelector('.roadmap-content')).toHaveClass('is-fullscreen')
    expect(screen.getByRole('button', { name: '退出全屏编辑' })).toBeVisible()
    expect(screen.getByText('交付物')).toBeVisible()

    await user.click(screen.getByRole('button', { name: '退出全屏编辑' }))
    expect(container.querySelector('.roadmap-content')).not.toHaveClass('is-fullscreen')
  })

  it('does not expose add-to-week from roadmap views', async () => {
    renderTasks()
    const user = userEvent.setup()

    await user.click(await screen.findByRole('tab', { name: '学习 Roadmap' }))
    expect(await screen.findByTestId('roadmap-canvas')).toBeVisible()

    expect(screen.queryByRole('button', { name: '加入本周' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '搜索文章' })).toBeVisible()

    await user.click(await screen.findByTestId('roadmap-node'))

    expect(screen.getByTestId('roadmap-node-dialog')).toBeVisible()
    expect(screen.queryByRole('button', { name: '加入本周' })).not.toBeInTheDocument()
    expect(tasksApi.createTask).not.toHaveBeenCalled()
  })

  it('labels duplicate linked tasks with sequence and creation metadata', async () => {
    vi.mocked(tasksApi.getTasks).mockImplementation(async (params) => {
      if (params.horizon === 'week') {
        return {
          tasks: [
            task({
              id: 'linked-task-1',
              title: 'AI Infra概述与系统设计基础',
              project: learningProject.name,
              project_id: learningProject.id,
              project_type: 'learning',
              roadmap_node_id: 'node-1',
              planned_date: '2026-06-15',
              horizon: 'week',
              scope: 'daily',
              created_at: 1781455200,
            }),
            task({
              id: 'linked-task-2',
              title: 'AI Infra概述与系统设计基础',
              project: learningProject.name,
              project_id: learningProject.id,
              project_type: 'learning',
              roadmap_node_id: 'node-1',
              planned_date: '2026-06-15',
              horizon: 'week',
              scope: 'daily',
              created_at: 1781458800,
            }),
          ],
          pagination,
        }
      }
      return { tasks: [], pagination }
    })
    renderTasks()
    const user = userEvent.setup()

    await user.click(await screen.findByRole('tab', { name: '学习 Roadmap' }))
    await user.click(await screen.findByTestId('roadmap-node'))

    const list = await screen.findByTestId('roadmap-linked-task-list')
    expect(within(list).getByText('AI Infra概述与系统设计基础 · 第 1 次推进')).toBeVisible()
    expect(within(list).getByText('AI Infra概述与系统设计基础 · 第 2 次推进')).toBeVisible()
    expect(within(list).getByText('第 1 次')).toBeVisible()
    expect(within(list).getByText('第 2 次')).toBeVisible()
    expect(within(list).getAllByText('未完成')).toHaveLength(2)
    expect(within(list).getAllByText(/创建/)).toHaveLength(2)
  })

  it('shows recurring linked learning tasks in the roadmap node dialog', async () => {
    vi.mocked(tasksApi.getRecurringTasks).mockResolvedValue({
      tasks: [
        task({
          id: 'recurring-roadmap-task',
          title: '每周复盘 HNSW',
          content: '整理索引参数实验结论',
          project: learningProject.name,
          project_id: learningProject.id,
          project_type: 'learning',
          roadmap_node_id: 'node-1',
          execution_type: 'recurring',
          horizon: 'week',
          scope: 'daily',
          created_at: 1781455200,
        }),
      ],
      pagination,
    })
    renderTasks()
    const user = userEvent.setup()

    await user.click(await screen.findByRole('tab', { name: '学习 Roadmap' }))
    await user.click(await screen.findByTestId('roadmap-node'))

    const list = await screen.findByTestId('roadmap-linked-task-list')
    expect(within(list).queryByText('暂无关联任务')).not.toBeInTheDocument()
    expect(within(list).getByText('每周复盘 HNSW · 第 1 次推进')).toBeVisible()
    expect(within(list).getByText('未完成')).toBeVisible()
    expect(within(list).getByDisplayValue('整理索引参数实验结论')).toBeVisible()
  })

  it('edits the concrete content of a linked roadmap task', async () => {
    vi.mocked(tasksApi.getTasks).mockImplementation(async (params) => {
      if (params.horizon === 'week') {
        return {
          tasks: [
            task({
              id: 'linked-task-with-content',
              title: 'AI Infra概述与系统设计基础 · 第 1 次推进',
              content: '阅读分布式系统综述，并列出 3 个关键问题',
              project: learningProject.name,
              project_id: learningProject.id,
              project_type: 'learning',
              roadmap_node_id: 'node-1',
              planned_date: '2026-06-15',
              horizon: 'week',
              scope: 'daily',
              created_at: 1781455200,
            }),
          ],
          pagination,
        }
      }
      return { tasks: [], pagination }
    })
    vi.mocked(tasksApi.updateTask).mockResolvedValue(
      task({
        id: 'linked-task-with-content',
        title: 'AI Infra概述与系统设计基础 · 第 1 次推进',
        content: '完成容器基础复盘，补充 Kubernetes 调度问题',
      }),
    )
    renderTasks()
    const user = userEvent.setup()

    await user.click(await screen.findByRole('tab', { name: '学习 Roadmap' }))
    await user.click(await screen.findByTestId('roadmap-node'))

    const contentInput = await screen.findByLabelText('任务内容：AI Infra概述与系统设计基础 · 第 1 次推进')
    expect(contentInput).toHaveValue('阅读分布式系统综述，并列出 3 个关键问题')

    await user.clear(contentInput)
    await user.type(contentInput, '完成容器基础复盘，补充 Kubernetes 调度问题')
    await user.click(screen.getByRole('button', { name: '保存任务内容' }))

    await waitFor(() =>
      expect(tasksApi.updateTask).toHaveBeenCalledWith('linked-task-with-content', {
        content: '完成容器基础复盘，补充 Kubernetes 调度问题',
      }),
    )
  })

  it('creates a custom linked learning task from the roadmap node dialog', async () => {
    const today = todayDateInputValue()
    vi.mocked(tasksApi.createTask).mockResolvedValue(
      task({
        id: 'manual-linked-task',
        title: '调研 HNSW 参数',
        content: '比较 efConstruction、M 和 rerank 的实际取舍',
        project: learningProject.name,
        project_id: learningProject.id,
        project_type: 'learning',
        roadmap_node_id: 'node-1',
        planned_date: today,
        due: dateInputToUnix(today),
        horizon: 'week',
        scope: 'daily',
      }),
    )
    renderTasks()
    const user = userEvent.setup()

    await user.click(await screen.findByRole('tab', { name: '学习 Roadmap' }))
    await user.click(await screen.findByTestId('roadmap-node'))

    await user.type(await screen.findByTestId('roadmap-linked-task-title-input'), '调研 HNSW 参数')
    await user.type(
      screen.getByTestId('roadmap-linked-task-content-input'),
      '比较 efConstruction、M 和 rerank 的实际取舍',
    )
    await user.click(screen.getByTestId('roadmap-linked-task-create-button'))

    await waitFor(() => expect(tasksApi.createTask).toHaveBeenCalled())
    expect(vi.mocked(tasksApi.createTask).mock.calls[0]?.[0]).toEqual(
      expect.objectContaining({
        title: '调研 HNSW 参数',
        content: '比较 efConstruction、M 和 rerank 的实际取舍',
        project_id: learningProject.id,
        roadmap_node_id: 'node-1',
        planned_date: today,
        due: dateInputToUnix(today),
        horizon: 'week',
        scope: 'daily',
      }),
    )
  })

  it('creates a recurring linked learning task from the roadmap node dialog', async () => {
    const startDate = '2026-06-15'
    vi.mocked(tasksApi.createTask).mockResolvedValue(
      task({
        id: 'recurring-linked-task',
        title: '每周复盘 HNSW',
        content: '整理索引参数实验结论',
        project: learningProject.name,
        project_id: learningProject.id,
        project_type: 'learning',
        roadmap_node_id: 'node-1',
        execution_type: 'recurring',
        horizon: 'week',
        scope: 'daily',
      }),
    )
    renderTasks()
    const user = userEvent.setup()

    await user.click(await screen.findByRole('tab', { name: '学习 Roadmap' }))
    await user.click(await screen.findByTestId('roadmap-node'))

    await user.type(await screen.findByTestId('roadmap-linked-task-title-input'), '每周复盘 HNSW')
    await user.type(screen.getByTestId('roadmap-linked-task-content-input'), '整理索引参数实验结论')
    await user.selectOptions(screen.getByTestId('roadmap-linked-task-execution-type'), 'recurring')
    await user.clear(screen.getByTestId('roadmap-linked-task-date-input'))
    await user.type(screen.getByTestId('roadmap-linked-task-date-input'), startDate)
    await user.selectOptions(screen.getByTestId('roadmap-linked-task-frequency-select'), 'weekly')
    await user.clear(screen.getByTestId('roadmap-linked-task-interval-input'))
    await user.type(screen.getByTestId('roadmap-linked-task-interval-input'), '2')
    await user.click(screen.getByTestId('roadmap-linked-task-create-button'))

    await waitFor(() => expect(tasksApi.createTask).toHaveBeenCalled())
    const payload = vi.mocked(tasksApi.createTask).mock.calls[0]?.[0]
    expect(payload).toEqual(
      expect.objectContaining({
        title: '每周复盘 HNSW',
        content: '整理索引参数实验结论',
        project_id: learningProject.id,
        roadmap_node_id: 'node-1',
        execution_type: 'recurring',
        horizon: 'week',
        scope: 'daily',
        recurrence: expect.objectContaining({
          start_date: startDate,
          frequency: 'weekly',
          interval: 2,
          weekdays: [1],
          month_days: [],
          timezone: expect.any(String),
        }),
      }),
    )
    expect(payload).not.toHaveProperty('planned_date')
    expect(payload).not.toHaveProperty('due')
  })
})
