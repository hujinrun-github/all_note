import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import EditorPage from './Editor'
import * as notesApi from '../api/notes'
import * as syncApi from '../api/sync'
import * as taskDomainApi from '../api/taskDomain'
import * as japaneseApi from '../api/japanese'
import { APIError } from '../api/client'

const tiptapMock = vi.hoisted(() => ({
  getMarkdown: vi.fn(() => 'updated markdown'),
  setContent: vi.fn(),
  isActive: vi.fn(() => false),
  insertContentAt: vi.fn(() => true),
  focus: vi.fn(() => true),
  textBetween: vi.fn(() => '附近'),
  captureEditorOptions: vi.fn(),
  captureEditor: vi.fn(),
}))

vi.mock('@tiptap/react', () => ({
  useEditor: vi.fn((options: unknown) => {
    tiptapMock.captureEditorOptions(options)
    const editor = {
      isDestroyed: false,
      view: { composing: false },
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
    }
    tiptapMock.captureEditor(editor)
    return editor
  }),
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
vi.mock('../api/taskDomain')
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
    vi.mocked(notesApi.getNoteAttachments).mockResolvedValue([])
    vi.mocked(notesApi.uploadNoteAttachment).mockResolvedValue({
      id: 'attachment-1',
      note_id: 'note-1',
      kind: 'video',
      original_name: 'demo.mp4',
      mime_type: 'video/mp4',
      size_bytes: 1024,
      sha256: 'abc',
      source: 'upload',
      deletable: true,
      created_at: 4,
      content_url: '/api/notes/note-1/attachments/attachment-1/content',
    })
    vi.mocked(notesApi.deleteNoteAttachment).mockResolvedValue()
    vi.mocked(notesApi.transcribeVoiceNote).mockResolvedValue({
      client_id: 'voice-client-1',
      note_id: 'note-1',
      body: '这是服务端识别出的文字。',
      transcription_state: 'completed',
      updated_at: 5,
    })
    vi.mocked(taskDomainApi.listProjects).mockResolvedValue([
      {
        id: 'project-1',
        name: '产品上线',
        kind: 'standard',
        horizon: 'short',
        status: 'active',
        revision: 2,
      },
    ])
    vi.mocked(taskDomainApi.listTaskDefinitions).mockResolvedValue([
      {
        id: 'task-1',
        project_id: 'project-1',
        title: '完成发布检查',
        priority: 1,
        sort_order: 0,
        lifecycle_status: 'active',
        revision: 3,
        schedule_revision: 4,
      },
    ])
    vi.mocked(taskDomainApi.updateTaskDefinition).mockResolvedValue({
      id: 'task-1',
      project_id: 'project-1',
      task_note_id: 'note-1',
      title: '完成发布检查',
      priority: 1,
      sort_order: 0,
      lifecycle_status: 'active',
      revision: 4,
      schedule_revision: 4,
    })
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
      })
    )
    await waitFor(() => expect(syncApi.syncNote).toHaveBeenCalledWith('note-1'))
    expect(syncApi.syncObsidianNote).not.toHaveBeenCalled()
  })

  it('links the note to a v2 task selected through its project', async () => {
    const user = userEvent.setup()
    renderEditor()

    expect(await screen.findByDisplayValue('Auto Sync Note')).toBeVisible()
    expect(
      await screen.findByRole('option', { name: /产品上线/ })
    ).toBeVisible()

    await user.selectOptions(
      screen.getByRole('combobox', { name: '选择关联任务' }),
      'task-1'
    )

    await waitFor(() =>
      expect(taskDomainApi.updateTaskDefinition).toHaveBeenCalledWith(
        'task-1',
        {
          task_note_id: 'note-1',
          expected_task_revision: 3,
          expected_schedule_revision: 4,
        }
      )
    )
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

  it('adds furigana after typing pauses when live annotation is enabled', async () => {
    vi.mocked(japaneseApi.annotateJapanese).mockResolvedValueOnce({
      source: 'ai',
      segments: [{ text: '日本語', reading: 'にほんご' }],
    })
    const user = userEvent.setup()
    renderEditor()

    expect(await screen.findByDisplayValue('Auto Sync Note')).toBeVisible()
    await user.click(screen.getByRole('checkbox', { name: '实时注音' }))
    expect(screen.getByText('已开启')).toBeVisible()

    const editor = tiptapMock.captureEditor.mock.lastCall?.[0] as {
      state: {
        selection: unknown
      }
    }
    editor.state.selection = {
      from: 4,
      to: 4,
      empty: true,
      $from: {
        parentOffset: 3,
        parent: {
          type: { name: 'paragraph' },
          forEach: (
            callback: (
              node: { isText: boolean; text: string; nodeSize: number },
              offset: number
            ) => void
          ) => callback({ isText: true, text: '日本語', nodeSize: 3 }, 0),
        },
        start: () => 1,
      },
    }
    tiptapMock.textBetween.mockReturnValue('日本語')

    const options = tiptapMock.captureEditorOptions.mock.lastCall?.[0] as {
      onUpdate: (payload: { editor: unknown }) => void
    }
    options.onUpdate({ editor })

    expect(await screen.findByText('等待停顿')).toBeVisible()
    await waitFor(
      () =>
        expect(japaneseApi.annotateJapanese).toHaveBeenCalledWith(
          '日本語',
          expect.objectContaining({
            signal: expect.any(AbortSignal),
            mode: 'local',
          })
        ),
      { timeout: 1500 }
    )
    expect(tiptapMock.insertContentAt).toHaveBeenCalledWith(
      { from: 1, to: 4 },
      [
        {
          type: 'ruby',
          attrs: { base: '日本語', reading: 'にほんご' },
        },
      ]
    )
    expect(await screen.findByText('已注音')).toBeVisible()
  })

  it('retries live annotation after Japanese IME composition ends', async () => {
    vi.mocked(japaneseApi.annotateJapanese).mockResolvedValueOnce({
      source: 'local',
      segments: [
        { text: '私', reading: 'わたし' },
        { text: 'はここで' },
        { text: '話', reading: 'はな' },
        { text: 'します' },
      ],
    })
    const user = userEvent.setup()
    renderEditor()

    expect(await screen.findByDisplayValue('Auto Sync Note')).toBeVisible()
    await user.click(screen.getByRole('checkbox', { name: '实时注音' }))

    const editor = tiptapMock.captureEditor.mock.lastCall?.[0] as {
      view: { composing: boolean }
      state: { selection: unknown }
    }
    editor.view.composing = true
    editor.state.selection = {
      from: 10,
      to: 10,
      empty: true,
      $from: {
        parentOffset: 9,
        parent: {
          type: { name: 'paragraph' },
          forEach: (
            callback: (
              node: { isText: boolean; text: string; nodeSize: number },
              offset: number
            ) => void
          ) =>
            callback(
              { isText: true, text: '私はここで話します', nodeSize: 9 },
              0
            ),
        },
        start: () => 1,
      },
    }
    tiptapMock.textBetween.mockReturnValue('私はここで話します')

    const options = tiptapMock.captureEditorOptions.mock.lastCall?.[0] as {
      onUpdate: (payload: { editor: unknown }) => void
    }
    options.onUpdate({ editor })

    expect(await screen.findByText('等待停顿')).toBeVisible()
    editor.view.composing = false

    await waitFor(
      () =>
        expect(japaneseApi.annotateJapanese).toHaveBeenCalledWith(
          '私はここで話します',
          expect.objectContaining({ mode: 'local' })
        ),
      { timeout: 1800 }
    )
    expect(tiptapMock.insertContentAt).toHaveBeenCalledWith(
      { from: 1, to: 10 },
      [
        { type: 'ruby', attrs: { base: '私', reading: 'わたし' } },
        { type: 'text', text: 'はここで' },
        { type: 'ruby', attrs: { base: '話', reading: 'はな' } },
        { type: 'text', text: 'します' },
      ]
    )
    expect(await screen.findByText('已注音')).toBeVisible()
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

  it('explains that a 404 attachment response needs a backend restart and can retry', async () => {
    vi.mocked(notesApi.getNoteAttachments).mockRejectedValueOnce(
      new APIError(404, 'UNKNOWN', 'Request failed')
    )
    const user = userEvent.setup()
    renderEditor()

    expect(await screen.findByText('后端尚未加载附件接口')).toBeVisible()
    expect(screen.getByText('请重启后端服务，然后点击重试。')).toBeVisible()

    await user.click(screen.getByRole('button', { name: '重试' }))

    await waitFor(() =>
      expect(notesApi.getNoteAttachments).toHaveBeenCalledTimes(2)
    )
    expect(await screen.findByText('还没有附件')).toBeVisible()
  })

  it('shows existing voice audio and uploads a new media attachment', async () => {
    vi.mocked(notesApi.getNoteAttachments).mockResolvedValueOnce([
      {
        id: 'voice-client-1',
        note_id: 'note-1',
        kind: 'audio',
        original_name: '散步录音.m4a',
        mime_type: 'audio/mp4',
        size_bytes: 2048,
        sha256: 'voice-sha',
        source: 'voice_note',
        deletable: false,
        created_at: 3,
        content_url: '/api/notes/note-1/attachments/voice-client-1/content',
      },
    ])
    const user = userEvent.setup()
    renderEditor()

    expect(await screen.findByText('散步录音.m4a')).toBeVisible()
    expect(screen.getByText('语音笔记')).toBeVisible()
    expect(screen.getByLabelText('播放 散步录音.m4a')).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: '删除' })
    ).not.toBeInTheDocument()

    const video = new File(['video'], 'demo.mp4', { type: 'video/mp4' })
    await user.upload(screen.getByLabelText('选择附件文件'), video)

    await waitFor(() =>
      expect(notesApi.uploadNoteAttachment).toHaveBeenCalledWith(
        'note-1',
        video
      )
    )
  })

  it('transcribes a voice attachment and applies the result to the editor', async () => {
    vi.mocked(notesApi.getNoteAttachments).mockResolvedValue([
      {
        id: 'voice-client-1',
        note_id: 'note-1',
        kind: 'audio',
        original_name: '散步录音.m4a',
        mime_type: 'audio/mp4',
        size_bytes: 2048,
        sha256: 'voice-sha',
        source: 'voice_note',
        deletable: false,
        created_at: 3,
        content_url: '/api/notes/note-1/attachments/voice-client-1/content',
        transcription_state: 'not_started',
      },
    ])
    const user = userEvent.setup()
    renderEditor()

    await user.click(await screen.findByRole('button', { name: '转成文字' }))

    await waitFor(() =>
      expect(notesApi.transcribeVoiceNote).toHaveBeenCalledWith(
        'voice-client-1'
      )
    )
    expect(
      await screen.findByText('转写完成，识别结果已写入正文。')
    ).toBeVisible()
    expect(tiptapMock.setContent).toHaveBeenCalledWith(
      '这是服务端识别出的文字。'
    )
  })
})
