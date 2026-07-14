import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { getCurrentUser } from '../../api/auth'
import { RequireAdmin } from './RequireAdmin'

vi.mock('../../api/auth', () => ({ getCurrentUser: vi.fn() }))

function renderGuard() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={['/admin/users']}>
        <Routes>
          <Route path="/" element={<span>今日页面</span>} />
          <Route element={<RequireAdmin />}>
            <Route path="/admin/users" element={<span>账号管理页面</span>} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  )
}

describe('RequireAdmin', () => {
  beforeEach(() => vi.clearAllMocks())

  it('redirects a regular user away from admin routes', async () => {
    vi.mocked(getCurrentUser).mockResolvedValue({
      user: { id: 'user-1', email: 'user@example.com', display_name: '用户', role: 'user', status: 'active', must_change_password: false, default_workspace_id: 'workspace-1', created_at: 0, updated_at: 0 },
      workspace: { id: 'workspace-1', name: 'Workspace', owner_user_id: 'user-1', created_at: 0, updated_at: 0 },
      must_change_password: false,
    })

    renderGuard()

    expect(await screen.findByText('今日页面')).toBeVisible()
    expect(screen.queryByText('账号管理页面')).not.toBeInTheDocument()
  })

  it('renders admin routes for administrators', async () => {
    vi.mocked(getCurrentUser).mockResolvedValue({
      user: { id: 'admin-1', email: 'admin@example.com', display_name: '管理员', role: 'admin', status: 'active', must_change_password: false, default_workspace_id: 'workspace-1', created_at: 0, updated_at: 0 },
      workspace: { id: 'workspace-1', name: 'Workspace', owner_user_id: 'admin-1', created_at: 0, updated_at: 0 },
      must_change_password: false,
    })

    renderGuard()

    expect(await screen.findByText('账号管理页面')).toBeVisible()
  })
})
