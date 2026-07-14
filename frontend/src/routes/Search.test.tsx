import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useSearch } from '../hooks/useSearch'
import Search from './Search'

vi.mock('../hooks/useSearch', () => ({ useSearch: vi.fn() }))

function renderSearch(initialEntry = '/search?q=计划') {
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <Search />
    </MemoryRouter>
  )
}

describe('Search workspace', () => {
  beforeEach(() => {
    window.localStorage.clear()
    vi.mocked(useSearch).mockReturnValue({
      data: {
        items: [
          { type: 'note', id: 'note-1', title: '计划笔记', highlight: '', updated_at: 100 },
          { type: 'task', id: 'task-1', title: '计划任务', highlight: '', updated_at: 100, done: 0 },
        ],
        pagination: { page: 1, page_size: 20, total: 2 },
      },
      isLoading: false,
      isFetching: false,
    } as ReturnType<typeof useSearch>)
  })

  it('filters results by content type', async () => {
    const user = userEvent.setup()
    renderSearch()

    expect(screen.getByRole('button', { name: /计划笔记/ })).toBeVisible()
    expect(screen.getByRole('button', { name: /计划任务/ })).toBeVisible()

    await user.click(screen.getByRole('tab', { name: '任务' }))

    expect(screen.queryByRole('button', { name: /计划笔记/ })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /计划任务/ })).toBeVisible()
  })

  it('clears stored recent searches', async () => {
    window.localStorage.setItem('flowspace.recent-searches', JSON.stringify(['kapathy', 'N2 语法']))
    const user = userEvent.setup()
    renderSearch('/search')

    expect(screen.getByRole('button', { name: 'kapathy' })).toBeVisible()
    await user.click(screen.getByRole('button', { name: '清除历史' }))

    expect(screen.queryByRole('button', { name: 'kapathy' })).not.toBeInTheDocument()
  })
})
