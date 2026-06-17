import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import Dashboard from './Dashboard'
import { api } from '../api/client'
import * as tasksApi from '../api/tasks'

vi.mock('../api/client', () => ({
  api: {
    get: vi.fn(),
  },
}))

vi.mock('../api/tasks')

function renderDashboard(queryClient: QueryClient) {
  return render(
    <QueryClientProvider client={queryClient}>
      <Dashboard />
    </QueryClientProvider>,
  )
}

describe('Dashboard task project cache', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(api.get).mockResolvedValue({
      data: {
        todayTasks: [],
        overdueTasks: [],
        events: [],
        recentNotes: [],
      },
    })
    vi.mocked(tasksApi.listTaskProjects).mockResolvedValue([
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
      {
        id: 'learning-1',
        name: '学习写小说',
        type: 'learning',
        description: '',
        created_at: 1,
        updated_at: 1,
      },
    ])
    vi.mocked(tasksApi.createTask).mockResolvedValue({
      id: 'new-task',
      title: '整理写作资料',
      content: '',
      project: '学习写小说',
      project_id: 'learning-1',
      project_type: 'learning',
      priority: 0,
      done: 0,
      status: 'open',
      horizon: 'week',
      scope: 'daily',
      sort_order: 0,
      created_at: 1,
      updated_at: 1,
    })
  })

  it('uses the same structured project options as the task workspace', async () => {
    const queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    })

    renderDashboard(queryClient)

    await waitFor(() => expect(tasksApi.listTaskProjects).toHaveBeenCalledTimes(1))
    expect(await screen.findByRole('option', { name: '个人 · 个人' })).toBeVisible()
    expect(screen.getByRole('option', { name: 'AI Infra · 任务项目' })).toBeVisible()
    expect(screen.getByRole('option', { name: '学习写小说 · 学习项目' })).toBeVisible()
    expect(queryClient.getQueryData(['task-projects'])).toHaveLength(3)
    expect(queryClient.getQueryData(['task-project-names'])).toBeUndefined()
  })

  it('creates today tasks with the selected structured project id', async () => {
    const queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    })
    renderDashboard(queryClient)
    const user = userEvent.setup()

    await user.selectOptions(await screen.findByLabelText('任务项目'), 'learning-1')
    await user.type(screen.getByPlaceholderText('新增任务'), '整理写作资料')
    await user.click(screen.getByRole('button', { name: '添加' }))

    await waitFor(() => expect(tasksApi.createTask).toHaveBeenCalled())
    expect(vi.mocked(tasksApi.createTask).mock.calls[0]?.[0]).toEqual(
      expect.objectContaining({
        title: '整理写作资料',
        project_id: 'learning-1',
        horizon: 'week',
        scope: 'daily',
      }),
    )
  })

  it('renders active long tasks returned by today api', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: {
        todayTasks: [
          {
            id: 'long-active',
            title: '读完故事这本书',
            project: '学习写小说',
            priority: 0,
            done: 0,
            scope: 'yearly',
          },
        ],
        overdueTasks: [],
        events: [],
        recentNotes: [],
      },
    })
    const queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    })

    renderDashboard(queryClient)

    const row = await screen.findByRole('button', { name: '完成 读完故事这本书' })
    expect(within(row).getByText('读完故事这本书')).toBeVisible()
    expect(within(row).getByLabelText('所属项目：学习写小说')).toBeVisible()
  })
})
