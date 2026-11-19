import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as taskHooks from '../hooks/useTaskDomain'
import * as workspaceHooks from '../hooks/useWorkspace'
import * as taskAPI from '../api/taskDomain'
import * as notesHooks from '../hooks/useNotes'
import type { WorkspaceOverview } from '../api/workspace'
import DashboardV2, { createDashboardDateRanges } from './DashboardV2'

vi.mock('../hooks/useTaskDomain')
vi.mock('../hooks/useWorkspace')
vi.mock('../api/taskDomain', async () => ({
  ...(await vi.importActual<typeof import('../api/taskDomain')>(
    '../api/taskDomain'
  )),
  getTaskDefinition: vi.fn(),
}))
vi.mock('../hooks/useNotes', () => ({
  useNotesList: vi.fn(() => ({
    data: { notes: [], pagination: { page: 1, page_size: 20, total: 0 } },
    isLoading: false,
    isError: false,
  })),
}))

describe('Dashboard v2 today projection', () => {
  const createTaskMock = vi.fn()

  beforeEach(() => {
    vi.clearAllMocks()
    createTaskMock.mockResolvedValue({})
    definitions = [task('today', '今天处理'), task('overdue', '补交周报')]
    vi.mocked(workspaceHooks.useWorkspaceOverview).mockReturnValue(
      queryResult(workspaceOverview())
    )
    vi.mocked(taskAPI.getTaskDefinition).mockImplementation(
      async (id) => definitions.find((item) => item.id === id)!
    )
    vi.mocked(taskHooks.useTaskDefinition).mockImplementation(
      (id) =>
        queryResult(definitions.find((item) => item.id === id)) as ReturnType<
          typeof taskHooks.useTaskDefinition
        >
    )
    vi.mocked(taskHooks.useOccurrence).mockReturnValue(
      queryResult(undefined) as unknown as ReturnType<
        typeof taskHooks.useOccurrence
      >
    )
    vi.mocked(taskHooks.useProject).mockReturnValue(
      queryResult(personalProject)
    )
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

  it('renders server totals independently of the bounded preview and defers other list reads', () => {
    vi.mocked(workspaceHooks.useWorkspaceOverview).mockReturnValue(
      queryResult(
        workspaceOverview({ today: 55, week: 89, completed_total: 400 })
      )
    )
    renderDashboard()

    expect(screen.getByRole('tab', { name: '今天 55' })).toBeVisible()
    expect(screen.getByRole('tab', { name: '本周 89' })).toBeVisible()
    expect(screen.getByRole('button', { name: /累计完成\s*400/ })).toBeVisible()
    expect(screen.getByText('今天处理')).toBeVisible()
    expect(taskHooks.useTaskDefinitions).not.toHaveBeenCalled()
    expect(notesHooks.useNotesList).not.toHaveBeenCalled()
    expect(taskHooks.useOccurrences).toHaveBeenCalled()
    for (const [, options] of vi.mocked(taskHooks.useOccurrences).mock.calls) {
      expect(options?.enabled).toBe(false)
    }
  })

  it('omits the empty flexible section when the bounded preview contains only timed items', () => {
    const overview = workspaceOverview()
    overview.today = [
      {
        ...overview.today[0],
        planned_start_at: '2026-11-19T04:00:00Z',
        planned_end_at: '2026-11-19T05:00:00Z',
      },
    ]
    vi.mocked(workspaceHooks.useWorkspaceOverview).mockReturnValue(
      queryResult(overview)
    )
    renderDashboard()

    expect(screen.getByText('固定时间')).toBeVisible()
    expect(screen.queryByText('今天要做')).not.toBeInTheDocument()
    expect(screen.queryByText('0 个行动')).not.toBeInTheDocument()
  })

  it('shows an aggregate error without loading complete lists to recover counts', () => {
    vi.mocked(workspaceHooks.useWorkspaceOverview).mockReturnValue({
      ...queryResult(undefined),
      isError: true,
      error: new Error('offline'),
    } as unknown as ReturnType<typeof workspaceHooks.useWorkspaceOverview>)
    renderDashboard()

    expect(screen.getByText('工作台暂时不可用，请刷新后重试。')).toBeVisible()
    expect(taskHooks.useTaskDefinitions).not.toHaveBeenCalled()
    expect(notesHooks.useNotesList).not.toHaveBeenCalled()
  })

  it('loads completed content only after selection and requests the full completed scope', async () => {
    renderDashboard()
    await userEvent.click(screen.getByRole('tab', { name: '已完成 10' }))

    expect(taskHooks.useOccurrences).toHaveBeenLastCalledWith(
      expect.objectContaining({ scope: 'all', execution_status: 'done' }),
      { enabled: true }
    )
  })

  it('opens a deep-linked occurrence through individual reads outside the bounded preview', () => {
    definitions.push(task('outside-preview', '完整详情任务'))
    const selected = occurrence('outside-occurrence', 'outside-preview')
    vi.mocked(taskHooks.useOccurrence).mockReturnValue(queryResult(selected))
    renderDashboard('/?occurrence_id=outside-occurrence')

    expect(
      screen.getByRole('complementary', { name: '执行详情：完整详情任务' })
    ).toBeVisible()
    expect(taskHooks.useOccurrence).toHaveBeenCalledWith('outside-occurrence')
    expect(taskHooks.useTaskDefinition).toHaveBeenCalledWith('outside-preview')
    expect(taskHooks.useTaskDefinitions).not.toHaveBeenCalled()
  })

  it('reads the single task before quick completion and preserves command revisions', async () => {
    const complete = vi.fn().mockResolvedValue({})
    vi.mocked(taskHooks.useCompleteOccurrenceMutation).mockReturnValue({
      mutateAsync: complete,
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useCompleteOccurrenceMutation>)
    renderDashboard()

    await userEvent.click(screen.getByRole('button', { name: '完成今天处理' }))

    expect(taskAPI.getTaskDefinition).toHaveBeenCalledExactlyOnceWith('today')
    expect(complete).toHaveBeenCalledWith({
      projectID: 'personal',
      taskID: 'today',
      occurrenceID: 'today-occ',
      expectedRevisions: {
        expected_task_revision: 2,
        expected_schedule_revision: 1,
        expected_occurrence_revisions: { 'today-occ': 3 },
      },
    })
  })

  it('shows open task occurrences for the current week and month', async () => {
    definitions = [
      task('today', '今天处理'),
      task('week', '本周复习'),
      task('month', '本月复盘'),
    ]
    vi.mocked(taskHooks.useOccurrences).mockImplementation((params) => {
      if (params?.scope !== 'upcoming' || !params.from || !params.to) {
        return {
          data:
            params?.scope === 'today' ? [occurrence('today-occ', 'today')] : [],
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
    expect(screen.getByText('固定时间')).toBeVisible()
    expect(screen.queryByText('0 个行动')).not.toBeInTheDocument()

    await user.click(month)
    expect(screen.getByText('本月复盘')).toBeVisible()
    expect(screen.getByText('固定时间')).toBeVisible()
    expect(screen.queryByText('0 个行动')).not.toBeInTheDocument()
  })

  it('keeps a recurring weekly task in a collection even with one execution', async () => {
    definitions = [task('study', '每周学习'), task('once', '一次性任务')]
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
    vi.mocked(workspaceHooks.useWorkspaceOverview).mockReturnValue(
      queryResult(workspaceOverview({ week: 2 }))
    )
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
    definitions = [task('study', '单词学习')]
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
    vi.mocked(workspaceHooks.useWorkspaceOverview).mockReturnValue(
      queryResult(workspaceOverview({ month: 3 }))
    )
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

  it('builds Monday-based week and calendar-month ranges in the requested timezone', () => {
    const ranges = createDashboardDateRanges(
      new Date('2026-08-05T12:00:00Z'),
      'Asia/Shanghai'
    )

    expect(ranges).toEqual({
      week: {
        from: '2026-08-02T16:00:00.000Z',
        to: '2026-08-09T16:00:00.000Z',
        timezone: 'Asia/Shanghai',
      },
      month: {
        from: '2026-07-31T16:00:00.000Z',
        to: '2026-08-31T16:00:00.000Z',
        timezone: 'Asia/Shanghai',
      },
    })
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

  it('loads the task gate before quick completion and opens required items without mutating', async () => {
    const complete = vi.fn()
    definitions = [
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
    ]
    vi.mocked(taskHooks.useCompleteOccurrenceMutation).mockReturnValue({
      mutateAsync: complete,
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useCompleteOccurrenceMutation>)

    renderDashboard()

    await userEvent.click(screen.getByRole('button', { name: '完成今天处理' }))
    expect(taskAPI.getTaskDefinition).toHaveBeenCalledWith('today')
    expect(await screen.findByText('请先完成必选项。')).toBeVisible()
    expect(
      screen.getByRole('complementary', { name: '执行详情：今天处理' })
    ).toBeVisible()
    expect(complete).not.toHaveBeenCalled()
  })
})

function renderDashboard(initialPath = '/') {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
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
  const result: taskAPI.OccurrenceV2 = {
    id,
    task_id: taskID,
    title: definitions.find((item) => item.id === taskID)?.title,
    project_id: 'personal',
    occurrence_key: 'once',
    execution_status: 'open',
    revision: 3,
    generated_schedule_revision: 1,
    planned_start_at: plannedStartAt,
    planned_end_at: plannedStartAt
      ? new Date(new Date(plannedStartAt).getTime() + 30 * 60_000).toISOString()
      : undefined,
  }
  return result
}

let definitions: taskAPI.TaskV2[] = []
const personalProject: taskAPI.ProjectV2 = {
  id: 'personal',
  name: '个人事务',
  kind: 'standard',
  horizon: 'short',
  status: 'active',
  system_role: 'personal',
  revision: 1,
}

function workspaceOverview(
  facets: Partial<WorkspaceOverview['facets']> = {}
): WorkspaceOverview {
  return {
    workspace_id: 'workspace-1',
    task_model_version: 2,
    runtime_epoch: 3,
    date: '2026-11-19',
    timezone: 'Asia/Shanghai',
    generated_at: '2026-11-19T01:00:00Z',
    read_consistency: 'planning_snapshot',
    facets: {
      today: 1,
      week: 1,
      month: 1,
      overdue: 1,
      completed_today: 0,
      completed_week: 1,
      completed_total: 10,
      unscheduled: 0,
      inbox: 0,
      recurring: 0,
      draft: 0,
      ...facets,
    },
    today: [{ ...occurrence('today-occ', 'today'), project_name: '个人事务' }],
    projects: [],
    notes: {
      state: 'available',
      authority: 'platform_compatibility',
      as_of: '2026-11-19T01:00:00Z',
      items: [],
    },
    partial: false,
  }
}

function queryResult<T>(data: T) {
  return {
    data,
    isLoading: false,
    isError: false,
    error: null,
    refetch: vi.fn(),
  } as unknown as import('@tanstack/react-query').UseQueryResult<T, Error>
}
