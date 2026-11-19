import {
  QueryClient,
  QueryClientProvider,
  useQueries,
} from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as taskHooks from '../hooks/useTaskDomain'
import * as workspaceHooks from '../hooks/useWorkspace'
import {
  createWorkspaceQueryParams,
  type WorkspaceFacetsResponse,
} from '../api/workspace'
import type { OccurrenceV2 } from '../api/taskDomain'
import TaskOccurrenceWorkspace from './TaskOccurrenceWorkspace'

vi.mock('../hooks/useTaskDomain')
vi.mock('../hooks/useWorkspace')
vi.mock('@tanstack/react-query', async () => ({
  ...(await vi.importActual<typeof import('@tanstack/react-query')>(
    '@tanstack/react-query'
  )),
  useQueries: vi.fn(
    (options: { queries: Array<{ queryKey: readonly unknown[] }> }) =>
      options.queries.map(({ queryKey }) =>
        queryResult(definitions.find((item) => item.id === queryKey.at(-1)))
      )
  ),
}))

describe('Task occurrence workspace', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    definitions = [...taskDefinitions]
    vi.mocked(taskHooks.taskDomainQueryKeys.task).mockImplementation((id) => [
      'task-domain',
      'tasks',
      'detail',
      id,
    ])
    vi.mocked(useQueries).mockImplementation(
      metadataQueries as unknown as typeof useQueries
    )
    vi.mocked(workspaceHooks.useTaskWorkbenchFacets).mockImplementation(
      (params) => {
        const response = facetsResponse()
        if (params.project_id) response.facets.week = 4
        return queryResult(response)
      }
    )
    vi.mocked(taskHooks.useTaskDefinition).mockImplementation((id) =>
      queryResult(definitions.find((item) => item.id === id))
    )
    vi.mocked(taskHooks.useOccurrence).mockImplementation(() =>
      queryResult(undefined)
    )
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
    vi.mocked(taskHooks.useTaskDefinitions).mockImplementation((params) =>
      queryResult(
        definitions.filter(
          (item) =>
            !params?.lifecycle_status ||
            item.lifecycle_status === params.lifecycle_status
        )
      )
    )
    vi.mocked(taskHooks.useOccurrences).mockImplementation(
      (params) =>
        ({
          data:
            params?.execution_status === 'done'
              ? [doneRecurring]
              : params?.task_id
                ? allOccurrences.filter(
                    (item) => item.task_id === params.task_id
                  )
                : (occurrencesByScope[params?.scope ?? 'all'] ?? []),
          isLoading: false,
          isError: false,
          refetch: vi.fn(),
        }) as unknown as ReturnType<typeof taskHooks.useOccurrences>
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

  it('uses authoritative facets for every view and only enables the selected content query', () => {
    renderWorkspace()

    for (const name of [
      '今天 9',
      '本周 60',
      '本月 80',
      '已逾期 7',
      '无日期 5',
      '重复 15',
      '已完成 10',
      '草稿 4',
    ]) {
      expect(screen.getByRole('tab', { name })).toBeVisible()
    }
    const enabledCollections = vi
      .mocked(taskHooks.useOccurrences)
      .mock.calls.filter(([, options]) => options?.enabled !== false)
    expect(enabledCollections).toHaveLength(1)
    expect(enabledCollections[0][0]).toEqual(
      expect.objectContaining({
        scope: 'upcoming',
        from: expect.any(String),
        to: expect.any(String),
        timezone: expect.any(String),
      })
    )
    expect(taskHooks.useTaskDefinitions).toHaveBeenCalledWith(
      expect.objectContaining({ lifecycle_status: 'draft' }),
      { enabled: false }
    )
    expect(
      vi
        .mocked(taskHooks.useTaskDefinitions)
        .mock.calls.every(([params]) => params?.lifecycle_status === 'draft')
    ).toBe(true)
    const metadata = (
      vi
        .mocked(useQueries)
        .mock.calls.at(-1)![0] as unknown as MetadataQueryOptions
    ).queries
    expect(metadata.map((query) => query.queryKey.at(-1))).toEqual([
      'open-task',
      'active-task',
      'blocked-task',
      'recurring-task',
      'release-task',
    ])
    expect(screen.getByText('当前显示 5 项')).toBeVisible()
  })

  it('requests project-scoped facets and keeps local list filters separate from view counts', async () => {
    renderWorkspace()
    const user = userEvent.setup()
    await user.selectOptions(
      screen.getByLabelText('按项目筛选'),
      'work-project'
    )

    expect(workspaceHooks.useTaskWorkbenchFacets).toHaveBeenLastCalledWith(
      expect.objectContaining({ project_id: 'work-project' })
    )
    expect(screen.getByRole('tab', { name: '本周 4' })).toBeVisible()
    expect(screen.getByText('当前显示 1 项')).toBeVisible()
    await user.selectOptions(screen.getByLabelText('按优先级筛选'), '0')
    expect(screen.getByRole('tab', { name: '本周 4' })).toBeVisible()
    expect(screen.getByText('当前显示 0 项')).toBeVisible()
  })

  it('keeps failed facet statistics unknown while the selected content remains usable', () => {
    vi.mocked(workspaceHooks.useTaskWorkbenchFacets).mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
    } as ReturnType<typeof workspaceHooks.useTaskWorkbenchFacets>)
    renderWorkspace()

    expect(screen.getByRole('tab', { name: /^本周$/ })).toBeVisible()
    expect(
      screen.queryByRole('tab', { name: '本周 0' })
    ).not.toBeInTheDocument()
    expect(screen.getByText('准备评审')).toBeVisible()
    expect(screen.getByRole('alert')).toHaveTextContent(/计数|统计/)
  })

  it('loads a deep-linked occurrence independently from the selected list', () => {
    const externalTask = task('external-task', '列表之外的任务')
    definitions.push(externalTask)
    vi.mocked(taskHooks.useOccurrence).mockReturnValue(
      queryResult(occurrence('external-occ', externalTask.id, 'open'))
    )
    renderWorkspace('/?occurrence_id=external-occ')

    expect(
      screen.getByRole('complementary', { name: '执行详情：列表之外的任务' })
    ).toBeVisible()
    expect(taskHooks.useOccurrence).toHaveBeenCalledWith('external-occ')
    expect(taskHooks.useTaskDefinition).toHaveBeenCalledWith('external-task')
  })

  it('keeps draft content and its server count separate from occurrence views', () => {
    definitions.push({
      ...task('draft-task', '一份草稿'),
      lifecycle_status: 'draft',
    })
    renderWorkspace('/?view=draft')

    expect(screen.getByRole('tab', { name: '草稿 4' })).toHaveAttribute(
      'aria-selected',
      'true'
    )
    expect(screen.getByText('一份草稿')).toBeVisible()
    expect(screen.getByText('当前显示 1 项')).toBeVisible()
    expect(
      vi
        .mocked(taskHooks.useOccurrences)
        .mock.calls.every(([, options]) => options?.enabled === false)
    ).toBe(true)
    expect(taskHooks.useTaskDefinitions).toHaveBeenCalledWith(
      expect.objectContaining({ lifecycle_status: 'draft' }),
      { enabled: true }
    )
  })

  it('anchors week, month, overdue and completed content to their facet semantics', async () => {
    renderWorkspace()
    const user = userEvent.setup()
    const activeParams = () =>
      vi
        .mocked(taskHooks.useOccurrences)
        .mock.calls.filter(([, options]) => options?.enabled !== false)
        .at(-1)![0]!
    const timezone = vi
      .mocked(workspaceHooks.useTaskWorkbenchFacets)
      .mock.calls.at(-1)![0].timezone
    const localPart = (value: string, type: Intl.DateTimeFormatPartTypes) =>
      new Intl.DateTimeFormat('en-GB', {
        timeZone: timezone,
        year: 'numeric',
        month: 'numeric',
        day: 'numeric',
        weekday: 'long',
        hour: 'numeric',
        hourCycle: 'h23',
      })
        .formatToParts(new Date(value))
        .find((part) => part.type === type)?.value

    expect(localPart(activeParams().from!, 'weekday')).toBe('Monday')
    await user.click(screen.getByRole('tab', { name: '本月 80' }))
    expect(activeParams().scope).toBe('upcoming')
    expect(Number(localPart(activeParams().from!, 'day'))).toBe(1)
    await user.click(screen.getByRole('tab', { name: '已逾期 7' }))
    const reference = vi
      .mocked(workspaceHooks.useTaskWorkbenchFacets)
      .mock.calls.at(-1)![0].date
    expect(activeParams().scope).toBe('overdue')
    expect(Number(localPart(activeParams().from!, 'hour'))).toBe(0)
    expect(Number(localPart(activeParams().from!, 'day'))).toBe(
      Number(reference.split('-')[2])
    )
    await user.click(screen.getByRole('tab', { name: '已完成 10' }))
    expect(activeParams()).toEqual(
      expect.objectContaining({
        scope: 'all',
        execution_status: 'done',
        timezone,
      })
    )
    expect(activeParams()).not.toHaveProperty('from')
    expect(activeParams()).not.toHaveProperty('to')
  })

  it('excludes draft definitions from pending action content without changing authoritative counts', () => {
    definitions = definitions.map((item) =>
      item.id === 'open-task' ? { ...item, lifecycle_status: 'draft' } : item
    )
    renderWorkspace()

    expect(screen.queryByText('准备评审')).not.toBeInTheDocument()
    expect(screen.getByRole('tab', { name: '本周 60' })).toBeVisible()
    expect(screen.getByText('当前显示 4 项')).toBeVisible()
  })

  it('keeps historical done occurrences visible after their task is archived', () => {
    definitions = definitions.map((item) =>
      item.id === 'recurring-task'
        ? { ...item, lifecycle_status: 'archived' }
        : item
    )
    renderWorkspace('/?view=completed')

    expect(
      screen.getByLabelText('每日复盘 · 7月22日执行状态')
    ).toHaveTextContent('已完成')
    expect(screen.getByRole('tab', { name: '已完成 10' })).toHaveAttribute(
      'aria-selected',
      'true'
    )
  })

  it('reports unavailable content metadata without turning it into a zero result', () => {
    vi.mocked(useQueries).mockImplementation(((options: MetadataQueryOptions) =>
      options.queries.map(() => ({
        data: undefined,
        isError: true,
        isLoading: false,
        isPending: false,
      }))) as unknown as typeof useQueries)
    renderWorkspace()

    expect(screen.getByRole('alert')).toHaveTextContent('执行实例暂时不可用')
    expect(screen.getByText('当前显示 — 项')).toBeVisible()
    expect(screen.queryByText('这个视图还没有任务')).not.toBeInTheDocument()
    expect(screen.getByRole('tab', { name: '本周 60' })).toBeVisible()
  })

  it('defaults to the current week and switches the active collection on selection', async () => {
    renderWorkspace()

    const upcoming = screen.getByRole('tab', { name: '本周 60' })
    const overdue = screen.getByRole('tab', { name: '已逾期 7' })
    expect(upcoming).toHaveAttribute('aria-selected', 'true')
    expect(overdue).toHaveAttribute('aria-selected', 'false')
    expect(screen.getByText('准备评审')).toBeVisible()
    expect(screen.queryByText('补交周报')).not.toBeInTheDocument()

    await userEvent.click(overdue)
    expect(screen.getByText('补交周报')).toBeVisible()
    expect(screen.queryByText('准备评审')).not.toBeInTheDocument()
  })

  it('renders pending execution states distinctly and keeps done history in its own view', async () => {
    renderWorkspace()

    const list = screen.getByRole('list', { name: '任务执行实例' })
    expect(within(list).getAllByText('未开始')[0]).toBeVisible()
    expect(within(list).getAllByText('进行中')[0]).toBeVisible()
    expect(within(list).getByText('阻塞')).toBeVisible()
    expect(within(list).queryByText('已完成')).not.toBeInTheDocument()
    expect(within(list).getByText('原因：等待接口评审')).toBeVisible()
    expect(within(list).getByText('下一步：周五跟进')).toBeVisible()
    await userEvent.click(screen.getByRole('tab', { name: '已完成 10' }))
    expect(
      within(screen.getByRole('list', { name: '任务执行实例' })).getByText(
        '已完成'
      )
    ).toBeVisible()
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
    expect(screen.getByText('当前显示 1 项')).toBeVisible()

    await user.selectOptions(screen.getByLabelText('按项目筛选'), '')
    await user.selectOptions(screen.getByLabelText('按优先级筛选'), '2')
    expect(screen.getByText('发布版本')).toBeVisible()
    expect(screen.queryByText('实现接口')).not.toBeInTheDocument()

    await user.selectOptions(screen.getByLabelText('按优先级筛选'), '')
    await user.selectOptions(screen.getByLabelText('按状态筛选'), 'blocked')
    expect(screen.getByText('联调服务')).toBeVisible()
    expect(screen.queryByText('发布版本')).not.toBeInTheDocument()
    expect(screen.getByText('当前显示 1 项')).toBeVisible()
  })

  it('keeps completed recurring history separate from the next open occurrence', async () => {
    renderWorkspace()

    expect(screen.queryByText('每日复盘 · 7月22日')).not.toBeInTheDocument()
    expect(
      screen.getByLabelText('每日复盘 · 7月23日执行状态')
    ).toHaveTextContent('未开始')
    await userEvent.click(screen.getByRole('tab', { name: '已完成 10' }))
    expect(
      screen.getByLabelText('每日复盘 · 7月22日执行状态')
    ).toHaveTextContent('已完成')
    expect(screen.queryByText('每日复盘 · 7月23日')).not.toBeInTheDocument()
  })

  it('shows and enforces completion requirements in the execution inspector', async () => {
    definitions = taskDefinitions.map((definition) =>
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
    )
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

    expect(await screen.findByRole('alert', { name: '' })).toHaveTextContent(
      '任务操作失败，请稍后重试。'
    )
  })

  it('opens a draft with the keyboard and publishes its task definition', async () => {
    const draft = {
      ...task('draft-task', '准备草稿'),
      lifecycle_status: 'draft' as const,
    }
    definitions = [...taskDefinitions, draft]
    const publish = vi.fn().mockResolvedValue(undefined)
    vi.mocked(taskHooks.usePublishTaskMutation).mockReturnValue({
      mutateAsync: publish,
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.usePublishTaskMutation>)
    const user = userEvent.setup()
    renderWorkspace()

    await user.click(screen.getByRole('tab', { name: '草稿 4' }))
    expect(screen.getByLabelText('按状态筛选')).toBeDisabled()
    expect(screen.getByLabelText('按日期范围筛选')).toBeDisabled()
    const row = within(
      screen.getByRole('list', { name: '任务草稿' })
    ).getByRole('listitem')
    row.focus()
    await user.keyboard('{Enter}')
    await user.click(screen.getByRole('button', { name: '发布' }))
    expect(publish).toHaveBeenCalledWith({
      projectID: draft.project_id,
      taskID: draft.id,
      expectedRevisions: {
        expected_task_revision: draft.revision,
        expected_schedule_revision: draft.schedule_revision,
        expected_occurrence_revisions: {},
      },
    })
  })

  it('offers task archive and delete from a completed execution', async () => {
    const completedTask = task('completed-task', '部署语音转文字服务')
    const completedOccurrence = occurrence(
      'completed-occurrence',
      completedTask.id,
      'done'
    )
    const archive = vi.fn().mockResolvedValue({})
    definitions = [...taskDefinitions, completedTask]
    vi.mocked(taskHooks.useOccurrences).mockImplementation(
      (params) =>
        ({
          data:
            params?.task_id === completedTask.id ||
            params?.execution_status === 'done'
              ? [completedOccurrence]
              : (occurrencesByScope[params?.scope ?? 'all'] ?? []),
          isLoading: false,
          isError: false,
        }) as unknown as ReturnType<typeof taskHooks.useOccurrences>
    )
    vi.mocked(taskHooks.useArchiveTaskMutation).mockReturnValue({
      mutateAsync: archive,
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useArchiveTaskMutation>)
    const user = userEvent.setup()
    renderWorkspace()

    await user.click(screen.getByRole('tab', { name: '已完成 10' }))
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

  it('refreshes the selected occurrence and task revisions before retrying a conflicted reschedule', async () => {
    const conflict = new (
      await import('../api/taskDomain')
    ).TaskDomainRevisionConflictError(
      'occurrence changed',
      {
        expected_task_revision: 2,
        expected_schedule_revision: 1,
        expected_occurrence_revisions: { 'open-occurrence': 3 },
      },
      {
        task_revision: 5,
        schedule_revision: 6,
        occurrence_revisions: { 'open-occurrence': 4 },
      }
    )
    let selected = {
      ...occurrence('open-occurrence', 'open-task', 'open'),
      task_revision: 2,
      schedule_revision: 1,
    }
    let collection = occurrencesByScope.upcoming!
    const refreshOccurrence = vi.fn(async () => {
      selected = {
        ...selected,
        revision: 4,
        task_revision: 5,
        schedule_revision: 6,
      }
      return queryResult(selected)
    })
    const refreshTask = vi.fn(async () => {
      definitions = definitions.map((item) =>
        item.id === 'open-task'
          ? { ...item, revision: 5, schedule_revision: 6 }
          : item
      )
      return queryResult(definitions.find((item) => item.id === 'open-task'))
    })
    const refreshCollection = vi.fn(async () => {
      collection = collection.map((item) =>
        item.id === 'open-occurrence'
          ? { ...item, revision: 4, task_revision: 5, schedule_revision: 6 }
          : item
      )
      return queryResult(collection)
    })
    const refreshHistory = vi.fn(async () => queryResult([selected]))
    const refreshFacets = vi.fn(async () => queryResult(facetsResponse()))
    vi.mocked(taskHooks.useOccurrence).mockImplementation((id) =>
      queryResult(id === selected.id ? selected : undefined, refreshOccurrence)
    )
    vi.mocked(taskHooks.useTaskDefinition).mockImplementation((id) =>
      queryResult(
        definitions.find((item) => item.id === id),
        refreshTask
      )
    )
    vi.mocked(taskHooks.useOccurrences).mockImplementation((params) =>
      params?.task_id
        ? queryResult([selected], refreshHistory)
        : queryResult(collection, refreshCollection)
    )
    vi.mocked(workspaceHooks.useTaskWorkbenchFacets).mockReturnValue(
      queryResult(facetsResponse(), refreshFacets)
    )
    const reschedule = vi
      .fn()
      .mockRejectedValueOnce(conflict)
      .mockResolvedValueOnce({})
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
      '执行实例已在其他窗口更新'
    )

    await user.click(screen.getByRole('button', { name: '刷新服务器版本' }))
    await waitFor(() =>
      expect(
        screen.queryByText('执行实例已在其他窗口更新')
      ).not.toBeInTheDocument()
    )
    for (const refresh of [
      refreshOccurrence,
      refreshTask,
      refreshCollection,
      refreshHistory,
      refreshFacets,
    ])
      expect(refresh).toHaveBeenCalledOnce()
    expect(date).toHaveValue('2026-07-25')
    await user.click(screen.getByRole('button', { name: '保存改期' }))
    expect(reschedule).toHaveBeenLastCalledWith(
      expect.objectContaining({
        occurrenceID: 'open-occurrence',
        input: expect.objectContaining({
          expected_task_revision: 5,
          expected_schedule_revision: 6,
          expected_occurrence_revision: 4,
          timing: expect.objectContaining({ planned_date: '2026-07-25' }),
        }),
      })
    )
  })

  it('preserves the reschedule draft and blocks stale writes when refreshing selected details fails', async () => {
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
    const selected = occurrence('open-occurrence', 'open-task', 'open')
    let readFailed = false
    const refreshSelected = vi.fn(async () => {
      readFailed = true
      return { data: selected, isError: true }
    })
    vi.mocked(taskHooks.useOccurrence).mockImplementation(
      (id) =>
        ({
          data: id === selected.id ? selected : undefined,
          isError: readFailed,
          isLoading: false,
          isPending: false,
          refetch: refreshSelected,
        }) as never
    )
    vi.mocked(taskHooks.useRescheduleOccurrenceMutation).mockReturnValue({
      mutateAsync: vi.fn().mockRejectedValue(conflict),
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
    expect(await screen.findByText('执行实例已在其他窗口更新')).toBeVisible()
    await user.click(screen.getByRole('button', { name: '刷新服务器版本' }))

    expect(
      await screen.findByText('刷新服务器版本失败，请稍后重试。')
    ).toBeVisible()
    expect(screen.getByText('执行实例已在其他窗口更新')).toBeVisible()
    expect(date).toHaveValue('2026-07-25')
    expect(screen.getByRole('button', { name: '保存改期' })).toBeDisabled()
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

  it('collapses repeated executions by task identity without merging equal titles or hiding single rows', async () => {
    definitions.push({
      ...task('other-recurring', '每日复盘'),
      project_id: 'work-project',
    })
    const rows = [
      recurringOccurrence('review-3', '2026-08-03', 'blocked'),
      {
        ...recurringOccurrence('other-1', '2026-08-01'),
        task_id: 'other-recurring',
      },
      recurringOccurrence('review-1', '2026-08-01'),
      occurrence('single', 'open-task', 'open'),
      recurringOccurrence('review-2', '2026-08-02', 'active'),
      {
        ...recurringOccurrence('other-2', '2026-08-02'),
        task_id: 'other-recurring',
      },
    ]
    mockOccurrenceRows(rows)
    renderWorkspace('/tasks?view=upcoming')
    const user = userEvent.setup()
    const first = screen.getByRole('button', { name: '展开每日复盘，3 次执行' })
    expect(first).toHaveAttribute('aria-expanded', 'false')
    expect(
      screen.getByRole('button', { name: '展开每日复盘，2 次执行' })
    ).toHaveAttribute('aria-expanded', 'false')
    expect(within(first).getByText('8月1日 – 8月3日')).toBeVisible()
    expect(
      within(first).getByText('收件箱 · 1 待执行 · 1 进行中 · 1 阻塞')
    ).toBeVisible()
    expect(screen.queryByText('每日复盘 · 8月1日')).not.toBeInTheDocument()
    expect(screen.getByText('准备评审')).toBeVisible()
    expect(screen.getByText('当前显示 3 个任务 · 6 次执行')).toBeVisible()
    expect(screen.getByRole('tab', { name: '本周 60' })).toBeVisible()
    expect(
      within(screen.getByRole('list', { name: '任务执行实例' })).getAllByRole(
        'listitem'
      )
    ).toHaveLength(3)

    await user.click(first)
    const instances = screen.getByRole('list', { name: '每日复盘执行实例' })
    expect(within(instances).getAllByRole('listitem')).toHaveLength(3)
    expect(within(instances).getByText('每日复盘 · 8月1日')).toBeVisible()
    expect(
      screen.getByRole('button', { name: '展开每日复盘，2 次执行' })
    ).toHaveAttribute('aria-expanded', 'false')
  })

  it('groups only the executions left after status and local-date filters', async () => {
    const today = createWorkspaceQueryParams().date
    const tomorrow = new Date(`${today}T12:00:00Z`)
    tomorrow.setUTCDate(tomorrow.getUTCDate() + 1)
    mockOccurrenceRows([
      recurringOccurrence('review-today', today),
      recurringOccurrence('review-blocked', today, 'blocked'),
      recurringOccurrence('review-later', tomorrow.toISOString().slice(0, 10)),
    ])
    renderWorkspace()
    const user = userEvent.setup()
    expect(
      screen.getByRole('button', { name: '展开每日复盘，3 次执行' })
    ).toBeVisible()
    await user.selectOptions(screen.getByLabelText('按状态筛选'), 'blocked')
    expect(
      screen.queryByRole('button', { name: /展开每日复盘/ })
    ).not.toBeInTheDocument()
    expect(screen.getByText('当前显示 1 项')).toBeVisible()
    expect(screen.getByRole('button', { name: /^改期每日复盘/ })).toBeVisible()
    await user.selectOptions(screen.getByLabelText('按状态筛选'), '')
    await user.selectOptions(screen.getByLabelText('按日期范围筛选'), 'today')
    expect(
      screen.getByRole('button', { name: '展开每日复盘，2 次执行' })
    ).toBeVisible()
    expect(screen.getByText('当前显示 1 个任务 · 2 次执行')).toBeVisible()
    expect(screen.getByRole('tab', { name: '本周 60' })).toBeVisible()
  })

  it('expands the collection for a deep-linked occurrence and closes that inspector when explicitly collapsed', async () => {
    mockOccurrenceRows([
      recurringOccurrence('review-1', '2026-08-01'),
      recurringOccurrence('review-2', '2026-08-02'),
    ])
    renderWorkspace('/tasks?view=upcoming&occurrence_id=review-2')
    const panel = screen.getByRole('complementary', {
      name: '执行详情：每日复盘 · 8月2日',
    })
    expect(panel).toBeVisible()
    const toggle = screen.getByRole('button', {
      name: '收起每日复盘，2 次执行',
    })
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    await userEvent.click(toggle)
    expect(
      screen.queryByRole('complementary', {
        name: '执行详情：每日复盘 · 8月2日',
      })
    ).not.toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: '展开每日复盘，2 次执行' })
    ).toHaveAttribute('aria-expanded', 'false')
    expect(
      screen.queryByRole('list', { name: '每日复盘执行实例' })
    ).not.toBeInTheDocument()
  })

  it('keeps completion and rescheduling commands bound to each expanded execution', async () => {
    const complete = vi.fn().mockResolvedValue({})
    const reschedule = vi.fn().mockResolvedValue({})
    vi.mocked(taskHooks.useCompleteOccurrenceMutation).mockReturnValue({
      mutateAsync: complete,
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useCompleteOccurrenceMutation>)
    vi.mocked(taskHooks.useRescheduleOccurrenceMutation).mockReturnValue({
      mutateAsync: reschedule,
      isPending: false,
    } as unknown as ReturnType<
      typeof taskHooks.useRescheduleOccurrenceMutation
    >)
    mockOccurrenceRows([
      recurringOccurrence('review-1', '2026-08-01'),
      recurringOccurrence('review-2', '2026-08-02'),
    ])
    renderWorkspace()
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: '展开每日复盘，2 次执行' })
    )
    await user.click(
      screen.getByRole('button', { name: '完成每日复盘 · 8月1日' })
    )
    expect(complete).toHaveBeenCalledWith(
      expect.objectContaining({
        taskID: 'recurring-task',
        occurrenceID: 'review-1',
        expectedRevisions: expect.objectContaining({
          expected_occurrence_revisions: { 'review-1': 3 },
        }),
      })
    )
    await user.click(
      screen.getByRole('button', { name: '改期每日复盘 · 8月2日' })
    )
    await user.clear(screen.getByLabelText('新的执行日期'))
    await user.type(screen.getByLabelText('新的执行日期'), '2026-08-05')
    await user.click(screen.getByRole('button', { name: '保存改期' }))
    expect(reschedule).toHaveBeenCalledWith(
      expect.objectContaining({
        taskID: 'recurring-task',
        occurrenceID: 'review-2',
        input: expect.objectContaining({
          expected_occurrence_revision: 3,
          timing: expect.objectContaining({ planned_date: '2026-08-05' }),
        }),
      })
    )
    expect(
      screen.getByRole('button', { name: '收起每日复盘，2 次执行' })
    ).toHaveAttribute('aria-expanded', 'true')
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
        }) as unknown as ReturnType<typeof taskHooks.useOccurrences>
    )
    renderWorkspace()
    const user = userEvent.setup()

    await user.click(screen.getByRole('button', { name: '改期准备评审' }))

    expect(screen.getByLabelText('新的安排方式')).toHaveValue('time_block')
    expect(screen.getByLabelText('新的开始时间')).toHaveValue('14:30')
    expect(screen.getByLabelText('新的结束时间')).toHaveValue('16:00')
  })
})

function renderWorkspace(initialPath = '/') {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
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

let definitions = [...taskDefinitions]
const doneRecurring = occurrencesByScope.upcoming!.find(
  (item) => item.id === 'done-recurring'
)!
const allOccurrences = Object.values(occurrencesByScope).flat()

function queryResult<T>(data: T, refetch = vi.fn()) {
  return {
    data,
    isLoading: false,
    isError: false,
    isPending: false,
    refetch,
  } as never
}

function facetsResponse(): WorkspaceFacetsResponse {
  return {
    workspace_id: 'workspace-test',
    task_model_version: 2,
    runtime_epoch: 1,
    date: '2026-11-19',
    timezone: 'Asia/Shanghai',
    generated_at: '2026-11-19T04:00:00Z',
    read_consistency: 'planning_snapshot',
    facets: {
      today: 9,
      week: 60,
      month: 80,
      overdue: 7,
      unscheduled: 5,
      inbox: 6,
      completed_today: 2,
      completed_week: 4,
      completed_total: 10,
      draft: 4,
      recurring: 15,
    },
  }
}

type MetadataQueryOptions = { queries: Array<{ queryKey: readonly unknown[] }> }

function metadataQueries(options: MetadataQueryOptions) {
  return options.queries.map((query) =>
    queryResult(definitions.find((item) => item.id === query.queryKey.at(-1)))
  ) as never
}

function recurringOccurrence(
  id: string,
  date: string,
  status: OccurrenceV2['execution_status'] = 'open'
): OccurrenceV2 {
  return {
    ...occurrence(id, 'recurring-task', status),
    occurrence_key: date,
    recurring: true,
    planned_date: date,
  }
}

function mockOccurrenceRows(rows: OccurrenceV2[]) {
  vi.mocked(taskHooks.useOccurrences).mockImplementation((params) =>
    queryResult(
      params?.task_id
        ? rows.filter((item) => item.task_id === params.task_id)
        : params?.execution_status
          ? rows.filter(
              (item) => item.execution_status === params.execution_status
            )
          : rows
    )
  )
}
