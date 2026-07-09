import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
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
    <MemoryRouter>
      <QueryClientProvider client={queryClient}>
        <Calendar />
      </QueryClientProvider>
    </MemoryRouter>,
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
    const currentDate = new Date()
    const eventStart = new Date(currentDate.getFullYear(), currentDate.getMonth(), currentDate.getDate(), 9, 30)
    const eventEnd = new Date(currentDate.getFullYear(), currentDate.getMonth(), currentDate.getDate(), 10, 15)

    vi.clearAllMocks()
    vi.mocked(useEventsList).mockReturnValue({
      data: {
        events: [
          {
            id: 'event-design-review',
            title: '设计评审',
            start_time: Math.floor(eventStart.getTime() / 1000),
            end_time: Math.floor(eventEnd.getTime() / 1000),
            kind: 'personal',
            created_at: Math.floor(eventStart.getTime() / 1000),
            updated_at: Math.floor(eventStart.getTime() / 1000),
          },
        ],
        pagination: { page: 1, page_size: 50, total: 1, total_pages: 1 },
      },
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

  it('shows readable event summaries inside the selected month day', async () => {
    renderCalendar()

    expect(await screen.findByLabelText('日程：设计评审，09:30')).toBeVisible()
  })

  it('switches calendar views from the top mode controls', async () => {
    const user = userEvent.setup()
    renderCalendar()

    await user.click(screen.getByRole('button', { name: '周' }))
    expect(screen.getByText('本周视图')).toBeVisible()

    await user.click(screen.getByRole('button', { name: '日' }))
    expect(screen.getByText('当日视图')).toBeVisible()
  })

  it('lets calendar sources change the visible calendar and open the add calendar form', async () => {
    const user = userEvent.setup()
    renderCalendar()

    expect(await screen.findByLabelText('日程：设计评审，09:30')).toBeVisible()

    await user.click(screen.getByRole('button', { name: '工作' }))
    expect(screen.queryByLabelText('日程：设计评审，09:30')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '添加日历' }))
    expect(screen.getByPlaceholderText('新日历名称')).toBeVisible()
  })
})
