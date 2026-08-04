import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type MouseEvent,
  type ReactNode,
} from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useEditor, EditorContent, type Editor } from '@tiptap/react'
import { BubbleMenu } from '@tiptap/react/menus'
import StarterKit from '@tiptap/starter-kit'
import Placeholder from '@tiptap/extension-placeholder'
import { Markdown } from 'tiptap-markdown'
import { NoteSyncCard } from '../components/sync/NoteSyncCard'
import { NoteAttachmentsSection } from '../components/NoteAttachments'
import { useNote, useUpdateNote } from '../hooks/useNotes'
import {
  useProjects,
  useTaskDefinitions,
  useUpdateTaskDefinitionMutation,
} from '../hooks/useTaskDomain'
import {
  useNoteSyncBinding,
  useSyncNote,
  useSyncTargets,
} from '../hooks/useSync'
import type { ProjectV2 } from '../api/taskDomain'
import { Ruby } from '../extensions/Ruby'
import { annotateJapanese, type FuriganaSegment } from '../api/japanese'

type RubyDialogState = {
  open: boolean
  base: string
  reading: string
  from: number
  to: number
  existing: boolean
  message: string
}

const EMPTY_RUBY_DIALOG: RubyDialogState = {
  open: false,
  base: '',
  reading: '',
  from: 0,
  to: 0,
  existing: false,
  message: '',
}

const LIVE_FURIGANA_DEBOUNCE_MS = 450
const LIVE_FURIGANA_COMPOSITION_RECHECK_MS = 80
const JAPANESE_RUN_PATTERN = /[一-龯々〆ヵヶぁ-ゖァ-ヺー]+/gu
const KANJI_PATTERN = /[一-龯々〆ヵヶ]/u

type LiveFuriganaStatus =
  | 'idle'
  | 'ready'
  | 'waiting'
  | 'annotating'
  | 'done'
  | 'error'

type LiveFuriganaTarget = {
  from: number
  to: number
  text: string
}

type EditorInlineContent = {
  type: 'ruby' | 'text' | 'hardBreak'
  text?: string
  attrs?: { base: string; reading: string }
}

function furiganaSegmentsToContent(
  segments: FuriganaSegment[]
): EditorInlineContent[] {
  const content: EditorInlineContent[] = []
  for (const segment of segments) {
    const lines = segment.text.split('\n')
    lines.forEach((line, index) => {
      if (index > 0) content.push({ type: 'hardBreak' })
      if (!line) return
      if (segment.reading && lines.length === 1) {
        content.push({
          type: 'ruby',
          attrs: { base: line, reading: segment.reading },
        })
      } else {
        content.push({ type: 'text', text: line })
      }
    })
  }
  return content
}

function getLiveFuriganaTarget(editor: Editor): LiveFuriganaTarget | null {
  if (editor.isDestroyed || editor.view.composing) return null

  const { selection } = editor.state
  if (!selection.empty) return null

  const cursor = selection.$from
  if (cursor.parent.type.name === 'codeBlock') return null

  let textBeforeCursor: { text: string; offset: number } | null = null
  cursor.parent.forEach((node, offset) => {
    if (!node.isText || typeof node.text !== 'string') return
    if (
      offset > cursor.parentOffset ||
      offset + node.nodeSize < cursor.parentOffset
    )
      return

    const prefix = node.text.slice(0, cursor.parentOffset - offset)
    if (prefix) textBeforeCursor = { text: prefix, offset }
  })
  const candidate = textBeforeCursor as {
    text: string
    offset: number
  } | null
  if (!candidate) return null

  let latestMatch: RegExpMatchArray | null = null
  for (const match of candidate.text.matchAll(JAPANESE_RUN_PATTERN)) {
    if (KANJI_PATTERN.test(match[0])) latestMatch = match
  }
  if (!latestMatch) return null

  const relativeFrom = candidate.offset + (latestMatch.index ?? 0)
  const from = cursor.start() + relativeFrom
  return {
    from,
    to: from + latestMatch[0].length,
    text: latestMatch[0],
  }
}

function liveFuriganaStatusText(status: LiveFuriganaStatus) {
  switch (status) {
    case 'waiting':
      return '等待停顿'
    case 'annotating':
      return '注音中'
    case 'done':
      return '已注音'
    case 'error':
      return '暂不可用'
    default:
      return '已开启'
  }
}

function isCanceledRequest(error: unknown) {
  return (
    (error instanceof DOMException && error.name === 'AbortError') ||
    (typeof error === 'object' &&
      error !== null &&
      'code' in error &&
      error.code === 'ERR_CANCELED')
  )
}

function getMarkdown(editor: Editor | null): string {
  if (!editor || editor.isDestroyed) return ''
  const storage = editor.storage as unknown as Record<
    string,
    { getMarkdown: () => string } | undefined
  >
  return storage.markdown?.getMarkdown() ?? ''
}

function countWords(markdown: string): number {
  const text = markdown.replace(/[#*`~>\n[\]()!|-]/g, ' ').trim()
  if (!text) return 0
  const cjk = (text.match(/[\u4e00-\u9fff]/g) || []).length
  const latin = text
    .replace(/[\u4e00-\u9fff]/g, ' ')
    .split(/\s+/)
    .filter(Boolean).length
  return cjk + latin
}

function formatProjectOption(project: ProjectV2): string {
  const kind = project.kind === 'learning' ? '学习项目' : '标准项目'
  const horizon = project.horizon === 'short' ? '短期' : '长期'
  return `${project.name} · ${kind} · ${horizon}`
}

export default function EditorPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { data: note, isLoading, error } = useNote(id!)
  const updateNote = useUpdateNote()
  const projectsQuery = useProjects()
  const tasksQuery = useTaskDefinitions()
  const updateTask = useUpdateTaskDefinitionMutation()
  const syncTargetsQ = useSyncTargets()
  const syncBindingQ = useNoteSyncBinding(id)
  const { mutate: syncCurrentNote, isPending: isAutoSyncPending } =
    useSyncNote(id)
  const boundSyncTargetID = syncBindingQ.data?.binding?.target_id
  const boundSyncTarget =
    syncBindingQ.data?.target ??
    syncTargetsQ.data?.find((target) => target.id === boundSyncTargetID)
  const autoSyncEnabled = Boolean(
    boundSyncTarget?.enabled && boundSyncTarget.auto_sync
  )

  const [title, setTitle] = useState('')
  const [isFullscreen, setIsFullscreen] = useState(false)
  const [inspectorOpen, setInspectorOpen] = useState(true)
  const [rubyDialog, setRubyDialog] =
    useState<RubyDialogState>(EMPTY_RUBY_DIALOG)
  const [isAutoAnnotating, setIsAutoAnnotating] = useState(false)
  const [liveFuriganaEnabled, setLiveFuriganaEnabled] = useState(false)
  const [liveFuriganaStatus, setLiveFuriganaStatus] =
    useState<LiveFuriganaStatus>('idle')
  const [rubyNotice, setRubyNotice] = useState('')
  const liveFuriganaEnabledRef = useRef(false)
  const liveFuriganaTimerRef = useRef<number | null>(null)
  const liveFuriganaRequestRef = useRef<AbortController | null>(null)
  const liveFuriganaGenerationRef = useRef(0)
  const isApplyingLiveFuriganaRef = useRef(false)
  const [projectFilterID, setProjectFilterID] = useState('')
  const [associationLoadedID, setAssociationLoadedID] = useState<string | null>(
    null
  )
  const [associationError, setAssociationError] = useState('')
  const allProjects = projectsQuery.data ?? []
  const allTasks = tasksQuery.data ?? []
  const linkedTasks = id
    ? allTasks.filter((task) => task.task_note_id === id)
    : []
  const linkedProjectIDs = [
    ...new Set(linkedTasks.map((task) => task.project_id)),
  ]
  const availableTasks = allTasks.filter(
    (task) =>
      task.project_id === projectFilterID &&
      !task.task_note_id &&
      task.lifecycle_status !== 'archived' &&
      task.lifecycle_status !== 'cancelled'
  )

  const editor = useEditor({
    immediatelyRender: true,
    extensions: [
      StarterKit.configure({ heading: { levels: [1, 2, 3] } }),
      Placeholder.configure({ placeholder: '开始书写...' }),
      Ruby,
      Markdown.configure({ html: false, breaks: true, linkify: true }),
    ],
    editorProps: {
      attributes: {
        class: 'max-w-none outline-none min-h-[420px]',
        spellcheck: 'false',
      },
      handleClickOn: (_view, _pos, node, nodePos, _event, direct) => {
        if (!direct || node.type.name !== 'ruby') return false
        setRubyDialog({
          open: true,
          base: String(node.attrs.base ?? ''),
          reading: String(node.attrs.reading ?? ''),
          from: nodePos,
          to: nodePos + node.nodeSize,
          existing: true,
          message: '',
        })
        return true
      },
    },
    onUpdate: ({ editor: activeEditor }) => {
      if (!activeEditor.isDestroyed) {
        ;(activeEditor as Editor & { _dirty?: boolean })._dirty = true
      }
      if (
        liveFuriganaEnabledRef.current &&
        !isApplyingLiveFuriganaRef.current
      ) {
        scheduleLiveFurigana(activeEditor)
      }
    },
  })

  // Initialize editor from note only when navigating to a different note
  const [lastLoadedID, setLastLoadedID] = useState<string | null>(null)
  useEffect(() => {
    if (!editor || editor.isDestroyed) return
    if (note && note.id === id && note.id !== lastLoadedID) {
      setTitle(note.title)
      editor.commands.setContent(note.body || '')
      setLastLoadedID(note.id)
    } else if (!note || note.id !== id) {
      setTitle('')
      editor.commands.setContent('')
      setLastLoadedID(null)
    }
  }, [id, note, editor, lastLoadedID])

  useEffect(() => {
    if (
      !id ||
      associationLoadedID === id ||
      projectsQuery.isLoading ||
      tasksQuery.isLoading
    ) {
      return
    }
    const linkedProjectID = linkedTasks[0]?.project_id
    setProjectFilterID(linkedProjectID ?? allProjects[0]?.id ?? '')
    setAssociationLoadedID(id)
  }, [
    allProjects,
    associationLoadedID,
    id,
    linkedTasks,
    projectsQuery.isLoading,
    tasksQuery.isLoading,
  ])

  const syncAfterSave = useCallback(() => {
    if (autoSyncEnabled && !isAutoSyncPending) {
      syncCurrentNote()
    }
  }, [autoSyncEnabled, isAutoSyncPending, syncCurrentNote])

  const save = useCallback(() => {
    if (!id || !title.trim() || !editor || editor.isDestroyed) return
    updateNote.mutate(
      {
        id,
        title: title.trim(),
        body: getMarkdown(editor),
      },
      { onSuccess: syncAfterSave }
    )
  }, [id, title, editor, updateNote, syncAfterSave])

  useEffect(() => {
    if (!editor || !id) return
    const timer = setInterval(() => {
      if (updateNote.isPending || editor.isDestroyed) return
      const markdown = getMarkdown(editor)
      if (!note) return
      if (
        title.trim() &&
        (markdown !== note.body || title.trim() !== note.title)
      ) {
        updateNote.mutate(
          {
            id,
            title: title.trim(),
            body: markdown,
          },
          { onSuccess: syncAfterSave }
        )
      }
    }, 5000)
    return () => clearInterval(timer)
  }, [editor, title, id, note, updateNote, syncAfterSave])

  async function setTaskNote(taskID: string, noteID: string) {
    const task = allTasks.find((candidate) => candidate.id === taskID)
    if (!task) return
    await updateTask.mutateAsync({
      projectID: task.project_id,
      taskID: task.id,
      input: {
        task_note_id: noteID,
        expected_task_revision: task.revision,
        expected_schedule_revision: task.schedule_revision,
      },
    })
  }

  async function linkTask(taskID: string) {
    if (!id || !taskID) return
    setAssociationError('')
    try {
      await setTaskNote(taskID, id)
    } catch {
      setAssociationError('关联任务失败，请刷新后重试。')
    }
  }

  async function unlinkTask(taskID: string) {
    setAssociationError('')
    try {
      await setTaskNote(taskID, '')
    } catch {
      setAssociationError('取消关联失败，请刷新后重试。')
    }
  }

  const markdown = editor ? getMarkdown(editor) : ''

  useEffect(() => {
    if (!isFullscreen && !rubyDialog.open) return
    function handleFullscreenKeyDown(event: KeyboardEvent) {
      if (event.key !== 'Escape') return
      if (rubyDialog.open) {
        setRubyDialog(EMPTY_RUBY_DIALOG)
        return
      }
      setIsFullscreen(false)
    }
    window.addEventListener('keydown', handleFullscreenKeyDown)
    return () => window.removeEventListener('keydown', handleFullscreenKeyDown)
  }, [isFullscreen, rubyDialog.open])

  useEffect(() => {
    if (!rubyNotice) return
    const timer = window.setTimeout(() => setRubyNotice(''), 3500)
    return () => window.clearTimeout(timer)
  }, [rubyNotice])

  useEffect(
    () => () => {
      if (liveFuriganaTimerRef.current !== null) {
        window.clearTimeout(liveFuriganaTimerRef.current)
      }
      liveFuriganaRequestRef.current?.abort()
    },
    []
  )

  if (isLoading) {
    return (
      <div className="editor-skeleton">
        <div className="editor-skeleton-title" />
        <div className="editor-skeleton-body" />
      </div>
    )
  }

  if (error || !note) {
    return (
      <div className="editor-error">
        <div className="editor-error-icon">!</div>
        <p className="editor-error-text">笔记未找到</p>
        <button
          onClick={() => navigate('/notes')}
          className="editor-error-back"
        >
          返回笔记列表
        </button>
      </div>
    )
  }

  const run = (event: MouseEvent, fn: () => void) => {
    event.preventDefault()
    fn()
  }

  function scheduleLiveFurigana(activeEditor: Editor) {
    if (liveFuriganaTimerRef.current !== null) {
      window.clearTimeout(liveFuriganaTimerRef.current)
      liveFuriganaTimerRef.current = null
    }
    liveFuriganaRequestRef.current?.abort()
    liveFuriganaRequestRef.current = null

    if (activeEditor.view.composing) {
      setLiveFuriganaStatus('waiting')
      liveFuriganaTimerRef.current = window.setTimeout(() => {
        liveFuriganaTimerRef.current = null
        if (liveFuriganaEnabledRef.current) {
          scheduleLiveFurigana(activeEditor)
        }
      }, LIVE_FURIGANA_COMPOSITION_RECHECK_MS)
      return
    }

    const generation = ++liveFuriganaGenerationRef.current
    const target = getLiveFuriganaTarget(activeEditor)
    if (!target) {
      setLiveFuriganaStatus('ready')
      return
    }

    setLiveFuriganaStatus('waiting')
    liveFuriganaTimerRef.current = window.setTimeout(() => {
      liveFuriganaTimerRef.current = null
      void annotateLiveFurigana(activeEditor, target, generation)
    }, LIVE_FURIGANA_DEBOUNCE_MS)
  }

  async function annotateLiveFurigana(
    activeEditor: Editor,
    target: LiveFuriganaTarget,
    generation: number
  ) {
    if (!liveFuriganaEnabledRef.current || activeEditor.isDestroyed) return

    const controller = new AbortController()
    liveFuriganaRequestRef.current = controller
    setLiveFuriganaStatus('annotating')

    try {
      const result = await annotateJapanese(target.text, {
        signal: controller.signal,
        mode: 'local',
      })
      if (
        controller.signal.aborted ||
        generation !== liveFuriganaGenerationRef.current ||
        !liveFuriganaEnabledRef.current ||
        activeEditor.isDestroyed
      ) {
        return
      }
      if (
        activeEditor.state.doc.textBetween(target.from, target.to, '') !==
        target.text
      ) {
        setLiveFuriganaStatus('ready')
        return
      }

      const hasReading = result.segments.some((segment) =>
        Boolean(segment.reading)
      )
      if (!hasReading) {
        setLiveFuriganaStatus('ready')
        return
      }

      isApplyingLiveFuriganaRef.current = true
      activeEditor.commands.insertContentAt(
        { from: target.from, to: target.to },
        furiganaSegmentsToContent(result.segments)
      )
      activeEditor.commands.focus()
      setLiveFuriganaStatus('done')
    } catch (error) {
      if (isCanceledRequest(error)) return
      setLiveFuriganaStatus('error')
      setRubyNotice('实时注音暂时不可用，原文已保留')
    } finally {
      isApplyingLiveFuriganaRef.current = false
      if (liveFuriganaRequestRef.current === controller) {
        liveFuriganaRequestRef.current = null
      }
    }
  }

  function toggleLiveFurigana(enabled: boolean) {
    setLiveFuriganaEnabled(enabled)
    liveFuriganaEnabledRef.current = enabled
    liveFuriganaGenerationRef.current += 1

    if (!enabled) {
      if (liveFuriganaTimerRef.current !== null) {
        window.clearTimeout(liveFuriganaTimerRef.current)
        liveFuriganaTimerRef.current = null
      }
      liveFuriganaRequestRef.current?.abort()
      liveFuriganaRequestRef.current = null
      setLiveFuriganaStatus('idle')
      return
    }

    setLiveFuriganaStatus('ready')
    if (editor && !editor.isDestroyed) scheduleLiveFurigana(editor)
  }

  async function openRubyDialog() {
    if (!editor || editor.isDestroyed) return
    const { from, to, empty } = editor.state.selection
    const base = empty ? '' : editor.state.doc.textBetween(from, to, '')
    if (empty) {
      setRubyDialog({
        open: true,
        base: '',
        reading: '',
        from,
        to,
        existing: false,
        message: '',
      })
      return
    }

    setIsAutoAnnotating(true)
    setRubyNotice('')
    try {
      const result = await annotateJapanese(base)
      if (editor.isDestroyed) return
      if (editor.state.doc.textBetween(from, to, '') !== base) {
        setRubyDialog({
          open: true,
          base,
          reading: '',
          from,
          to,
          existing: false,
          message: '选中的文本已经变化，请重新选择后再试。',
        })
        return
      }

      const hasReading = result.segments.some((segment) =>
        Boolean(segment.reading)
      )
      if (!hasReading) {
        setRubyDialog({
          open: true,
          base,
          reading: '',
          from,
          to,
          existing: false,
          message: '未识别到需要注音的汉字，可以手动填写假名。',
        })
        return
      }

      editor.commands.insertContentAt(
        { from, to },
        furiganaSegmentsToContent(result.segments)
      )
      editor.commands.focus()
      setRubyNotice(
        result.source === 'ai' ? 'AI 注音完成' : 'AI 不可用，已使用本地注音'
      )
    } catch {
      setRubyDialog({
        open: true,
        base,
        reading: '',
        from,
        to,
        existing: false,
        message: '自动注音失败，请手动填写假名。',
      })
    } finally {
      setIsAutoAnnotating(false)
    }
  }

  function applyRubyAnnotation() {
    if (!editor || editor.isDestroyed) return
    const base = rubyDialog.base.trim()
    const reading = rubyDialog.reading.trim()
    if (!base || !reading) return

    editor.commands.insertContentAt(
      { from: rubyDialog.from, to: rubyDialog.to },
      { type: 'ruby', attrs: { base, reading } }
    )
    editor.commands.focus()
    setRubyDialog(EMPTY_RUBY_DIALOG)
  }

  function removeRubyAnnotation() {
    if (!editor || editor.isDestroyed || !rubyDialog.existing) return
    editor.commands.insertContentAt(
      { from: rubyDialog.from, to: rubyDialog.to },
      rubyDialog.base
    )
    editor.commands.focus()
    setRubyDialog(EMPTY_RUBY_DIALOG)
  }

  return (
    <div
      className={`editor-workspace ${isFullscreen ? 'is-fullscreen' : ''} ${inspectorOpen ? '' : 'is-inspector-hidden'}`}
    >
      <section className="editor-page">
        <div className="editor-meta">
          <button
            onClick={() => navigate('/notes')}
            className="editor-back-btn"
          >
            <ArrowLeft /> 返回笔记
          </button>
          <div className="editor-meta-actions">
            <div className="editor-meta-info" aria-live="polite">
              <span>
                {new Date(note.updated_at * 1000).toLocaleDateString('zh-CN', {
                  month: 'short',
                  day: 'numeric',
                  hour: '2-digit',
                  minute: '2-digit',
                })}
              </span>
              <span>{updateNote.isPending ? '保存中' : '已保存'}</span>
              {updateNote.isPending && (
                <span className="editor-save-dot" title="保存中" />
              )}
              {isAutoSyncPending && <span>同步中</span>}
            </div>
            {!isFullscreen && (
              <button
                type="button"
                className={`editor-view-btn ${inspectorOpen ? 'is-active' : ''}`}
                onClick={() => setInspectorOpen((open) => !open)}
                aria-label={inspectorOpen ? '隐藏笔记信息' : '显示笔记信息'}
                title={inspectorOpen ? '隐藏笔记信息' : '显示笔记信息'}
              >
                <PanelIcon />
              </button>
            )}
            <button
              type="button"
              className={`editor-view-btn ${isFullscreen ? 'is-active' : ''}`}
              onClick={() => setIsFullscreen((fullscreen) => !fullscreen)}
              aria-label={isFullscreen ? '退出全屏写作' : '进入全屏写作'}
              title={isFullscreen ? '退出全屏写作（Esc）' : '进入全屏写作'}
            >
              {isFullscreen ? <MinimizeIcon /> : <FullscreenIcon />}
            </button>
          </div>
        </div>

        <input
          value={title}
          onChange={(event) => setTitle(event.target.value)}
          onBlur={save}
          placeholder="无标题"
          className="editor-title-input"
        />

        <div className="editor-paper">
          {editor && (
            <div className="editor-toolbar">
              <div className="editor-toolbar-group">
                <ToolbarBtn
                  active={editor.isActive('bold')}
                  onClick={(event) =>
                    run(event, () => editor.chain().focus().toggleBold().run())
                  }
                  title="粗体"
                  mono
                >
                  B
                </ToolbarBtn>
                <ToolbarBtn
                  active={editor.isActive('italic')}
                  onClick={(event) =>
                    run(event, () =>
                      editor.chain().focus().toggleItalic().run()
                    )
                  }
                  title="斜体"
                  mono
                >
                  I
                </ToolbarBtn>
                <ToolbarBtn
                  active={editor.isActive('strike')}
                  onClick={(event) =>
                    run(event, () =>
                      editor.chain().focus().toggleStrike().run()
                    )
                  }
                  title="删除线"
                  mono
                >
                  S
                </ToolbarBtn>
              </div>

              <div className="editor-toolbar-divider" />

              <div className="editor-toolbar-group">
                <ToolbarBtn
                  active={editor.isActive('heading', { level: 1 })}
                  onClick={(event) =>
                    run(event, () =>
                      editor.chain().focus().toggleHeading({ level: 1 }).run()
                    )
                  }
                  title="一级标题"
                  label="H1"
                />
                <ToolbarBtn
                  active={editor.isActive('heading', { level: 2 })}
                  onClick={(event) =>
                    run(event, () =>
                      editor.chain().focus().toggleHeading({ level: 2 }).run()
                    )
                  }
                  title="二级标题"
                  label="H2"
                />
                <ToolbarBtn
                  active={editor.isActive('heading', { level: 3 })}
                  onClick={(event) =>
                    run(event, () =>
                      editor.chain().focus().toggleHeading({ level: 3 }).run()
                    )
                  }
                  title="三级标题"
                  label="H3"
                />
              </div>

              <div className="editor-toolbar-divider" />

              <div className="editor-toolbar-group">
                <ToolbarBtn
                  active={
                    rubyDialog.open ||
                    isAutoAnnotating ||
                    liveFuriganaStatus === 'annotating'
                  }
                  disabled={
                    isAutoAnnotating || liveFuriganaStatus === 'annotating'
                  }
                  onClick={(event) => run(event, openRubyDialog)}
                  title={
                    isAutoAnnotating || liveFuriganaStatus === 'annotating'
                      ? '正在自动注音'
                      : '假名标注'
                  }
                  label={isAutoAnnotating ? '…' : 'あ'}
                />
                <label
                  className={`ruby-live-toggle ${
                    liveFuriganaEnabled ? 'is-enabled' : ''
                  }`}
                  title="开启后，输入日文并停顿片刻即可自动添加假名"
                >
                  <input
                    type="checkbox"
                    checked={liveFuriganaEnabled}
                    onChange={(event) =>
                      toggleLiveFurigana(event.target.checked)
                    }
                  />
                  <span className="ruby-live-toggle-label">实时注音</span>
                  {liveFuriganaEnabled && (
                    <span
                      className="ruby-live-toggle-status"
                      data-status={liveFuriganaStatus}
                      aria-live="polite"
                    >
                      {liveFuriganaStatusText(liveFuriganaStatus)}
                    </span>
                  )}
                </label>
              </div>

              <div className="editor-toolbar-divider" />

              <div className="editor-toolbar-group">
                <ToolbarBtn
                  active={editor.isActive('bulletList')}
                  onClick={(event) =>
                    run(event, () =>
                      editor.chain().focus().toggleBulletList().run()
                    )
                  }
                  title="无序列表"
                >
                  •
                </ToolbarBtn>
                <ToolbarBtn
                  active={editor.isActive('orderedList')}
                  onClick={(event) =>
                    run(event, () =>
                      editor.chain().focus().toggleOrderedList().run()
                    )
                  }
                  title="有序列表"
                  mono
                >
                  1.
                </ToolbarBtn>
                <ToolbarBtn
                  active={editor.isActive('blockquote')}
                  onClick={(event) =>
                    run(event, () =>
                      editor.chain().focus().toggleBlockquote().run()
                    )
                  }
                  title="引用"
                >
                  "
                </ToolbarBtn>
                <ToolbarBtn
                  active={editor.isActive('codeBlock')}
                  onClick={(event) =>
                    run(event, () =>
                      editor.chain().focus().toggleCodeBlock().run()
                    )
                  }
                  title="代码块"
                  mono
                >
                  &lt;/&gt;
                </ToolbarBtn>
              </div>

              <div className="editor-toolbar-divider" />

              <div className="editor-toolbar-group">
                <ToolbarBtn
                  active={false}
                  onClick={(event) =>
                    run(event, () =>
                      editor.chain().focus().setHorizontalRule().run()
                    )
                  }
                  title="分割线"
                >
                  -
                </ToolbarBtn>
              </div>
            </div>
          )}

          {editor && rubyDialog.open && (
            <form
              className="ruby-popover"
              role="dialog"
              aria-label="假名标注"
              onSubmit={(event) => {
                event.preventDefault()
                applyRubyAnnotation()
              }}
            >
              <div className="ruby-popover-heading">
                <div>
                  <strong>假名标注</strong>
                  <span>假名会显示在汉字上方</span>
                </div>
                <button
                  type="button"
                  onClick={() => setRubyDialog(EMPTY_RUBY_DIALOG)}
                  aria-label="关闭假名标注"
                >
                  <CloseIcon />
                </button>
              </div>

              <div className="ruby-preview" aria-label="标注预览">
                {rubyDialog.base ? (
                  <ruby>
                    {rubyDialog.base}
                    <rt>{rubyDialog.reading || 'かな'}</rt>
                  </ruby>
                ) : (
                  <span>预览</span>
                )}
              </div>

              {rubyDialog.message && (
                <p className="ruby-popover-message" role="status">
                  {rubyDialog.message}
                </p>
              )}

              <label>
                <span>汉字或词语</span>
                <input
                  value={rubyDialog.base}
                  onChange={(event) =>
                    setRubyDialog((current) => ({
                      ...current,
                      base: event.target.value,
                    }))
                  }
                  placeholder="例如：附近"
                  autoFocus={!rubyDialog.base}
                />
              </label>
              <label>
                <span>假名</span>
                <input
                  value={rubyDialog.reading}
                  onChange={(event) =>
                    setRubyDialog((current) => ({
                      ...current,
                      reading: event.target.value,
                    }))
                  }
                  placeholder="例如：ふきん"
                  autoFocus={Boolean(rubyDialog.base)}
                />
              </label>

              <div className="ruby-popover-actions">
                {rubyDialog.existing && (
                  <button
                    type="button"
                    className="is-danger"
                    onClick={removeRubyAnnotation}
                  >
                    移除标注
                  </button>
                )}
                <button
                  type="button"
                  onClick={() => setRubyDialog(EMPTY_RUBY_DIALOG)}
                >
                  取消
                </button>
                <button
                  type="submit"
                  className="is-primary"
                  disabled={
                    !rubyDialog.base.trim() || !rubyDialog.reading.trim()
                  }
                >
                  {rubyDialog.existing ? '保存标注' : '添加标注'}
                </button>
              </div>
            </form>
          )}

          {rubyNotice && (
            <div className="ruby-annotation-notice" role="status">
              {rubyNotice}
            </div>
          )}

          <EditorContent editor={editor} />

          {id ? (
            <NoteAttachmentsSection
              noteID={id}
              onTranscribed={(body) => {
                if (!editor || editor.isDestroyed) return
                editor.commands.setContent(body)
                ;(editor as Editor & { _dirty?: boolean })._dirty = false
              }}
            />
          ) : null}

          {editor && (
            <BubbleMenu editor={editor} className="bubble-menu">
              <button
                type="button"
                onClick={() => editor.chain().focus().toggleBold().run()}
                className={`bubble-menu-btn ${editor.isActive('bold') ? 'is-active' : ''}`}
              >
                <strong>B</strong>
              </button>
              <button
                type="button"
                onClick={() => editor.chain().focus().toggleItalic().run()}
                className={`bubble-menu-btn ${editor.isActive('italic') ? 'is-active' : ''}`}
              >
                <em>I</em>
              </button>
              <button
                type="button"
                onClick={() => editor.chain().focus().toggleStrike().run()}
                className={`bubble-menu-btn ${editor.isActive('strike') ? 'is-active' : ''}`}
              >
                <s>S</s>
              </button>

              <span className="bubble-menu-divider" />

              <button
                type="button"
                onClick={() =>
                  editor.chain().focus().toggleHeading({ level: 1 }).run()
                }
                className={`bubble-menu-btn ${editor.isActive('heading', { level: 1 }) ? 'is-active' : ''}`}
              >
                H1
              </button>
              <button
                type="button"
                onClick={() =>
                  editor.chain().focus().toggleHeading({ level: 2 }).run()
                }
                className={`bubble-menu-btn ${editor.isActive('heading', { level: 2 }) ? 'is-active' : ''}`}
              >
                H2
              </button>
              <button
                type="button"
                onClick={() =>
                  editor.chain().focus().toggleHeading({ level: 3 }).run()
                }
                className={`bubble-menu-btn ${editor.isActive('heading', { level: 3 }) ? 'is-active' : ''}`}
              >
                H3
              </button>

              <span className="bubble-menu-divider" />

              <button
                type="button"
                onClick={() => editor.chain().focus().toggleBulletList().run()}
                className={`bubble-menu-btn ${editor.isActive('bulletList') ? 'is-active' : ''}`}
              >
                •
              </button>
              <button
                type="button"
                onClick={() => editor.chain().focus().toggleOrderedList().run()}
                className={`bubble-menu-btn ${editor.isActive('orderedList') ? 'is-active' : ''}`}
              >
                1.
              </button>
              <button
                type="button"
                onClick={() => editor.chain().focus().toggleBlockquote().run()}
                className={`bubble-menu-btn ${editor.isActive('blockquote') ? 'is-active' : ''}`}
              >
                "
              </button>
              <button
                type="button"
                onClick={() => editor.chain().focus().toggleCodeBlock().run()}
                className={`bubble-menu-btn ${editor.isActive('codeBlock') ? 'is-active' : ''}`}
              >
                &lt;/&gt;
              </button>

              <span className="bubble-menu-divider" />

              <button
                type="button"
                onClick={() => editor.chain().focus().setHorizontalRule().run()}
                className="bubble-menu-btn"
              >
                -
              </button>
            </BubbleMenu>
          )}
        </div>

        <div className="editor-footer">
          <span className="editor-footer-hint">
            {countWords(markdown)} 字 · 选中文本显示格式菜单 · 支持 Markdown
          </span>
          <button onClick={save} className="editor-save-btn">
            保存
          </button>
        </div>
      </section>

      {inspectorOpen && !isFullscreen && (
        <aside className="editor-inspector">
          <div className="panel-heading is-compact">
            <div>
              <span>正文</span>
              <h2>笔记信息</h2>
            </div>
          </div>
          {id && <NoteSyncCard noteID={id} />}
          <div className="inspector-section">
            <h4 className="inspector-label">关联项目</h4>
            <div className="chip-list">
              {linkedProjectIDs.map((pid) => {
                const project = allProjects.find((p) => p.id === pid)
                if (!project) return null
                return (
                  <span key={pid} className="sync-tag-chip">
                    {project.name}
                  </span>
                )
              })}
            </div>
            <select
              className="project-select"
              aria-label="选择关联项目"
              value={projectFilterID}
              onChange={(event) => setProjectFilterID(event.target.value)}
              disabled={projectsQuery.isLoading || allProjects.length === 0}
            >
              {allProjects.length === 0 ? (
                <option value="">暂无项目</option>
              ) : null}
              {allProjects.map((project) => (
                <option key={project.id} value={project.id}>
                  {formatProjectOption(project)}
                </option>
              ))}
            </select>
            <p className="editor-association-hint">
              项目用于筛选任务，关联任务后会自动建立项目关系。
            </p>
          </div>
          <div className="inspector-section">
            <h4 className="inspector-label">关联任务</h4>
            <div className="chip-list">
              {linkedTasks.map((task) => (
                <button
                  key={task.id}
                  type="button"
                  className="sync-tag-chip"
                  disabled={updateTask.isPending}
                  onClick={() => void unlinkTask(task.id)}
                  title="取消关联"
                >
                  {task.title}
                  <span aria-hidden="true">&times;</span>
                </button>
              ))}
            </div>
            <select
              className="project-select"
              aria-label="选择关联任务"
              value=""
              disabled={
                !projectFilterID || tasksQuery.isLoading || updateTask.isPending
              }
              onChange={(event) => void linkTask(event.target.value)}
            >
              <option value="">
                {projectFilterID ? '+ 关联任务' : '请先选择项目'}
              </option>
              {availableTasks.map((task) => (
                <option key={task.id} value={task.id}>
                  {task.title}
                </option>
              ))}
            </select>
            {projectFilterID &&
            !tasksQuery.isLoading &&
            availableTasks.length === 0 ? (
              <p className="editor-association-hint">
                该项目没有可关联的任务。
              </p>
            ) : null}
            {associationError ? (
              <p className="editor-association-error" role="alert">
                {associationError}
              </p>
            ) : null}
          </div>
          <div className="inspector-section">
            <h4 className="inspector-label">最近版本</h4>
            <div className="linked-note">
              今天{' '}
              {new Date().toLocaleTimeString('zh-CN', {
                hour: '2-digit',
                minute: '2-digit',
              })}
            </div>
            <div className="linked-note">
              昨天{' '}
              {new Date(Date.now() - 86400000).toLocaleTimeString('zh-CN', {
                hour: '2-digit',
                minute: '2-digit',
              })}
            </div>
          </div>
        </aside>
      )}
    </div>
  )
}

function ToolbarBtn({
  active,
  onClick,
  title,
  children,
  mono,
  label,
  disabled,
}: {
  active: boolean
  onClick: (event: MouseEvent) => void
  title: string
  children?: ReactNode
  mono?: boolean
  label?: string
  disabled?: boolean
}) {
  return (
    <button
      type="button"
      onMouseDown={onClick}
      title={title}
      aria-label={title}
      disabled={disabled}
      data-label={label || undefined}
      className={`editor-toolbar-btn ${active ? 'is-active' : ''}`}
      style={
        mono && !label ? { fontFamily: 'var(--editor-font-mono)' } : undefined
      }
    >
      {children || label}
    </button>
  )
}

function ArrowLeft() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <path d="M19 12H5M12 19l-7-7 7-7" />
    </svg>
  )
}

function PanelIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="M15 4v16" />
    </svg>
  )
}

function FullscreenIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M8 3H3v5M16 3h5v5M8 21H3v-5M16 21h5v-5" />
    </svg>
  )
}

function MinimizeIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M8 8H3M8 8V3M16 8h5M16 8V3M8 16H3M8 16v5M16 16h5M16 16v5" />
    </svg>
  )
}

function CloseIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="m7 7 10 10M17 7 7 17" />
    </svg>
  )
}
