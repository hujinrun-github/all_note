import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as adminApi from '../api/admin'
import AccountAdmin from './AccountAdmin'

vi.mock('../api/admin', () => ({
  listAdminUsers: vi.fn(),
  createAdminUser: vi.fn(),
  resetAdminUserPassword: vi.fn(),
  setAdminUserStatus: vi.fn(),
  updateAdminUser: vi.fn(),
}))

function renderAccountAdmin() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <AccountAdmin />
    </QueryClientProvider>
  )
}

describe('AccountAdmin', () => {
  beforeEach(() => {
    vi.mocked(adminApi.listAdminUsers).mockResolvedValue({
      users: [],
      pagination: { page: 1, page_size: 20, total: 0 },
    })
  })

  it('opens the invite form as a focused dialog', async () => {
    const user = userEvent.setup()
    renderAccountAdmin()

    await user.click(screen.getByRole('button', { name: '邀请用户' }))

    expect(screen.getByRole('dialog', { name: '邀请用户' })).toBeVisible()
    expect(screen.getByLabelText('邮箱')).toBeVisible()
  })
})
