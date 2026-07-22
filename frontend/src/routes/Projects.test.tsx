import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as taskHooks from '../hooks/useTaskDomain'
import Projects from './Projects'

vi.mock('../hooks/useTaskDomain')

const mutateAsync = vi.fn()

describe('Projects v2', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(taskHooks.useProjects).mockReturnValue({
      data: [
        project({ id: 'system-inbox', name: '收件箱', system_role: 'inbox' }),
        project({ id: 'personal', name: '个人', system_role: 'personal' }),
        project({ id: 'learning', name: '日语学习', kind: 'learning', horizon: 'long' }),
      ],
      isLoading: false,
      isError: false,
    } as ReturnType<typeof taskHooks.useProjects>)
    vi.mocked(taskHooks.useCreateProjectMutation).mockReturnValue({
      mutateAsync,
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useCreateProjectMutation>)
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

    expect(taskHooks.useProjects).toHaveBeenLastCalledWith({
      kind: 'learning',
      horizon: 'long',
    })
  })

  it('marks system projects and never renders a delete action for them', () => {
    renderProjects()

    expect(screen.getByText('系统收件箱')).toBeVisible()
    expect(screen.getByText('系统个人项目')).toBeVisible()
    expect(screen.queryByRole('button', { name: '删除收件箱' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '删除个人' })).not.toBeInTheDocument()
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
    Partial<import('../api/taskDomain').ProjectV2> = {}
): import('../api/taskDomain').ProjectV2 {
  return {
    id: 'project-1',
    name: '项目',
    kind: 'standard',
    horizon: 'short',
    status: 'active',
    revision: 1,
    ...overrides,
  }
}
