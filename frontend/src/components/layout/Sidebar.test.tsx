import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ComponentProps } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import { Sidebar } from './Sidebar'

vi.mock('../../hooks/useInbox', () => ({
  useInboxList: () => ({ data: { pagination: { total: 0 } } }),
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
})
