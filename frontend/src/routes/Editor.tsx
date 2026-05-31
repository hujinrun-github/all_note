import { useState, useEffect, useCallback } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { useEditor, EditorContent, type Editor } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import Placeholder from '@tiptap/extension-placeholder'
import { Markdown } from 'tiptap-markdown'
import { useNote, useUpdateNote } from '../hooks/useNotes'

// tiptap-markdown extends storage at runtime; TS types lag behind
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
  })

  useEffect(() => {
    if (note && editor && !editor.isDestroyed) {
      setTitle(note.title)
      editor.commands.setContent(note.body)
    }
  }, [note, editor])

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

      <EditorContent editor={editor} />

      <div className="flex justify-between text-xs text-fs-text-muted">
        <span>Markdown · 支持 # 标题 **粗体** *斜体* `代码` 等语法</span>
        <button onClick={save} className="border border-fs-border rounded-md px-4 py-1.5 bg-transparent cursor-pointer hover:bg-fs-hover transition-colors text-sm">
          保存
        </button>
      </div>
    </div>
  )
}
