import { type FormEvent, useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '../api/client'
import { listTaskProjects } from '../api/tasks'
import { TaskRow, type TaskData } from '../components/ui/TaskRow'
import { EventChip, type EventData } from '../components/ui/EventChip'
import { NoteCard, type NoteData } from '../components/ui/NoteCard'
import { MiniCalendar } from '../components/ui/MiniCalendar'
import { useCreateTask, useUpdateTask } from '../hooks/useTasks'
import { dateInputToUnix, todayDateInputValue } from '../utils/taskForm'
import { formatTaskProjectOption } from '../utils/taskProjects'

interface TodayData {
  todayTasks: TaskData[]
  overdueTasks: TaskData[]
  events: EventData[]
  recentNotes: NoteData[]
}

export default function Dashboard() {
  const [newTaskTitle, setNewTaskTitle] = useState('')
  const [newTaskDate, setNewTaskDate] = useState(() => todayDateInputValue())
  const [newTaskProjectID, setNewTaskProjectID] = useState('personal')
  const [taskFilter, setTaskFilter] = useState<'all' | 'todo' | 'done'>('all')
  const createTask = useCreateTask()
  const updateTask = useUpdateTask()

  const { data, isLoading, error } = useQuery({
    queryKey: ['today'],
    queryFn: async () => {
      const res = await api.get<TodayData>('/api/today')
      return res.data
    },
  })

  const { data: taskProjects = [] } = useQuery({
    queryKey: ['task-projects'],
    queryFn: listTaskProjects,
  })

  const selectedTaskProject = taskProjects.find((project) => project.id === newTaskProjectID)

  useEffect(() => {
    if (taskProjects.length === 0) return
    if (!taskProjects.some((project) => project.id === newTaskProjectID)) {
      setNewTaskProjectID(taskProjects[0].id)
    }
  }, [newTaskProjectID, taskProjects])

  async function handleAddTask(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const title = newTaskTitle.trim()
    if (!title) return

    if (!selectedTaskProject) return

    await createTask.mutateAsync({
      title,
      project_id: selectedTaskProject.id,
      due: dateInputToUnix(newTaskDate),
      planned_date: newTaskDate,
      horizon: 'week',
      scope: 'daily',
    })
    setNewTaskTitle('')
  }

  async function handleToggleTask(id: string) {
    const task = [...(data?.todayTasks ?? []), ...(data?.overdueTasks ?? [])].find((item) => item.id === id)
    if (!task) return
    await updateTask.mutateAsync({ id, done: task.done ? 0 : 1 })
  }

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

  const filterTasks = (tasks: TaskData[]) => {
    if (taskFilter === 'todo') return tasks.filter(t => !t.done)
    if (taskFilter === 'done') return tasks.filter(t => t.done)
    return tasks
  }
  const filteredOverdue = filterTasks(data.overdueTasks)
  const filteredToday = filterTasks(data.todayTasks)

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
            <button className={taskFilter === 'all' ? 'is-active' : ''} onClick={() => setTaskFilter('all')}>全部</button>
            <button className={taskFilter === 'todo' ? 'is-active' : ''} onClick={() => setTaskFilter('todo')}>待办</button>
            <button className={taskFilter === 'done' ? 'is-active' : ''} onClick={() => setTaskFilter('done')}>已完成</button>
          </div>
        </div>

        <form className="inline-create task-create-form" onSubmit={handleAddTask}>
          <input
            className="task-title-input"
            value={newTaskTitle}
            onChange={(event) => setNewTaskTitle(event.target.value)}
            placeholder="新增任务"
          />
          <select
            value={selectedTaskProject?.id ?? ''}
            onChange={(event) => setNewTaskProjectID(event.target.value)}
            aria-label="任务项目"
          >
            {taskProjects.length === 0 && <option value="">项目加载中</option>}
            {taskProjects.map((project) => (
              <option key={project.id} value={project.id}>{formatTaskProjectOption(project)}</option>
            ))}
          </select>
          <input
            type="date"
            value={newTaskDate}
            onChange={(event) => setNewTaskDate(event.target.value)}
            aria-label="任务日期"
            required
          />
          <button type="submit" disabled={!newTaskTitle.trim() || !selectedTaskProject || createTask.isPending}>
            {createTask.isPending ? '添加中...' : '添加'}
          </button>
        </form>

        {filteredOverdue.length === 0 && filteredToday.length === 0 ? (
          <p className="empty-copy">{taskFilter === 'done' ? '没有已完成的任务' : taskFilter === 'todo' ? '没有待办任务' : '今天还没有任务'}</p>
        ) : (
          <>
            {filteredOverdue.length > 0 && (
              <div className="task-section">
                <span>逾期</span>
                <div className="row-stack">
                  {filteredOverdue.map((t) => <TaskRow key={t.id} task={t} onToggle={handleToggleTask} />)}
                </div>
              </div>
            )}
            {filteredToday.length > 0 && (
              <div className="task-section">
                <span>今天</span>
                <div className="row-stack">
                  {filteredToday.map((t) => <TaskRow key={t.id} task={t} onToggle={handleToggleTask} />)}
                </div>
              </div>
            )}
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
