import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
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
    <MemoryRouter>
      <QueryClientProvider client={queryClient}>
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/calendar" element={<div>日历目标页</div>} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
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

  it('uses the same structured project cache as the task workspace', async () => {
    const queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    })

    renderDashboard(queryClient)

    await waitFor(() => expect(tasksApi.listTaskProjects).toHaveBeenCalledTimes(1))
    expect(await screen.findByText('项目：个人 · 个人')).toBeVisible()
    expect(queryClient.getQueryData(['task-projects'])).toHaveLength(3)
    expect(queryClient.getQueryData(['task-project-names'])).toBeUndefined()
  })

  it('opens the calendar from the today agenda', async () => {
    const queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    })
    renderDashboard(queryClient)
    const user = userEvent.setup()

    await user.click(await screen.findByRole('button', { name: '＋ 添加日程' }))
    expect(await screen.findByText('日历目标页')).toBeVisible()
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

    const matchingButtons = await screen.findAllByRole('button', { name: '完成 读完故事这本书' })
    const row = matchingButtons.find((button) => within(button).queryByText('读完故事这本书'))
    expect(row).toBeDefined()
    if (!row) return
    expect(within(row).getByText('读完故事这本书')).toBeVisible()
    expect(within(row).getByLabelText('所属项目：学习写小说')).toBeVisible()
  })

  it('keeps next selected by default and shows only incomplete overdue tasks on demand', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: {
        todayTasks: [
          {
            id: 'today-open',
            title: '今天的任务',
            priority: 0,
            done: 0,
            scope: 'daily',
          },
        ],
        overdueTasks: [
          {
            id: 'overdue-open',
            title: '补齐逾期报告',
            planned_date: '2026-07-20',
            priority: 0,
            done: 0,
            scope: 'daily',
          },
          {
            id: 'overdue-done',
            title: '已完成的旧任务',
            planned_date: '2026-07-19',
            priority: 0,
            done: 1,
            scope: 'daily',
          },
        ],
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
    const user = userEvent.setup()

    renderDashboard(queryClient)

    const nextTab = await screen.findByRole('button', { name: '接下来 1' })
    const overdueTab = screen.getByRole('button', { name: '已逾期 1' })
    expect(nextTab).toHaveClass('is-active')
    expect(overdueTab).not.toHaveClass('is-active')
    expect(screen.getByText('今天的任务')).toBeVisible()
    expect(screen.queryByText('补齐逾期报告')).not.toBeInTheDocument()
    expect(screen.queryByText('已完成的旧任务')).not.toBeInTheDocument()

    await user.click(overdueTab)

    expect(overdueTab).toHaveClass('is-active')
    expect(screen.getByText('补齐逾期报告')).toBeVisible()
    expect(screen.queryByText('今天的任务')).not.toBeInTheDocument()
    expect(screen.queryByText('已完成的旧任务')).not.toBeInTheDocument()
  })
})
