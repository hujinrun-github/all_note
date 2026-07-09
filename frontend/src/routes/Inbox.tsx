import { useState } from 'react'
import { useInboxList, useConvertInboxItem, useDeleteInboxItem, useBatchInbox } from '../hooks/useInbox'
import type { InboxItem } from '../api/inbox'

const kinds = ['all', 'note', 'task', 'event', 'idea'] as const
const kindLabels: Record<string, string> = { all: '全部', note: '笔记', task: '任务', event: '日程', idea: '想法' }

export default function Inbox() {
  const [kind, setKind] = useState('all')
  const [selected, setSelected] = useState<Set<string>>(new Set())

  const { data, isLoading, error, refetch } = useInboxList({ kind: kind === 'all' || kind === 'idea' ? undefined : kind })
  const convertItem = useConvertInboxItem()
  const deleteItem = useDeleteInboxItem()
  const batchInbox = useBatchInbox()

  const items = data?.items ?? []
  const selectedItem = items.find((item) => selected.has(item.id)) ?? items[0]

  function toggleSelect(id: string) {
    const next = new Set(selected)
    if (next.has(id)) {
      next.delete(id)
    } else {
      next.add(id)
    }
    setSelected(next)
  }

  async function handleConvert(id: string, targetKind: string) {
    await convertItem.mutateAsync({ id, kind: targetKind })
    await refetch()
  }

  async function handleDelete(id: string) {
    await deleteItem.mutateAsync(id)
    await refetch()
  }

  async function handleBatch(action: 'archive' | 'delete') {
    if (selected.size === 0) return
    await batchInbox.mutateAsync({ ids: Array.from(selected), action })
    setSelected(new Set())
    await refetch()
  }

  if (isLoading) return <Skeleton />
  if (error) {
    return (
      <div className="empty-state">
        <strong>加载失败</strong>
        <p>收件箱暂时不可用，请稍后重试。</p>
      </div>
    )
  }

  return (
    <div className="inbox-page">
      <div className="page-local-actions">
        <div className="segmented-tabs">
          {kinds.slice(0, 4).map((itemKind) => (
            <button key={itemKind} type="button" className={kind === itemKind ? 'is-active' : ''} onClick={() => setKind(itemKind)}>
              {kindLabels[itemKind]}
            </button>
          ))}
        </div>
      </div>

      <div className="inbox-workspace">
        <aside className="surface-panel inbox-filter-panel">
          <h2>捕获类型</h2>
          {kinds.map((itemKind) => (
            <button key={itemKind} onClick={() => setKind(itemKind)} className={kind === itemKind ? 'is-active' : ''}>
              <span>{kindLabels[itemKind]}</span>
              <em>{itemKind === 'all' ? items.length : items.filter((item) => item.kind === itemKind).length}</em>
            </button>
          ))}
          <div className="rail-summary">
            <span>待处理</span>
            <strong>{items.length}</strong>
            <p>先捕获，再整理成具体对象</p>
          </div>
        </aside>

        <section className="surface-panel inbox-list-panel">
          <div className="panel-heading">
            <div>
              <h2>捕获列表</h2>
            </div>
            <button className="secondary-action" onClick={() => handleBatch('archive')} disabled={selected.size === 0}>
              批量操作
            </button>
          </div>

          <div className="capture-list">
            {items.map((item: InboxItem) => (
              <article key={item.id} className={`capture-row ${selected.has(item.id) ? 'is-selected' : ''}`}>
                <input type="checkbox" checked={selected.has(item.id)} onChange={() => toggleSelect(item.id)} />
                <div>
                  <strong>{item.title}</strong>
                  <p>
                    <em>{kindLabels[item.kind]}</em>
                    <span>{new Date(item.created_at * 1000).toLocaleDateString('zh-CN', { month: 'long', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</span>
                  </p>
                </div>
                <div className="row-actions">
                  <button onClick={() => handleConvert(item.id, 'note')} disabled={convertItem.isPending}>转笔记</button>
                  <button onClick={() => handleConvert(item.id, 'task')} disabled={convertItem.isPending}>转任务</button>
                  <button className="is-primary" onClick={() => handleConvert(item.id, 'event')} disabled={convertItem.isPending}>转日程</button>
                  <button className="icon-danger" onClick={() => handleDelete(item.id)} disabled={deleteItem.isPending}>×</button>
                </div>
              </article>
            ))}
          </div>

          {items.length === 0 && <p className="empty-copy">收件箱为空</p>}

          <div className="inbox-batch-bar">
            <span>已选择 {selected.size} 项</span>
            <button className="secondary-action" disabled={selected.size === 0} onClick={() => handleBatch('archive')}>
              批量归档
            </button>
            <button className="danger-action" disabled={selected.size === 0} onClick={() => handleBatch('delete')}>
              删除
            </button>
          </div>
        </section>

        <aside className="surface-panel inbox-detail-panel">
          <div className="panel-heading is-compact">
            <div>
              <h2>整理为任务</h2>
              <p>将捕获转成可执行事项</p>
            </div>
          </div>
          <form className="inbox-convert-form">
            <label>
              <span>标题</span>
              <input value={selectedItem?.title ?? '测试'} readOnly />
            </label>
            <label>
              <span>项目</span>
              <input value="Personal ▾" readOnly />
            </label>
            <label>
              <span>截止日期</span>
              <input value="2026/07/06" readOnly />
            </label>
            <label>
              <span>优先级</span>
              <input value="中" readOnly />
            </label>
            <label>
              <span>备注</span>
              <textarea value={selectedItem?.body || '无备注'} readOnly />
            </label>
            <div className="form-actions">
              <button type="button" className="secondary-action">取消</button>
              <button
                type="button"
                className="primary-action"
                disabled={!selectedItem || convertItem.isPending}
                onClick={() => selectedItem && void handleConvert(selectedItem.id, 'task')}
              >
                确认整理
              </button>
            </div>
          </form>
        </aside>
      </div>
    </div>
  )
}

function Skeleton() {
  return (
    <div className="inbox-workspace">
      <aside className="surface-panel inbox-filter-panel" />
      <section className="surface-panel inbox-list-panel" />
      <aside className="surface-panel inbox-detail-panel" />
    </div>
  )
}
