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
        {
          id: 'work-project',
          name: '工作项目',
          kind: 'standard',
          horizon: 'short',
          status: 'active',
          revision: 1,
        },
      ],
      isLoading: false,
    } as ReturnType<typeof taskHooks.useProjects>)
    vi.mocked(taskHooks.useTaskDefinitions).mockReturnValue({
      data: taskDefinitions,
      isLoading: false,
    } as ReturnType<typeof taskHooks.useTaskDefinitions>)
    vi.mocked(taskHooks.useOccurrences).mockImplementation(
      (params) =>
        ({
          data: occurrencesByScope[params?.scope ?? 'all'] ?? [],
          isLoading: false,
          isError: false,
        }) as ReturnType<typeof taskHooks.useOccurrences>
    )
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
    } as unknown as ReturnType<
      typeof taskHooks.useRescheduleOccurrenceMutation
    >)
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
    vi.mocked(taskHooks.usePublishTaskMutation).mockReturnValue(
      idleMutation() as ReturnType<typeof taskHooks.usePublishTaskMutation>
    )
    vi.mocked(taskHooks.usePauseTaskMutation).mockReturnValue(
      idleMutation() as ReturnType<typeof taskHooks.usePauseTaskMutation>
    )
    vi.mocked(taskHooks.useResumeTaskMutation).mockReturnValue(
      idleMutation() as ReturnType<typeof taskHooks.useResumeTaskMutation>
    )
    vi.mocked(taskHooks.useCancelTaskMutation).mockReturnValue(
      idleMutation() as ReturnType<typeof taskHooks.useCancelTaskMutation>
    )
    vi.mocked(taskHooks.useRestoreTaskMutation).mockReturnValue(
      idleMutation() as ReturnType<typeof taskHooks.useRestoreTaskMutation>
    )
    vi.mocked(taskHooks.useArchiveTaskMutation).mockReturnValue(
      idleMutation() as ReturnType<typeof taskHooks.useArchiveTaskMutation>
    )
    vi.mocked(taskHooks.useDeleteTaskMutation).mockReturnValue(
      idleMutation() as ReturnType<typeof taskHooks.useDeleteTaskMutation>
    )
    vi.mocked(taskHooks.useUpdateTaskDefinitionMutation).mockReturnValue(
      idleMutation() as ReturnType<
        typeof taskHooks.useUpdateTaskDefinitionMutation
      >
    )
  })

  it('keeps upcoming selected by default and never mixes overdue into it', async () => {
    renderWorkspace()

    const upcoming = screen.getByRole('tab', { name: '接下来 6' })
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

  it('filters visible occurrences by project, priority, and status', async () => {
    renderWorkspace()
    const user = userEvent.setup()

    await user.selectOptions(
      screen.getByLabelText('按项目筛选'),
      'work-project'
    )
    expect(screen.getByText('发布版本')).toBeVisible()
    expect(screen.queryByText('准备评审')).not.toBeInTheDocument()
    expect(screen.getByText('1 个结果')).toBeVisible()

    await user.selectOptions(screen.getByLabelText('按项目筛选'), '')
    await user.selectOptions(screen.getByLabelText('按优先级筛选'), '2')
    expect(screen.getByText('发布版本')).toBeVisible()
    expect(screen.queryByText('实现接口')).not.toBeInTheDocument()

    await user.selectOptions(screen.getByLabelText('按优先级筛选'), '')
    await user.selectOptions(screen.getByLabelText('按状态筛选'), 'blocked')
    expect(screen.getByText('联调服务')).toBeVisible()
    expect(screen.queryByText('发布版本')).not.toBeInTheDocument()
    expect(screen.getByText('1 个结果')).toBeVisible()
  })

  it('completing one recurring occurrence does not mark its next occurrence done', () => {
    renderWorkspace()

    expect(screen.getByText('每日复盘 · 7月22日')).toBeVisible()
    expect(screen.getByText('每日复盘 · 7月23日')).toBeVisible()
    expect(
      screen.getByLabelText('每日复盘 · 7月22日执行状态')
    ).toHaveTextContent('已完成')
    expect(
      screen.getByLabelText('每日复盘 · 7月23日执行状态')
    ).toHaveTextContent('未开始')
  })

  it('shows and enforces completion requirements in the execution inspector', async () => {
    vi.mocked(taskHooks.useTaskDefinitions).mockReturnValue({
      data: taskDefinitions.map((definition) =>
        definition.id === 'open-task'
          ? {
              ...definition,
              completion_requirements: [
                {
                  id: 'article-1',
                  kind: 'article',
                  title: '读完评审材料',
                  completed: false,
                },
              ],
            }
          : definition
      ),
      isLoading: false,
    } as ReturnType<typeof taskHooks.useTaskDefinitions>)
    const user = userEvent.setup()
    renderWorkspace()

    expect(screen.getByRole('button', { name: '完成准备评审' })).toBeDisabled()
    await user.click(screen.getByText('准备评审'))

    expect(screen.getByText('完成门槛')).toBeVisible()
    expect(screen.getByText('0 / 1')).toBeVisible()
    expect(screen.getByRole('button', { name: '还差 1 项' })).toBeDisabled()
  })

  it('completes an occurrence from the execution inspector', async () => {
    const complete = vi.fn().mockResolvedValue({})
    vi.mocked(taskHooks.useCompleteOccurrenceMutation).mockReturnValue({
      mutateAsync: complete,
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useCompleteOccurrenceMutation>)
    const user = userEvent.setup()
    renderWorkspace()

    await user.click(screen.getByText('准备评审'))
    await user.click(screen.getByRole('button', { name: '完成' }))

    expect(complete).toHaveBeenCalledWith(
      expect.objectContaining({
        taskID: 'open-task',
        occurrenceID: 'open-occurrence',
      })
    )
  })

  it('shows an error when completing an occurrence fails', async () => {
    vi.mocked(taskHooks.useCompleteOccurrenceMutation).mockReturnValue({
      mutateAsync: vi.fn().mockRejectedValue(new Error('request failed')),
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useCompleteOccurrenceMutation>)
    const user = userEvent.setup()
    renderWorkspace()

    await user.click(screen.getByText('准备评审'))
    await user.click(screen.getByRole('button', { name: '完成' }))

    expect(
      await screen.findByRole('alert', { name: '' })
    ).toHaveTextContent('任务操作失败，请稍后重试。')
  })

  it('offers task archive and delete from a completed execution', async () => {
    const completedTask = task('completed-task', '部署语音转文字服务')
    const completedOccurrence = occurrence(
      'completed-occurrence',
      completedTask.id,
      'done'
    )
    const archive = vi.fn().mockResolvedValue({})
    vi.mocked(taskHooks.useTaskDefinitions).mockReturnValue({
      data: [...taskDefinitions, completedTask],
      isLoading: false,
    } as ReturnType<typeof taskHooks.useTaskDefinitions>)
    vi.mocked(taskHooks.useOccurrences).mockImplementation(
      (params) =>
        ({
          data:
            params?.task_id === completedTask.id ||
            params?.scope === 'completed'
              ? [completedOccurrence]
              : (occurrencesByScope[params?.scope ?? 'all'] ?? []),
          isLoading: false,
          isError: false,
        }) as ReturnType<typeof taskHooks.useOccurrences>
    )
    vi.mocked(taskHooks.useArchiveTaskMutation).mockReturnValue({
      mutateAsync: archive,
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useArchiveTaskMutation>)
    const user = userEvent.setup()
    renderWorkspace()

    await user.click(screen.getByRole('tab', { name: '已完成 1' }))
    await user.click(screen.getByText(completedTask.title))

    expect(
      screen.getByRole('complementary', {
        name: `执行详情：${completedTask.title}`,
      })
    ).toBeVisible()
    expect(screen.getByRole('button', { name: '归档任务' })).toBeVisible()
    expect(screen.getByRole('button', { name: '永久删除' })).toBeVisible()

    await user.click(screen.getByRole('button', { name: '归档任务' }))
    await user.click(screen.getByRole('button', { name: '确认归档' }))

    expect(archive).toHaveBeenCalledWith({
      projectID: completedTask.project_id,
      taskID: completedTask.id,
      expectedRevisions: {
        expected_task_revision: completedTask.revision,
        expected_schedule_revision: completedTask.schedule_revision,
        expected_occurrence_revisions: {
          [completedOccurrence.id]: completedOccurrence.revision,
        },
      },
    })
    await waitFor(() =>
      expect(
        screen.queryByRole('complementary', {
          name: `执行详情：${completedTask.title}`,
        })
      ).not.toBeInTheDocument()
    )
  })

  it('preserves the local date and offers refresh/compare when reschedule conflicts', async () => {
    const conflict = new (
      await import('../api/taskDomain')
    ).TaskDomainRevisionConflictError(
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
    } as unknown as ReturnType<
      typeof taskHooks.useRescheduleOccurrenceMutation
    >)
    renderWorkspace()
    const user = userEvent.setup()

    await user.click(screen.getByRole('button', { name: '改期准备评审' }))
    const date = screen.getByLabelText('新的执行日期')
    await user.type(date, '2026-07-25')
    await user.click(screen.getByRole('button', { name: '保存改期' }))

    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        '执行实例已在其他窗口更新'
      )
    )
    expect(date).toHaveValue('2026-07-25')
    expect(screen.getByRole('button', { name: '刷新服务器版本' })).toBeVisible()
    expect(screen.getByRole('button', { name: '比较差异' })).toBeVisible()
  })

  it('shows a visible error and preserves the selected date when reschedule fails', async () => {
    const reschedule = vi
      .fn()
      .mockRejectedValue(new Error('service unavailable'))
    vi.mocked(taskHooks.useRescheduleOccurrenceMutation).mockReturnValue({
      mutateAsync: reschedule,
      isPending: false,
    } as unknown as ReturnType<
      typeof taskHooks.useRescheduleOccurrenceMutation
    >)
    renderWorkspace()
    const user = userEvent.setup()

    await user.click(screen.getByRole('button', { name: '改期准备评审' }))
    const date = screen.getByLabelText('新的执行日期')
    await user.type(date, '2026-07-25')
    await user.click(screen.getByRole('button', { name: '保存改期' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      '保存改期失败，请稍后重试。'
    )
    expect(date).toHaveValue('2026-07-25')
  })

  it('can reschedule an existing task to a concrete time range', async () => {
    const reschedule = vi.fn().mockResolvedValue({})
    vi.mocked(taskHooks.useRescheduleOccurrenceMutation).mockReturnValue({
      mutateAsync: reschedule,
      isPending: false,
    } as unknown as ReturnType<
      typeof taskHooks.useRescheduleOccurrenceMutation
    >)
    renderWorkspace()
    const user = userEvent.setup()

    await user.click(screen.getByRole('button', { name: '改期准备评审' }))
    await user.selectOptions(
      screen.getByLabelText('新的安排方式'),
      'time_block'
    )
    await user.type(screen.getByLabelText('新的执行日期'), '2026-07-25')
    await user.clear(screen.getByLabelText('新的开始时间'))
    await user.type(screen.getByLabelText('新的开始时间'), '14:30')
    await user.clear(screen.getByLabelText('新的结束时间'))
    await user.type(screen.getByLabelText('新的结束时间'), '16:00')
    await user.click(screen.getByRole('button', { name: '保存改期' }))

    expect(reschedule).toHaveBeenCalledWith(
      expect.objectContaining({
        occurrenceID: 'open-occurrence',
        input: expect.objectContaining({
          timing: {
            timing_type: 'time_block',
            timezone: expect.any(String),
            planned_date: '2026-07-25',
            local_start_time: '14:30',
            duration_minutes: 90,
          },
        }),
      })
    )
  })

  it('recognizes an existing time block even when timing_type is omitted', async () => {
    vi.mocked(taskHooks.useOccurrences).mockImplementation(
      (params) =>
        ({
          data:
            params?.scope === 'upcoming'
              ? [
                  {
                    ...occurrence('open-occurrence', 'open-task', 'open'),
                    planned_date: '2026-07-25',
                    planned_start_at: '2026-07-25T06:30:00Z',
                    planned_end_at: '2026-07-25T08:00:00Z',
                    timezone: 'Asia/Shanghai',
                  },
                ]
              : [],
          isLoading: false,
          isError: false,
        }) as ReturnType<typeof taskHooks.useOccurrences>
    )
    renderWorkspace()
    const user = userEvent.setup()

    await user.click(screen.getByRole('button', { name: '改期准备评审' }))

    expect(screen.getByLabelText('新的安排方式')).toHaveValue('time_block')
    expect(screen.getByLabelText('新的开始时间')).toHaveValue('14:30')
    expect(screen.getByLabelText('新的结束时间')).toHaveValue('16:00')
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

function idleMutation() {
  return { mutateAsync: vi.fn(), isPending: false } as unknown
}

const taskDefinitions: import('../api/taskDomain').TaskV2[] = [
  task('open-task', '准备评审'),
  task('active-task', '实现接口'),
  task('blocked-task', '联调服务'),
  task('recurring-task', '每日复盘'),
  {
    ...task('release-task', '发布版本'),
    project_id: 'work-project',
    priority: 2,
  },
]

const occurrencesByScope: Partial<
  Record<
    import('../api/taskDomain').OccurrenceListScope,
    import('../api/taskDomain').OccurrenceV2[]
  >
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
    occurrence('release-occurrence', 'release-task', 'active'),
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
