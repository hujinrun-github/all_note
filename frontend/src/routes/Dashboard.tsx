import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../api/client'
import { completeOccurrence, listTaskProjects, reopenOccurrence } from '../api/tasks'
import { TaskRow, type TaskData } from '../components/ui/TaskRow'
import { EventChip, type EventData } from '../components/ui/EventChip'
import { NoteCard, type NoteData } from '../components/ui/NoteCard'
import { useUpdateTask } from '../hooks/useTasks'
import { todayDateInputValue } from '../utils/taskForm'
import { formatTaskProjectOption } from '../utils/taskProjects'
import { TaskDomainGate } from '../components/taskDomain/TaskDomainGate'
import DashboardV2 from './DashboardV2'

interface TodayData {
  todayTasks: TaskData[]
  overdueTasks: TaskData[]
  events: EventData[]
  recentNotes: NoteData[]
}

type TaskFlowTab = 'overdue' | 'next' | 'done'

export default function Dashboard() {
  return <TaskDomainGate legacy={<LegacyDashboard />} v2={<DashboardV2 />} />
}

export function LegacyDashboard() {
  const navigate = useNavigate()
  const [newTaskDate, setNewTaskDate] = useState(() => todayDateInputValue())
  const [newTaskProjectID, setNewTaskProjectID] = useState('personal')
  const [taskFilter, setTaskFilter] = useState<TaskFlowTab>('next')
  const updateTask = useUpdateTask()
  const queryClient = useQueryClient()

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

  async function handleToggleTask(id: string) {
    const task = [...(data?.todayTasks ?? []), ...(data?.overdueTasks ?? [])].find((item) => item.id === id)
    if (!task) return
    await updateTask.mutateAsync({ id, done: task.done ? 0 : 1 })
  }

  async function handleOccurrenceToggle(taskId: string, date: string, currentStatus: string) {
    if (currentStatus === 'done') {
      await reopenOccurrence(taskId, date)
    } else {
      await completeOccurrence(taskId, date)
    }
    queryClient.invalidateQueries({ queryKey: ['today'] })
  }

  if (isLoading) {
    return (
      <div className="dashboard-grid">
        <section className="metric-strip">
          {Array.from({ length: 4 }).map((_, index) => (
            <div key={index} className="metric-tile animate-pulse">
              <span>加载中</span>
              <strong>--</strong>
              <p>同步数据</p>
            </div>
          ))}
        </section>
        <section className="surface-panel task-command-panel" />
        <aside className="dashboard-side">
          <section className="surface-panel agenda-panel" />
          <section className="surface-panel notes-rail" />
        </aside>
      </div>
    )
  }

  if (error) {
    return (
      <div className="empty-state">
        <strong>加载失败</strong>
        <p>今日视图暂时不可用，请稍后重试。</p>
        <button onClick={() => window.location.reload()} className="filter-pill is-active">
          重试
        </button>
      </div>
    )
  }

  if (!data) return null

  const isTaskDone = (task: TaskData) => task.done === 1 || task.occurrence_status === 'done'
  const overdueTasks = data.overdueTasks.filter((task) => !isTaskDone(task))
  const nextTasks = data.todayTasks.filter((task) => !isTaskDone(task))
  const completedTasks = data.todayTasks.filter(isTaskDone)
  const activeTasks = taskFilter === 'overdue' ? overdueTasks : taskFilter === 'next' ? nextTasks : completedTasks
  const doneToday = completedTasks.length
  const projectLabel = selectedTaskProject ? formatTaskProjectOption(selectedTaskProject) : 'Personal'
  const emptyCopy =
    taskFilter === 'overdue'
      ? '暂无未完成的逾期任务'
      : taskFilter === 'done'
        ? '暂无已完成任务'
        : '今天还没有待推进任务'
  const sectionLabel = taskFilter === 'overdue' ? '已逾期' : taskFilter === 'done' ? '已完成' : '接下来'

  return (
    <div className="dashboard-grid">
      <section className="metric-strip">
        <Metric label="逾期任务" value={String(overdueTasks.length)} hint="需要处理" tone="danger" />
        <Metric label="今日任务" value={String(nextTasks.length)} hint="待完成" />
        <Metric label="今日日程" value={String(data.events.length)} hint="待安排" />
        <Metric label="最近笔记" value={String(data.recentNotes.length)} hint="可继续整理" />
      </section>

      <section className="surface-panel task-command-panel">
        <div className="panel-heading">
          <div>
            <h2>任务流</h2>
          </div>
          <div className="segmented-tabs">
            <button type="button" className={taskFilter === 'overdue' ? 'is-active' : ''} onClick={() => setTaskFilter('overdue')}>
              已逾期 {overdueTasks.length}
            </button>
            <button type="button" className={taskFilter === 'next' ? 'is-active' : ''} onClick={() => setTaskFilter('next')}>
              接下来 {nextTasks.length}
            </button>
            <button type="button" className={taskFilter === 'done' ? 'is-active' : ''} onClick={() => setTaskFilter('done')}>
              已完成 {doneToday}
            </button>
          </div>
        </div>

        <div className="dashboard-task-settings">
          <span>项目：{projectLabel}</span>
          <span>日期：{newTaskDate}</span>
          <button type="button" onClick={() => setNewTaskDate(todayDateInputValue())}>
            今天
          </button>
        </div>

        {activeTasks.length === 0 ? (
          <p className="empty-copy">{emptyCopy}</p>
        ) : (
          <div className="task-section">
            <span>{sectionLabel}</span>
            <div className="row-stack">
              {activeTasks.map((task) => (
                <TaskRow
                  key={`${task.id}-${task.occurrence_date ?? 'task'}`}
                  task={task}
                  onToggle={handleToggleTask}
                  onOccurrenceToggle={handleOccurrenceToggle}
                />
              ))}
            </div>
            {taskFilter === 'overdue' && (
              <p className="dashboard-advice">建议：先重新评估截止日期，或拆分为 30 分钟内可完成的小任务。</p>
            )}
          </div>
        )}
      </section>

      <aside className="dashboard-side">
        <section className="surface-panel agenda-panel">
          <div className="panel-heading is-compact">
            <div>
              <h2>今日安排</h2>
            </div>
            <button className="soft-badge" type="button">
              时间线
            </button>
          </div>
          {data.events.length > 0 ? (
            <div className="timeline-list">
              {data.events.map((event) => (
                <EventChip key={event.id} event={event} />
              ))}
            </div>
          ) : (
            <p className="empty-copy">暂无已安排日程</p>
          )}
          <button className="wide-secondary-action" type="button" onClick={() => navigate('/calendar')}>
            ＋ 添加日程
          </button>
        </section>

        <section className="surface-panel notes-rail">
          <div className="panel-heading is-compact">
            <div>
              <h2>最近笔记</h2>
            </div>
            <button className="link-action" type="button" onClick={() => navigate('/notes')}>
              继续整理
            </button>
          </div>
          <div className="row-stack">
            {data.recentNotes.map((note) => (
              <NoteCard key={note.id} note={note} onOpen={(selectedNote) => navigate(`/editor/${encodeURIComponent(selectedNote.id)}`)} />
            ))}
          </div>
        </section>
      </aside>
    </div>
  )
}

function Metric({ label, value, hint, tone }: { label: string; value: string; hint: string; tone?: 'danger' }) {
  return (
    <div className="metric-tile">
      <span>{label}</span>
      <strong className={tone === 'danger' ? 'is-danger' : ''}>{value}</strong>
      <p>{hint}</p>
    </div>
  )
}
