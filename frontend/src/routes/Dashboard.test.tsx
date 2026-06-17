import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
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
    vi.mocked(tasksApi.getTaskProjects).mockResolvedValue(['个人', '学习写小说'])
  })

  it('keeps project name cache separate from structured task projects', async () => {
    const queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    })

    renderDashboard(queryClient)

    await waitFor(() => expect(tasksApi.getTaskProjects).toHaveBeenCalledTimes(1))
    expect(queryClient.getQueryData(['task-project-names'])).toEqual(['个人', '学习写小说'])
    expect(queryClient.getQueryData(['task-projects'])).toBeUndefined()
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
