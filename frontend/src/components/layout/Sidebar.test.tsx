import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ComponentProps } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { Sidebar } from './Sidebar'
import { getCurrentUser } from '../../api/auth'

vi.mock('../../hooks/useInbox', () => ({
  useInboxList: () => ({ data: { pagination: { total: 0 } } }),
}))

vi.mock('../../api/auth', () => ({
  getCurrentUser: vi.fn(),
}))

function renderSidebar(
  queryClient: QueryClient,
  initialPath = '/',
  sidebarProps: Partial<ComponentProps<typeof Sidebar>> = {},
) {
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[initialPath]}>
        <Sidebar {...sidebarProps} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('Sidebar navigation refresh', () => {
  beforeEach(() => {
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

    await userEvent.click(screen.getByRole('link', { name: '今日' }))

    await waitFor(() => expect(queryClient.getQueryState(['today'])?.isInvalidated).toBe(true))
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
      </QueryClientProvider>,
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
      </QueryClientProvider>,
    )

    expect(screen.getByRole('button', { name: '展开侧边栏' })).toHaveAttribute('aria-expanded', 'false')
    expect(screen.getByRole('complementary')).toHaveClass('is-collapsed')
  })

  it('hides the entire system group from non-admin users', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })

    renderSidebar(queryClient)

    expect(await screen.findByRole('link', { name: '今日' })).toBeVisible()
    expect(screen.queryByText('系统')).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: '账号管理' })).not.toBeInTheDocument()
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
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })

    renderSidebar(queryClient)

    expect(await screen.findByRole('link', { name: '账号管理' })).toBeVisible()
    expect(screen.getByText('系统')).toBeVisible()
  })
})
