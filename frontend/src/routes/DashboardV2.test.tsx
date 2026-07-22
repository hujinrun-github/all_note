import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as taskHooks from '../hooks/useTaskDomain'
import DashboardV2 from './DashboardV2'

vi.mock('../hooks/useTaskDomain')

describe('Dashboard v2 today projection', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(taskHooks.useTaskDefinitions).mockReturnValue({
      data: [task('today', '今天处理'), task('overdue', '补交周报')],
      isLoading: false,
    } as ReturnType<typeof taskHooks.useTaskDefinitions>)
    vi.mocked(taskHooks.useOccurrences).mockImplementation((params) => ({
      data:
        params?.scope === 'today'
          ? [occurrence('today-occ', 'today')]
          : params?.scope === 'overdue'
            ? [occurrence('overdue-occ', 'overdue')]
            : [],
      isLoading: false,
      isError: false,
    } as ReturnType<typeof taskHooks.useOccurrences>))
    vi.mocked(taskHooks.useCompleteOccurrenceMutation).mockReturnValue({
      mutateAsync: vi.fn(),
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useCompleteOccurrenceMutation>)
  })

  it('defaults to today instead of the overdue collection', async () => {
    renderDashboard()
    const user = userEvent.setup()

    const today = screen.getByRole('tab', { name: '今天 1' })
    const overdue = screen.getByRole('tab', { name: '已逾期 1' })
    expect(today).toHaveAttribute('aria-selected', 'true')
    expect(overdue).toHaveAttribute('aria-selected', 'false')
    expect(screen.getByText('今天处理')).toBeVisible()
    expect(screen.queryByText('补交周报')).not.toBeInTheDocument()

    await user.click(overdue)
    expect(screen.getByText('补交周报')).toBeVisible()
  })
})

function renderDashboard() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <MemoryRouter>
      <QueryClientProvider client={client}>
        <DashboardV2 />
      </QueryClientProvider>
    </MemoryRouter>
  )
}

function task(id: string, title: string): import('../api/taskDomain').TaskV2 {
  return {
    id,
    project_id: 'personal',
    title,
    priority: 0,
    sort_order: 0,
    lifecycle_status: 'active',
    revision: 2,
    schedule_revision: 1,
  }
}

function occurrence(id: string, taskID: string): import('../api/taskDomain').OccurrenceV2 {
  return {
    id,
    task_id: taskID,
    occurrence_key: 'once',
    execution_status: 'open',
    revision: 3,
    generated_schedule_revision: 1,
  }
}
