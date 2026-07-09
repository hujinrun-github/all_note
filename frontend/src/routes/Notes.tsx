import { useMemo, useState } from 'react'
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
  const [syncMounted, setSyncMounted] = useState(false)
  const [query, setQuery] = useState('')
  const [projectID, setProjectID] = useState('')
  const [unassigned, setUnassigned] = useState(false)
  const { data: allProjects = [] } = useQuery({
    queryKey: ['task-projects'],
    queryFn: listTaskProjects,
  })
  const createNote = useCreateNote()

  const notesQ = useQuery({
    queryKey: ['notes', sort, projectID, unassigned],
    queryFn: () =>
      getNotes({
        sort,
        project_id: projectID || undefined,
        unassigned: unassigned || undefined,
      }),
  })

  const notes = notesQ.data?.notes ?? []
  const filteredNotes = useMemo(() => {
    const keyword = query.trim().toLowerCase()
    if (!keyword) return notes
    return notes.filter((note) => note.title.toLowerCase().includes(keyword))
  }, [notes, query])
  const selectedNote = filteredNotes[0]
  const selectedTags = selectedNote ? parseTags(selectedNote.tags) : []

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

  function handleOpenSyncSettings() {
    setSyncMounted(true)
    setSyncOpen(true)
  }

  if (notesQ.isLoading) return <Skeleton />
  if (notesQ.error) {
    return (
      <div className="empty-state">
        <strong>加载失败</strong>
        <p>笔记库暂时不可用，请稍后重试。</p>
      </div>
    )
  }

  return (
    <div className="notes-page">
      <div className="page-local-actions">
        <button type="button" onClick={handleOpenSyncSettings} className="secondary-action">
          同步设置
        </button>
        <button onClick={handleCreateNote} disabled={createNote.isPending} className="primary-action">
          ＋ 新建笔记
        </button>
      </div>

      <div className="notes-workspace">
        <aside className="surface-panel note-filter-panel">
          <section>
            <h2>项目</h2>
            <button
              className={!projectID && !unassigned ? 'is-active' : ''}
              onClick={() => {
                setProjectID('')
                setUnassigned(false)
              }}
            >
              <span>全部笔记</span>
              <em>{notesQ.data?.pagination.total ?? 0}</em>
            </button>
            <button
              className={unassigned ? 'is-active' : ''}
              onClick={() => {
                setProjectID('')
                setUnassigned(true)
              }}
            >
              <span>未归属</span>
              <em>0</em>
            </button>
            {allProjects.map((project) => (
              <button
                key={project.id}
                className={projectID === project.id ? 'is-active' : ''}
                onClick={() => {
                  setProjectID(project.id)
                  setUnassigned(false)
                }}
                title={formatTaskProjectOption(project)}
              >
                <span>{formatTaskProjectOption(project)}</span>
                <em>{project.id === projectID ? filteredNotes.length : 0}</em>
              </button>
            ))}
          </section>
          <section>
            <h2>标签</h2>
            <button>
              <span>学习</span>
              <em>{selectedTags.includes('学习') ? 1 : 0}</em>
            </button>
            <button>
              <span>想法</span>
              <em>{selectedTags.includes('想法') ? 1 : 0}</em>
            </button>
            <button className="link-like">＋ 新建标签</button>
          </section>
          <section>
            <button>
              <span>回收站</span>
            </button>
          </section>
        </aside>

        <section className="surface-panel notes-list-panel">
          <div className="search-command">
            <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索笔记..." />
          </div>
          <div className="segmented-tabs note-tabs">
            <button className={sort === 'recent' ? 'is-active' : ''} onClick={() => setSort('recent')}>
              最近更新
            </button>
            <button className={sort !== 'recent' ? 'is-active' : ''} onClick={() => setSort('az')}>
              按项目
            </button>
            <button type="button">有任务关联</button>
          </div>

          <div className="note-card-list">
            {filteredNotes.map((note, index) => (
              <article
                key={note.id}
                className={`note-library-card ${index === 0 ? 'is-selected' : ''}`}
                onClick={() => navigate(`/editor/${encodeURIComponent(note.id)}`)}
              >
                <div>
                  <strong>{note.title}</strong>
                  <time>{new Date(note.updated_at * 1000).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })}</time>
                </div>
                <p>{index === 0 ? '这是我的第一篇笔记，记录一些想法和学习内容...' : '从收件箱整理来的想法会出现在这里。'}</p>
                <footer>
                  <span>{new Date(note.updated_at * 1000).toLocaleDateString('zh-CN')}</span>
                  <em>{note.projects?.[0] ? formatTaskProjectOption(note.projects[0]) : 'Personal'}</em>
                  <span>128 字</span>
                  <button
                    type="button"
                    onClick={(event) => {
                      event.stopPropagation()
                      void deleteNote(note.id).then(() => notesQ.refetch())
                    }}
                  >
                    删除
                  </button>
                </footer>
              </article>
            ))}
          </div>

          {filteredNotes.length === 0 && <p className="empty-copy">从收件箱整理来的想法会出现在这里。</p>}
        </section>

        <aside className="surface-panel note-detail-panel">
          {selectedNote ? (
            <>
              <div className="note-detail-heading">
                <div>
                  <h2>{selectedNote.title}</h2>
                  <p>
                    <em>{selectedNote.projects?.[0] ? formatTaskProjectOption(selectedNote.projects[0]) : 'Personal'}</em>
                    <span>{new Date(selectedNote.updated_at * 1000).toLocaleString('zh-CN')}</span>
                  </p>
                </div>
                <button type="button" aria-label="笔记菜单">
                  ▣
                </button>
              </div>
              <div className="note-detail-body">
                <p>这是我的第一篇笔记，记录一些想法和学习内容。</p>
                <p>通过笔记，我可以更好地沉淀知识、连接任务和日程，让灵感不再散落在不同地方。</p>
              </div>
              <div className="note-links">
                <h3>关联内容</h3>
                <span>关联任务 <em>0</em></span>
                <span>关联日程 <em>0</em></span>
              </div>
              <button className="wide-secondary-action" onClick={() => navigate(`/editor/${encodeURIComponent(selectedNote.id)}`)}>
                编辑笔记
              </button>
            </>
          ) : (
            <p className="empty-copy">选择一篇笔记查看详情</p>
          )}
        </aside>
      </div>
      {syncMounted && <SyncSettingsPanel open={syncOpen} onClose={() => setSyncOpen(false)} />}
    </div>
  )
}

function parseTags(raw: string) {
  try {
    const parsed = JSON.parse(raw || '[]')
    return Array.isArray(parsed) ? parsed.map(String) : []
  } catch {
    return []
  }
}

function Skeleton() {
  return (
    <div className="notes-workspace">
      <aside className="surface-panel note-filter-panel" />
      <section className="surface-panel notes-list-panel">
        <div className="grid gap-2">
          {Array.from({ length: 4 }).map((_, index) => (
            <div key={index} className="h-20 bg-fs-hover rounded-lg animate-pulse" />
          ))}
        </div>
      </section>
      <aside className="surface-panel note-detail-panel" />
    </div>
  )
}
