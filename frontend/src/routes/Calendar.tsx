import { useState, type FormEvent } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { api } from '../api/client'
import { useCreateEvent, useEventsList } from '../hooks/useEvents'
import { TaskRow, type TaskData } from '../components/ui/TaskRow'
import type { Event } from '../api/events'
import { useUpdateTask } from '../hooks/useTasks'

type CalendarView = 'month' | 'week' | 'day'

interface RecentNote {
  id?: string
  title: string
  project?: string
  word_count?: number
}

interface TodayData {
  todayTasks: TaskData[]
  overdueTasks: TaskData[]
  events: unknown[]
  recentNotes: RecentNote[]
}

const weekdays = ['日', '一', '二', '三', '四', '五', '六']
const calendarSources = [
  { id: 'work', label: '工作', className: 'is-work' },
  { id: 'personal', label: '个人', className: 'is-personal' },
  { id: 'reminder', label: '提醒', className: 'is-reminder' },
]

function formatEventStart(event: Pick<Event, 'start_time'>) {
  return new Date(event.start_time * 1000).toLocaleTimeString('zh-CN', {
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  })
}

function formatEventRange(event: Pick<Event, 'start_time' | 'end_time'>) {
  const start = formatEventStart(event)
  const end = new Date(event.end_time * 1000).toLocaleTimeString('zh-CN', {
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  })
  return `${start} - ${end}`
}

function getCalendarKindClass(kind: string) {
  return `is-${kind === 'personal' || kind === 'reminder' ? kind : 'work'}`
}

export default function Calendar() {
  const navigate = useNavigate()
  const [currentDate, setCurrentDate] = useState(new Date())
  const [calendarView, setCalendarView] = useState<CalendarView>('month')
  const [selectedSource, setSelectedSource] = useState('personal')
  const [customSources, setCustomSources] = useState<Array<{ id: string; label: string }>>([])
  const [isAddingSource, setIsAddingSource] = useState(false)
  const [sourceDraft, setSourceDraft] = useState('')
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
  const daysInPrevMonth = new Date(year, month, 0).getDate()
  const today = new Date()
  const [selectedDay, setSelectedDay] = useState<number | null>(today.getDate())
  const [newEventTitle, setNewEventTitle] = useState('')

  const eventsByDay: Record<number, Event[]> = {}
  const visibleEvents = data?.events.filter((event) => event.kind === selectedSource) ?? []
  visibleEvents.forEach((event) => {
    const start = new Date(event.start_time * 1000)
    if (start.getMonth() !== month) return
    const startDay = start.getDate()
    eventsByDay[startDay] = [...(eventsByDay[startDay] ?? []), event]
  })

  const selectedEvents = selectedDay ? eventsByDay[selectedDay] ?? [] : []
  const isSelectedToday = selectedDay === today.getDate() && month === today.getMonth() && year === today.getFullYear()
  const todayTasks = todayData?.todayTasks ?? []
  const overdueTasks = todayData?.overdueTasks ?? []
  const selectedTaskCount = isSelectedToday ? todayTasks.length + overdueTasks.length : 0
  const selectedDateLabel = selectedDay
    ? new Intl.DateTimeFormat('zh-CN', { month: 'long', day: 'numeric', weekday: 'long' }).format(new Date(year, month, selectedDay))
    : '选择日期'
  const selectedDate = new Date(year, month, selectedDay ?? today.getDate())
  const selectedWeekStart = new Date(selectedDate)
  selectedWeekStart.setDate(selectedDate.getDate() - selectedDate.getDay())
  const selectedWeekDates = Array.from(
    { length: 7 },
    (_, index) => new Date(selectedWeekStart.getFullYear(), selectedWeekStart.getMonth(), selectedWeekStart.getDate() + index),
  )
  const linkedNotes =
    todayData?.recentNotes && todayData.recentNotes.length > 0
      ? todayData.recentNotes
      : [{ title: '第一篇笔记', project: 'Personal', word_count: 128 }]

  function selectDate(date: Date) {
    setCurrentDate(new Date(date.getFullYear(), date.getMonth(), 1))
    setSelectedDay(date.getDate())
  }

  function goToday() {
    const now = new Date()
    setCurrentDate(now)
    setSelectedDay(now.getDate())
  }

  function getEventsForDate(date: Date) {
    if (date.getFullYear() !== year || date.getMonth() !== month) return []
    return eventsByDay[date.getDate()] ?? []
  }

  function handleAddSource(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const label = sourceDraft.trim()
    if (!label) return
    const id = `custom-${Date.now()}`
    setCustomSources((sources) => [...sources, { id, label }])
    setSelectedSource(id)
    setSourceDraft('')
    setIsAddingSource(false)
  }

  async function handleAddEvent() {
    if (!selectedDay || !newEventTitle.trim()) return
    const start = new Date(year, month, selectedDay, 9, 0, 0, 0)
    const end = new Date(year, month, selectedDay, 10, 0, 0, 0)
    const eventKind = calendarSources.some((source) => source.id === selectedSource) ? selectedSource : 'work'
    await createEvent.mutateAsync({
      title: newEventTitle.trim(),
      start_time: Math.floor(start.getTime() / 1000),
      end_time: Math.floor(end.getTime() / 1000),
      kind: eventKind,
    })
    setNewEventTitle('')
  }

  async function handleToggleTask(id: string) {
    const task = [...todayTasks, ...overdueTasks].find((item) => item.id === id)
    if (!task) return
    await updateTask.mutateAsync({ id, done: task.done ? 0 : 1 })
  }

  function openLinkedNote(note: RecentNote) {
    if (note.id) {
      navigate(`/editor/${encodeURIComponent(note.id)}`)
      return
    }
    navigate('/notes')
  }

  const monthTitle = currentDate.toLocaleDateString('zh-CN', { month: 'long', year: 'numeric' })

  return (
    <div className="calendar-page">
      <div className="page-local-actions">
        <div className="segmented-tabs">
          <button className={calendarView === 'month' ? 'is-active' : ''} type="button" onClick={() => setCalendarView('month')}>月</button>
          <button className={calendarView === 'week' ? 'is-active' : ''} type="button" onClick={() => setCalendarView('week')}>周</button>
          <button className={calendarView === 'day' ? 'is-active' : ''} type="button" onClick={() => setCalendarView('day')}>日</button>
          <button type="button" onClick={goToday}>今天</button>
        </div>
      </div>

      <div className="calendar-workspace">
        <aside className="surface-panel calendar-source-panel">
          <h2>{monthTitle}</h2>
          <div className="calendar-source-nav">
            <button onClick={() => setCurrentDate(new Date(year, month - 1, 1))}>上一月</button>
            <span>·</span>
            <button onClick={() => setCurrentDate(new Date(year, month + 1, 1))}>下一月</button>
          </div>
          <section>
            <h3>我的日历</h3>
            {calendarSources.map((source) => (
              <button
                key={source.id}
                type="button"
                className={`calendar-source-item ${selectedSource === source.id ? 'is-active' : ''}`}
                onClick={() => setSelectedSource(source.id)}
              >
                <i className={source.className} />
                {source.label}
              </button>
            ))}
            {customSources.map((source) => (
              <button
                key={source.id}
                type="button"
                className={`calendar-source-item ${selectedSource === source.id ? 'is-active' : ''}`}
                onClick={() => setSelectedSource(source.id)}
              >
                <i className="is-custom" />
                {source.label}
              </button>
            ))}
          </section>
          {isAddingSource ? (
            <form className="calendar-source-add" onSubmit={handleAddSource}>
              <input
                value={sourceDraft}
                onChange={(event) => setSourceDraft(event.target.value)}
                placeholder="新日历名称"
              />
              <div>
                <button type="submit">保存</button>
                <button
                  type="button"
                  onClick={() => {
                    setIsAddingSource(false)
                    setSourceDraft('')
                  }}
                >
                  取消
                </button>
              </div>
            </form>
          ) : (
            <button className="link-like" type="button" aria-label="添加日历" onClick={() => setIsAddingSource(true)}>
              ＋ 添加日历
            </button>
          )}
        </aside>

        <section className="surface-panel calendar-panel">
          <div className="calendar-month-heading">
            <h2>{monthTitle}</h2>
            <div>
              <button type="button" onClick={() => setCurrentDate(new Date(year, month - 1, 1))}>‹</button>
              <button type="button" onClick={goToday}>今天</button>
              <button type="button" onClick={() => setCurrentDate(new Date(year, month + 1, 1))}>›</button>
            </div>
          </div>

          {isLoading ? (
            <div className="h-[560px] bg-fs-hover rounded-lg animate-pulse" />
          ) : calendarView === 'month' ? (
            <div className="calendar-grid">
              {weekdays.map((weekday) => (
                <div key={weekday} className="calendar-weekday">{weekday}</div>
              ))}
              {Array.from({ length: 42 }).map((_, index) => {
                const rawDay = index - firstDay + 1
                const isCurrentMonth = rawDay >= 1 && rawDay <= daysInMonth
                const displayDay = rawDay < 1 ? daysInPrevMonth + rawDay : rawDay > daysInMonth ? rawDay - daysInMonth : rawDay
                const dayEvents = isCurrentMonth ? eventsByDay[displayDay] ?? [] : []
                const isToday =
                  isCurrentMonth &&
                  displayDay === today.getDate() &&
                  month === today.getMonth() &&
                  year === today.getFullYear()
                const isSelected = isCurrentMonth && displayDay === selectedDay

                return (
                  <button
                    type="button"
                    key={`${index}-${displayDay}`}
                    onClick={() => isCurrentMonth && setSelectedDay(displayDay)}
                    className={`calendar-day ${isSelected ? 'is-selected' : ''} ${isToday ? 'is-today-cell' : ''} ${
                      dayEvents.length > 0 ? 'has-events' : ''
                    } ${!isCurrentMonth ? 'is-muted' : ''}`}
                  >
                    <span className="calendar-day-head">
                      <span className={isToday ? 'calendar-day-number is-today' : 'calendar-day-number'}>{displayDay}</span>
                      {dayEvents.length > 0 && <span className="calendar-day-count">{dayEvents.length}</span>}
                    </span>
                    <span className="calendar-day-agenda">
                      {dayEvents.slice(0, 2).map((event) => (
                        <span
                          key={event.id}
                          className={`calendar-month-event ${getCalendarKindClass(event.kind)}`}
                          aria-label={`日程：${event.title}，${formatEventStart(event)}`}
                        >
                          <span>{formatEventStart(event)}</span>
                          <strong>{event.title}</strong>
                        </span>
                      ))}
                      {dayEvents.length > 2 && <span className="calendar-month-more">+{dayEvents.length - 2} 项日程</span>}
                      {isSelected && selectedTaskCount > 0 && <em>{selectedTaskCount} 项任务</em>}
                    </span>
                  </button>
                )
              })}
            </div>
          ) : calendarView === 'week' ? (
            <div className="calendar-mode-panel">
              <h3>本周视图</h3>
              <div className="calendar-week-view">
                {selectedWeekDates.map((date) => {
                  const dayEvents = getEventsForDate(date)
                  const isSelected =
                    selectedDay === date.getDate() &&
                    currentDate.getMonth() === date.getMonth() &&
                    currentDate.getFullYear() === date.getFullYear()
                  return (
                    <button
                      key={date.toISOString()}
                      type="button"
                      className={`calendar-week-column ${isSelected ? 'is-selected' : ''}`}
                      onClick={() => selectDate(date)}
                    >
                      <span>{weekdays[date.getDay()]}</span>
                      <strong>{date.getDate()}</strong>
                      {dayEvents.length === 0 ? (
                        <em>暂无日程</em>
                      ) : (
                        dayEvents.slice(0, 3).map((event) => (
                          <span key={event.id} className={`calendar-month-event ${getCalendarKindClass(event.kind)}`}>
                            <span>{formatEventStart(event)}</span>
                            <strong>{event.title}</strong>
                          </span>
                        ))
                      )}
                    </button>
                  )
                })}
              </div>
            </div>
          ) : (
            <div className="calendar-mode-panel calendar-day-view">
              <h3>当日视图</h3>
              <div className="calendar-day-agenda-panel">
                <span>{selectedDateLabel}</span>
                {selectedEvents.length === 0 ? (
                  <p className="empty-copy calendar-empty-agenda">当天暂无日程</p>
                ) : (
                  selectedEvents.map((event) => (
                    <article key={event.id} className={`calendar-timeline-event ${getCalendarKindClass(event.kind)}`}>
                      <time>{formatEventRange(event)}</time>
                      <div>
                        <strong>{event.title}</strong>
                        {event.location && <span>{event.location}</span>}
                      </div>
                    </article>
                  ))
                )}
              </div>
            </div>
          )}
        </section>

        <aside className="surface-panel calendar-inspector">
          <div className="panel-heading is-compact">
            <div>
              <h2>{selectedDateLabel}</h2>
              <p>选中日期详情</p>
            </div>
          </div>
          <section className="inspector-section">
            <h3>今日时间线</h3>
            {selectedEvents.length === 0 ? (
              <p className="empty-copy calendar-empty-agenda">暂无日程</p>
            ) : (
              <div className="calendar-timeline">
                {selectedEvents.map((event) => (
                  <article key={event.id} className={`calendar-timeline-event ${getCalendarKindClass(event.kind)}`}>
                    <time>{formatEventRange(event)}</time>
                    <div>
                      <strong>{event.title}</strong>
                      {event.location && <span>{event.location}</span>}
                    </div>
                  </article>
                ))}
              </div>
            )}
            <div className="inline-create is-stacked">
              <input
                value={newEventTitle}
                onChange={(event) => setNewEventTitle(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter') void handleAddEvent()
                }}
                placeholder="新增日程"
              />
              <button onClick={handleAddEvent} disabled={!selectedDay || !newEventTitle.trim() || createEvent.isPending}>
                添加日程
              </button>
            </div>
          </section>

          <section className="inspector-section calendar-task-flow" data-testid="calendar-today-task-flow">
            <h3>今日任务流</h3>
            {!isSelectedToday ? (
              <p className="empty-copy">选中今天查看任务流</p>
            ) : isTodayLoading ? (
              <div className="h-20 bg-fs-hover rounded-lg animate-pulse" />
            ) : todayTasks.length === 0 && overdueTasks.length === 0 ? (
              <p className="empty-copy">今天还没有任务</p>
            ) : (
              <div className="row-stack">
                {[...overdueTasks, ...todayTasks].map((task) => (
                  <TaskRow key={task.id} task={task} onToggle={handleToggleTask} />
                ))}
              </div>
            )}
          </section>

          <section className="inspector-section">
            <h3>相关笔记</h3>
            {linkedNotes.map((note) => (
              <button
                key={note.id ?? note.title}
                className="linked-note-card"
                type="button"
                onClick={() => openLinkedNote(note)}
              >
                <strong>{note.title}</strong>
                <span>{note.project ?? 'Personal'} · {note.word_count ?? 128} 字</span>
              </button>
            ))}
          </section>
        </aside>
      </div>
    </div>
  )
}
