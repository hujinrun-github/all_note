import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import Calendar from './Calendar'
import { api } from '../api/client'
import { useCreateEvent, useEventsList } from '../hooks/useEvents'

vi.mock('../api/client', () => ({
  api: {
    get: vi.fn(),
  },
}))

vi.mock('../hooks/useEvents', () => ({
  useEventsList: vi.fn(),
  useCreateEvent: vi.fn(),
}))

function renderCalendar(queryClient = createTestQueryClient()) {
  return render(
    <QueryClientProvider client={queryClient}>
      <Calendar />
    </QueryClientProvider>,
  )
}

function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
}

describe('Calendar today task flow', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(useEventsList).mockReturnValue({
      data: { events: [], pagination: { page: 1, page_size: 50, total: 0, total_pages: 1 } },
      isLoading: false,
    } as unknown as ReturnType<typeof useEventsList>)
    vi.mocked(useCreateEvent).mockReturnValue({
      mutateAsync: vi.fn(),
      isPending: false,
    } as unknown as ReturnType<typeof useCreateEvent>)
    vi.mocked(api.get).mockResolvedValue({
      data: {
        todayTasks: [
          {
            id: 'task-today',
            title: '推进 PostgreSQL 迁移',
            project: 'AI Infra',
            planned_date: '2026-06-17',
            priority: 0,
            done: 0,
            scope: 'daily',
          },
        ],
        overdueTasks: [
          {
            id: 'task-overdue',
            title: '补齐日历任务流',
            project: 'FlowSpace',
            planned_date: '2026-06-16',
            priority: 1,
            done: 0,
            scope: 'daily',
          },
        ],
        events: [],
        recentNotes: [],
      },
    })
  })

  it('shows the today task flow in the calendar inspector', async () => {
    renderCalendar()

    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/api/today'))

    const taskFlow = await screen.findByTestId('calendar-today-task-flow')
    expect(within(taskFlow).getByText('今日任务流')).toBeVisible()
    expect(within(taskFlow).getByText('推进 PostgreSQL 迁移')).toBeVisible()
    expect(within(taskFlow).getByLabelText('所属项目：AI Infra')).toBeVisible()
    expect(within(taskFlow).getByText('补齐日历任务流')).toBeVisible()
    expect(within(taskFlow).getByLabelText('所属项目：FlowSpace')).toBeVisible()
  })
})
