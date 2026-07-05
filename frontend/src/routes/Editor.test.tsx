import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import EditorPage from './Editor'
import * as notesApi from '../api/notes'
import * as syncApi from '../api/sync'
import * as tasksApi from '../api/tasks'

const tiptapMock = vi.hoisted(() => ({
  getMarkdown: vi.fn(() => 'updated markdown'),
  setContent: vi.fn(),
  isActive: vi.fn(() => false),
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

vi.mock('../api/notes')
vi.mock('../api/sync')
vi.mock('../api/tasks')

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
    </QueryClientProvider>,
  )
}

describe('Editor auto sync', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    tiptapMock.getMarkdown.mockReturnValue('updated markdown')
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
      binding: { note_id: 'note-1', target_id: 'notion-1', created_at: 1, updated_at: 1 },
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

    const saveButton = container.querySelector<HTMLButtonElement>('.editor-save-btn')
    expect(saveButton).not.toBeNull()
    await user.click(saveButton!)

    await waitFor(() =>
      expect(notesApi.updateNote).toHaveBeenCalledWith('note-1', {
        title: 'Auto Sync Note',
        body: 'updated markdown',
        project_ids: [],
      }),
    )
    await waitFor(() => expect(syncApi.syncNote).toHaveBeenCalledWith('note-1'))
    expect(syncApi.syncObsidianNote).not.toHaveBeenCalled()
  })
})
