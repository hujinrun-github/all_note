import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import EditorPage from './Editor'
import * as notesApi from '../api/notes'
import * as syncApi from '../api/sync'
import * as tasksApi from '../api/tasks'
import * as japaneseApi from '../api/japanese'

const tiptapMock = vi.hoisted(() => ({
  getMarkdown: vi.fn(() => 'updated markdown'),
  setContent: vi.fn(),
  isActive: vi.fn(() => false),
  insertContentAt: vi.fn(() => true),
  focus: vi.fn(() => true),
  textBetween: vi.fn(() => '附近'),
}))

vi.mock('@tiptap/react', () => ({
  useEditor: vi.fn(() => ({
    isDestroyed: false,
    storage: {
      markdown: {
        getMarkdown: tiptapMock.getMarkdown,
      },
    },
    commands: {
      setContent: tiptapMock.setContent,
      insertContentAt: tiptapMock.insertContentAt,
      focus: tiptapMock.focus,
    },
    state: {
      selection: { from: 2, to: 4, empty: false },
      doc: { textBetween: tiptapMock.textBetween },
    },
    isActive: tiptapMock.isActive,
    chain: () => ({
      focus: () => ({
        toggleBold: () => ({ run: vi.fn() }),
        toggleItalic: () => ({ run: vi.fn() }),
        toggleStrike: () => ({ run: vi.fn() }),
        toggleHeading: () => ({ run: vi.fn() }),
        toggleBulletList: () => ({ run: vi.fn() }),
        toggleOrderedList: () => ({ run: vi.fn() }),
        toggleBlockquote: () => ({ run: vi.fn() }),
        toggleCodeBlock: () => ({ run: vi.fn() }),
        setHorizontalRule: () => ({ run: vi.fn() }),
      }),
    }),
  })),
  EditorContent: () => null,
}))

vi.mock('@tiptap/react/menus', () => ({
  BubbleMenu: () => null,
}))

vi.mock('../extensions/Ruby', () => ({
  Ruby: {},
}))

vi.mock('../api/notes')
vi.mock('../api/sync')
vi.mock('../api/tasks')
vi.mock('../api/japanese')

function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
}

function renderEditor(queryClient = createQueryClient()) {
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={['/editor/note-1']}>
        <Routes>
          <Route path="/editor/:id" element={<EditorPage />} />
          <Route path="/notes" element={<div>notes page</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  )
}

describe('Editor auto sync', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    tiptapMock.getMarkdown.mockReturnValue('updated markdown')
    tiptapMock.textBetween.mockReturnValue('附近')
    vi.mocked(notesApi.getNote).mockResolvedValue({
      id: 'note-1',
      title: 'Auto Sync Note',
      body: 'original markdown',
      folder_id: '__uncategorized',
      tags: '[]',
      projects: [],
      created_at: 1,
      updated_at: 2,
    })
    vi.mocked(notesApi.updateNote).mockResolvedValue({
      id: 'note-1',
      title: 'Auto Sync Note',
      body: 'updated markdown',
      folder_id: '__uncategorized',
      tags: '[]',
      projects: [],
      created_at: 1,
      updated_at: 3,
    })
    vi.mocked(tasksApi.listTaskProjects).mockResolvedValue([])
    vi.mocked(japaneseApi.annotateJapanese).mockResolvedValue({
      source: 'ai',
      segments: [{ text: '近', reading: 'ちか' }, { text: 'く' }],
    })
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue([
      {
        id: 'notion-1',
        type: 'notion',
        name: 'Personal Notion',
        vault_path: '',
        base_folder: '',
        config_json: '{}',
        enabled: true,
        auto_sync: true,
        is_default: true,
        created_at: 1,
        updated_at: 1,
      },
    ])
    vi.mocked(syncApi.getNoteSyncBinding).mockResolvedValue({
      binding: {
        note_id: 'note-1',
        target_id: 'notion-1',
        created_at: 1,
        updated_at: 1,
      },
      target: {
        id: 'notion-1',
        type: 'notion',
        name: 'Personal Notion',
        vault_path: '',
        base_folder: '',
        config_json: '{}',
        enabled: true,
        auto_sync: true,
        is_default: true,
        created_at: 1,
        updated_at: 1,
      },
      candidates: [],
    })
    vi.mocked(syncApi.syncNote).mockResolvedValue({
      note_id: 'note-1',
      status: 'synced',
    })
    vi.mocked(syncApi.syncObsidianNote).mockResolvedValue({
      note_id: 'note-1',
      status: 'synced',
    })
  })

  it('syncs through the bound note target after saving when Notion auto sync is enabled', async () => {
    const user = userEvent.setup()
    const { container } = renderEditor()

    expect(await screen.findByDisplayValue('Auto Sync Note')).toBeVisible()

    const saveButton =
      container.querySelector<HTMLButtonElement>('.editor-save-btn')
    expect(saveButton).not.toBeNull()
    await user.click(saveButton!)

    await waitFor(() =>
      expect(notesApi.updateNote).toHaveBeenCalledWith('note-1', {
        title: 'Auto Sync Note',
        body: 'updated markdown',
        project_ids: [],
      })
    )
    await waitFor(() => expect(syncApi.syncNote).toHaveBeenCalledWith('note-1'))
    expect(syncApi.syncObsidianNote).not.toHaveBeenCalled()
  })

  it('enters fullscreen writing mode and exits with Escape', async () => {
    const user = userEvent.setup()
    const { container } = renderEditor()

    expect(await screen.findByDisplayValue('Auto Sync Note')).toBeVisible()
    const fullscreenButton = screen.getByRole('button', {
      name: '进入全屏写作',
    })

    await user.click(fullscreenButton)

    expect(container.querySelector('.editor-workspace')).toHaveClass(
      'is-fullscreen'
    )
    expect(screen.getByRole('button', { name: '退出全屏写作' })).toBeVisible()
    expect(
      screen.queryByRole('heading', { name: '笔记信息' })
    ).not.toBeInTheDocument()

    await user.keyboard('{Escape}')

    expect(container.querySelector('.editor-workspace')).not.toHaveClass(
      'is-fullscreen'
    )
    expect(screen.getByRole('button', { name: '进入全屏写作' })).toBeVisible()
  })

  it('can collapse and restore the note information panel', async () => {
    const user = userEvent.setup()
    renderEditor()

    expect(
      await screen.findByRole('heading', { name: '笔记信息' })
    ).toBeVisible()
    await user.click(screen.getByRole('button', { name: '隐藏笔记信息' }))
    expect(
      screen.queryByRole('heading', { name: '笔记信息' })
    ).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '显示笔记信息' }))
    expect(screen.getByRole('heading', { name: '笔记信息' })).toBeVisible()
  })

  it('automatically adds furigana to the selected Japanese text', async () => {
    const user = userEvent.setup()
    renderEditor()

    expect(await screen.findByDisplayValue('Auto Sync Note')).toBeVisible()
    await user.click(screen.getByRole('button', { name: '假名标注' }))

    await waitFor(() =>
      expect(japaneseApi.annotateJapanese).toHaveBeenCalledWith('附近')
    )
    expect(tiptapMock.insertContentAt).toHaveBeenCalledWith(
      { from: 2, to: 4 },
      [
        { type: 'ruby', attrs: { base: '近', reading: 'ちか' } },
        { type: 'text', text: 'く' },
      ]
    )
    expect(screen.getByRole('status')).toHaveTextContent('AI 注音完成')
    expect(
      screen.queryByRole('dialog', { name: '假名标注' })
    ).not.toBeInTheDocument()
  })

  it('falls back to the manual dialog when automatic annotation fails', async () => {
    vi.mocked(japaneseApi.annotateJapanese).mockRejectedValueOnce(
      new Error('offline')
    )
    const user = userEvent.setup()
    renderEditor()

    expect(await screen.findByDisplayValue('Auto Sync Note')).toBeVisible()
    await user.click(screen.getByRole('button', { name: '假名标注' }))

    expect(
      await screen.findByRole('dialog', { name: '假名标注' })
    ).toBeVisible()
    expect(screen.getByLabelText('汉字或词语')).toHaveValue('附近')
    expect(screen.getByText('自动注音失败，请手动填写假名。')).toBeVisible()
  })
})
