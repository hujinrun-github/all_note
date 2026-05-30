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
      <div className="grid grid-cols-[5fr_4fr_3fr] gap-4 max-[1120px]:grid-cols-2 max-[760px]:grid-cols-1">
        <div className="grid gap-4"><SkeletonBlock rows={4} /></div>
        <div className="grid gap-4"><SkeletonBlock rows={3} /><div className="h-[200px] bg-fs-hover rounded-lg animate-pulse" /></div>
        <div className="grid gap-4"><SkeletonBlock rows={3} /></div>
      </div>
    )
  }

  if (error) {
    return <div className="text-red-500 text-sm">加载失败 <button onClick={() => window.location.reload()} className="underline ml-2">重试</button></div>
  }

  if (!data) return null

  return (
    <div className="grid grid-cols-[5fr_4fr_3fr] gap-4 max-[1120px]:grid-cols-2 max-[760px]:grid-cols-1">
      {/* Column 1: Tasks */}
      <div className="grid gap-4 content-start">
        <div className="flex justify-between items-center mb-3">
          <h2 className="text-sm font-semibold uppercase tracking-wider text-fs-text-muted">今日任务</h2>
          <span className="text-xs text-fs-text-muted tabular-nums font-mono">{data.todayTasks.length}</span>
        </div>
        {data.todayTasks.length === 0 && data.overdueTasks.length === 0 ? (
          <p className="text-fs-text-muted text-sm text-center py-4">今天还没有任务，点击右上角"快速捕获"开始</p>
        ) : (
          <>
            {data.overdueTasks.length > 0 && (
              <div className="mb-2">
                <span className="text-fs-warning text-[11px] font-semibold uppercase tracking-wider">逾期</span>
                <div className="grid gap-1.5 mt-1">
                  {data.overdueTasks.map((t) => <TaskRow key={t.id} task={t} onToggle={() => {}} />)}
                </div>
              </div>
            )}
            <div className="grid gap-1.5">
              {data.todayTasks.map((t) => <TaskRow key={t.id} task={t} onToggle={() => {}} />)}
            </div>
          </>
        )}
      </div>

      {/* Column 2: Calendar + Schedule */}
      <div className="grid gap-4 content-start">
        <div className="flex justify-between items-center mb-3">
          <h2 className="text-sm font-semibold uppercase tracking-wider text-fs-text-muted">日程</h2>
          <span className="text-xs text-fs-text-muted tabular-nums font-mono">{data.events.length}</span>
        </div>
        <MiniCalendar />
        {data.events.length > 0 && (
          <div className="grid gap-2 mt-2">
            {data.events.map((e) => <EventChip key={e.id} event={e} />)}
          </div>
        )}
      </div>

      {/* Column 3: Recent Notes */}
      <div className="grid gap-4 content-start">
        <div className="flex justify-between items-center mb-3">
          <h2 className="text-sm font-semibold uppercase tracking-wider text-fs-text-muted">最近笔记</h2>
          <span className="text-xs text-fs-text-muted tabular-nums font-mono">{data.recentNotes.length}</span>
        </div>
        <div className="grid gap-2">
          {data.recentNotes.map((n) => <NoteCard key={n.id} note={n} />)}
        </div>
      </div>
    </div>
  )
}

function SkeletonBlock({ rows }: { rows: number }) {
  return (
    <div className="grid gap-2">
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className="h-10 bg-fs-hover rounded-md animate-pulse" />
      ))}
    </div>
  )
}
