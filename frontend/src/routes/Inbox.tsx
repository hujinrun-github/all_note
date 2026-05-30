import { useState } from 'react'
import { useInboxList, useConvertInboxItem, useDeleteInboxItem, useBatchInbox } from '../hooks/useInbox'
import type { InboxItem } from '../api/inbox'

const kinds = ['all', 'note', 'task', 'event'] as const
const kindLabels: Record<string, string> = { all: '全部', note: '笔记', task: '任务', event: '日程' }

export default function Inbox() {
  const [kind, setKind] = useState('all')
  const [selected, setSelected] = useState<Set<string>>(new Set())

  const { data, isLoading, error, refetch } = useInboxList({ kind: kind === 'all' ? undefined : kind })
  const convertItem = useConvertInboxItem()
  const deleteItem = useDeleteInboxItem()
  const batchInbox = useBatchInbox()

  function toggleSelect(id: string) {
    const next = new Set(selected)
    if (next.has(id)) { next.delete(id) } else { next.add(id) }
    setSelected(next)
  }

  async function handleConvert(id: string, targetKind: string) {
    await convertItem.mutateAsync({ id, kind: targetKind })
    refetch()
  }

  async function handleDelete(id: string) {
    await deleteItem.mutateAsync(id)
    refetch()
  }

  async function handleBatch(action: 'archive' | 'delete') {
    if (selected.size === 0) return
    await batchInbox.mutateAsync({ ids: Array.from(selected), action })
    setSelected(new Set())
    refetch()
  }

  const convertKinds = [
    { kind: 'note', label: '转为笔记' },
    { kind: 'task', label: '转为任务' },
    { kind: 'event', label: '转为日程' },
  ]

  if (isLoading) return <div className="grid gap-2">{Array.from({ length: 5 }).map((_, i) => <div key={i} className="h-12 bg-fs-hover rounded-md animate-pulse" />)}</div>
  if (error) return <div className="text-red-500 text-sm">加载失败</div>

  return (
    <div className="grid gap-5 max-w-[720px]">
      <div className="flex justify-between items-center">
        <div className="flex gap-2">
          {kinds.map((k) => (
            <button key={k} onClick={() => setKind(k)}
              className={`border-0 rounded-md px-3 py-1.5 text-xs cursor-pointer transition-colors ${kind === k ? 'bg-fs-accent text-white' : 'bg-fs-hover text-fs-text-secondary hover:bg-fs-border'}`}>
              {kindLabels[k]}
            </button>
          ))}
        </div>
        {selected.size > 0 && (
          <div className="flex gap-2">
            <button onClick={() => handleBatch('archive')} className="border border-fs-border rounded-md px-3 py-1 text-xs bg-transparent cursor-pointer hover:bg-fs-hover transition-colors">
              归档 ({selected.size})
            </button>
            <button onClick={() => handleBatch('delete')} className="border border-red-300 rounded-md px-3 py-1 text-xs bg-transparent text-red-500 cursor-pointer hover:bg-red-50 transition-colors">
              删除 ({selected.size})
            </button>
          </div>
        )}
      </div>

      <div className="grid gap-2">
        {data?.items.map((item: InboxItem) => (
          <div key={item.id} className="flex items-start gap-3 px-3 py-2.5 border border-fs-border rounded-md hover:border-fs-border-hover transition-colors">
            <input type="checkbox" checked={selected.has(item.id)} onChange={() => toggleSelect(item.id)} className="mt-1.5" />
            <div className="flex-1 min-w-0">
              <div className="flex items-center gap-2">
                <span className={`text-[10px] font-semibold uppercase px-1.5 py-0.5 rounded-sm ${item.kind === 'note' ? 'bg-fs-accent/10 text-fs-accent' : item.kind === 'task' ? 'bg-fs-warning/10 text-fs-warning' : 'bg-fs-success/10 text-fs-success'}`}>
                  {kindLabels[item.kind]}
                </span>
                <strong className="text-sm">{item.title}</strong>
              </div>
              {item.body && <p className="text-fs-text-secondary text-xs mt-1 truncate">{item.body}</p>}
              <span className="text-fs-text-muted text-[11px] mt-1 block">{new Date(item.created_at * 1000).toLocaleDateString('zh-CN', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</span>
            </div>
            <div className="flex gap-1 shrink-0">
              {convertKinds.map(({ kind: ck, label }) => (
                <button key={ck} onClick={() => handleConvert(item.id, ck)} disabled={convertItem.isPending}
                  className="border border-fs-border rounded px-2 py-1 text-[10px] bg-transparent cursor-pointer hover:bg-fs-hover transition-colors disabled:opacity-50">
                  {label}
                </button>
              ))}
              <button onClick={() => handleDelete(item.id)} className="border-0 bg-transparent text-fs-text-muted hover:text-red-500 cursor-pointer text-xs px-1 transition-colors">×</button>
            </div>
          </div>
        ))}
      </div>

      {data?.items.length === 0 && (
        <p className="text-fs-text-muted text-sm text-center py-8">收件箱为空</p>
      )}
    </div>
  )
}
