import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { deleteNote, getNotes } from '../api/notes'
import { useCreateNote } from '../hooks/useNotes'
import { listTaskProjects } from '../api/tasks'
import { formatTaskProjectOption } from '../utils/taskProjects'
import { SyncSettingsPanel } from '../components/sync/SyncSettingsPanel'

export default function Notes() {
  const navigate = useNavigate()
  const [sort, setSort] = useState('recent')
  const [syncOpen, setSyncOpen] = useState(false)
  const [projectID, setProjectID] = useState('')
  const [unassigned, setUnassigned] = useState(false)
  const { data: allProjects = [] } = useQuery({
    queryKey: ['task-projects'],
    queryFn: listTaskProjects,
  })
  const createNote = useCreateNote()

  const notesQ = useQuery({
    queryKey: ['notes', sort, projectID, unassigned],
    queryFn: () => getNotes({
      sort,
      project_id: projectID || undefined,
      unassigned: unassigned || undefined,
    }),
  })

  async function handleCreateNote() {
    const note = await createNote.mutateAsync({
      title: '未命名笔记',
      body: '',
      folder_id: '__uncategorized',
      tags: '[]',
      project_ids: projectID ? [projectID] : undefined,
    })
    navigate(`/editor/${note.id}`)
  }

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
        <h4 className="text-xs font-semibold text-fs-text-muted mb-2">项目</h4>
        <button
          className={`block w-full text-left px-2 py-1 rounded text-sm ${!projectID && !unassigned ? 'bg-fs-accent/10 text-fs-accent' : ''}`}
          onClick={() => { setProjectID(''); setUnassigned(false) }}
        >
          全部笔记
        </button>
        <button
          className={`block w-full text-left px-2 py-1 rounded text-sm ${unassigned ? 'bg-fs-accent/10 text-fs-accent' : ''}`}
          onClick={() => { setProjectID(''); setUnassigned(true) }}
        >
          未归属项目
        </button>
        {allProjects.map(p => (
          <button
            key={p.id}
            className={`block w-full text-left px-2 py-1 rounded text-sm truncate ${projectID === p.id ? 'bg-fs-accent/10 text-fs-accent' : ''}`}
            onClick={() => { setProjectID(p.id); setUnassigned(false) }}
            title={formatTaskProjectOption(p)}
          >
            {formatTaskProjectOption(p)}
          </button>
        ))}
        <div className="rail-summary">
          <span>最近更新</span>
          <strong>{notesQ.data?.pagination.total ?? 0}</strong>
          <p>按项目整理笔记</p>
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
            <button onClick={handleCreateNote} disabled={createNote.isPending} className="primary-action">
              {createNote.isPending ? '创建中...' : '新建笔记'}
            </button>
          </div>
        </div>

        <div className="list-rows">
          {(notesQ.data?.notes ?? []).map((note) => (
            <div key={note.id} className="rich-row group" onClick={() => navigate(`/editor/${encodeURIComponent(note.id)}`)}>
              <div className="min-w-0">
                <strong className="text-sm font-medium text-fs-text block truncate">{note.title}</strong>
                <div className="text-fs-text-muted text-xs mt-1">
                  {new Date(note.updated_at * 1000).toLocaleDateString('zh-CN')}
                </div>
                {note.projects && note.projects.length > 0 && (
                  <div className="chip-list mt-1">
                    {note.projects.slice(0, 2).map(p => (
                      <em key={p.id}>{formatTaskProjectOption(p)}</em>
                    ))}
                    {note.projects.length > 2 && (
                      <em>+{note.projects.length - 2}</em>
                    )}
                  </div>
                )}
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

      {syncOpen && <SyncSettingsPanel onClose={() => setSyncOpen(false)} />}
    </div>
  )
}

function Skeleton() {
  return (
    <div className="list-workspace">
      <aside className="filter-rail">
        {Array.from({ length: 3 }).map((_, index) => (
          <div key={index} className="h-9 bg-fs-hover rounded-lg animate-pulse" />
        ))}
      </aside>
      <section className="surface-panel list-panel">
        <div className="grid gap-2">
          {Array.from({ length: 5 }).map((_, index) => (
            <div key={index} className="h-14 bg-fs-hover rounded-lg animate-pulse" />
          ))}
        </div>
      </section>
    </div>
  )
}
