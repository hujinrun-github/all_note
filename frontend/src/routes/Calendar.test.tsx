import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import Calendar from './Calendar'
import { api } from '../api/client'
import { useCreateEvent, useEventsList } from '../hooks/useEvents'
import { useCalendarProjectSources, useSaveCalendarProjectSources } from '../hooks/useCalendarSources'

const createEventMock = vi.fn()
const saveProjectSourcesMock = vi.fn()

vi.mock('../api/client', () => ({
  api: {
    get: vi.fn(),
  },
}))

vi.mock('../hooks/useEvents', () => ({
  useEventsList: vi.fn(),
  useCreateEvent: vi.fn(),
}))

vi.mock('../hooks/useCalendarSources', () => ({
  useCalendarProjectSources: vi.fn(),
  useSaveCalendarProjectSources: vi.fn(),
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

function getCalendarButtonByText(...parts: string[]) {
  const button = screen
    .getAllByRole('button')
    .find((candidate) => parts.some((part) => candidate.textContent?.includes(part)))
  expect(button).toBeTruthy()
  return button as HTMLElement
}

describe('Calendar today task flow', () => {
  beforeEach(() => {
    const currentDate = new Date()
    const eventStart = new Date(currentDate.getFullYear(), currentDate.getMonth(), currentDate.getDate(), 9, 30)
    const eventEnd = new Date(currentDate.getFullYear(), currentDate.getMonth(), currentDate.getDate(), 10, 15)

    vi.clearAllMocks()
    saveProjectSourcesMock.mockResolvedValue({})
    createEventMock.mockResolvedValue({})
    vi.mocked(useEventsList).mockReturnValue({
      data: {
        events: [
          {
            id: 'event-design-review',
            title: '设计评审',
            start_time: Math.floor(eventStart.getTime() / 1000),
            end_time: Math.floor(eventEnd.getTime() / 1000),
            kind: 'personal',
            project_id: 'personal',
            project: 'Personal',
            project_type: 'personal',
            created_at: Math.floor(eventStart.getTime() / 1000),
            updated_at: Math.floor(eventStart.getTime() / 1000),
          },
          {
            id: 'event-learning',
            title: '算法复盘',
            start_time: Math.floor(eventStart.getTime() / 1000),
            end_time: Math.floor(eventEnd.getTime() / 1000),
            kind: 'work',
            project_id: 'learning',
            project: '学习计划',
            project_type: 'learning',
            created_at: Math.floor(eventStart.getTime() / 1000),
            updated_at: Math.floor(eventStart.getTime() / 1000),
          },
          {
            id: 'event-regular',
            title: '周会准备',
            start_time: Math.floor(eventStart.getTime() / 1000),
            end_time: Math.floor(eventEnd.getTime() / 1000),
            kind: 'work',
            project_id: 'regular',
            project: '固定事项',
            project_type: 'regular',
            created_at: Math.floor(eventStart.getTime() / 1000),
            updated_at: Math.floor(eventStart.getTime() / 1000),
          },
        ],
        pagination: { page: 1, page_size: 50, total: 3, total_pages: 1 },
      },
      isLoading: false,
    } as unknown as ReturnType<typeof useEventsList>)
    vi.mocked(useCreateEvent).mockReturnValue({
      mutateAsync: createEventMock,
      isPending: false,
    } as unknown as ReturnType<typeof useCreateEvent>)
    vi.mocked(useCalendarProjectSources).mockReturnValue({
      data: {
        sources: [
          {
            project_id: 'personal',
            name: 'Personal',
            type: 'personal',
            enabled: true,
            default: true,
            color: '#2e90fa',
            order_index: 0,
          },
          {
            project_id: 'learning',
            name: '学习计划',
            type: 'learning',
            enabled: true,
            default: false,
            color: '#12b76a',
            order_index: 1,
          },
          {
            project_id: 'regular',
            name: '固定事项',
            type: 'regular',
            enabled: true,
            default: false,
            color: '#f79009',
            order_index: 2,
          },
        ],
        available_projects: [
          {
            project_id: 'archived',
            name: '归档项目',
            type: 'regular',
            enabled: false,
            default: false,
            color: '#667085',
            order_index: 3,
          },
        ],
      },
      isLoading: false,
    } as unknown as ReturnType<typeof useCalendarProjectSources>)
    vi.mocked(useSaveCalendarProjectSources).mockReturnValue({
      mutateAsync: saveProjectSourcesMock,
      isPending: false,
    } as unknown as ReturnType<typeof useSaveCalendarProjectSources>)
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

  it('renders project calendar sources and filters events by project id', async () => {
    const user = userEvent.setup()
    renderCalendar()

    expect(screen.getByRole('button', { name: '个人' })).toBeVisible()
    expect(screen.queryByRole('button', { name: 'Personal' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '学习计划' })).toBeVisible()
    expect(screen.getByRole('button', { name: '固定事项' })).toBeVisible()
    expect(await screen.findByLabelText('日程：设计评审，09:30')).toBeVisible()
    expect(screen.queryByLabelText('日程：算法复盘，09:30')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '学习计划' }))
    expect(screen.queryByLabelText('日程：设计评审，09:30')).not.toBeInTheDocument()
    expect(screen.getByLabelText('日程：算法复盘，09:30')).toBeVisible()

    await user.click(screen.getByRole('button', { name: '固定事项' }))
    expect(screen.queryByLabelText('日程：算法复盘，09:30')).not.toBeInTheDocument()
    expect(screen.getByLabelText('日程：周会准备，09:30')).toBeVisible()
  })

  it('saves configurable project source states including disabled available projects', async () => {
    const user = userEvent.setup()
    renderCalendar()

    await user.click(screen.getByRole('button', { name: '配置项目' }))
    expect(screen.getByLabelText('学习计划')).toBeChecked()
    expect(screen.getByLabelText('固定事项')).toBeChecked()
    expect(screen.getByLabelText('归档项目')).not.toBeChecked()

    await user.click(screen.getByRole('button', { name: '保存配置' }))

    expect(saveProjectSourcesMock).toHaveBeenCalledWith({
      sources: [
        { project_id: 'learning', enabled: true, color: '#12b76a', order_index: 1 },
        { project_id: 'regular', enabled: true, color: '#f79009', order_index: 2 },
        { project_id: 'archived', enabled: false, color: '#667085', order_index: 3 },
      ],
    })
  })

  it('guides users to task project management when no configurable projects exist', async () => {
    const user = userEvent.setup()
    vi.mocked(useCalendarProjectSources).mockReturnValue({
      data: {
        sources: [
          {
            project_id: 'personal',
            name: 'Personal',
            type: 'personal',
            enabled: true,
            default: true,
            color: '#2e90fa',
            order_index: 0,
          },
        ],
        available_projects: [],
      },
      isLoading: false,
    } as unknown as ReturnType<typeof useCalendarProjectSources>)
    renderCalendar()

    await user.click(getCalendarButtonByText('配置项目', '閰嶇疆椤圭洰'))
    expect(screen.queryByLabelText('calendar-source-name')).not.toBeInTheDocument()
    expect(screen.getByText('请到任务工作台新建长期项目或学习项目。')).toBeVisible()
    expect(screen.getByRole('button', { name: '去任务工作台' })).toBeVisible()
  })

  it('creates events with the selected project id while keeping compatible kind', async () => {
    const user = userEvent.setup()
    renderCalendar()

    await user.click(screen.getByRole('button', { name: '学习计划' }))
    await user.type(screen.getByPlaceholderText('新增日程'), '读论文')
    await user.click(screen.getByRole('button', { name: '添加日程' }))

    expect(createEventMock).toHaveBeenCalledWith(
      expect.objectContaining({
        title: '读论文',
        project_id: 'learning',
        kind: 'work',
      }),
    )
  })

  it('creates an event with the selected start and end times', async () => {
    const user = userEvent.setup()
    const currentDate = new Date()
    renderCalendar()

    await user.type(screen.getByPlaceholderText('新增日程'), '下午评审')
    fireEvent.change(screen.getByLabelText('开始时间'), { target: { value: '13:30' } })
    fireEvent.change(screen.getByLabelText('结束时间'), { target: { value: '15:00' } })
    await user.click(screen.getByRole('button', { name: '添加日程' }))

    const start = new Date(currentDate.getFullYear(), currentDate.getMonth(), currentDate.getDate(), 13, 30)
    const end = new Date(currentDate.getFullYear(), currentDate.getMonth(), currentDate.getDate(), 15, 0)
    expect(createEventMock).toHaveBeenCalledWith(
      expect.objectContaining({
        title: '下午评审',
        start_time: Math.floor(start.getTime() / 1000),
        end_time: Math.floor(end.getTime() / 1000),
      }),
    )
  })

  it('prevents creating an event when the end time is not after the start time', async () => {
    const user = userEvent.setup()
    renderCalendar()

    await user.type(screen.getByPlaceholderText('新增日程'), '无效日程')
    fireEvent.change(screen.getByLabelText('开始时间'), { target: { value: '15:00' } })
    fireEvent.change(screen.getByLabelText('结束时间'), { target: { value: '14:30' } })

    expect(screen.getByText('结束时间必须晚于开始时间')).toBeVisible()
    expect(screen.getByRole('button', { name: '添加日程' })).toBeDisabled()
  })

  it('creates an unassigned event when project sources are empty', async () => {
    const user = userEvent.setup()
    vi.mocked(useCalendarProjectSources).mockReturnValue({
      data: { sources: [], available_projects: [] },
      isLoading: false,
    } as unknown as ReturnType<typeof useCalendarProjectSources>)
    renderCalendar()

    await user.type(screen.getByRole('textbox'), 'unsourced event')
    await user.click(getCalendarButtonByText('添加日程', '娣诲姞鏃ョ▼'))

    const payload = createEventMock.mock.calls[0]?.[0]
    expect(payload).toEqual(
      expect.objectContaining({
        title: 'unsourced event',
        kind: 'work',
      }),
    )
    expect(payload).not.toHaveProperty('project_id')
  })

  it('creates without project_id from the unassigned source fallback', async () => {
    const user = userEvent.setup()
    const currentDate = new Date()
    const eventStart = new Date(currentDate.getFullYear(), currentDate.getMonth(), currentDate.getDate(), 13, 0)
    vi.mocked(useEventsList).mockReturnValue({
      data: {
        events: [
          {
            id: 'event-unassigned',
            title: 'orphan event',
            start_time: Math.floor(eventStart.getTime() / 1000),
            end_time: Math.floor(eventStart.getTime() / 1000) + 3600,
            kind: 'work',
            created_at: Math.floor(eventStart.getTime() / 1000),
            updated_at: Math.floor(eventStart.getTime() / 1000),
          },
        ],
        pagination: { page: 1, page_size: 50, total: 1, total_pages: 1 },
      },
      isLoading: false,
    } as unknown as ReturnType<typeof useEventsList>)
    renderCalendar()

    const unassignedButton = screen
      .getAllByRole('button')
      .find((button) => button.textContent?.includes('未归属') || button.textContent?.includes('鏈綊'))
    expect(unassignedButton).toBeTruthy()
    await user.click(unassignedButton as HTMLElement)
    await user.type(screen.getByRole('textbox'), 'new orphan event')
    await user.click(getCalendarButtonByText('添加日程', '娣诲姞鏃ョ▼'))

    const payload = createEventMock.mock.calls[0]?.[0]
    expect(payload).toEqual(
      expect.objectContaining({
        title: 'new orphan event',
        kind: 'work',
      }),
    )
    expect(payload).not.toHaveProperty('project_id')
  })
})
