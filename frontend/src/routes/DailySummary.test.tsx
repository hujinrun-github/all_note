import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useSummary } from '../hooks/useSummary'
import { useTodayOverview } from '../hooks/useTodayOverview'
import DailySummary from './DailySummary'

vi.mock('../hooks/useSummary', () => ({ useSummary: vi.fn() }))
vi.mock('../hooks/useTodayOverview', () => ({ useTodayOverview: vi.fn() }))

describe('DailySummary', () => {
  beforeEach(() => {
    vi.mocked(useSummary).mockReturnValue({
      data: {
        summary: {
          groups: [{ date: '2026-07-13', count: 1, tasks: [{ id: 'done-1', title: '完成真实任务', done: 1 }] }],
          active_days: 1,
          project_count: 1,
        },
        pagination: { page: 1, page_size: 50, total: 1 },
      },
      isLoading: false,
      error: null,
    } as ReturnType<typeof useSummary>)
    vi.mocked(useTodayOverview).mockReturnValue({
      data: { todayTasks: [], overdueTasks: [], events: [], recentNotes: [] },
      isLoading: false,
      error: null,
    } as unknown as ReturnType<typeof useTodayOverview>)
  })

  it('renders fetched review data without hard-coded sample content', async () => {
    render(<MemoryRouter><DailySummary /></MemoryRouter>)

    expect(screen.getByText('完成真实任务')).toBeVisible()
    expect(screen.queryByText('尝试 kapathy 的知识库方案')).not.toBeInTheDocument()
    expect(screen.queryByText('第一篇笔记')).not.toBeInTheDocument()
  })
})
