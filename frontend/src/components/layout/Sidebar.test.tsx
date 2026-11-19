import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ComponentProps } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { Sidebar } from './Sidebar'
import { getCurrentUser } from '../../api/auth'
import * as workspaceHooks from '../../hooks/useWorkspace'
import * as taskHooks from '../../hooks/useTaskDomain'

vi.mock('../../hooks/useTaskInbox', () => ({
  useTaskInbox: vi.fn(() => ({ occurrencesQuery: { data: [] } })),
}))

vi.mock('../../hooks/useWorkspace')

vi.mock('../../api/auth', () => ({
  getCurrentUser: vi.fn(),
}))

vi.mock('../../hooks/useTaskDomain', () => ({
  useTaskDomainCapabilities: vi.fn(() => ({
    data: { model_version: 'legacy', available: true },
    isLoading: false,
    isError: false,
  })),
}))

function renderSidebar(
  queryClient: QueryClient,
  initialPath = '/',
  sidebarProps: Partial<ComponentProps<typeof Sidebar>> = {}
) {
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[initialPath]}>
        <Sidebar {...sidebarProps} />
      </MemoryRouter>
    </QueryClientProvider>
  )
}

describe('Sidebar navigation refresh', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(workspaceHooks.useTaskWorkbenchFacets).mockReturnValue({
      data: { facets: { inbox: 0 } },
      isLoading: false,
      isError: false,
    } as unknown as ReturnType<typeof workspaceHooks.useTaskWorkbenchFacets>)
    vi.mocked(getCurrentUser).mockResolvedValue({
      user: {
        id: 'user-1',
        email: 'user@example.com',
        display_name: '普通用户',
        role: 'user',
        status: 'active',
        must_change_password: false,
        default_workspace_id: 'workspace-1',
        created_at: 0,
        updated_at: 0,
      },
      workspace: {
        id: 'workspace-1',
        name: 'Workspace',
        owner_user_id: 'user-1',
        created_at: 0,
        updated_at: 0,
      },
      must_change_password: false,
    })
  })

  it('invalidates cached page data when a sidebar tab is clicked', async () => {
    const queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false, staleTime: 30_000 },
        mutations: { retry: false },
      },
    })
    queryClient.setQueryData(['today'], { todayTasks: [] })

    renderSidebar(queryClient)
    expect(queryClient.getQueryState(['today'])?.isInvalidated).toBe(false)

    await userEvent.click(screen.getByRole('link', { name: '工作台' }))

    await waitFor(() =>
      expect(queryClient.getQueryState(['today'])?.isInvalidated).toBe(true)
    )
  })

  it('exposes a compact sidebar toggle state', async () => {
    const queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    })
    const onToggleCollapsed = vi.fn()
    const { rerender } = render(
      <QueryClientProvider client={queryClient}>
        <MemoryRouter>
          <Sidebar collapsed={false} onToggleCollapsed={onToggleCollapsed} />
        </MemoryRouter>
      </QueryClientProvider>
    )

    const collapseButton = screen.getByRole('button', { name: '收起侧边栏' })
    expect(collapseButton).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByRole('complementary')).not.toHaveClass('is-collapsed')

    await userEvent.click(collapseButton)
    expect(onToggleCollapsed).toHaveBeenCalledTimes(1)

    rerender(
      <QueryClientProvider client={queryClient}>
        <MemoryRouter>
          <Sidebar collapsed onToggleCollapsed={onToggleCollapsed} />
        </MemoryRouter>
      </QueryClientProvider>
    )

    expect(screen.getByRole('button', { name: '展开侧边栏' })).toHaveAttribute(
      'aria-expanded',
      'false'
    )
    expect(screen.getByRole('complementary')).toHaveClass('is-collapsed')
  })

  it('hides the entire system group from non-admin users', async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })

    renderSidebar(queryClient)

    expect(await screen.findByRole('link', { name: '工作台' })).toBeVisible()
    expect(screen.queryByText('系统')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('link', { name: '账号管理' })
    ).not.toBeInTheDocument()
  })

  it('shows account management to administrators', async () => {
    vi.mocked(getCurrentUser).mockResolvedValue({
      user: {
        id: 'admin-1',
        email: 'admin@example.com',
        display_name: '管理员',
        role: 'admin',
        status: 'active',
        must_change_password: false,
        default_workspace_id: 'workspace-1',
        created_at: 0,
        updated_at: 0,
      },
      workspace: {
        id: 'workspace-1',
        name: 'Workspace',
        owner_user_id: 'admin-1',
        created_at: 0,
        updated_at: 0,
      },
      must_change_password: false,
    })
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })

    renderSidebar(queryClient)

    expect(await screen.findByRole('link', { name: '账号管理' })).toBeVisible()
    expect(screen.getByText('系统')).toBeVisible()
  })

  it('uses the server inbox facet when the workspace supports v2', () => {
    vi.mocked(taskHooks.useTaskDomainCapabilities).mockReturnValue({
      data: { model_version: 'v2', available: true },
      isLoading: false,
      isError: false,
    } as ReturnType<typeof taskHooks.useTaskDomainCapabilities>)
    vi.mocked(workspaceHooks.useTaskWorkbenchFacets).mockReturnValue({
      data: { facets: { inbox: 120 } },
      isLoading: false,
      isError: false,
    } as unknown as ReturnType<typeof workspaceHooks.useTaskWorkbenchFacets>)
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })

    renderSidebar(queryClient)

    expect(screen.getByLabelText('120 条未整理')).toHaveTextContent('99+')
    expect(workspaceHooks.useTaskWorkbenchFacets).toHaveBeenCalledWith(
      {
        date: expect.stringMatching(/^\d{4}-\d{2}-\d{2}$/),
        timezone: expect.any(String),
      },
      { enabled: true }
    )
  })

  it('hides the inbox badge when the aggregate read fails', () => {
    vi.mocked(workspaceHooks.useTaskWorkbenchFacets).mockReturnValue({
      data: { facets: { inbox: 120 } },
      isLoading: false,
      isError: true,
    } as unknown as ReturnType<typeof workspaceHooks.useTaskWorkbenchFacets>)
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })

    renderSidebar(queryClient)

    expect(screen.queryByLabelText('120 条未整理')).not.toBeInTheDocument()
  })
})
