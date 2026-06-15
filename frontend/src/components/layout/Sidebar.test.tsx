import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import { Sidebar } from './Sidebar'

vi.mock('../../hooks/useInbox', () => ({
  useInboxList: () => ({ data: { pagination: { total: 0 } } }),
}))

function renderSidebar(queryClient: QueryClient, initialPath = '/') {
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[initialPath]}>
        <Sidebar />
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
})
