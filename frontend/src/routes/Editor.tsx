import { useCallback, useEffect, useState, type ReactNode } from 'react'
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
        class: 'prose prose-sm max-w-none outline-none min-h-[400px] text-[15px] leading-relaxed',
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
    if (note && editor && !editor.isDestroyed) {
      setTitle(note.title)
      editor.commands.setContent(note.body || '')
    }
  }, [note, editor])

  useEffect(() => {
    if (!id) return
    setTitle('')
    if (editor && !editor.isDestroyed) {
      editor.commands.setContent('')
    }
  }, [id, editor])

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

  if (isLoading) {
    return (
      <div className="max-w-[740px] mx-auto grid gap-4">
        <div className="h-10 bg-fs-hover rounded-md animate-pulse" />
        <div className="h-96 bg-fs-hover rounded-md animate-pulse" />
      </div>
    )
  }

  if (error || !note) {
    return (
      <div className="text-red-500 text-sm">
        笔记未找到
        <button onClick={() => navigate('/notes')} className="underline ml-2">
          返回列表
        </button>
      </div>
    )
  }

  return (
    <div className="max-w-[740px] mx-auto grid gap-4">
      <div className="flex justify-between items-center">
        <button
          onClick={() => navigate('/notes')}
          className="border-0 bg-transparent text-fs-text-muted hover:text-fs-text cursor-pointer text-sm transition-colors"
        >
          ← 返回
        </button>
        <span className="text-fs-text-muted text-xs">
          {new Date(note.updated_at * 1000).toLocaleDateString('zh-CN', {
            month: 'short',
            day: 'numeric',
            hour: '2-digit',
            minute: '2-digit',
          })}
          {updateNote.isPending && <span className="ml-2 text-fs-accent">保存中...</span>}
          {isAutoSyncPending && <span className="ml-2 text-fs-accent">同步中...</span>}
        </span>
      </div>

      <input
        value={title}
        onChange={(event) => setTitle(event.target.value)}
        onBlur={save}
        placeholder="笔记标题"
        className="w-full border-0 bg-transparent text-[28px] font-bold outline-none font-sans"
      />

      {id && <NoteSyncCard noteID={id} />}

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
            •
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
            -
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

function MenuBtn({ onClick, active, children }: { onClick: () => void; active: boolean; children: ReactNode }) {
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
