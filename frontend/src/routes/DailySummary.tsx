import { useMemo } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import type { TaskSummaryItem } from '../api/summary'
import type { NoteData } from '../components/ui/NoteCard'
import type { TaskData } from '../components/ui/TaskRow'
import { useSummary } from '../hooks/useSummary'
import { useTodayOverview } from '../hooks/useTodayOverview'

function toDateInput(date: Date) {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

function getMonday(date: Date = new Date()) {
  const day = date.getDay()
  const diff = date.getDate() - day + (day === 0 ? -6 : 1)
  return toDateInput(new Date(date.getFullYear(), date.getMonth(), diff))
}

function todayDateInputValue() {
  return toDateInput(new Date())
}

function getMonthStart() {
  const date = new Date()
  return toDateInput(new Date(date.getFullYear(), date.getMonth(), 1))
}

export default function DailySummary() {
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const from = searchParams.get('from') || getMonday()
  const to = searchParams.get('to') || todayDateInputValue()
  const page = Number.parseInt(searchParams.get('page') || '1', 10)
  const summaryQuery = useSummary(from, to, page)
  const todayQuery = useTodayOverview()

  const activePreset = useMemo(() => {
    if (from === getMonday() && to === todayDateInputValue()) return 'week'
    if (from === getMonthStart() && to === todayDateInputValue()) return 'month'
    return null
  }, [from, to])

  function setRange(newFrom: string, newTo: string) {
    setSearchParams({ from: newFrom, to: newTo, page: '1' })
  }

  if (summaryQuery.isLoading || todayQuery.isLoading) {
    return <div className="summary-page"><section className="summary-metrics">{Array.from({ length: 4 }).map((_, index) => <div key={index} className="summary-metric animate-pulse" />)}</section></div>
  }

  if (summaryQuery.error || todayQuery.error) {
    return <div className="empty-state"><strong>加载失败</strong><p>每日总结暂时不可用，请稍后重试。</p></div>
  }

  const summary = summaryQuery.data?.summary
  const overview = todayQuery.data
  const completedCount = summaryQuery.data?.pagination.total ?? 0
  const todayTasks = (overview?.todayTasks ?? []).filter((task) => !isTaskDone(task))
  const overdueTasks = (overview?.overdueTasks ?? []).filter((task) => !isTaskDone(task))
  const recentNotes = overview?.recentNotes ?? []
  const denominator = completedCount + todayTasks.length + overdueTasks.length
  const completionRate = denominator > 0 ? Math.round((completedCount / denominator) * 100) : 0

  return (
    <div className="summary-page">
      <div className="summary-toolbar">
        <div>
          <span>复盘周期</span>
          <strong>{formatRange(from, to)}</strong>
        </div>
        <div className="segmented-tabs" role="tablist" aria-label="复盘周期">
          <button role="tab" aria-selected={activePreset === 'week'} className={activePreset === 'week' ? 'is-active' : ''} onClick={() => setRange(getMonday(), todayDateInputValue())}>本周</button>
          <button role="tab" aria-selected={activePreset === 'month'} className={activePreset === 'month' ? 'is-active' : ''} onClick={() => setRange(getMonthStart(), todayDateInputValue())}>本月</button>
        </div>
      </div>

      <section className="summary-metrics">
        <SummaryMetric label="周期完成" value={completedCount} hint={`${summary?.active_days ?? 0} 个活跃日`} />
        <SummaryMetric label="今日待办" value={todayTasks.length} hint="等待推进" />
        <SummaryMetric label="逾期任务" value={overdueTasks.length} hint="需要重新安排" tone="danger" />
        <SummaryMetric label="最近笔记" value={recentNotes.length} hint="可继续沉淀" tone="success" />
      </section>

      <section className="summary-board">
        <article className="surface-panel summary-completed-panel">
          <div className="summary-panel-heading">
            <div><span className="section-eyebrow">完成记录</span><h2>任务回顾</h2></div>
            <div className="summary-rate"><strong>{completionRate}%</strong><span>综合完成率</span></div>
          </div>
          <div className="summary-progress"><i><span style={{ width: `${completionRate}%` }} /></i></div>
          {!summary?.groups?.length ? (
            <div className="summary-empty"><strong>这个周期还没有完成记录</strong><p>完成任务后会按日期汇总到这里。</p></div>
          ) : (
            <div className="summary-group-list">
              {summary.groups.map((group) => (
                <section key={group.date} className="summary-date-group">
                  <header><time>{formatDay(group.date)}</time><span>{group.count} 项</span></header>
                  {group.tasks.map((task) => <CompletedTask key={task.id} task={task} onOpen={() => navigate('/tasks')} />)}
                </section>
              ))}
            </div>
          )}
        </article>

        <aside className="summary-side-stack">
          <section className="surface-panel summary-focus-panel">
            <div className="summary-panel-heading"><div><span className="section-eyebrow">今天</span><h2>需要关注</h2></div><span className="summary-count-badge">{overdueTasks.length + todayTasks.length}</span></div>
            {overdueTasks.length === 0 && todayTasks.length === 0 ? (
              <p className="summary-inline-empty">今天没有未完成事项。</p>
            ) : (
              <div className="summary-focus-list">
                {overdueTasks.slice(0, 3).map((task) => <FocusTask key={task.id} task={task} overdue onOpen={() => navigate('/tasks')} />)}
                {todayTasks.slice(0, Math.max(0, 4 - overdueTasks.length)).map((task) => <FocusTask key={task.id} task={task} onOpen={() => navigate('/tasks')} />)}
              </div>
            )}
            <button className="wide-secondary-action" type="button" onClick={() => navigate('/tasks')}>打开任务工作台</button>
          </section>

          <section className="surface-panel summary-notes-panel">
            <div className="summary-panel-heading"><div><span className="section-eyebrow">知识产出</span><h2>最近笔记</h2></div><span className="summary-count-badge">{recentNotes.length}</span></div>
            {recentNotes.length === 0 ? <p className="summary-inline-empty">还没有最近更新的笔记。</p> : (
              <div className="summary-note-list">{recentNotes.slice(0, 3).map((note) => <SummaryNote key={note.id} note={note} onOpen={() => navigate(`/editor/${encodeURIComponent(note.id)}`)} />)}</div>
            )}
            <button className="wide-secondary-action" type="button" onClick={() => navigate('/notes')}>查看笔记库</button>
          </section>
        </aside>
      </section>
    </div>
  )
}

function SummaryMetric({ label, value, hint, tone }: { label: string; value: number; hint: string; tone?: 'danger' | 'success' }) {
  return <div className={`summary-metric ${tone ? `is-${tone}` : ''}`}><span>{label}</span><strong>{value}</strong><p>{hint}</p></div>
}

function CompletedTask({ task, onOpen }: { task: TaskSummaryItem; onOpen: () => void }) {
  return <button type="button" className="summary-completed-row" onClick={onOpen}><span aria-hidden="true">✓</span><div><strong>{task.title}</strong><small>{task.project?.name || '未归属项目'}</small></div><i aria-hidden="true">›</i></button>
}

function FocusTask({ task, overdue = false, onOpen }: { task: TaskData; overdue?: boolean; onOpen: () => void }) {
  return <button type="button" className="summary-focus-row" onClick={onOpen}><span className={overdue ? 'is-overdue' : ''} aria-hidden="true" /><div><strong>{task.title}</strong><small>{overdue ? '已逾期' : '今日待办'}{task.project ? ` · ${task.project}` : ''}</small></div><i aria-hidden="true">›</i></button>
}

function SummaryNote({ note, onOpen }: { note: NoteData; onOpen: () => void }) {
  return <button type="button" className="summary-note-row" onClick={onOpen}><div><strong>{note.title || '未命名笔记'}</strong><small>{formatTimestamp(note.updated_at)}</small></div><i aria-hidden="true">›</i></button>
}

function isTaskDone(task: TaskData) {
  return task.done === 1 || task.occurrence_status === 'done'
}

function formatRange(from: string, to: string) {
  return `${from.replaceAll('-', '.')} - ${to.replaceAll('-', '.')}`
}

function formatDay(value: string) {
  const date = new Date(`${value}T00:00:00`)
  return new Intl.DateTimeFormat('zh-CN', { month: 'long', day: 'numeric', weekday: 'short' }).format(date)
}

function formatTimestamp(timestamp: number) {
  return new Intl.DateTimeFormat('zh-CN', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }).format(timestamp * 1000)
}
