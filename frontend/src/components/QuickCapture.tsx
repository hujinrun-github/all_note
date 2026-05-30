import { useEffect, useState } from 'react'
import { useUIStore } from '../stores/ui'

type Kind = 'note' | 'task' | 'event'

export function QuickCapture() {
  const setCaptureOpen = useUIStore((s) => s.setCaptureOpen)
  const [kind, setKind] = useState<Kind>('note')
  const [title, setTitle] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') setCaptureOpen(false)
      if (e.metaKey && e.shiftKey && e.key === 'K') {
        e.preventDefault()
        setCaptureOpen(true)
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [setCaptureOpen])

  async function handleSubmit() {
    if (!title.trim()) return
    setSubmitting(true)
    setError(null)
    try {
      const res = await fetch('/api/inbox', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ kind, title: title.trim() }),
      })
      if (!res.ok) throw new Error('创建失败')
      setTitle('')
      setCaptureOpen(false)
    } catch {
      setError('创建失败，请重试')
    } finally {
      setSubmitting(false)
    }
  }

  const kinds: { value: Kind; label: string }[] = [
    { value: 'note', label: '笔记' },
    { value: 'task', label: '任务' },
    { value: 'event', label: '日程' },
  ]

  return (
    <>
      <div className="fixed inset-0 bg-black/20 z-[100] grid place-items-start pt-[60px]" onClick={() => setCaptureOpen(false)}>
        <div
          className="w-[520px] bg-fs-surface rounded-[14px] shadow-popover p-6 grid gap-[18px] animate-[slideDown_200ms_var(--ease-standard)] max-[760px]:w-[calc(100vw-32px)] max-[760px]:mx-4"
          onClick={(e) => e.stopPropagation()}
        >
          <div className="flex justify-between items-center">
            <strong className="text-[15px]">快速捕获</strong>
            <button onClick={() => setCaptureOpen(false)} className="border-0 bg-transparent text-fs-text-muted cursor-pointer text-lg leading-none">&times;</button>
          </div>

          <div className="flex gap-1.5">
            {kinds.map(({ value, label }) => (
              <button
                key={value}
                onClick={() => setKind(value)}
                className={`border-0 rounded-md px-3 py-1.5 text-xs cursor-pointer transition-colors ${
                  kind === value ? 'bg-fs-accent text-white' : 'bg-fs-hover text-fs-text-secondary hover:bg-fs-border'
                }`}
              >
                {label}
              </button>
            ))}
          </div>

          <textarea
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder={kind === 'note' ? '输入笔记标题...' : kind === 'task' ? '输入任务名称...' : '输入日程标题...'}
            className="w-full border border-fs-border rounded-md p-3 text-[15px] leading-relaxed resize-none outline-none focus:border-fs-accent transition-colors font-sans"
            rows={3}
            autoFocus
            onKeyDown={(e) => {
              if (e.metaKey && e.key === 'Enter') handleSubmit()
            }}
          />

          {error && <div className="text-red-500 text-xs">{error}</div>}

          <div className="flex justify-between items-center">
            <span className="text-[13px] text-fs-text-muted">⌘+Enter 创建</span>
            <div className="flex gap-2">
              <button onClick={() => setCaptureOpen(false)} className="border border-fs-border rounded-md px-4 py-1.5 text-sm bg-transparent cursor-pointer hover:bg-fs-hover transition-colors">取消</button>
              <button onClick={handleSubmit} disabled={submitting || !title.trim()} className="border-0 rounded-md px-4 py-1.5 text-sm bg-fs-accent text-white cursor-pointer hover:bg-fs-accent-hover transition-colors disabled:opacity-50 disabled:cursor-not-allowed">
                {submitting ? '创建中...' : '创建'}
              </button>
            </div>
          </div>
        </div>
      </div>

      <style>{`
        @keyframes slideDown {
          from { opacity: 0; transform: translateY(-12px); }
          to { opacity: 1; transform: translateY(0); }
        }
        @media (prefers-reduced-motion: reduce) {
          .animate-\\[slideDown_200ms\\] { animation: none; }
        }
      `}</style>
    </>
  )
}
