import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { getFolders } from '../api/folders'
import { deleteNote, getNotes } from '../api/notes'
import { ObsidianSyncPanel } from '../components/sync/ObsidianSyncPanel'

export default function Notes() {
  const navigate = useNavigate()
  const [folder, setFolder] = useState('')
  const [sort, setSort] = useState('recent')
  const [syncOpen, setSyncOpen] = useState(false)

  const foldersQ = useQuery({ queryKey: ['folders'], queryFn: getFolders })
  const notesQ = useQuery({
    queryKey: ['notes', folder, sort],
    queryFn: () => getNotes({ folder_id: folder || undefined, sort }),
  })

  if (notesQ.isLoading) return <Skeleton />
  if (notesQ.error) {
    return (
      <div className="text-center py-12">
        <p className="text-fs-text-muted text-sm">加载失败</p>
      </div>
    )
  }

  return (
    <div className="list-workspace">
      <aside className="filter-rail">
        <div className="filter-title">文件夹</div>
        <button onClick={() => setFolder('')} className={!folder ? 'is-active' : ''}>
          <span>全部</span>
          <span className="text-xs opacity-60">{notesQ.data?.pagination.total ?? 0}</span>
        </button>
        {(foldersQ.data ?? []).map((item) => (
          <button key={item.id} onClick={() => setFolder(item.id)} className={folder === item.id ? 'is-active' : ''}>
            <span>{item.name}</span>
            <span className="text-fs-text-muted text-xs">{item.note_count}</span>
          </button>
        ))}
        <div className="rail-summary">
          <span>最近更新</span>
          <strong>{notesQ.data?.pagination.total ?? 0}</strong>
          <p>按文件夹保持知识有序</p>
        </div>
      </aside>

      <section className="surface-panel list-panel">
        <div className="panel-heading">
          <div>
            <span>{notesQ.data?.pagination.total ?? 0} 篇笔记</span>
            <h2>笔记库</h2>
          </div>
          <div className="toolbar-actions">
            <button onClick={() => setSyncOpen(true)} className="secondary-action">
              同步
            </button>
            <button onClick={() => setSort(sort === 'recent' ? 'az' : 'recent')} className="secondary-action">
              {sort === 'recent' ? '最近更新' : 'A-Z'}
            </button>
            <button className="primary-action">新建笔记</button>
          </div>
        </div>

        <div className="list-rows">
          {(notesQ.data?.notes ?? []).map((note) => (
            <div key={note.id} className="rich-row group" onClick={() => navigate(`/editor/${encodeURIComponent(note.id)}`)}>
              <div className="min-w-0">
                <strong className="text-sm font-medium text-fs-text block truncate">{note.title}</strong>
                <div className="text-fs-text-muted text-xs mt-1">
                  {new Date(note.updated_at * 1000).toLocaleDateString('zh-CN')}
                  {note.folder_id !== '__uncategorized' && note.folder_id !== '__work' && (
                    <span> · {note.folder_id.replace('__', '')}</span>
                  )}
                </div>
              </div>
              <button
                onClick={(event) => {
                  event.stopPropagation()
                  deleteNote(note.id).then(() => notesQ.refetch())
                }}
                className="border-0 bg-transparent text-fs-text-muted hover:text-red-500 cursor-pointer text-xs px-2 py-1 rounded transition-colors opacity-0 group-hover:opacity-100"
              >
                删除
              </button>
            </div>
          ))}
        </div>

        {(notesQ.data?.notes ?? []).length === 0 && <p className="empty-copy">暂无笔记</p>}
      </section>

      {syncOpen && <ObsidianSyncPanel onClose={() => setSyncOpen(false)} />}
    </div>
  )
}

function Skeleton() {
  return (
    <div className="grid grid-cols-[180px_1fr] gap-6">
      <div className="grid gap-2">
        {Array.from({ length: 4 }).map((_, index) => (
          <div key={index} className="h-9 bg-fs-hover rounded-lg animate-pulse" />
        ))}
      </div>
      <div className="grid gap-2">
        {Array.from({ length: 5 }).map((_, index) => (
          <div key={index} className="h-14 bg-fs-hover rounded-lg animate-pulse" />
        ))}
      </div>
    </div>
  )
}
