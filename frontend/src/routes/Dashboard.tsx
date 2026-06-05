import { useQuery } from '@tanstack/react-query'
import { api } from '../api/client'
import { TaskRow, type TaskData } from '../components/ui/TaskRow'
import { EventChip, type EventData } from '../components/ui/EventChip'
import { NoteCard, type NoteData } from '../components/ui/NoteCard'
import { MiniCalendar } from '../components/ui/MiniCalendar'

interface TodayData {
  todayTasks: TaskData[]
  overdueTasks: TaskData[]
  events: EventData[]
  recentNotes: NoteData[]
}

export default function Dashboard() {
  const { data, isLoading, error } = useQuery({
    queryKey: ['today'],
    queryFn: async () => {
      const res = await api.get<TodayData>('/api/today')
      return res.data
    },
  })

  if (isLoading) {
    return (
      <div className="grid grid-cols-[5fr_4fr_3fr] gap-5 max-[1120px]:grid-cols-2 max-[760px]:grid-cols-1">
        <SkeletonCol rows={4} />
        <SkeletonCol rows={3} extra />
        <SkeletonCol rows={3} />
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

  const taskTotal = data.todayTasks.length + data.overdueTasks.length

  return (
    <div className="dashboard-grid">
      <section className="metric-strip">
        <Metric label="今日概览" value={`${taskTotal}`} hint="待处理任务" />
        <Metric label="日程" value={`${data.events.length}`} hint="今天安排" />
        <Metric label="最近笔记" value={`${data.recentNotes.length}`} hint="可继续整理" />
      </section>

      <section className="surface-panel task-command-panel">
        <div className="panel-heading">
          <div>
            <span>任务流</span>
            <h2>今天要完成</h2>
          </div>
          <div className="segmented-tabs">
            <button className="is-active">全部</button>
            <button>待办</button>
            <button>已完成</button>
          </div>
        </div>

        <div className="inline-add-row">
          <span />
          <p>新增任务</p>
        </div>

        {data.todayTasks.length === 0 && data.overdueTasks.length === 0 ? (
          <p className="empty-copy">今天还没有任务</p>
        ) : (
          <>
            {data.overdueTasks.length > 0 && (
              <div className="task-section">
                <span>逾期</span>
                <div className="row-stack">
                  {data.overdueTasks.map((t) => <TaskRow key={t.id} task={t} onToggle={() => {}} />)}
                </div>
              </div>
            )}
            <div className="task-section">
              <span>今天</span>
              <div className="row-stack">
                {data.todayTasks.map((t) => <TaskRow key={t.id} task={t} onToggle={() => {}} />)}
              </div>
            </div>
          </>
        )}
      </section>

      <aside className="dashboard-side">
        <MiniCalendar />
        <section className="surface-panel agenda-panel">
          <div className="panel-heading is-compact">
            <div>
              <span>今天安排</span>
              <h2>日程时间线</h2>
            </div>
          </div>
          {data.events.length > 0 ? (
            <div className="timeline-list">
              {data.events.map((e) => <EventChip key={e.id} event={e} />)}
            </div>
          ) : (
            <p className="empty-copy">暂无日程</p>
          )}
        </section>
      </aside>

      <section className="surface-panel notes-rail">
        <div className="panel-heading is-compact">
          <div>
            <span>继续整理</span>
            <h2>最近笔记</h2>
          </div>
        </div>
        <div className="row-stack">
          {data.recentNotes.map((n) => <NoteCard key={n.id} note={n} />)}
        </div>
      </section>
    </div>
  )
}

function Metric({ label, value, hint }: { label: string; value: string; hint: string }) {
  return (
    <div className="metric-tile">
      <span>{label}</span>
      <strong>{value}</strong>
      <p>{hint}</p>
    </div>
  )
}

function SkeletonCol({ rows, extra }: { rows: number; extra?: boolean }) {
  return (
    <div className="grid gap-5">
      <div className="page-card grid gap-2">
        {Array.from({ length: rows }).map((_, i) => (
          <div key={i} className="h-10 bg-fs-hover rounded-md animate-pulse" />
        ))}
      </div>
      {extra && <div className="h-[200px] bg-fs-hover rounded-lg animate-pulse" />}
    </div>
  )
}
