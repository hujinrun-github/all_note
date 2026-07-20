import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { getCurrentUser, logout } from '../../api/auth'
import { TopBar } from './TopBar'

vi.mock('../../api/auth', () => ({
  getCurrentUser: vi.fn(),
  logout: vi.fn(),
}))

function renderTopBar(initialPath = '/') {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })

  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[initialPath]}>
        <Routes>
          <Route path="*" element={<TopBar />} />
          <Route path="/login" element={<span>登录页</span>} />
          <Route path="/settings" element={<span>设置页面</span>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  )
}

describe('TopBar account menu', () => {
  beforeEach(() => {
    vi.mocked(getCurrentUser).mockReset()
    vi.mocked(getCurrentUser).mockResolvedValue({
      user: {
        id: 'user_regular',
        email: 'regular@example.com',
        display_name: '王小明',
        must_change_password: false,
        default_workspace_id: 'workspace_regular',
        role: 'user',
        status: 'active',
        created_at: 0,
        updated_at: 0,
      },
      workspace: {
        id: 'workspace_regular',
        name: 'Regular Workspace',
        owner_user_id: 'user_regular',
        created_at: 0,
        updated_at: 0,
      },
      must_change_password: false,
    })
    vi.mocked(logout).mockReset()
    vi.mocked(logout).mockResolvedValue(undefined)
  })

  it('shows the current user instead of a hard-coded avatar', async () => {
    const user = userEvent.setup()
    renderTopBar()

    const avatar = await screen.findByRole('button', { name: '打开账号菜单' })
    await waitFor(() => expect(avatar).toHaveTextContent('王'))

    await user.click(avatar)

    expect(screen.getByText('王小明')).toBeVisible()
    expect(screen.getByText('regular@example.com')).toBeVisible()
  })

  it('logs out from the avatar menu and returns to login', async () => {
    const user = userEvent.setup()
    renderTopBar()

    await user.click(screen.getByRole('button', { name: '打开账号菜单' }))
    await user.click(screen.getByRole('menuitem', { name: '退出登录' }))

    await waitFor(() => expect(logout).toHaveBeenCalledTimes(1))
    expect(await screen.findByText('登录页')).toBeVisible()
  })

  it('opens user settings from the avatar menu', async () => {
    const user = userEvent.setup()
    renderTopBar()
    await user.click(screen.getByRole('button', { name: '打开账号菜单' }))
    await user.click(screen.getByRole('menuitem', { name: '用户设置' }))
    expect(await screen.findByText('设置页面')).toBeVisible()
  })
})
