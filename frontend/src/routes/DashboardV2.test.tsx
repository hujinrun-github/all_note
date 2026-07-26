import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as taskHooks from '../hooks/useTaskDomain'
import DashboardV2 from './DashboardV2'

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
  taskID: string
): import('../api/taskDomain').OccurrenceV2 {
  return {
    id,
    task_id: taskID,
    occurrence_key: 'once',
    execution_status: 'open',
    revision: 3,
    generated_schedule_revision: 1,
  }
}
