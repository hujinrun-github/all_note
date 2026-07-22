import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as taskHooks from '../hooks/useTaskDomain'
import TaskOccurrenceWorkspace from './TaskOccurrenceWorkspace'

vi.mock('../hooks/useTaskDomain')

describe('Task occurrence workspace', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(taskHooks.useProjects).mockReturnValue({
      data: [
        {
          id: 'system-inbox',
          name: '收件箱',
          kind: 'standard',
          horizon: 'short',
          status: 'active',
          system_role: 'inbox',
          revision: 1,
        },
      ],
      isLoading: false,
    } as ReturnType<typeof taskHooks.useProjects>)
    vi.mocked(taskHooks.useTaskDefinitions).mockReturnValue({
      data: taskDefinitions,
      isLoading: false,
    } as ReturnType<typeof taskHooks.useTaskDefinitions>)
    vi.mocked(taskHooks.useOccurrences).mockImplementation((params) => ({
      data: occurrencesByScope[params?.scope ?? 'all'] ?? [],
      isLoading: false,
      isError: false,
    } as ReturnType<typeof taskHooks.useOccurrences>))
    vi.mocked(taskHooks.useCompleteOccurrenceMutation).mockReturnValue({
      mutateAsync: vi.fn(),
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useCompleteOccurrenceMutation>)
    vi.mocked(taskHooks.useCreateTaskMutation).mockReturnValue({
      mutateAsync: vi.fn(),
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useCreateTaskMutation>)
    vi.mocked(taskHooks.useRescheduleOccurrenceMutation).mockReturnValue({
      mutateAsync: vi.fn(),
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useRescheduleOccurrenceMutation>)
  })

  it('keeps upcoming selected by default and never mixes overdue into it', async () => {
    renderWorkspace()

    const upcoming = screen.getByRole('tab', { name: '接下来 5' })
    const overdue = screen.getByRole('tab', { name: '已逾期 1' })
    expect(upcoming).toHaveAttribute('aria-selected', 'true')
    expect(overdue).toHaveAttribute('aria-selected', 'false')
    expect(screen.getByText('准备评审')).toBeVisible()
    expect(screen.queryByText('补交周报')).not.toBeInTheDocument()

    await userEvent.click(overdue)
    expect(screen.getByText('补交周报')).toBeVisible()
    expect(screen.queryByText('准备评审')).not.toBeInTheDocument()
  })

  it('renders open, active, blocked, and done distinctly with block metadata', () => {
    renderWorkspace()

    const list = screen.getByRole('list', { name: '任务执行实例' })
    expect(within(list).getAllByText('未开始')[0]).toBeVisible()
    expect(within(list).getAllByText('进行中')[0]).toBeVisible()
    expect(within(list).getByText('阻塞')).toBeVisible()
    expect(within(list).getByText('已完成')).toBeVisible()
    expect(within(list).getByText('原因：等待接口评审')).toBeVisible()
    expect(within(list).getByText('下一步：周五跟进')).toBeVisible()
  })

  it('completing one recurring occurrence does not mark its next occurrence done', () => {
    renderWorkspace()

    expect(screen.getByText('每日复盘 · 7月22日')).toBeVisible()
    expect(screen.getByText('每日复盘 · 7月23日')).toBeVisible()
    expect(screen.getByLabelText('每日复盘 · 7月22日执行状态')).toHaveTextContent('已完成')
    expect(screen.getByLabelText('每日复盘 · 7月23日执行状态')).toHaveTextContent('未开始')
  })

  it('preserves the local date and offers refresh/compare when reschedule conflicts', async () => {
    const conflict = new (await import('../api/taskDomain')).TaskDomainRevisionConflictError(
      'occurrence changed',
      {
        expected_task_revision: 2,
        expected_schedule_revision: 1,
        expected_occurrence_revisions: { 'open-occurrence': 3 },
      },
      { occurrence_revisions: { 'open-occurrence': 4 } }
    )
    const reschedule = vi.fn().mockRejectedValue(conflict)
    vi.mocked(taskHooks.useRescheduleOccurrenceMutation).mockReturnValue({
      mutateAsync: reschedule,
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useRescheduleOccurrenceMutation>)
    renderWorkspace()
    const user = userEvent.setup()

    await user.click(screen.getByRole('button', { name: '改期准备评审' }))
    const date = screen.getByLabelText('新的执行日期')
    await user.type(date, '2026-07-25')
    await user.click(screen.getByRole('button', { name: '保存改期' }))

    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent('执行实例已在其他窗口更新')
    )
    expect(date).toHaveValue('2026-07-25')
    expect(screen.getByRole('button', { name: '刷新服务器版本' })).toBeVisible()
    expect(screen.getByRole('button', { name: '比较差异' })).toBeVisible()
  })
})

function renderWorkspace() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <MemoryRouter>
      <QueryClientProvider client={client}>
        <TaskOccurrenceWorkspace />
      </QueryClientProvider>
    </MemoryRouter>
  )
}

const taskDefinitions: import('../api/taskDomain').TaskV2[] = [
  task('open-task', '准备评审'),
  task('active-task', '实现接口'),
  task('blocked-task', '联调服务'),
  task('recurring-task', '每日复盘'),
]

const occurrencesByScope: Partial<
  Record<import('../api/taskDomain').OccurrenceListScope, import('../api/taskDomain').OccurrenceV2[]>
> = {
  upcoming: [
    occurrence('open-occurrence', 'open-task', 'open'),
    occurrence('active-occurrence', 'active-task', 'active'),
    {
      ...occurrence('blocked-occurrence', 'blocked-task', 'blocked'),
      blocked_reason: '等待接口评审',
      next_action: '周五跟进',
    },
    {
      ...occurrence('done-recurring', 'recurring-task', 'done'),
      occurrence_key: '2026-07-22',
      recurring: true,
      planned_date: '2026-07-22',
    },
    {
      ...occurrence('next-recurring', 'recurring-task', 'open'),
      occurrence_key: '2026-07-23',
      recurring: true,
      planned_date: '2026-07-23',
    },
  ],
  overdue: [
    { ...occurrence('overdue', 'open-task', 'open'), title: '补交周报' },
  ],
  unscheduled: [],
  completed: [],
}

function task(id: string, title: string): import('../api/taskDomain').TaskV2 {
  return {
    id,
    project_id: 'system-inbox',
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
  status: import('../api/taskDomain').ExecutionStatus
): import('../api/taskDomain').OccurrenceV2 {
  return {
    id,
    task_id: taskID,
    occurrence_key: 'once',
    execution_status: status,
    revision: 3,
    generated_schedule_revision: 1,
  }
}
