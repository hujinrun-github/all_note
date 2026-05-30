import { useState, useEffect, useCallback } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { useNote, useUpdateNote } from '../hooks/useNotes'

export default function Editor() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { data: note, isLoading, error } = useNote(id!)
  const updateNote = useUpdateNote()

  const [title, setTitle] = useState('')
  const [body, setBody] = useState('')

  useEffect(() => {
    if (note) {
      setTitle(note.title)
      setBody(note.body)
    }
  }, [note])

  const save = useCallback(() => {
    if (!id || !title.trim()) return
    updateNote.mutate({ id, title, body })
  }, [id, title, body, updateNote])

  // Auto-save on blur
  useEffect(() => {
    const handler = () => save()
    window.addEventListener('beforeunload', handler)
    return () => window.removeEventListener('beforeunload', handler)
  }, [save])

  if (isLoading) return <div className="max-w-[740px] mx-auto grid gap-4"><div className="h-10 bg-fs-hover rounded-md animate-pulse" /><div className="h-96 bg-fs-hover rounded-md animate-pulse" /></div>
  if (error || !note) return <div className="text-red-500 text-sm">笔记未找到 <button onClick={() => navigate('/notes')} className="underline ml-2">返回列表</button></div>

  return (
    <div className="max-w-[740px] mx-auto grid gap-4">
      <div className="flex justify-between items-center">
        <button onClick={() => navigate('/notes')} className="border-0 bg-transparent text-fs-text-muted hover:text-fs-text cursor-pointer text-sm transition-colors">
          ← 返回
        </button>
        <div className="flex gap-3 items-center">
          <span className="text-fs-text-muted text-xs">
            {new Date(note.updated_at * 1000).toLocaleDateString('zh-CN', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}
            {updateNote.isPending && <span className="ml-2 text-fs-accent">保存中...</span>}
            {updateNote.isSuccess && <span className="ml-2 text-fs-success">已保存</span>}
          </span>
        </div>
      </div>

      <input
        value={title}
        onChange={(e) => setTitle(e.target.value)}
        onBlur={save}
        placeholder="笔记标题"
        className="w-full border-0 bg-transparent text-[28px] font-bold outline-none font-sans"
      />

      <textarea
        value={body}
        onChange={(e) => setBody(e.target.value)}
        onBlur={save}
        placeholder="开始写作..."
        className="w-full border-0 bg-transparent text-[15px] leading-relaxed resize-none outline-none min-h-[400px] font-sans"
        rows={20}
      />

      <div className="flex justify-between text-xs text-fs-text-muted">
        <span>{body.split(/\s+/).filter(Boolean).length} 词</span>
        <button onClick={save} className="border border-fs-border rounded-md px-4 py-1.5 bg-transparent cursor-pointer hover:bg-fs-hover transition-colors text-sm">
          保存
        </button>
      </div>
    </div>
  )
}
