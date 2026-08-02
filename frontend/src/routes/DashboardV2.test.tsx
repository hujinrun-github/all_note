import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as taskHooks from '../hooks/useTaskDomain'
import DashboardV2, { createDashboardDateRanges } from './DashboardV2'

vi.mock('../hooks/useTaskDomain')

describe('Dashboard v2 today projection', () => {
  const createTaskMock = vi.fn()

  beforeEach(() => {
    vi.clearAllMocks()
    createTaskMock.mockResolvedValue({})
    vi.mocked(taskHooks.useTaskDefinitions).mockReturnValue({
      data: [task('today', '今天处理'), task('overdue', '补交周报')],
      isLoading: false,
    } as ReturnType<typeof taskHooks.useTaskDefinitions>)
    vi.mocked(taskHooks.useProjects).mockReturnValue({
      data: [
        {
          id: 'personal',
          name: '个人事务',
          kind: 'standard',
          horizon: 'short',
          status: 'active',
          system_role: 'personal',
          revision: 1,
        },
      ],
      isLoading: false,
    } as ReturnType<typeof taskHooks.useProjects>)
    vi.mocked(taskHooks.useOccurrences).mockImplementation(
      (params) =>
        ({
          data:
            params?.scope === 'today'
              ? [occurrence('today-occ', 'today')]
              : params?.scope === 'overdue'
                ? [occurrence('overdue-occ', 'overdue')]
                : [],
          isLoading: false,
          isError: false,
        }) as ReturnType<typeof taskHooks.useOccurrences>
    )
    vi.mocked(taskHooks.useCompleteOccurrenceMutation).mockReturnValue({
      mutateAsync: vi.fn(),
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useCompleteOccurrenceMutation>)
    vi.mocked(taskHooks.useStartOccurrenceMutation).mockReturnValue(
      idleMutation() as ReturnType<typeof taskHooks.useStartOccurrenceMutation>
    )
    vi.mocked(taskHooks.useBlockOccurrenceMutation).mockReturnValue(
      idleMutation() as ReturnType<typeof taskHooks.useBlockOccurrenceMutation>
    )
    vi.mocked(taskHooks.useUnblockOccurrenceMutation).mockReturnValue(
      idleMutation() as ReturnType<
        typeof taskHooks.useUnblockOccurrenceMutation
      >
    )
    vi.mocked(taskHooks.useReopenOccurrenceMutation).mockReturnValue(
      idleMutation() as ReturnType<typeof taskHooks.useReopenOccurrenceMutation>
    )
    vi.mocked(taskHooks.useUpdateTaskDefinitionMutation).mockReturnValue(
      idleMutation() as ReturnType<
        typeof taskHooks.useUpdateTaskDefinitionMutation
      >
    )
    vi.mocked(taskHooks.useCreateTaskMutation).mockReturnValue({
      mutateAsync: createTaskMock,
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useCreateTaskMutation>)
  })

  it('defaults to today instead of the overdue collection', async () => {
    renderDashboard()
    const user = userEvent.setup()

    const today = screen.getByRole('tab', { name: '今天 1' })
    const overdue = screen.getByRole('tab', { name: '已逾期 1' })
    expect(today).toHaveAttribute('aria-selected', 'true')
    expect(overdue).toHaveAttribute('aria-selected', 'false')
    expect(screen.getByText('今天处理')).toBeVisible()
    expect(screen.queryByText('补交周报')).not.toBeInTheDocument()

    await user.click(overdue)
    expect(screen.getByText('补交周报')).toBeVisible()
  })

  it('shows open task occurrences for the current week and month', async () => {
    vi.mocked(taskHooks.useTaskDefinitions).mockReturnValue({
      data: [
        task('today', '今天处理'),
        task('week', '本周复习'),
        task('month', '本月复盘'),
      ],
      isLoading: false,
    } as ReturnType<typeof taskHooks.useTaskDefinitions>)
    vi.mocked(taskHooks.useOccurrences).mockImplementation((params) => {
      if (params?.scope !== 'upcoming' || !params.from || !params.to) {
        return {
          data:
            params?.scope === 'today'
              ? [occurrence('today-occ', 'today')]
              : [],
          isLoading: false,
          isError: false,
        } as ReturnType<typeof taskHooks.useOccurrences>
      }
      const rangeDays =
        (new Date(params.to).getTime() - new Date(params.from).getTime()) /
        86_400_000
      const item =
        rangeDays > 20
          ? occurrence('month-occ', 'month', '2026-08-20T03:00:00.000Z')
          : occurrence('week-occ', 'week', '2026-08-06T03:00:00.000Z')
      return {
        data: [item],
        isLoading: false,
        isError: false,
      } as ReturnType<typeof taskHooks.useOccurrences>
    })
    const user = userEvent.setup()
    renderDashboard()

    const week = screen.getByRole('tab', { name: '本周 1' })
    const month = screen.getByRole('tab', { name: '本月 1' })
    await user.click(week)
    expect(screen.getByText('本周复习')).toBeVisible()
    expect(screen.getByText('本周要做')).toBeVisible()

    await user.click(month)
    expect(screen.getByText('本月复盘')).toBeVisible()
    expect(screen.getByText('本月要做')).toBeVisible()
  })

  it('keeps a recurring weekly task in a collection even with one execution', async () => {
    vi.mocked(taskHooks.useTaskDefinitions).mockReturnValue({
      data: [task('study', '每周学习'), task('once', '一次性任务')],
      isLoading: false,
    } as ReturnType<typeof taskHooks.useTaskDefinitions>)
    const weeklyOccurrences = [
      {
        ...occurrence('study-2', 'study', '2026-08-02T03:00:00.000Z'),
        occurrence_key: '2026-08-02',
        planned_date: '2026-08-02',
        recurring: true,
      },
      occurrence('once-2', 'once', '2026-08-02T05:00:00.000Z'),
    ]
    vi.mocked(taskHooks.useOccurrences).mockImplementation((params) => {
      const rangeDays =
        params?.from && params.to
          ? (new Date(params.to).getTime() - new Date(params.from).getTime()) /
            86_400_000
          : 0
      return {
        data:
          params?.scope === 'upcoming' && rangeDays > 0 && rangeDays < 10
            ? weeklyOccurrences
            : [],
        isLoading: false,
        isError: false,
      } as ReturnType<typeof taskHooks.useOccurrences>
    })
    const user = userEvent.setup()
    renderDashboard()

    await user.click(screen.getByRole('tab', { name: '本周 2' }))
    expect(
      screen.getByRole('button', { name: '展开每周学习，1 次执行' })
    ).toHaveAttribute('aria-expanded', 'false')
    expect(screen.getByText('一次性任务')).toBeVisible()
    expect(
      screen.queryByRole('button', { name: '展开一次性任务，1 次执行' })
    ).not.toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: '展开每周学习，1 次执行' })
    )
    expect(screen.getByText('每周学习 · 8月2日')).toBeVisible()
  })

  it('groups different execution times of the same task into an expandable collection', async () => {
    vi.mocked(taskHooks.useTaskDefinitions).mockReturnValue({
      data: [task('study', '单词学习')],
      isLoading: false,
    } as ReturnType<typeof taskHooks.useTaskDefinitions>)
    const monthlyOccurrences = [2, 3, 31].map((day) => ({
      ...occurrence(
        `study-${day}`,
        'study',
        `2026-08-${String(day).padStart(2, '0')}T03:00:00.000Z`
      ),
      occurrence_key: `2026-08-${String(day).padStart(2, '0')}`,
      planned_date: `2026-08-${String(day).padStart(2, '0')}`,
      recurring: true,
    }))
    vi.mocked(taskHooks.useOccurrences).mockImplementation((params) => {
      const rangeDays =
        params?.from && params.to
          ? (new Date(params.to).getTime() - new Date(params.from).getTime()) /
            86_400_000
          : 0
      return {
        data:
          params?.scope === 'upcoming' && rangeDays > 20
            ? monthlyOccurrences
            : [],
        isLoading: false,
        isError: false,
      } as ReturnType<typeof taskHooks.useOccurrences>
    })
    const user = userEvent.setup()
    renderDashboard()

    await user.click(screen.getByRole('tab', { name: '本月 3' }))
    const collection = screen.getByRole('button', {
      name: '展开单词学习，3 次执行',
    })
    expect(collection).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByText('单词学习 · 8月2日')).not.toBeInTheDocument()

    await user.click(collection)
    const instances = screen.getByRole('list', {
      name: '单词学习执行实例',
    })
    expect(
      screen.getByRole('button', { name: '收起单词学习，3 次执行' })
    ).toHaveAttribute('aria-expanded', 'true')
    expect(within(instances).getByText('单词学习 · 8月2日')).toBeVisible()
    expect(within(instances).getByText('单词学习 · 8月31日')).toBeVisible()

    await user.click(within(instances).getByText('单词学习 · 8月3日'))
    expect(
      screen.getByRole('complementary', {
        name: '执行详情：单词学习 · 8月3日',
      })
    ).toBeVisible()

    await user.click(
      screen.getByRole('button', { name: '收起单词学习，3 次执行' })
    )
    expect(
      screen.queryByRole('list', { name: '单词学习执行实例' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('complementary', {
        name: '执行详情：单词学习 · 8月3日',
      })
    ).not.toBeInTheDocument()
  })

  it('builds Monday-based week and calendar-month query ranges', () => {
    const ranges = createDashboardDateRanges(
      new Date(2026, 7, 5, 12),
      'Test/Timezone'
    )

    const weekStart = new Date(ranges.week.from)
    const weekEnd = new Date(ranges.week.to)
    const monthStart = new Date(ranges.month.from)
    const monthEnd = new Date(ranges.month.to)
    expect([
      weekStart.getDay(),
      weekStart.getHours(),
      weekEnd.getDay(),
      weekEnd.getHours(),
    ]).toEqual([1, 0, 1, 0])
    expect([
      monthStart.getDate(),
      monthStart.getHours(),
      monthEnd.getDate(),
      monthEnd.getHours(),
    ]).toEqual([1, 0, 1, 0])
    expect(ranges.week.timezone).toBe('Test/Timezone')
    expect(ranges.month.timezone).toBe('Test/Timezone')
  })

  it('creates a timed schedule in the Personal project', async () => {
    const user = userEvent.setup()
    renderDashboard()

    await user.click(screen.getByRole('button', { name: '新增日程' }))

    const dialog = screen.getByRole('dialog', { name: '新增日程' })
    expect(dialog).toBeVisible()
    expect(within(dialog).getByText('个人事务')).toBeVisible()
    expect(screen.queryByLabelText('日程项目')).not.toBeInTheDocument()

    await user.type(screen.getByLabelText('日程标题'), '项目方案评审')
    fireEvent.change(screen.getByLabelText('日程日期'), {
      target: { value: '2026-07-26' },
    })
    fireEvent.change(screen.getByLabelText('开始时间'), {
      target: { value: '14:00' },
    })
    fireEvent.change(screen.getByLabelText('结束时间'), {
      target: { value: '15:30' },
    })
    await user.click(screen.getByRole('button', { name: '创建日程' }))

    expect(createTaskMock).toHaveBeenCalledWith({
      project_id: 'personal',
      title: '项目方案评审',
      description: undefined,
      priority: 0,
      schedule: {
        recurrence_type: 'none',
        timing_type: 'time_block',
        timezone: expect.any(String),
        starts_on: '2026-07-26',
        local_start_time: '14:00',
        duration_minutes: 90,
      },
    })
  })

  it('creates a monthly recurring schedule in the Personal project', async () => {
    const user = userEvent.setup()
    renderDashboard()

    await user.click(screen.getByRole('button', { name: '新增日程' }))
    await user.type(screen.getByLabelText('日程标题'), '月度学习复盘')
    fireEvent.change(screen.getByLabelText('日程日期'), {
      target: { value: '2026-08-26' },
    })
    await user.selectOptions(
      screen.getByRole('combobox', { name: '日程重复方式' }),
      'monthly'
    )
    fireEvent.change(screen.getByLabelText('开始时间'), {
      target: { value: '20:00' },
    })
    fireEvent.change(screen.getByLabelText('结束时间'), {
      target: { value: '21:00' },
    })
    await user.click(screen.getByRole('button', { name: '创建日程' }))

    expect(createTaskMock).toHaveBeenCalledWith(
      expect.objectContaining({
        project_id: 'personal',
        title: '月度学习复盘',
        schedule: {
          recurrence_type: 'monthly',
          timing_type: 'time_block',
          timezone: expect.any(String),
          starts_on: '2026-08-26',
          local_start_time: '20:00',
          duration_minutes: 60,
          rule: { interval: 1, month_days: [26] },
        },
      })
    )
  })

  it('locks quick completion when a normal task still has required items', () => {
    const complete = vi.fn()
    vi.mocked(taskHooks.useTaskDefinitions).mockReturnValue({
      data: [
        {
          ...task('today', '今天处理'),
          completion_requirements: [
            {
              id: 'article-1',
              kind: 'article',
              title: '读完方案',
              completed: false,
            },
          ],
        },
        task('overdue', '补交周报'),
      ],
      isLoading: false,
    } as ReturnType<typeof taskHooks.useTaskDefinitions>)
    vi.mocked(taskHooks.useCompleteOccurrenceMutation).mockReturnValue({
      mutateAsync: complete,
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useCompleteOccurrenceMutation>)

    renderDashboard()

    expect(screen.getByRole('button', { name: '完成今天处理' })).toBeDisabled()
    expect(screen.getByText('完成门槛 0/1')).toBeVisible()
    expect(complete).not.toHaveBeenCalled()
  })
})

function renderDashboard() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <MemoryRouter>
      <QueryClientProvider client={client}>
        <DashboardV2 />
      </QueryClientProvider>
    </MemoryRouter>
  )
}

function idleMutation() {
  return { mutateAsync: vi.fn(), isPending: false } as unknown
}

function task(id: string, title: string): import('../api/taskDomain').TaskV2 {
  return {
    id,
    project_id: 'personal',
    title,
    priority: 0,
    sort_order: 0,
    lifecycle_status: 'active',
    revision: 2,
    schedule_revision: 1,
  }
}

function occurrence(
  id: string,
  taskID: string,
  plannedStartAt?: string
): import('../api/taskDomain').OccurrenceV2 {
  return {
    id,
    task_id: taskID,
    occurrence_key: 'once',
    execution_status: 'open',
    revision: 3,
    generated_schedule_revision: 1,
    planned_start_at: plannedStartAt,
    planned_end_at: plannedStartAt
      ? new Date(new Date(plannedStartAt).getTime() + 30 * 60_000).toISOString()
      : undefined,
  }
}
