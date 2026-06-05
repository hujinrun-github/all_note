import { useCallback, useEffect, useState, type MouseEvent, type ReactNode } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useEditor, EditorContent, type Editor } from '@tiptap/react'
import { BubbleMenu } from '@tiptap/react/menus'
import StarterKit from '@tiptap/starter-kit'
import Placeholder from '@tiptap/extension-placeholder'
import { Markdown } from 'tiptap-markdown'
import { NoteSyncCard } from '../components/sync/NoteSyncCard'
import { useNote, useUpdateNote } from '../hooks/useNotes'
import { useSyncObsidianNote, useSyncTargets } from '../hooks/useSync'

function getMarkdown(editor: Editor | null): string {
  if (!editor || editor.isDestroyed) return ''
  const storage = editor.storage as unknown as Record<string, { getMarkdown: () => string } | undefined>
  return storage.markdown?.getMarkdown() ?? ''
}

function countWords(markdown: string): number {
  const text = markdown.replace(/[#*`~>\-\n\[\]()!|]/g, ' ').trim()
  if (!text) return 0
  const cjk = (text.match(/[\u4e00-\u9fff]/g) || []).length
  const latin = text.replace(/[\u4e00-\u9fff]/g, ' ').split(/\s+/).filter(Boolean).length
  return cjk + latin
}

export default function EditorPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { data: note, isLoading, error } = useNote(id!)
  const updateNote = useUpdateNote()
  const syncTargetsQ = useSyncTargets()
  const { mutate: syncCurrentNote, isPending: isAutoSyncPending } = useSyncObsidianNote(id)
  const autoSyncEnabled = Boolean(
    syncTargetsQ.data?.some((target) => target.type === 'obsidian' && target.enabled && target.auto_sync),
  )

  const [title, setTitle] = useState('')

  const editor = useEditor({
    immediatelyRender: true,
    extensions: [
      StarterKit.configure({ heading: { levels: [1, 2, 3] } }),
      Placeholder.configure({ placeholder: '开始书写...' }),
      Markdown.configure({ html: false, breaks: true, linkify: true }),
    ],
    editorProps: {
      attributes: {
        class: 'max-w-none outline-none min-h-[420px]',
        spellcheck: 'false',
      },
    },
    onUpdate: ({ editor: activeEditor }) => {
      if (!activeEditor.isDestroyed) {
        ;(activeEditor as Editor & { _dirty?: boolean })._dirty = true
      }
    },
  })

  useEffect(() => {
    if (!editor || editor.isDestroyed) return
    if (note && note.id === id) {
      setTitle(note.title)
      editor.commands.setContent(note.body || '')
    } else {
      setTitle('')
      editor.commands.setContent('')
    }
  }, [id, note, editor])

  const syncAfterSave = useCallback(() => {
    if (autoSyncEnabled && !isAutoSyncPending) {
      syncCurrentNote()
    }
  }, [autoSyncEnabled, isAutoSyncPending, syncCurrentNote])

  const save = useCallback(() => {
    if (!id || !title.trim() || !editor || editor.isDestroyed) return
    updateNote.mutate(
      { id, title: title.trim(), body: getMarkdown(editor) },
      { onSuccess: syncAfterSave },
    )
  }, [id, title, editor, updateNote, syncAfterSave])

  useEffect(() => {
    if (!editor || !id) return
    const timer = setInterval(() => {
      if (updateNote.isPending || editor.isDestroyed) return
      const markdown = getMarkdown(editor)
      if (!note) return
      if (title.trim() && (markdown !== note.body || title.trim() !== note.title)) {
        updateNote.mutate(
          { id, title: title.trim(), body: markdown },
          { onSuccess: syncAfterSave },
        )
      }
    }, 5000)
    return () => clearInterval(timer)
  }, [editor, title, id, note, updateNote, syncAfterSave])

  const markdown = editor ? getMarkdown(editor) : ''

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
        <button onClick={() => navigate('/notes')} className="editor-error-back">
          返回笔记列表
        </button>
      </div>
    )
  }

  const run = (event: MouseEvent, fn: () => void) => {
    event.preventDefault()
    fn()
  }

  return (
    <div className="editor-workspace">
      <section className="editor-page">
        <div className="editor-meta">
          <button onClick={() => navigate('/notes')} className="editor-back-btn">
            <ArrowLeft /> 返回笔记
          </button>
          <div className="editor-meta-info">
            <span>{new Date(note.updated_at * 1000).toLocaleDateString('zh-CN', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</span>
            <span>{updateNote.isPending ? '保存中' : '已保存'}</span>
            {updateNote.isPending && <span className="editor-save-dot" title="保存中" />}
            {isAutoSyncPending && <span>同步中</span>}
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
                <ToolbarBtn active={editor.isActive('bold')} onClick={(event) => run(event, () => editor.chain().focus().toggleBold().run())} title="粗体" mono>
                  B
                </ToolbarBtn>
                <ToolbarBtn active={editor.isActive('italic')} onClick={(event) => run(event, () => editor.chain().focus().toggleItalic().run())} title="斜体" mono>
                  I
                </ToolbarBtn>
                <ToolbarBtn active={editor.isActive('strike')} onClick={(event) => run(event, () => editor.chain().focus().toggleStrike().run())} title="删除线" mono>
                  S
                </ToolbarBtn>
              </div>

              <div className="editor-toolbar-divider" />

              <div className="editor-toolbar-group">
                <ToolbarBtn active={editor.isActive('heading', { level: 1 })} onClick={(event) => run(event, () => editor.chain().focus().toggleHeading({ level: 1 }).run())} title="一级标题" label="H1" />
                <ToolbarBtn active={editor.isActive('heading', { level: 2 })} onClick={(event) => run(event, () => editor.chain().focus().toggleHeading({ level: 2 }).run())} title="二级标题" label="H2" />
                <ToolbarBtn active={editor.isActive('heading', { level: 3 })} onClick={(event) => run(event, () => editor.chain().focus().toggleHeading({ level: 3 }).run())} title="三级标题" label="H3" />
              </div>

              <div className="editor-toolbar-divider" />

              <div className="editor-toolbar-group">
                <ToolbarBtn active={editor.isActive('bulletList')} onClick={(event) => run(event, () => editor.chain().focus().toggleBulletList().run())} title="无序列表">
                  •
                </ToolbarBtn>
                <ToolbarBtn active={editor.isActive('orderedList')} onClick={(event) => run(event, () => editor.chain().focus().toggleOrderedList().run())} title="有序列表" mono>
                  1.
                </ToolbarBtn>
                <ToolbarBtn active={editor.isActive('blockquote')} onClick={(event) => run(event, () => editor.chain().focus().toggleBlockquote().run())} title="引用">
                  "
                </ToolbarBtn>
                <ToolbarBtn active={editor.isActive('codeBlock')} onClick={(event) => run(event, () => editor.chain().focus().toggleCodeBlock().run())} title="代码块" mono>
                  &lt;/&gt;
                </ToolbarBtn>
              </div>

              <div className="editor-toolbar-divider" />

              <div className="editor-toolbar-group">
                <ToolbarBtn active={false} onClick={(event) => run(event, () => editor.chain().focus().setHorizontalRule().run())} title="分割线">
                  -
                </ToolbarBtn>
              </div>
            </div>
          )}

          <EditorContent editor={editor} />

          {editor && (
            <BubbleMenu editor={editor} className="bubble-menu">
              <button type="button" onClick={() => editor.chain().focus().toggleBold().run()} className={`bubble-menu-btn ${editor.isActive('bold') ? 'is-active' : ''}`}>
                <strong>B</strong>
              </button>
              <button type="button" onClick={() => editor.chain().focus().toggleItalic().run()} className={`bubble-menu-btn ${editor.isActive('italic') ? 'is-active' : ''}`}>
                <em>I</em>
              </button>
              <button type="button" onClick={() => editor.chain().focus().toggleStrike().run()} className={`bubble-menu-btn ${editor.isActive('strike') ? 'is-active' : ''}`}>
                <s>S</s>
              </button>

              <span className="bubble-menu-divider" />

              <button type="button" onClick={() => editor.chain().focus().toggleHeading({ level: 1 }).run()} className={`bubble-menu-btn ${editor.isActive('heading', { level: 1 }) ? 'is-active' : ''}`}>
                H1
              </button>
              <button type="button" onClick={() => editor.chain().focus().toggleHeading({ level: 2 }).run()} className={`bubble-menu-btn ${editor.isActive('heading', { level: 2 }) ? 'is-active' : ''}`}>
                H2
              </button>
              <button type="button" onClick={() => editor.chain().focus().toggleHeading({ level: 3 }).run()} className={`bubble-menu-btn ${editor.isActive('heading', { level: 3 }) ? 'is-active' : ''}`}>
                H3
              </button>

              <span className="bubble-menu-divider" />

              <button type="button" onClick={() => editor.chain().focus().toggleBulletList().run()} className={`bubble-menu-btn ${editor.isActive('bulletList') ? 'is-active' : ''}`}>
                •
              </button>
              <button type="button" onClick={() => editor.chain().focus().toggleOrderedList().run()} className={`bubble-menu-btn ${editor.isActive('orderedList') ? 'is-active' : ''}`}>
                1.
              </button>
              <button type="button" onClick={() => editor.chain().focus().toggleBlockquote().run()} className={`bubble-menu-btn ${editor.isActive('blockquote') ? 'is-active' : ''}`}>
                "
              </button>
              <button type="button" onClick={() => editor.chain().focus().toggleCodeBlock().run()} className={`bubble-menu-btn ${editor.isActive('codeBlock') ? 'is-active' : ''}`}>
                &lt;/&gt;
              </button>

              <span className="bubble-menu-divider" />

              <button type="button" onClick={() => editor.chain().focus().setHorizontalRule().run()} className="bubble-menu-btn">
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

      <aside className="editor-inspector">
        <div className="panel-heading is-compact">
          <div>
            <span>正文</span>
            <h2>笔记信息</h2>
          </div>
        </div>
        {id && <NoteSyncCard noteID={id} />}
        <div className="inspector-section">
          <span>标签</span>
          <div className="chip-list">
            <em>产品</em>
            <em>会议</em>
            <em>评审</em>
          </div>
        </div>
        <div className="inspector-section">
          <span>关联任务</span>
          <div className="linked-note">整理行动项</div>
          <div className="linked-note">同步设计结论</div>
        </div>
        <div className="inspector-section">
          <span>关联日程</span>
          <div className="linked-note">产品需求评审会议记录</div>
        </div>
        <div className="inspector-section">
          <span>最近版本</span>
          <div className="linked-note">今天 09:15</div>
          <div className="linked-note">昨天 20:40</div>
        </div>
      </aside>
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
}: {
  active: boolean
  onClick: (event: MouseEvent) => void
  title: string
  children?: ReactNode
  mono?: boolean
  label?: string
}) {
  return (
    <button
      type="button"
      onMouseDown={onClick}
      title={title}
      data-label={label || undefined}
      className={`editor-toolbar-btn ${active ? 'is-active' : ''}`}
      style={mono && !label ? { fontFamily: 'var(--editor-font-mono)' } : undefined}
    >
      {children || label}
    </button>
  )
}

function ArrowLeft() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M19 12H5M12 19l-7-7 7-7" />
    </svg>
  )
}
