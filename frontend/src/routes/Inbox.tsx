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

  if (isLoading) return <Skeleton />
  if (error) return <div className="text-center py-12"><p className="text-fs-text-muted text-sm">加载失败</p></div>

  return (
    <div className="list-workspace">
      <aside className="filter-rail">
        <div className="filter-title">捕获类型</div>
          {kinds.map((k) => (
            <button
              key={k}
              onClick={() => setKind(k)}
              className={kind === k ? 'is-active' : ''}
            >
              {kindLabels[k]}
            </button>
          ))}
        <div className="rail-summary">
          <span>待处理</span>
          <strong>{data?.items.length ?? 0}</strong>
          <p>先捕获，再整理成具体对象</p>
        </div>
      </aside>

      <section className="surface-panel list-panel">
        <div className="panel-heading">
          <div>
            <span>收件箱</span>
            <h2>未整理捕获</h2>
          </div>
          <div className="toolbar-actions">
            <button onClick={() => handleBatch('archive')} disabled={selected.size === 0} className="secondary-action">
              批量归档{selected.size > 0 ? ` (${selected.size})` : ''}
            </button>
            <button onClick={() => handleBatch('delete')} disabled={selected.size === 0} className="danger-action">
              删除{selected.size > 0 ? ` (${selected.size})` : ''}
            </button>
          </div>
        </div>

      <div className="list-rows">
        {(data?.items ?? []).map((item: InboxItem) => (
          <div
            key={item.id}
            className={`capture-row ${selected.has(item.id) ? 'is-selected' : ''}`}
          >
            <input
              type="checkbox"
              checked={selected.has(item.id)}
              onChange={() => toggleSelect(item.id)}
              className="mt-1.5 accent-fs-accent"
            />
            <div className="flex-1 min-w-0">
              <div className="flex items-center gap-2">
                <span className={`text-[10px] font-semibold uppercase px-1.5 py-0.5 rounded ${
                  item.kind === 'note' ? 'bg-fs-accent/10 text-fs-accent' :
                  item.kind === 'task' ? 'bg-fs-warning/10 text-fs-warning' :
                  'bg-fs-success/10 text-fs-success'
                }`}>
                  {kindLabels[item.kind]}
                </span>
                <strong className="text-sm text-fs-text">{item.title}</strong>
              </div>
              {item.body && <p className="text-fs-text-secondary text-xs mt-1 truncate">{item.body}</p>}
              <span className="text-fs-text-muted text-[11px] mt-1.5 block">
                {new Date(item.created_at * 1000).toLocaleDateString('zh-CN', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}
              </span>
            </div>
            <div className="row-actions">
              {convertKinds.map(({ kind: ck, label }) => (
                <button
                  key={ck}
                  onClick={() => handleConvert(item.id, ck)}
                  disabled={convertItem.isPending}
                >
                  {label}
                </button>
              ))}
              <button
                onClick={() => handleDelete(item.id)}
                className="icon-danger"
              >
                ×
              </button>
            </div>
          </div>
        ))}
      </div>

      {(data?.items ?? []).length === 0 && (
        <p className="empty-copy">收件箱为空</p>
      )}
      </section>
    </div>
  )
}

function Skeleton() {
  return (
    <div className="max-w-[780px] grid gap-3">
      <div className="flex gap-2">{Array.from({ length: 4 }).map((_, i) => <div key={i} className="h-8 w-14 bg-fs-hover rounded-full animate-pulse" />)}</div>
      <div className="grid gap-2">{Array.from({ length: 5 }).map((_, i) => <div key={i} className="h-16 bg-fs-hover rounded-lg animate-pulse" />)}</div>
    </div>
  )
}
