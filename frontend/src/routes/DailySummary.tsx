import { useMemo } from 'react'
import { useSearchParams, useNavigate, Link } from 'react-router-dom'
import { useSummary } from '../hooks/useSummary'
import type { DateGroup } from '../api/summary'

function getMonday(d: Date = new Date()): string {
  const day = d.getDay()
  const diff = d.getDate() - day + (day === 0 ? -6 : 1)
  const monday = new Date(d.getFullYear(), d.getMonth(), diff)
  return monday.toISOString().slice(0, 10)
}

function todayDateInputValue(): string {
  return new Date().toISOString().slice(0, 10)
}

function getMonthStart(): string {
  const d = new Date()
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-01`
}

export default function DailySummary() {
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const from = searchParams.get('from') || getMonday()
  const to = searchParams.get('to') || todayDateInputValue()
  const page = parseInt(searchParams.get('page') || '1', 10)

  const { data, isLoading, error } = useSummary(from, to, page)

  const activePreset = useMemo(() => {
    if (from === getMonday() && to === todayDateInputValue()) return 'week'
    if (from === getMonthStart() && to === todayDateInputValue()) return 'month'
    return null
  }, [from, to])

  function setRange(newFrom: string, newTo: string) {
    setSearchParams({ from: newFrom, to: newTo, page: '1' })
  }

  function setPage(newPage: number) {
    setSearchParams({ from, to, page: String(newPage) })
  }

  if (isLoading) {
    return (
      <div className="summary-grid">
        <div className="surface-panel animate-pulse h-48" />
        <div className="surface-panel animate-pulse h-96" />
      </div>
    )
  }

  if (error) {
    return (
      <div className="text-center py-12">
        <p className="text-fs-text-muted text-sm mb-3">加载失败</p>
        <button onClick={() => window.location.reload()} className="filter-pill is-active">重试</button>
      </div>
    )
  }

  if (!data) return null

  const { summary, pagination } = data
  const totalPages = Math.ceil(pagination.total / pagination.page_size)

  return (
    <div className="summary-page">
      {/* Date bar */}
      <div className="summary-date-bar">
        <div className="segmented-tabs">
          <button
            className={activePreset === 'week' ? 'is-active' : ''}
            onClick={() => setRange(getMonday(), todayDateInputValue())}
          >本周</button>
          <button
            className={activePreset === 'month' ? 'is-active' : ''}
            onClick={() => setRange(getMonthStart(), todayDateInputValue())}
          >本月</button>
        </div>
        <div className="summary-date-inputs">
          <label>
            <span>从</span>
            <input type="date" value={from}
              onChange={e => setRange(e.target.value, to)} />
          </label>
          <label>
            <span>到</span>
            <input type="date" value={to}
              onChange={e => setRange(from, e.target.value)} />
          </label>
        </div>
      </div>

      <div className="summary-grid">
        {/* Stats cards */}
        <div className="summary-stats">
          <div className="metric-tile">
            <span>已完成</span>
            <strong>{pagination.total}</strong>
            <p>项任务</p>
          </div>
          <div className="metric-tile">
            <span>活跃</span>
            <strong>{summary.active_days}</strong>
            <p>天有产出</p>
          </div>
          <div className="metric-tile">
            <span>参与</span>
            <strong>{summary.project_count}</strong>
            <p>个项目</p>
          </div>
        </div>

        {/* Task list */}
        <div className="summary-task-list">
          {summary.groups.length === 0 ? (
            <p className="empty-copy">这个时间段还没有完成的任务，试试调整日期范围</p>
          ) : (
            summary.groups.map((group: DateGroup) => (
              <div key={group.date} className="task-section">
                <span className="summary-date-heading">
                  📅 {group.date} · {group.count}项
                </span>
                <div className="row-stack">
                  {group.tasks.map(task => (
                    <details key={task.id} className="summary-task-card">
                      <summary className="summary-task-header">
                        <span className="summary-task-check">✓</span>
                        <strong className={task.done ? 'is-done' : ''}>{task.title}</strong>
                        {task.project && (
                          <button type="button" className="task-project-tag"
                            onClick={e => { e.preventDefault(); navigate('/tasks') }}>
                            {task.project.name}
                          </button>
                        )}
                      </summary>
                      <div className="summary-task-detail">
                        {task.note_id && (
                          <p>📄 来源笔记：<Link to={`/editor/${encodeURIComponent(task.note_id)}`} className="text-fs-accent hover:underline">查看</Link></p>
                        )}
                        {(!task.note_id && (!task.linked_notes || task.linked_notes.length === 0)) && (
                          <p className="text-fs-text-muted">无关联笔记</p>
                        )}
                        {task.linked_notes && task.linked_notes.length > 0 && (
                          <div>
                            <span>📎 项目笔记：</span>
                            {task.linked_notes.map(note => (
                              <Link key={note.id} to={`/editor/${encodeURIComponent(note.id)}`}
                                className="text-fs-accent hover:underline ml-1">{note.title}</Link>
                            ))}
                          </div>
                        )}
                      </div>
                    </details>
                  ))}
                </div>
              </div>
            ))
          )}
        </div>
      </div>

      {/* Pagination */}
      {totalPages > 1 && (
        <div className="summary-pagination">
          <button disabled={page <= 1} onClick={() => setPage(page - 1)} className="filter-pill">上一页</button>
          <span className="text-fs-text-muted">第 {page}/{totalPages} 页</span>
          <button disabled={page >= totalPages} onClick={() => setPage(page + 1)} className="filter-pill">下一页</button>
        </div>
      )}
    </div>
  )
}
