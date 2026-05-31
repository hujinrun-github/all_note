import { useState, useEffect, useCallback } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { useEditor, EditorContent, type Editor } from '@tiptap/react'
import { BubbleMenu } from '@tiptap/react/menus'
import StarterKit from '@tiptap/starter-kit'
import Placeholder from '@tiptap/extension-placeholder'
import { Markdown } from 'tiptap-markdown'
import { useNote, useUpdateNote } from '../hooks/useNotes'

function getMarkdown(e: Editor | null): string {
  if (!e || e.isDestroyed) return ''
  const s = e.storage as unknown as Record<string, { getMarkdown: () => string } | undefined>
  return s.markdown?.getMarkdown() ?? ''
}

export default function EditorPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { data: note, isLoading, error } = useNote(id!)
  const updateNote = useUpdateNote()

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
        class: 'prose prose-sm max-w-none outline-none min-h-[400px] text-[15px] leading-relaxed',
        spellcheck: 'false',
      },
    },
    onUpdate: ({ editor: ed }) => {
      // Mark this so auto-save doesn't miss unsaved changes
      if (!ed.isDestroyed) {
        ;(ed as Editor & { _dirty?: boolean })._dirty = true
      }
    },
  })

  // Load note content into editor when data arrives
  useEffect(() => {
    if (note && editor && !editor.isDestroyed) {
      setTitle(note.title)
      // editor.commands.setContent with Markdown extension parses Markdown → HTML
      editor.commands.setContent(note.body || '')
    }
  }, [note, editor])

  // Also load when id changes (navigate between notes)
  useEffect(() => {
    if (!id) return
    setTitle('')
    if (editor && !editor.isDestroyed) {
      editor.commands.setContent('')
    }
  }, [id])

  const save = useCallback(() => {
    if (!id || !title.trim() || !editor || editor.isDestroyed) return
    updateNote.mutate({ id, title: title.trim(), body: getMarkdown(editor) })
  }, [id, title, editor, updateNote])

  // Debounced auto-save every 5s
  useEffect(() => {
    if (!editor) return
    const t = setInterval(() => {
      if (updateNote.isPending || editor.isDestroyed) return
      const md = getMarkdown(editor)
      if (!note) return
      if (title.trim() && (md !== note.body || title.trim() !== note.title)) {
        updateNote.mutate({ id: id!, title: title.trim(), body: md })
      }
    }, 5000)
    return () => clearInterval(t)
  }, [editor, title, id, note, updateNote])

  if (isLoading) return (
    <div className="max-w-[740px] mx-auto grid gap-4">
      <div className="h-10 bg-fs-hover rounded-md animate-pulse" />
      <div className="h-96 bg-fs-hover rounded-md animate-pulse" />
    </div>
  )

  if (error || !note) return (
    <div className="text-red-500 text-sm">
      笔记未找到 <button onClick={() => navigate('/notes')} className="underline ml-2">返回列表</button>
    </div>
  )

  return (
    <div className="max-w-[740px] mx-auto grid gap-4">
      <div className="flex justify-between items-center">
        <button onClick={() => navigate('/notes')} className="border-0 bg-transparent text-fs-text-muted hover:text-fs-text cursor-pointer text-sm transition-colors">
          ← 返回
        </button>
        <span className="text-fs-text-muted text-xs">
          {new Date(note.updated_at * 1000).toLocaleDateString('zh-CN', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}
          {updateNote.isPending && <span className="ml-2 text-fs-accent">保存中...</span>}
        </span>
      </div>

      <input
        value={title}
        onChange={(e) => setTitle(e.target.value)}
        onBlur={save}
        placeholder="笔记标题"
        className="w-full border-0 bg-transparent text-[28px] font-bold outline-none font-sans"
      />

      {editor && (
        <BubbleMenu editor={editor} className="flex gap-0.5 bg-white border border-fs-border rounded-lg shadow-popover px-1 py-1">
          <MenuBtn onClick={() => editor.chain().focus().toggleBold().run()} active={editor.isActive('bold')}>
            <strong>B</strong>
          </MenuBtn>
          <MenuBtn onClick={() => editor.chain().focus().toggleItalic().run()} active={editor.isActive('italic')}>
            <em>I</em>
          </MenuBtn>
          <MenuBtn onClick={() => editor.chain().focus().toggleStrike().run()} active={editor.isActive('strike')}>
            <s>S</s>
          </MenuBtn>
          <div className="w-px bg-fs-border mx-0.5" />
          <MenuBtn onClick={() => editor.chain().focus().toggleHeading({ level: 1 }).run()} active={editor.isActive('heading', { level: 1 })}>
            H1
          </MenuBtn>
          <MenuBtn onClick={() => editor.chain().focus().toggleHeading({ level: 2 }).run()} active={editor.isActive('heading', { level: 2 })}>
            H2
          </MenuBtn>
          <MenuBtn onClick={() => editor.chain().focus().toggleHeading({ level: 3 }).run()} active={editor.isActive('heading', { level: 3 })}>
            H3
          </MenuBtn>
          <div className="w-px bg-fs-border mx-0.5" />
          <MenuBtn onClick={() => editor.chain().focus().toggleBulletList().run()} active={editor.isActive('bulletList')}>
            •≡
          </MenuBtn>
          <MenuBtn onClick={() => editor.chain().focus().toggleOrderedList().run()} active={editor.isActive('orderedList')}>
            1.
          </MenuBtn>
          <MenuBtn onClick={() => editor.chain().focus().toggleBlockquote().run()} active={editor.isActive('blockquote')}>
            "
          </MenuBtn>
          <MenuBtn onClick={() => editor.chain().focus().toggleCodeBlock().run()} active={editor.isActive('codeBlock')}>
            &lt;/&gt;
          </MenuBtn>
          <div className="w-px bg-fs-border mx-0.5" />
          <MenuBtn onClick={() => editor.chain().focus().setHorizontalRule().run()} active={false}>
            —
          </MenuBtn>
        </BubbleMenu>
      )}

      <EditorContent editor={editor} />

      <div className="flex justify-between text-xs text-fs-text-muted">
        <span>选中文字显示格式工具栏 · 支持 Markdown 语法</span>
        <button onClick={save} className="border border-fs-border rounded-md px-4 py-1.5 bg-transparent cursor-pointer hover:bg-fs-hover transition-colors text-sm">
          保存
        </button>
      </div>
    </div>
  )
}

function MenuBtn({ onClick, active, children }: { onClick: () => void; active: boolean; children: React.ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`w-7 h-7 flex items-center justify-center rounded text-xs border-0 cursor-pointer transition-colors ${
        active ? 'bg-fs-accent text-white' : 'bg-transparent text-fs-text-secondary hover:bg-fs-hover'
      }`}
    >
      {children}
    </button>
  )
}
