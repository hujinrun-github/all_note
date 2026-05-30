import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { getFolders } from '../api/folders'
import { getNotes, deleteNote } from '../api/notes'

export default function Notes() {
  const navigate = useNavigate()
  const [folder, setFolder] = useState('')
  const [sort, setSort] = useState('recent')

  const foldersQ = useQuery({ queryKey: ['folders'], queryFn: getFolders })
  const notesQ = useQuery({
    queryKey: ['notes', folder, sort],
    queryFn: () => getNotes({ folder_id: folder || undefined, sort }),
  })

  if (notesQ.isLoading) return <div className="grid gap-2">{Array.from({ length: 5 }).map((_, i) => <div key={i} className="h-12 bg-fs-hover rounded-md animate-pulse" />)}</div>
  if (notesQ.error) return <div className="text-red-500 text-sm">加载失败</div>

  return (
    <div className="grid grid-cols-[180px_1fr] gap-6 max-[760px]:grid-cols-1">
      <aside className="grid gap-1 content-start">
        <button onClick={() => setFolder('')} className={`text-left border-0 bg-transparent rounded-md px-2.5 py-1.5 text-sm cursor-pointer transition-colors ${!folder ? 'bg-fs-hover text-fs-accent font-semibold' : 'text-fs-text-secondary hover:bg-fs-hover'}`}>
          全部
        </button>
        {foldersQ.data?.map((f) => (
          <button key={f.id} onClick={() => setFolder(f.id)}
            className={`text-left border-0 bg-transparent rounded-md px-2.5 py-1.5 text-sm cursor-pointer transition-colors flex justify-between ${folder === f.id ? 'bg-fs-hover text-fs-accent font-semibold' : 'text-fs-text-secondary hover:bg-fs-hover'}`}>
            <span>{f.name}</span>
            <span className="text-fs-text-muted text-xs">{f.note_count}</span>
          </button>
        ))}
      </aside>

      <div>
        <div className="flex justify-between items-center mb-4">
          <span className="text-fs-text-muted text-xs">{notesQ.data?.pagination.total ?? 0} 篇笔记</span>
          <button onClick={() => setSort(sort === 'recent' ? 'az' : 'recent')} className="border border-fs-border rounded-md px-3 py-1 text-xs bg-transparent cursor-pointer hover:bg-fs-hover transition-colors">
            {sort === 'recent' ? '最近' : 'A-Z'}
          </button>
        </div>

        <div className="grid gap-1">
          {notesQ.data?.notes.map((n) => (
            <div key={n.id} className="flex justify-between items-center px-3 py-2.5 rounded-md hover:bg-fs-hover cursor-pointer transition-colors" onClick={() => navigate(`/editor/${n.id}`)}>
              <div>
                <strong className="text-sm font-medium">{n.title}</strong>
                <div className="text-fs-text-muted text-xs mt-0.5">
                  {new Date(n.updated_at * 1000).toLocaleDateString('zh-CN')}
                  {n.folder_id !== '__uncategorized' && ` · ${n.folder_id.replace('__', '')}`}
                </div>
              </div>
              <button onClick={(e) => { e.stopPropagation(); deleteNote(n.id).then(() => notesQ.refetch()) }}
                className="border-0 bg-transparent text-fs-text-muted hover:text-red-500 cursor-pointer text-xs transition-colors">
                删除
              </button>
            </div>
          ))}
        </div>

        {notesQ.data?.notes.length === 0 && (
          <p className="text-fs-text-muted text-sm text-center py-8">暂无笔记</p>
        )}
      </div>
    </div>
  )
}
