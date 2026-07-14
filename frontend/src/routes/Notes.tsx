import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { deleteNote, getNotes, type Note } from '../api/notes'
import { useCreateNote } from '../hooks/useNotes'
import { listTaskProjects } from '../api/tasks'
import { formatTaskProjectOption } from '../utils/taskProjects'
import { markdownToPlainText } from '../utils/noteText'
import { SyncSettingsPanel } from '../components/sync/SyncSettingsPanel'

export default function Notes() {
  const navigate = useNavigate()
  const [sort, setSort] = useState<'recent' | 'az'>('recent')
  const [syncOpen, setSyncOpen] = useState(false)
  const [syncMounted, setSyncMounted] = useState(false)
  const [query, setQuery] = useState('')
  const [projectID, setProjectID] = useState('')
  const [unassigned, setUnassigned] = useState(false)
  const [activeTag, setActiveTag] = useState('')
  const [selectedNoteID, setSelectedNoteID] = useState('')

  const { data: allProjects = [] } = useQuery({
    queryKey: ['task-projects'],
    queryFn: listTaskProjects,
  })
  const createNote = useCreateNote()
  const notesQ = useQuery({
    queryKey: ['notes', sort],
    queryFn: () => getNotes({ sort, page_size: 100 }),
  })

  const notes = notesQ.data?.notes ?? []
  const tagCounts = useMemo(() => {
    const counts = new Map<string, number>()
    notes.forEach((note) => {
      parseTags(note.tags).forEach((tag) => counts.set(tag, (counts.get(tag) ?? 0) + 1))
    })
    return [...counts.entries()].sort((a, b) => b[1] - a[1])
  }, [notes])

  const filteredNotes = useMemo(() => {
    const keyword = query.trim().toLowerCase()
    return notes.filter((note) => {
      const projects = getNoteProjects(note)
      const matchesProject = unassigned
        ? projects.length === 0
        : !projectID || projects.some((project) => project.id === projectID)
      const matchesTag = !activeTag || parseTags(note.tags).includes(activeTag)
      const matchesQuery =
        !keyword || `${note.title} ${markdownToPlainText(note.body ?? '')}`.toLowerCase().includes(keyword)
      return matchesProject && matchesTag && matchesQuery
    })
  }, [activeTag, notes, projectID, query, unassigned])

  const selectedNote =
    filteredNotes.find((note) => note.id === selectedNoteID) ?? filteredNotes[0]
  const selectedTags = selectedNote ? parseTags(selectedNote.tags) : []
  const unassignedCount = notes.filter((note) => getNoteProjects(note).length === 0).length
  const scopeTitle = activeTag
    ? `标签：${activeTag}`
    : unassigned
      ? '未归属'
      : allProjects.find((project) => project.id === projectID)?.name ?? '全部笔记'

  async function handleCreateNote() {
    const note = await createNote.mutateAsync({
      title: '未命名笔记',
      body: '',
      folder_id: '__uncategorized',
      tags: activeTag ? JSON.stringify([activeTag]) : '[]',
      project_ids: projectID ? [projectID] : undefined,
    })
    navigate(`/editor/${note.id}`)
  }

  async function handleDeleteNote(note: Note) {
    if (!window.confirm(`确定删除“${note.title}”吗？`)) return
    await deleteNote(note.id)
    if (selectedNoteID === note.id) setSelectedNoteID('')
    await notesQ.refetch()
  }

  function selectProject(id = '', showUnassigned = false) {
    setProjectID(id)
    setUnassigned(showUnassigned)
    setActiveTag('')
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
      <div className="page-local-actions notes-page-actions">
        <button type="button" onClick={handleOpenSyncSettings} className="secondary-action">
          <SyncIcon />
          同步设置
        </button>
        <button onClick={handleCreateNote} disabled={createNote.isPending} className="primary-action">
          <PlusIcon />
          {createNote.isPending ? '创建中' : '新建笔记'}
        </button>
      </div>

      <div className="notes-workspace">
        <aside className="surface-panel note-filter-panel">
          <div className="note-filter-heading">
            <div>
              <span>资料库</span>
              <strong>{notes.length}</strong>
            </div>
            <p>篇笔记</p>
          </div>

          <section>
            <h2>项目</h2>
            <button
              className={!projectID && !unassigned && !activeTag ? 'is-active' : ''}
              onClick={() => selectProject()}
            >
              <span><LibraryIcon />全部笔记</span>
              <em>{notes.length}</em>
            </button>
            <button className={unassigned ? 'is-active' : ''} onClick={() => selectProject('', true)}>
              <span><FolderIcon />未归属</span>
              <em>{unassignedCount}</em>
            </button>
            {allProjects.map((project) => {
              const count = notes.filter((note) =>
                getNoteProjects(note).some((noteProject) => noteProject.id === project.id)
              ).length
              return (
                <button
                  key={project.id}
                  className={projectID === project.id ? 'is-active' : ''}
                  onClick={() => selectProject(project.id)}
                  title={formatTaskProjectOption(project)}
                >
                  <span><ProjectIcon />{project.name}</span>
                  <em>{count}</em>
                </button>
              )
            })}
          </section>

          <section>
            <h2>标签</h2>
            {tagCounts.length > 0 ? tagCounts.map(([tag, count]) => (
              <button
                key={tag}
                className={activeTag === tag ? 'is-active' : ''}
                onClick={() => {
                  setActiveTag(activeTag === tag ? '' : tag)
                  setProjectID('')
                  setUnassigned(false)
                }}
              >
                <span><TagIcon />{tag}</span>
                <em>{count}</em>
              </button>
            )) : <p className="note-filter-empty">暂无标签</p>}
          </section>
        </aside>

        <section className="surface-panel notes-list-panel">
          <header className="notes-list-header">
            <div>
              <span>{scopeTitle}</span>
              <strong>{filteredNotes.length} 篇</strong>
            </div>
            <div className="segmented-tabs note-tabs" aria-label="笔记排序">
              <button className={sort === 'recent' ? 'is-active' : ''} onClick={() => setSort('recent')}>
                最近更新
              </button>
              <button className={sort === 'az' ? 'is-active' : ''} onClick={() => setSort('az')}>
                标题排序
              </button>
            </div>
          </header>

          <label className="notes-search">
            <SearchIcon />
            <input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="搜索标题或正文"
              aria-label="搜索笔记"
            />
            {query && (
              <button type="button" onClick={() => setQuery('')} aria-label="清空搜索">
                <CloseIcon />
              </button>
            )}
          </label>

          <div className="note-card-list">
            {filteredNotes.map((note) => {
              const isSelected = selectedNote?.id === note.id
              const noteProjects = getNoteProjects(note)
              const projectLabel = noteProjects[0]
                ? noteProjects[0].name
                : '未归属'
              return (
                <article key={note.id} className={`note-library-card ${isSelected ? 'is-selected' : ''}`}>
                  <button
                    type="button"
                    className="note-library-select"
                    onClick={() => setSelectedNoteID(note.id)}
                    onDoubleClick={() => navigate(`/editor/${encodeURIComponent(note.id)}`)}
                    aria-pressed={isSelected}
                  >
                    <span className="note-row-heading">
                      <strong>{note.title || '未命名笔记'}</strong>
                      <time>{formatRelativeDate(note.updated_at)}</time>
                    </span>
                    <span className="note-row-preview">{getPreview(note)}</span>
                    <span className="note-row-meta">
                      <em>{projectLabel}</em>
                      {parseTags(note.tags).slice(0, 2).map((tag) => <i key={tag}>#{tag}</i>)}
                      <small>{countCharacters(note.body ?? '')} 字</small>
                    </span>
                  </button>
                  <button
                    type="button"
                    className="note-delete-button"
                    onClick={() => void handleDeleteNote(note)}
                    aria-label={`删除笔记 ${note.title}`}
                    title="删除笔记"
                  >
                    <TrashIcon />
                  </button>
                </article>
              )
            })}
          </div>

          {filteredNotes.length === 0 && (
            <div className="notes-empty-state">
              <SearchEmptyIcon />
              <strong>没有找到笔记</strong>
              <p>试试其他项目、标签或搜索词。</p>
            </div>
          )}
        </section>

        <aside className="surface-panel note-detail-panel">
          {selectedNote ? (
            <>
              <div className="note-detail-heading">
                <div>
                  <span className="note-detail-kicker">笔记预览</span>
                  <h2>{selectedNote.title || '未命名笔记'}</h2>
                  <p>最后更新于 {formatFullDate(selectedNote.updated_at)}</p>
                </div>
                <button
                  type="button"
                  aria-label="打开笔记编辑器"
                  title="打开笔记编辑器"
                  onClick={() => navigate(`/editor/${encodeURIComponent(selectedNote.id)}`)}
                >
                  <OpenIcon />
                </button>
              </div>

              <div className={`note-detail-body ${(selectedNote.body ?? '').trim() ? '' : 'is-empty'}`}>
                {markdownToPlainText(selectedNote.body ?? '') || '这篇笔记还没有正文。'}
              </div>

              <div className="note-properties">
                <h3>信息</h3>
                <dl>
                  <div>
                    <dt>项目</dt>
                    <dd>{getNoteProjects(selectedNote).length > 0
                      ? getNoteProjects(selectedNote).map((project) => project.name).join('、')
                      : '未归属'}</dd>
                  </div>
                  <div>
                    <dt>标签</dt>
                    <dd>{selectedTags.length > 0 ? selectedTags.map((tag) => `#${tag}`).join(' ') : '无标签'}</dd>
                  </div>
                  <div>
                    <dt>字数</dt>
                    <dd>{countCharacters(selectedNote.body ?? '')}</dd>
                  </div>
                </dl>
              </div>

              <button className="wide-secondary-action note-edit-action" onClick={() => navigate(`/editor/${encodeURIComponent(selectedNote.id)}`)}>
                <EditIcon />
                打开编辑
              </button>
            </>
          ) : (
            <div className="note-detail-empty">
              <NoteIcon />
              <strong>选择一篇笔记</strong>
            </div>
          )}
        </aside>
      </div>
      {syncMounted && <SyncSettingsPanel open={syncOpen} onClose={() => setSyncOpen(false)} />}
    </div>
  )
}

function parseTags(raw?: string | null) {
  try {
    const parsed = JSON.parse(raw || '[]')
    return Array.isArray(parsed) ? parsed.map(String).filter(Boolean) : []
  } catch {
    return []
  }
}

function getPreview(note: Note) {
  const body = markdownToPlainText(note.body ?? '')
  return body || '暂无正文内容'
}

function countCharacters(value: string) {
  return markdownToPlainText(value).replace(/\s/g, '').length
}

function getNoteProjects(note: Note) {
  return note.projects ?? []
}

function formatRelativeDate(timestamp: number) {
  const date = new Date(timestamp * 1000)
  const today = new Date()
  if (date.toDateString() === today.toDateString()) {
    return date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
  }
  return date.toLocaleDateString('zh-CN', { month: 'numeric', day: 'numeric' })
}

function formatFullDate(timestamp: number) {
  return new Date(timestamp * 1000).toLocaleString('zh-CN', {
    year: 'numeric',
    month: 'long',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

function Icon({ children }: { children: React.ReactNode }) {
  return <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">{children}</svg>
}

function SearchIcon() { return <Icon><circle cx="11" cy="11" r="7" /><path d="m20 20-3.6-3.6" /></Icon> }
function PlusIcon() { return <Icon><path d="M12 5v14M5 12h14" /></Icon> }
function SyncIcon() { return <Icon><path d="M20 7h-5V2" /><path d="M4 17h5v5" /><path d="M5.6 9A7 7 0 0 1 17 5l3 2M18.4 15A7 7 0 0 1 7 19l-3-2" /></Icon> }
function LibraryIcon() { return <Icon><path d="M4 5.5h16v14H4z" /><path d="M8 9h8M8 13h8M8 17h5" /></Icon> }
function FolderIcon() { return <Icon><path d="M3 6.5h7l2 2h9v10H3z" /></Icon> }
function ProjectIcon() { return <Icon><path d="M5 4h14v16H5z" /><path d="M9 8h6M9 12h6M9 16h4" /></Icon> }
function TagIcon() { return <Icon><path d="M20 13 13 20l-9-9V4h7z" /><circle cx="8.5" cy="8.5" r="1" /></Icon> }
function CloseIcon() { return <Icon><path d="m7 7 10 10M17 7 7 17" /></Icon> }
function TrashIcon() { return <Icon><path d="M4 7h16M9 7V4h6v3M7 7l1 13h8l1-13M10 11v5M14 11v5" /></Icon> }
function OpenIcon() { return <Icon><path d="M14 4h6v6M20 4l-9 9" /><path d="M18 13v6H5V6h6" /></Icon> }
function EditIcon() { return <Icon><path d="m4 20 4.5-1 10-10-3.5-3.5-10 10zM13.5 7 17 10.5" /></Icon> }
function NoteIcon() { return <Icon><path d="M6 3h9l4 4v14H6z" /><path d="M14 3v5h5M9 13h6M9 17h4" /></Icon> }
function SearchEmptyIcon() { return <Icon><circle cx="10" cy="10" r="6" /><path d="m14.5 14.5 5 5M8 10h4" /></Icon> }

function Skeleton() {
  return (
    <div className="notes-workspace notes-skeleton">
      <aside className="surface-panel note-filter-panel" />
      <section className="surface-panel notes-list-panel">
        {Array.from({ length: 5 }).map((_, index) => <div key={index} className="note-skeleton-row" />)}
      </section>
      <aside className="surface-panel note-detail-panel" />
    </div>
  )
}
