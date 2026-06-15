import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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
      <Tasks />
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

    expect(await screen.findByRole('option', { name: 'AI Infra' })).toBeVisible()
    expect(screen.queryByText('先在左侧新增一个任务项目')).not.toBeInTheDocument()
  })
})

describe('Tasks learning roadmap weekly linking', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(tasksApi.listTaskProjects).mockResolvedValue([...projects, learningProject])
    vi.mocked(tasksApi.getLearningRoadmap).mockResolvedValue(roadmap)
    vi.mocked(tasksApi.getTasks).mockResolvedValue({ tasks: [], pagination })
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

  it('keeps the roadmap canvas mounted after adding a node to this week', async () => {
    renderTasks()
    const user = userEvent.setup()

    await user.click(await screen.findByRole('tab', { name: '学习 Roadmap' }))

    expect(await screen.findByTestId('roadmap-canvas')).toBeVisible()
    expect(screen.getAllByText('AI Infra概述与系统设计基础')[0]).toBeVisible()

    vi.mocked(tasksApi.getLearningRoadmap).mockResolvedValue(null)

    await user.click(screen.getByRole('button', { name: '加入本周' }))

    await waitFor(() => expect(tasksApi.createTask).toHaveBeenCalled())
    expect(vi.mocked(tasksApi.createTask).mock.calls[0]?.[0]).toEqual(
      expect.objectContaining({
        title: 'AI Infra概述与系统设计基础 · 第 1 次推进',
        project_id: learningProject.id,
        roadmap_node_id: 'node-1',
        horizon: 'week',
        scope: 'daily',
      }),
    )
    await waitFor(() => expect(tasksApi.getTasks).toHaveBeenCalledTimes(4))

    expect(tasksApi.getLearningRoadmap).toHaveBeenCalledTimes(1)
    expect(screen.getByTestId('roadmap-canvas')).toBeVisible()
    expect(screen.queryByText('这个学习项目还没有 Roadmap')).not.toBeInTheDocument()
  })

  it('increments the generated title for the next linked roadmap task', async () => {
    vi.mocked(tasksApi.getTasks).mockImplementation(async (params) => {
      if (params.horizon === 'week') {
        return {
          tasks: [
            task({
              id: 'existing-roadmap-task',
              title: 'AI Infra概述与系统设计基础 · 第 1 次推进',
              project: learningProject.name,
              project_id: learningProject.id,
              project_type: 'learning',
              roadmap_node_id: 'node-1',
              planned_date: '2026-06-14',
              horizon: 'week',
              scope: 'daily',
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
    await screen.findByTestId('roadmap-canvas')

    await user.click(screen.getByRole('button', { name: '加入本周' }))

    await waitFor(() => expect(tasksApi.createTask).toHaveBeenCalled())
    expect(vi.mocked(tasksApi.createTask).mock.calls[0]?.[0]).toEqual(
      expect.objectContaining({
        title: 'AI Infra概述与系统设计基础 · 第 2 次推进',
        roadmap_node_id: 'node-1',
      }),
    )
  })

  it('does not create another weekly task when the roadmap node is already planned today', async () => {
    const today = todayDateInputValue()
    vi.mocked(tasksApi.getTasks).mockImplementation(async (params) => {
      if (params.horizon === 'week') {
        return {
          tasks: [
            task({
              id: 'existing-week-roadmap-task',
              title: 'AI Infra概述与系统设计基础',
              project: learningProject.name,
              project_id: learningProject.id,
              project_type: 'learning',
              roadmap_node_id: 'node-1',
              planned_date: today,
              due: dateInputToUnix(today),
              horizon: 'week',
              scope: 'daily',
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
    await screen.findByTestId('roadmap-canvas')

    await user.click(screen.getByRole('button', { name: '加入本周' }))

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
})
