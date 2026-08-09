import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import Notes from './Notes'
import { getNotes } from '../api/notes'
import { useCreateNote } from '../hooks/useNotes'
import { useProjects } from '../hooks/useTaskDomain'

vi.mock('../api/notes', () => ({
  getNotes: vi.fn(),
  deleteNote: vi.fn(),
}))

vi.mock('../hooks/useNotes', () => ({
  useCreateNote: vi.fn(),
}))

vi.mock('../hooks/useTaskDomain', () => ({
  useProjects: vi.fn(),
}))

function renderNotes() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
  return render(
    <MemoryRouter>
      <QueryClientProvider client={queryClient}>
        <Notes />
      </QueryClientProvider>
    </MemoryRouter>
  )
}

describe('Notes library', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(useProjects).mockReturnValue({
      data: [
        {
          id: 'project-ai',
          name: 'AI 学习',
          kind: 'learning',
          horizon: 'long',
          status: 'active',
          revision: 2,
        },
      ],
      isLoading: false,
      isError: false,
    } as unknown as ReturnType<typeof useProjects>)
    vi.mocked(useCreateNote).mockReturnValue({
      mutateAsync: vi.fn(),
      isPending: false,
    } as unknown as ReturnType<typeof useCreateNote>)
    vi.mocked(getNotes).mockResolvedValue({
      notes: [
        {
          id: 'note-1',
          title: '第一篇',
          body: '第一篇正文',
          folder_id: '__uncategorized',
          tags: '[]',
          projects: null,
          created_at: 100,
          updated_at: 200,
        },
        {
          id: 'note-2',
          title: '第二篇',
          body: '第二篇正文',
          folder_id: '__uncategorized',
          tags: null,
          projects: [],
          created_at: 90,
          updated_at: 190,
        },
      ],
      pagination: { page: 1, page_size: 100, total: 2, total_pages: 1 },
    } as unknown as Awaited<ReturnType<typeof getNotes>>)
  })

  it('handles legacy null fields and changes the detail preview without navigating', async () => {
    const user = userEvent.setup()
    renderNotes()

    expect(await screen.findByRole('heading', { name: '第一篇' })).toBeVisible()
    expect(document.querySelector('.note-detail-body')).toHaveTextContent(
      '第一篇正文'
    )

    await user.click(screen.getByRole('button', { name: /第二篇.*第二篇正文/ }))

    expect(screen.getByRole('heading', { name: '第二篇' })).toBeVisible()
    expect(document.querySelector('.note-detail-body')).toHaveTextContent(
      '第二篇正文'
    )
    expect(
      screen.getByRole('button', { name: /第二篇.*第二篇正文/ })
    ).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', { name: /AI 学习/ })).toBeVisible()
  })

  it('shows clean text for ordered lists containing ruby annotations', async () => {
    vi.mocked(getNotes).mockResolvedValue({
      notes: [
        {
          id: 'note-ruby',
          title: 'QA 自动注音验证',
          body: '1. すぐ｜近《ちか》く、｜日本語《にほんご》を｜勉強《べんきょう》する。\n2.',
          folder_id: '__uncategorized',
          tags: '[]',
          projects: [],
          created_at: 100,
          updated_at: 200,
        },
      ],
      pagination: { page: 1, page_size: 100, total: 1, total_pages: 1 },
    } as unknown as Awaited<ReturnType<typeof getNotes>>)

    renderNotes()

    expect(
      await screen.findByRole('heading', { name: 'QA 自动注音验证' })
    ).toBeVisible()
    expect(document.querySelector('.note-row-preview')).toHaveTextContent(
      'すぐ近く、日本語を勉強する。'
    )
    expect(document.querySelector('.note-detail-body')).toHaveTextContent(
      'すぐ近く、日本語を勉強する。'
    )
    expect(document.querySelector('.note-row-preview')).not.toHaveTextContent(
      '1.'
    )
    expect(document.querySelector('.note-row-preview')).not.toHaveTextContent(
      '2.'
    )
    expect(document.querySelector('.note-row-preview')).not.toHaveTextContent(
      'ちか'
    )
  })

  it('creates a note in the selected project', async () => {
    const create = vi.fn().mockResolvedValue({ id: 'new-note' })
    vi.mocked(useCreateNote).mockReturnValue({
      mutateAsync: create,
      isPending: false,
    } as unknown as ReturnType<typeof useCreateNote>)
    const user = userEvent.setup()
    renderNotes()

    await screen.findByRole('heading', { name: '第一篇' })
    await user.click(screen.getByRole('button', { name: /AI 学习/ }))
    await user.click(screen.getByRole('button', { name: '新建笔记' }))

    expect(create).toHaveBeenCalledWith(
      expect.objectContaining({ project_ids: ['project-ai'] })
    )
  })

  it('shows an error when note creation fails', async () => {
    vi.mocked(useCreateNote).mockReturnValue({
      mutateAsync: vi.fn().mockRejectedValue(new Error('request failed')),
      isPending: false,
    } as unknown as ReturnType<typeof useCreateNote>)
    const user = userEvent.setup()
    renderNotes()

    await screen.findByRole('heading', { name: '第一篇' })
    await user.click(screen.getByRole('button', { name: '新建笔记' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      '新建笔记失败，请稍后重试。'
    )
  })
})
