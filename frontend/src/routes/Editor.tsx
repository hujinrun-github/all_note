import {
  useCallback,
  useEffect,
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
import { useQuery } from '@tanstack/react-query'
import { NoteSyncCard } from '../components/sync/NoteSyncCard'
import { useNote, useUpdateNote } from '../hooks/useNotes'
import {
  useNoteSyncBinding,
  useSyncNote,
  useSyncTargets,
} from '../hooks/useSync'
import { listTaskProjects } from '../api/tasks'
import { formatTaskProjectOption } from '../utils/taskProjects'
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

export default function EditorPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { data: note, isLoading, error } = useNote(id!)
  const updateNote = useUpdateNote()
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
  const [rubyNotice, setRubyNotice] = useState('')

  // Fetch all projects for the selector
  const { data: allProjects = [] } = useQuery({
    queryKey: ['task-projects'],
    queryFn: listTaskProjects,
  })

  // Local state for selected project IDs
  const [selectedProjectIDs, setSelectedProjectIDs] = useState<string[]>([])

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
    },
  })

  // Initialize editor from note only when navigating to a different note
  const [lastLoadedID, setLastLoadedID] = useState<string | null>(null)
  useEffect(() => {
    if (!editor || editor.isDestroyed) return
    if (note && note.id === id && note.id !== lastLoadedID) {
      setTitle(note.title)
      editor.commands.setContent(note.body || '')
      setSelectedProjectIDs(note.projects?.map((p) => p.id) || [])
      setLastLoadedID(note.id)
    } else if (!note || note.id !== id) {
      setTitle('')
      editor.commands.setContent('')
      setLastLoadedID(null)
    }
  }, [id, note, editor, lastLoadedID])

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
        project_ids: selectedProjectIDs,
      },
      { onSuccess: syncAfterSave }
    )
  }, [id, title, editor, updateNote, syncAfterSave, selectedProjectIDs])

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
            project_ids: selectedProjectIDs,
          },
          { onSuccess: syncAfterSave }
        )
      }
    }, 5000)
    return () => clearInterval(timer)
  }, [editor, title, id, note, updateNote, syncAfterSave, selectedProjectIDs])

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
                  active={rubyDialog.open || isAutoAnnotating}
                  disabled={isAutoAnnotating}
                  onClick={(event) => run(event, openRubyDialog)}
                  title={isAutoAnnotating ? '正在自动注音' : '假名标注'}
                  label={isAutoAnnotating ? '…' : 'あ'}
                />
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
            <h4 className="inspector-label">所属项目</h4>
            <div className="chip-list">
              {selectedProjectIDs.map((pid) => {
                const project = allProjects.find((p) => p.id === pid)
                if (!project) return null
                return (
                  <button
                    key={pid}
                    type="button"
                    className="sync-tag-chip"
                    onClick={() =>
                      setSelectedProjectIDs((prev) =>
                        prev.filter((id) => id !== pid)
                      )
                    }
                  >
                    {formatTaskProjectOption(project)}
                    <span aria-hidden="true">&times;</span>
                  </button>
                )
              })}
            </div>
            <select
              className="project-select"
              value=""
              onChange={(e) => {
                const pid = e.target.value
                if (pid && !selectedProjectIDs.includes(pid)) {
                  setSelectedProjectIDs((prev) => [...prev, pid])
                }
              }}
              style={{
                marginTop: '0.35rem',
                width: '100%',
                fontSize: '0.78rem',
              }}
            >
              <option value="">+ 添加项目</option>
              {allProjects
                .filter((p) => !selectedProjectIDs.includes(p.id))
                .map((p) => (
                  <option key={p.id} value={p.id}>
                    {formatTaskProjectOption(p)}
                  </option>
                ))}
            </select>
          </div>
          <div className="inspector-section">
            <span>最近版本</span>
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
