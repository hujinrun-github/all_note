import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '../api/client'
import { useCreateEvent, useEventsList } from '../hooks/useEvents'
import { EventChip } from '../components/ui/EventChip'
import { TaskRow, type TaskData } from '../components/ui/TaskRow'
import type { Event } from '../api/events'
import { useUpdateTask } from '../hooks/useTasks'

interface TodayData {
  todayTasks: TaskData[]
  overdueTasks: TaskData[]
  events: unknown[]
  recentNotes: unknown[]
}

export default function Calendar() {
  const [currentDate, setCurrentDate] = useState(new Date())
  const year = currentDate.getFullYear()
  const month = currentDate.getMonth()

  const monthStr = `${year}-${String(month + 1).padStart(2, '0')}`
  const { data, isLoading } = useEventsList({ month: monthStr })
  const { data: todayData, isLoading: isTodayLoading } = useQuery({
    queryKey: ['today'],
    queryFn: async () => {
      const res = await api.get<TodayData>('/api/today')
      return res.data
    },
  })
  const createEvent = useCreateEvent()
  const updateTask = useUpdateTask()

  const firstDay = new Date(year, month, 1).getDay()
  const daysInMonth = new Date(year, month + 1, 0).getDate()
  const today = new Date()

  const days = ['日', '一', '二', '三', '四', '五', '六']

  const prevMonth = () => setCurrentDate(new Date(year, month - 1, 1))
  const nextMonth = () => setCurrentDate(new Date(year, month + 1, 1))

  const eventsByDay: Record<number, Event[]> = {}
  data?.events.forEach((e) => {
    const startDay = new Date(e.start_time * 1000).getDate()
    const startMonth = new Date(e.start_time * 1000).getMonth()
    if (startMonth === month) {
      if (!eventsByDay[startDay]) eventsByDay[startDay] = []
      eventsByDay[startDay].push(e)
    }
  })

  const [selectedDay, setSelectedDay] = useState<number | null>(today.getDate())
  const [newEventTitle, setNewEventTitle] = useState('')
  const selectedEvents = selectedDay ? eventsByDay[selectedDay] ?? [] : []
  const isSelectedToday = selectedDay === today.getDate() && month === today.getMonth() && year === today.getFullYear()
  const todayTasks = todayData?.todayTasks ?? []
  const overdueTasks = todayData?.overdueTasks ?? []

  async function handleAddEvent() {
    if (!selectedDay || !newEventTitle.trim()) return
    const start = new Date(year, month, selectedDay, 9, 0, 0, 0)
    const end = new Date(year, month, selectedDay, 10, 0, 0, 0)
    await createEvent.mutateAsync({
      title: newEventTitle.trim(),
      start_time: Math.floor(start.getTime() / 1000),
      end_time: Math.floor(end.getTime() / 1000),
      kind: 'work',
    })
    setNewEventTitle('')
  }

  async function handleToggleTask(id: string) {
    const task = [...todayTasks, ...overdueTasks].find((item) => item.id === id)
    if (!task) return
    await updateTask.mutateAsync({ id, done: task.done ? 0 : 1 })
  }

  return (
    <div className="calendar-workspace">
      <section className="surface-panel calendar-panel">
        <div className="panel-heading">
          <div>
            <span>2026年6月</span>
            <h2>{currentDate.toLocaleDateString('zh-CN', { month: 'long', year: 'numeric' })}</h2>
          </div>
          <div className="toolbar-actions">
            <div className="segmented-tabs">
              <button className="is-active">月</button>
              <button>周</button>
              <button>日</button>
            </div>
            <button onClick={() => setCurrentDate(new Date())} className="secondary-action">今天</button>
          </div>
        </div>

        <div className="calendar-toolbar">
          <button onClick={prevMonth}>上一月</button>
          <div className="calendar-legend">
            <span><i className="is-work" />工作</span>
            <span><i className="is-personal" />个人</span>
            <span><i className="is-reminder" />提醒</span>
          </div>
          <button onClick={nextMonth}>下一月</button>
        </div>

        {isLoading ? (
          <div className="h-[400px] bg-fs-hover rounded-lg animate-pulse" />
        ) : (
          <div className="calendar-grid">
            {days.map((d) => (
              <div key={d} className="calendar-weekday">{d}</div>
            ))}
            {Array.from({ length: firstDay }).map((_, i) => (
              <div key={`empty-${i}`} className="calendar-day is-empty" />
            ))}
            {Array.from({ length: daysInMonth }).map((_, i) => {
              const day = i + 1
              const isToday = day === today.getDate() && month === today.getMonth() && year === today.getFullYear()
              const dayEvents = eventsByDay[day] || []
              const isSelected = day === selectedDay

              return (
                <div
                  key={day}
                  onClick={() => setSelectedDay(isSelected ? null : day)}
                  className={`calendar-day ${isSelected ? 'is-selected' : ''}`}
                >
                  <span className={`text-xs tabular-nums inline-grid place-items-center w-6 h-6 rounded-full ${
                    isToday ? 'bg-fs-accent text-white font-semibold' : 'text-fs-text-secondary'
                  }`}>{day}</span>
                  {dayEvents.length > 0 && (
                    <div className="calendar-dots">
                      {dayEvents.slice(0, 3).map((e) => (
                        <div
                          key={e.id}
                          className={`w-2 h-2 rounded-full ${
                            e.kind === 'work' ? 'bg-fs-accent' : e.kind === 'personal' ? 'bg-fs-success' : 'bg-fs-warning'
                          }`}
                        />
                      ))}
                      {dayEvents.length > 3 && (
                        <span className="text-[9px] text-fs-text-muted leading-none">+{dayEvents.length - 3}</span>
                      )}
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        )}
      </section>

      <aside className="surface-panel calendar-inspector">
        <div className="panel-heading is-compact">
          <div>
            <span>选中日期</span>
            <h2>{selectedDay ? `${month + 1}月${selectedDay}日` : '选择日期'}</h2>
          </div>
        </div>
        <div className="inline-create is-stacked">
          <input
            value={newEventTitle}
            onChange={(event) => setNewEventTitle(event.target.value)}
            onKeyDown={(event) => { if (event.key === 'Enter') handleAddEvent() }}
            placeholder="新增日程"
          />
          <button onClick={handleAddEvent} disabled={!selectedDay || !newEventTitle.trim() || createEvent.isPending}>
            {createEvent.isPending ? '添加中...' : '添加'}
          </button>
        </div>
        <div className="inspector-section">
          <span>日程</span>
          {selectedEvents.length === 0 ? (
            <p className="empty-copy">今天无更多日程</p>
          ) : (
            selectedEvents.map((e) => <EventChip key={e.id} event={e} />)
          )}
        </div>
        <div className="inspector-section calendar-task-flow" data-testid="calendar-today-task-flow">
          <span>{isSelectedToday ? '今日任务流' : '任务流'}</span>
          {!isSelectedToday ? (
            <p className="empty-copy">选中今天查看任务流</p>
          ) : isTodayLoading ? (
            <div className="h-20 bg-fs-hover rounded-lg animate-pulse" />
          ) : todayTasks.length === 0 && overdueTasks.length === 0 ? (
            <p className="empty-copy">今天还没有任务</p>
          ) : (
            <>
              {overdueTasks.length > 0 && (
                <div className="task-section">
                  <span>逾期</span>
                  <div className="row-stack">
                    {overdueTasks.map((task) => (
                      <TaskRow key={task.id} task={task} onToggle={handleToggleTask} />
                    ))}
                  </div>
                </div>
              )}
              <div className="task-section">
                <span>今天</span>
                <div className="row-stack">
                  {todayTasks.map((task) => (
                    <TaskRow key={task.id} task={task} onToggle={handleToggleTask} />
                  ))}
                </div>
              </div>
            </>
          )}
        </div>
        <div className="inspector-section">
          <span>关联笔记</span>
          <div className="linked-note">设计评审记录</div>
          <div className="linked-note">用户访谈摘要</div>
        </div>
      </aside>
    </div>
  )
}
