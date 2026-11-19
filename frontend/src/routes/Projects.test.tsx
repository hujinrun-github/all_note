import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as taskHooks from '../hooks/useTaskDomain'
import * as workspaceHooks from '../hooks/useWorkspace'
import type { ProjectWithSummary } from '../api/workspace'
import Projects from './Projects'

vi.mock('../hooks/useTaskDomain')
vi.mock('../hooks/useWorkspace')

const mutateAsync = vi.fn()
const activateProject = vi.fn()

describe('Projects v2', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(workspaceHooks.useProjectSummaries).mockReturnValue({
      data: {
        projects: [
          project({ id: 'system-inbox', name: '收件箱', system_role: 'inbox' }),
          project({ id: 'personal', name: '个人', system_role: 'personal' }),
          project({
            id: 'learning',
            name: '日语学习',
            kind: 'learning',
            horizon: 'long',
            status: 'planning',
          }),
        ],
      },
      isLoading: false,
      isError: false,
    } as ReturnType<typeof workspaceHooks.useProjectSummaries>)
    vi.mocked(taskHooks.useCreateProjectMutation).mockReturnValue({
      mutateAsync,
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useCreateProjectMutation>)
    vi.mocked(taskHooks.useActivateProjectMutation).mockReturnValue({
      mutateAsync: activateProject,
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useActivateProjectMutation>)
    vi.mocked(taskHooks.useDeleteProjectMutation).mockReturnValue({
      mutateAsync: vi.fn(),
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useDeleteProjectMutation>)
  })

  it('combines kind and horizon filters in one query', async () => {
    renderProjects()
    const user = userEvent.setup()

    await user.selectOptions(screen.getByLabelText('项目类型'), 'learning')
    await user.selectOptions(screen.getByLabelText('项目周期'), 'long')

    expect(workspaceHooks.useProjectSummaries).toHaveBeenLastCalledWith({
      date: expect.stringMatching(/^\d{4}-\d{2}-\d{2}$/),
      timezone: expect.any(String),
      kind: 'learning',
      horizon: 'long',
    })
  })

  it('renders server summary progress without requesting task or occurrence lists', () => {
    renderProjects()

    expect(screen.getByLabelText('个人进度')).toHaveTextContent(
      '已完成 25% · 9 个待执行'
    )
    expect(taskHooks.useProjects).not.toHaveBeenCalled()
    expect(taskHooks.useTaskDefinitions).not.toHaveBeenCalled()
    expect(taskHooks.useOccurrences).not.toHaveBeenCalled()
  })

  it('keeps indefinite recurrence progress unquantified', () => {
    const indefiniteProject = project()
    indefiniteProject.summary = {
      ...indefiniteProject.summary,
      has_open_ended_recurrence: true,
      completion_percent: null,
    }
    vi.mocked(workspaceHooks.useProjectSummaries).mockReturnValue({
      data: { projects: [indefiniteProject] },
      isLoading: false,
      isError: false,
    } as ReturnType<typeof workspaceHooks.useProjectSummaries>)
    renderProjects()

    expect(screen.getByLabelText('项目进度')).toHaveTextContent('持续推进')
    expect(screen.getByLabelText('项目进度')).not.toHaveTextContent('%')
  })

  it('shows summary failures instead of falling back to complete lists', () => {
    vi.mocked(workspaceHooks.useProjectSummaries).mockReturnValue({
      isLoading: false,
      isError: true,
    } as ReturnType<typeof workspaceHooks.useProjectSummaries>)
    renderProjects()

    expect(screen.getByText('项目暂时不可用。')).toBeVisible()
    expect(taskHooks.useProjects).not.toHaveBeenCalled()
    expect(taskHooks.useTaskDefinitions).not.toHaveBeenCalled()
    expect(taskHooks.useOccurrences).not.toHaveBeenCalled()
  })

  it('marks system projects and never renders a delete action for them', () => {
    renderProjects()

    expect(screen.getByRole('heading', { name: '项目列表' })).toBeVisible()
    expect(screen.getByText('3 个项目')).toBeVisible()
    expect(screen.getByText('系统收件箱')).toBeVisible()
    expect(screen.getByText('系统个人项目')).toBeVisible()
    expect(screen.getByRole('link', { name: '打开收件箱' })).toHaveAttribute(
      'href',
      '/projects/system-inbox'
    )
    expect(
      screen.queryByRole('button', { name: '删除收件箱' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: '删除个人' })
    ).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '删除日语学习' })).toBeVisible()
  })

  it('creates a project with independent kind and horizon values', async () => {
    renderProjects()
    const user = userEvent.setup()

    await user.click(screen.getByRole('button', { name: '新建项目' }))
    await user.type(screen.getByLabelText('项目名称'), '年度写作')
    await user.selectOptions(screen.getByLabelText('新项目类型'), 'standard')
    await user.selectOptions(screen.getByLabelText('新项目周期'), 'long')
    await user.click(screen.getByRole('button', { name: '创建项目' }))

    expect(mutateAsync).toHaveBeenCalledWith({
      name: '年度写作',
      kind: 'standard',
      horizon: 'long',
      status: 'planning',
    })
  })

  it('starts a planning project from the status column', async () => {
    renderProjects()
    const user = userEvent.setup()

    await user.click(screen.getByRole('button', { name: '开始日语学习' }))

    expect(activateProject).toHaveBeenCalledWith({
      projectID: 'learning',
      expectedRevision: {
        expected_project_revision: 1,
      },
    })
  })

  it('resets both project filters from the compact command bar', async () => {
    renderProjects()
    const user = userEvent.setup()

    await user.selectOptions(screen.getByLabelText('项目类型'), 'learning')
    await user.selectOptions(screen.getByLabelText('项目周期'), 'long')
    await user.click(screen.getByRole('button', { name: '重置' }))

    expect(screen.getByLabelText('项目类型')).toHaveValue('')
    expect(screen.getByLabelText('项目周期')).toHaveValue('')
    expect(workspaceHooks.useProjectSummaries).toHaveBeenLastCalledWith({
      date: expect.stringMatching(/^\d{4}-\d{2}-\d{2}$/),
      timezone: expect.any(String),
      kind: undefined,
      horizon: undefined,
    })
  })
})

function renderProjects() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <MemoryRouter>
      <QueryClientProvider client={client}>
        <Projects />
      </QueryClientProvider>
    </MemoryRouter>
  )
}

function project(
  overrides: Partial<taskHooks.ProjectListParams> &
    Partial<ProjectWithSummary> = {}
): ProjectWithSummary {
  return {
    id: 'project-1',
    name: '项目',
    kind: 'standard',
    horizon: 'short',
    status: 'active',
    revision: 1,
    summary: {
      completed_task_count: 2,
      total_actionable_task_count: 8,
      open_occurrence_count: 9,
      blocked_occurrence_count: 1,
      has_open_ended_recurrence: false,
      next_occurrence: null,
      completion_percent: 25,
    },
    ...overrides,
  }
}
