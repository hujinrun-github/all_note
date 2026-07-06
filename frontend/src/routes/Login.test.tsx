import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { listAuthProviders, login } from '../api/auth'
import Login from './Login'

vi.mock('../api/auth', () => ({
  listAuthProviders: vi.fn(),
  login: vi.fn(),
}))

describe('Login', () => {
  beforeEach(() => {
    vi.mocked(listAuthProviders).mockReset()
    vi.mocked(listAuthProviders).mockResolvedValue(['github'])
    vi.mocked(login).mockReset()
    vi.mocked(login).mockResolvedValue({
      user: {
        id: 'user_admin',
        email: 'admin@example.com',
        display_name: 'Admin',
        must_change_password: false,
        default_workspace_id: 'workspace_admin',
        role: 'admin',
        status: 'active',
        created_at: 0,
        updated_at: 0,
      },
      workspace: {
        id: 'workspace_admin',
        name: 'Admin Workspace',
        owner_user_id: 'user_admin',
        created_at: 0,
        updated_at: 0,
      },
    })
  })

  it('submits credentials to the auth API', async () => {
    const user = userEvent.setup()
    render(
      <MemoryRouter initialEntries={['/login?next=/tasks']}>
        <Login />
      </MemoryRouter>,
    )

    await user.type(screen.getByLabelText('邮箱'), 'admin@example.com')
    await user.type(screen.getByLabelText('密码'), 'admin12345')
    await user.click(screen.getByRole('button', { name: '登录' }))

    await waitFor(() => {
      expect(login).toHaveBeenCalledWith({
        email: 'admin@example.com',
        password: 'admin12345',
        remember_me: true,
      })
    })
  })

  it('does not expose a public create-workspace shortcut to the protected app', () => {
    render(
      <MemoryRouter initialEntries={['/login']}>
        <Login />
      </MemoryRouter>,
    )

    expect(screen.queryByRole('link', { name: '创建工作区' })).not.toBeInTheDocument()
    expect(screen.getByText('需要账号？请联系管理员创建账号')).toBeVisible()
  })
  it('renders GitHub login as a full-page link when provider is enabled', async () => {
    render(
      <MemoryRouter initialEntries={['/login?next=/tasks']}>
        <Login />
      </MemoryRouter>,
    )

    const link = await screen.findByRole('link', { name: /GitHub/ })
    expect(link).toHaveAttribute('href', '/api/auth/github/start?next=%2Ftasks')
  })

  it('hides GitHub login when provider list does not include GitHub', async () => {
    vi.mocked(listAuthProviders).mockResolvedValue([])

    render(
      <MemoryRouter initialEntries={['/login']}>
        <Login />
      </MemoryRouter>,
    )

    await waitFor(() => {
      expect(screen.queryByRole('link', { name: /GitHub/ })).not.toBeInTheDocument()
    })
  })

  it('does not render GitHub login when providers request fails', async () => {
    vi.mocked(listAuthProviders).mockRejectedValue(new Error('offline'))

    render(
      <MemoryRouter initialEntries={['/login']}>
        <Login />
      </MemoryRouter>,
    )

    await waitFor(() => {
      expect(screen.queryByRole('link', { name: /GitHub/ })).not.toBeInTheDocument()
    })
  })

  it('shows oauth_error message from the query string', async () => {
    render(
      <MemoryRouter initialEntries={['/login?oauth_error=github_no_verified_email']}>
        <Login />
      </MemoryRouter>,
    )

    expect(await screen.findByRole('alert')).toHaveTextContent('GitHub 账号没有已验证邮箱')
  })

  it('shows generic oauth_error message for unknown error codes', async () => {
    render(
      <MemoryRouter initialEntries={['/login?oauth_error=unknown_code']}>
        <Login />
      </MemoryRouter>,
    )

    expect(await screen.findByRole('alert')).toHaveTextContent('GitHub 登录失败，请重新尝试')
  })
})
