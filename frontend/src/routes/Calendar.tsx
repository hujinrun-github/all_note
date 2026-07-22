import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { ChevronDown, ChevronLeft, ChevronRight } from 'lucide-react'
import { api } from '../api/client'
import {
  eventsListQueryOptions,
  useCreateEvent,
  useEventsList,
} from '../hooks/useEvents'
import { TaskRow, type TaskData } from '../components/ui/TaskRow'
import type { Event } from '../api/events'
import { useUpdateTask } from '../hooks/useTasks'
import {
  useCalendarProjectSources,
  useSaveCalendarProjectSources,
} from '../hooks/useCalendarSources'
import type { CalendarProjectSource } from '../api/calendar'
import {
  completeOccurrence,
  getTaskOccurrences,
  getTasks,
  reopenOccurrence,
  type Task,
  type TaskOccurrence,
} from '../api/tasks'
import { getTaskColor } from '../utils/taskColors'
import { TaskDomainGate } from '../components/taskDomain/TaskDomainGate'
import CalendarV2 from './CalendarV2'

type CalendarView = 'month' | 'week' | 'day'

const calendarViewLabels: Record<CalendarView, string> = {
  month: '月',
  week: '周',
  day: '日',
}

const calendarNavigationLabels: Record<
  CalendarView,
  { previous: string; next: string }
> = {
  month: { previous: '上个月', next: '下个月' },
  week: { previous: '上一周', next: '下一周' },
  day: { previous: '前一天', next: '后一天' },
}

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

interface CalendarTask extends TaskData {
  calendar_key: string
  project_id?: string
  color?: string
}

type CalendarAgendaEntry =
  | { type: 'event'; event: Event }
  | { type: 'task'; task: CalendarTask }

const weekdays = ['日', '一', '二', '三', '四', '五', '六']
const unassignedSourceId = '__unassigned__'
const emptyEvents: Event[] = []
const emptyCalendarTasks: CalendarTask[] = []
const emptyTaskData: TaskData[] = []
const emptyProjectSources: CalendarProjectSource[] = []

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

function getCalendarSourceClass(type: string) {
  return `is-${type === 'personal' || type === 'reminder' ? type : 'work'}`
}

function mergeConfigurableSources(
  sources: CalendarProjectSource[],
  availableProjects: CalendarProjectSource[]
) {
  const sourceMap = new Map<string, CalendarProjectSource>()
  const allSources = [...sources, ...availableProjects]
  allSources.forEach((source) => {
    if (!source.default && !sourceMap.has(source.project_id)) {
      sourceMap.set(source.project_id, source)
    }
  })
  return Array.from(sourceMap.values()).sort(
    (a, b) => a.order_index - b.order_index
  )
}

function getCalendarSourceName(
  source: Pick<CalendarProjectSource, 'project_id' | 'name'>
) {
  return source.project_id === 'personal' ? '个人' : source.name
}

function dateToInputValue(date: Date) {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

function toCalendarTask(task: Task): CalendarTask | null {
  if (!task.planned_date || task.execution_type === 'recurring') return null
  return {
    ...task,
    calendar_key: task.id,
  }
}

function occurrenceToCalendarTask(occurrence: TaskOccurrence): CalendarTask {
  return {
    id: occurrence.task_id,
    calendar_key: `${occurrence.task_id}:${occurrence.occurrence_date}`,
    title: occurrence.title,
    project: occurrence.project,
    project_id: occurrence.project_id,
    planned_date: occurrence.occurrence_date,
    priority: 0,
    done: occurrence.status === 'done' ? 1 : 0,
    scope: 'recurring',
    execution_type: 'recurring',
    occurrence_date: occurrence.occurrence_date,
    occurrence_status: occurrence.status,
    recurrence_label: occurrence.recurrence_label,
    color: occurrence.color,
  }
}

function taskProjectionToCalendarTask(task: TaskData): CalendarTask | null {
  const date = task.occurrence_date ?? task.planned_date
  if (!date) return null
  return {
    ...task,
    calendar_key: `${task.id}:${date}`,
  }
}

function getCalendarTaskIdentity(task: CalendarTask) {
  return `${task.id}:${task.occurrence_date ?? task.planned_date ?? ''}`
}

async function getTasksByDateRange(from: string, to: string) {
  const pageSize = 100
  const firstPage = await getTasks({
    status: 'all',
    planned_from: from,
    planned_to: to,
    page: 1,
    page_size: pageSize,
  })
  const totalPages = Math.max(
    1,
    Math.ceil(firstPage.pagination.total / firstPage.pagination.page_size)
  )
  if (totalPages === 1) return firstPage.tasks

  const remainingPages = await Promise.all(
    Array.from({ length: totalPages - 1 }, (_, index) =>
      getTasks({
        status: 'all',
        planned_from: from,
        planned_to: to,
        page: index + 2,
        page_size: pageSize,
      })
    )
  )
  return [firstPage, ...remainingPages].flatMap((page) => page.tasks)
}

async function getCalendarTasks(from: string, to: string) {
  const [tasks, occurrences] = await Promise.all([
    getTasksByDateRange(from, to),
    getTaskOccurrences(from, to),
  ])

  const scheduledTasks = tasks
    .map(toCalendarTask)
    .filter((task): task is CalendarTask => Boolean(task))
    .filter(
      (task) =>
        Boolean(task.planned_date) &&
        task.planned_date! >= from &&
        task.planned_date! <= to
    )
  return [...scheduledTasks, ...occurrences.map(occurrenceToCalendarTask)]
}

function getCalendarMonthQuery(date: Date) {
  const year = date.getFullYear()
  const month = date.getMonth()
  return {
    monthStr: `${year}-${String(month + 1).padStart(2, '0')}`,
    from: dateToInputValue(new Date(year, month, 1)),
    to: dateToInputValue(new Date(year, month + 1, 0)),
  }
}

function calendarTasksQueryOptions(monthStr: string, from: string, to: string) {
  return {
    queryKey: ['calendar-task-schedule', monthStr] as const,
    queryFn: () => getCalendarTasks(from, to),
    staleTime: 5 * 60_000,
    gcTime: 30 * 60_000,
  }
}

function CalendarTaskChip({ task }: { task: CalendarTask }) {
  const color = getTaskColor(task.id, task.color)
  return (
    <span
      className={`calendar-month-task${task.done ? ' is-done' : ''}`}
      aria-label={`任务：${task.title}`}
    >
      <i style={{ backgroundColor: color }} aria-hidden="true" />
      <strong>{task.title}</strong>
    </span>
  )
}

function CalendarAgendaList({ entries }: { entries: CalendarAgendaEntry[] }) {
  return (
    <>
      {entries.slice(0, 3).map((entry) =>
        entry.type === 'event' ? (
          <span
            key={`event:${entry.event.id}`}
            className={`calendar-month-event ${getCalendarKindClass(entry.event.kind)}`}
            aria-label={`日程：${entry.event.title}，${formatEventStart(entry.event)}`}
          >
            <span>{formatEventStart(entry.event)}</span>
            <strong>{entry.event.title}</strong>
          </span>
        ) : (
          <CalendarTaskChip
            key={`task:${entry.task.calendar_key}`}
            task={entry.task}
          />
        )
      )}
      {entries.length > 3 ? (
        <span className="calendar-month-more">+{entries.length - 3} 项</span>
      ) : null}
    </>
  )
}

export default function Calendar() {
  return <TaskDomainGate legacy={<LegacyCalendar />} v2={<CalendarV2 />} />
}

export function LegacyCalendar() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [currentDate, setCurrentDate] = useState(new Date())
  const [calendarView, setCalendarView] = useState<CalendarView>('month')
  const [selectedSourceProjectID, setSelectedSourceProjectID] =
    useState('personal')
  const [isConfiguringSources, setIsConfiguringSources] = useState(false)
  const [sourceConfigDraft, setSourceConfigDraft] = useState<
    Record<string, boolean>
  >({})
  const year = currentDate.getFullYear()
  const month = currentDate.getMonth()
  const monthStr = `${year}-${String(month + 1).padStart(2, '0')}`
  const monthStart = dateToInputValue(new Date(year, month, 1))
  const monthEnd = dateToInputValue(new Date(year, month + 1, 0))

  const { data, isLoading, isFetching } = useEventsList({ month: monthStr })
  const { data: todayData, isLoading: isTodayLoading } = useQuery({
    queryKey: ['today'],
    queryFn: async () => {
      const res = await api.get<TodayData>('/api/today')
      return res.data
    },
  })
  const calendarTasksQuery = useQuery({
    ...calendarTasksQueryOptions(monthStr, monthStart, monthEnd),
    placeholderData: (previousData) => previousData,
  })
  const createEvent = useCreateEvent()
  const updateTask = useUpdateTask()
  const { data: projectSourcesData } = useCalendarProjectSources()
  const saveProjectSources = useSaveCalendarProjectSources()
  const occurrenceMutation = useMutation({
    mutationFn: ({
      taskID,
      occurrenceDate,
      currentStatus,
    }: {
      taskID: string
      occurrenceDate: string
      currentStatus: string
    }) =>
      currentStatus === 'done'
        ? reopenOccurrence(taskID, occurrenceDate)
        : completeOccurrence(taskID, occurrenceDate),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['calendar-task-schedule'] }),
        queryClient.invalidateQueries({ queryKey: ['tasks'] }),
        queryClient.invalidateQueries({ queryKey: ['today'] }),
      ])
    },
  })
  const isCalendarRefreshing =
    isLoading || isFetching || calendarTasksQuery.isFetching

  useEffect(() => {
    if (isCalendarRefreshing) return
    ;([-1, 1] as const).forEach((offset) => {
      const adjacentMonth = getCalendarMonthQuery(
        new Date(year, month + offset, 1)
      )
      void queryClient.prefetchQuery(
        eventsListQueryOptions({ month: adjacentMonth.monthStr })
      )
      void queryClient.prefetchQuery(
        calendarTasksQueryOptions(
          adjacentMonth.monthStr,
          adjacentMonth.from,
          adjacentMonth.to
        )
      )
    })
  }, [isCalendarRefreshing, month, queryClient, year])

  const firstDay = new Date(year, month, 1).getDay()
  const daysInMonth = new Date(year, month + 1, 0).getDate()
  const daysInPrevMonth = new Date(year, month, 0).getDate()
  const today = new Date()
  const [selectedDay, setSelectedDay] = useState<number | null>(today.getDate())
  const [newEventTitle, setNewEventTitle] = useState('')
  const [newEventStartTime, setNewEventStartTime] = useState('09:00')
  const [newEventEndTime, setNewEventEndTime] = useState('10:00')
  const events = data?.events ?? emptyEvents
  const calendarTasks = calendarTasksQuery.data ?? emptyCalendarTasks
  const todayTasks = todayData?.todayTasks ?? emptyTaskData
  const overdueTasks = todayData?.overdueTasks ?? emptyTaskData
  const projectedCalendarTasks = useMemo(() => {
    const tasksByIdentity = new Map<string, CalendarTask>()
    calendarTasks.forEach((task) => {
      tasksByIdentity.set(getCalendarTaskIdentity(task), task)
    })
    ;[...todayTasks, ...overdueTasks].forEach((task) => {
      const projected = taskProjectionToCalendarTask(task)
      if (!projected) return
      const identity = getCalendarTaskIdentity(projected)
      const existing = tasksByIdentity.get(identity)
      tasksByIdentity.set(identity, {
        ...existing,
        ...projected,
        project_id: projected.project_id ?? existing?.project_id,
        color: projected.color ?? existing?.color,
      })
    })
    return Array.from(tasksByIdentity.values())
  }, [calendarTasks, overdueTasks, todayTasks])
  const projectSources = projectSourcesData?.sources ?? emptyProjectSources
  const configurableSources = useMemo(
    () =>
      mergeConfigurableSources(
        projectSourcesData?.sources ?? [],
        projectSourcesData?.available_projects ?? []
      ),
    [projectSourcesData]
  )
  const hasUnassignedItems =
    events.some((event) => !event.project_id) ||
    projectedCalendarTasks.some((task) => !task.project_id)
  const visibleSources = useMemo(
    () =>
      hasUnassignedItems
        ? [
            ...projectSources,
            {
              project_id: unassignedSourceId,
              name: '未归属',
              type: 'work',
              enabled: true,
              default: false,
              color: '#667085',
              order_index: Number.MAX_SAFE_INTEGER,
            },
          ]
        : projectSources,
    [hasUnassignedItems, projectSources]
  )
  const selectedVisibleSource = visibleSources.find(
    (source) => source.project_id === selectedSourceProjectID
  )
  const selectedProjectSource = projectSources.find(
    (source) => source.project_id === selectedVisibleSource?.project_id
  )
  const isNewEventTimeInvalid =
    !newEventStartTime ||
    !newEventEndTime ||
    newEventEndTime <= newEventStartTime
  const canCreateEvent =
    Boolean(selectedDay && newEventTitle.trim()) &&
    !isNewEventTimeInvalid &&
    !createEvent.isPending

  useEffect(() => {
    if (visibleSources.length === 0) return
    if (
      visibleSources.some(
        (source) => source.project_id === selectedSourceProjectID
      )
    )
      return
    setSelectedSourceProjectID(visibleSources[0].project_id)
  }, [selectedSourceProjectID, visibleSources])

  const visibleEvents = useMemo(
    () =>
      selectedVisibleSource
        ? events.filter((event) =>
            selectedVisibleSource.project_id === unassignedSourceId
              ? !event.project_id
              : event.project_id === selectedVisibleSource.project_id
          )
        : emptyEvents,
    [events, selectedVisibleSource]
  )
  const visibleTasks = useMemo(
    () =>
      selectedVisibleSource
        ? projectedCalendarTasks.filter((task) =>
            selectedVisibleSource.project_id === unassignedSourceId
              ? !task.project_id
              : task.project_id === selectedVisibleSource.project_id
          )
        : emptyCalendarTasks,
    [projectedCalendarTasks, selectedVisibleSource]
  )
  const eventsByDay = useMemo(() => {
    const grouped = new Map<number, Event[]>()
    visibleEvents.forEach((event) => {
      const start = new Date(event.start_time * 1000)
      if (start.getMonth() !== month || start.getFullYear() !== year) return
      const day = start.getDate()
      grouped.set(day, [...(grouped.get(day) ?? []), event])
    })
    return grouped
  }, [month, visibleEvents, year])
  const tasksByDate = useMemo(() => {
    const grouped = new Map<string, CalendarTask[]>()
    visibleTasks.forEach((task) => {
      const date = task.occurrence_date ?? task.planned_date
      if (!date) return
      grouped.set(date, [...(grouped.get(date) ?? []), task])
    })
    return grouped
  }, [visibleTasks])

  const selectedEvents = selectedDay ? (eventsByDay.get(selectedDay) ?? []) : []
  const isSelectedToday =
    selectedDay === today.getDate() &&
    month === today.getMonth() &&
    year === today.getFullYear()
  const selectedDateInput = selectedDay
    ? dateToInputValue(new Date(year, month, selectedDay))
    : ''
  const selectedCalendarTasks = selectedDateInput
    ? (tasksByDate.get(selectedDateInput) ?? emptyCalendarTasks)
    : emptyCalendarTasks
  const inspectorTasks = useMemo(() => {
    const candidates = isSelectedToday
      ? [...overdueTasks, ...todayTasks, ...selectedCalendarTasks]
      : selectedCalendarTasks
    const uniqueTasks = new Map<string, TaskData>()
    candidates.forEach((task) => {
      const key = `${task.id}:${task.occurrence_date ?? task.planned_date ?? ''}`
      if (!uniqueTasks.has(key)) uniqueTasks.set(key, task)
    })
    return Array.from(uniqueTasks.values())
  }, [isSelectedToday, overdueTasks, selectedCalendarTasks, todayTasks])
  const selectedDateLabel = selectedDay
    ? new Intl.DateTimeFormat('zh-CN', {
        month: 'long',
        day: 'numeric',
        weekday: 'long',
      }).format(new Date(year, month, selectedDay))
    : '选择日期'
  const selectedDate = new Date(year, month, selectedDay ?? today.getDate())
  const selectedWeekStart = new Date(selectedDate)
  selectedWeekStart.setDate(selectedDate.getDate() - selectedDate.getDay())
  const selectedWeekDates = Array.from(
    { length: 7 },
    (_, index) =>
      new Date(
        selectedWeekStart.getFullYear(),
        selectedWeekStart.getMonth(),
        selectedWeekStart.getDate() + index
      )
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

  function navigateMonth(direction: -1 | 1) {
    const targetDate = new Date(year, month + direction, 1)
    const targetMonthDays = new Date(
      targetDate.getFullYear(),
      targetDate.getMonth() + 1,
      0
    ).getDate()
    setCurrentDate(targetDate)
    setSelectedDay((day) => (day ? Math.min(day, targetMonthDays) : day))
  }

  function navigateCalendar(direction: -1 | 1) {
    if (calendarView === 'month') {
      navigateMonth(direction)
      return
    }

    const targetDate = new Date(selectedDate)
    targetDate.setDate(
      targetDate.getDate() + direction * (calendarView === 'week' ? 7 : 1)
    )
    selectDate(targetDate)
  }

  function getEventsForDate(date: Date) {
    if (date.getFullYear() !== year || date.getMonth() !== month) return []
    return eventsByDay.get(date.getDate()) ?? []
  }

  function getTasksForDate(date: Date) {
    return tasksByDate.get(dateToInputValue(date)) ?? emptyCalendarTasks
  }

  function getAgendaForDate(date: Date): CalendarAgendaEntry[] {
    return [
      ...getEventsForDate(date).map((event) => ({
        type: 'event' as const,
        event,
      })),
      ...getTasksForDate(date).map((task) => ({ type: 'task' as const, task })),
    ]
  }

  async function handleAddEvent() {
    if (!selectedDay || !newEventTitle.trim() || isNewEventTimeInvalid) return
    const [startHour, startMinute] = newEventStartTime.split(':').map(Number)
    const [endHour, endMinute] = newEventEndTime.split(':').map(Number)
    const start = new Date(
      year,
      month,
      selectedDay,
      startHour,
      startMinute,
      0,
      0
    )
    const end = new Date(year, month, selectedDay, endHour, endMinute, 0, 0)
    const eventKind =
      selectedProjectSource?.type === 'personal' ? 'personal' : 'work'
    await createEvent.mutateAsync({
      title: newEventTitle.trim(),
      start_time: Math.floor(start.getTime() / 1000),
      end_time: Math.floor(end.getTime() / 1000),
      kind: eventKind,
      ...(selectedProjectSource
        ? { project_id: selectedProjectSource.project_id }
        : {}),
    })
    setNewEventTitle('')
  }

  function openProjectSourceConfig() {
    setSourceConfigDraft(
      Object.fromEntries(
        configurableSources.map((source) => [source.project_id, source.enabled])
      )
    )
    setIsConfiguringSources(true)
  }

  async function handleSaveProjectSourceConfig() {
    await saveProjectSources.mutateAsync({
      sources: configurableSources.map((source) => ({
        project_id: source.project_id,
        enabled: sourceConfigDraft[source.project_id] ?? source.enabled,
        color: source.color,
        order_index: source.order_index,
      })),
    })
    setIsConfiguringSources(false)
  }

  async function handleToggleTask(id: string) {
    const task = inspectorTasks.find((item) => item.id === id)
    if (!task) return
    await updateTask.mutateAsync({ id, done: task.done ? 0 : 1 })
  }

  function handleToggleOccurrence(
    taskID: string,
    occurrenceDate: string,
    currentStatus: string
  ) {
    occurrenceMutation.mutate({ taskID, occurrenceDate, currentStatus })
  }

  function openLinkedNote(note: RecentNote) {
    if (note.id) {
      navigate(`/editor/${encodeURIComponent(note.id)}`)
      return
    }
    navigate('/notes')
  }

  const monthTitle = currentDate.toLocaleDateString('zh-CN', {
    month: 'long',
    year: 'numeric',
  })
  const weekStart = selectedWeekDates[0]
  const weekEnd = selectedWeekDates[selectedWeekDates.length - 1]
  const weekTitle =
    weekStart.getFullYear() === weekEnd.getFullYear() &&
    weekStart.getMonth() === weekEnd.getMonth()
      ? `${weekStart.getFullYear()}年${weekStart.getMonth() + 1}月${weekStart.getDate()}日–${weekEnd.getDate()}日`
      : weekStart.getFullYear() === weekEnd.getFullYear()
        ? `${weekStart.getFullYear()}年${weekStart.getMonth() + 1}月${weekStart.getDate()}日–${weekEnd.getMonth() + 1}月${weekEnd.getDate()}日`
        : `${weekStart.getFullYear()}年${weekStart.getMonth() + 1}月${weekStart.getDate()}日–${weekEnd.getFullYear()}年${weekEnd.getMonth() + 1}月${weekEnd.getDate()}日`
  const dayTitle = selectedDate.toLocaleDateString('zh-CN', {
    year: 'numeric',
    month: 'long',
    day: 'numeric',
  })
  const calendarPeriodTitle =
    calendarView === 'month'
      ? monthTitle
      : calendarView === 'week'
        ? weekTitle
        : dayTitle
  const calendarNavigationLabel = calendarNavigationLabels[calendarView]

  return (
    <div className="calendar-page">
      <div className="calendar-workspace">
        <aside className="surface-panel calendar-source-panel">
          <h2>{monthTitle}</h2>
          <div className="calendar-source-nav">
            <button onClick={() => navigateMonth(-1)}>上一月</button>
            <span>·</span>
            <button onClick={() => navigateMonth(1)}>下一月</button>
          </div>
          <section>
            <h3>我的日历</h3>
            {visibleSources.map((source) => (
              <button
                key={source.project_id}
                type="button"
                className={`calendar-source-item ${selectedSourceProjectID === source.project_id ? 'is-active' : ''}`}
                onClick={() => setSelectedSourceProjectID(source.project_id)}
              >
                <i
                  className={getCalendarSourceClass(source.type)}
                  style={{ background: source.color }}
                />
                {getCalendarSourceName(source)}
              </button>
            ))}
          </section>
          {isConfiguringSources ? (
            <div className="calendar-source-add">
              {configurableSources.length > 0 ? (
                configurableSources.map((source) => (
                  <label
                    key={source.project_id}
                    className="calendar-source-check"
                  >
                    <input
                      type="checkbox"
                      checked={
                        sourceConfigDraft[source.project_id] ?? source.enabled
                      }
                      onChange={(event) =>
                        setSourceConfigDraft((draft) => ({
                          ...draft,
                          [source.project_id]: event.target.checked,
                        }))
                      }
                    />
                    {getCalendarSourceName(source)}
                  </label>
                ))
              ) : (
                <div className="calendar-source-empty">
                  <p>请到任务工作台新建长期项目或学习项目。</p>
                  <button type="button" onClick={() => navigate('/tasks')}>
                    去任务工作台
                  </button>
                </div>
              )}
              <div className="calendar-source-actions">
                <button
                  type="button"
                  onClick={handleSaveProjectSourceConfig}
                  disabled={saveProjectSources.isPending}
                >
                  保存配置
                </button>
                <button
                  type="button"
                  onClick={() => {
                    setIsConfiguringSources(false)
                    setSourceConfigDraft({})
                  }}
                >
                  取消
                </button>
              </div>
            </div>
          ) : (
            <button
              className="link-like"
              type="button"
              onClick={openProjectSourceConfig}
            >
              配置项目
            </button>
          )}
        </aside>

        <section className="surface-panel calendar-panel">
          <div className="calendar-month-heading">
            <div className="calendar-period-navigation">
              <button
                className="calendar-today-control"
                type="button"
                onClick={goToday}
              >
                今天
              </button>
              <div className="calendar-period-stepper" aria-label="日历翻页">
                <button
                  className="calendar-step-button"
                  type="button"
                  aria-label={calendarNavigationLabel.previous}
                  title={calendarNavigationLabel.previous}
                  onClick={() => navigateCalendar(-1)}
                >
                  <ChevronLeft size={18} strokeWidth={2} aria-hidden="true" />
                </button>
                <button
                  className="calendar-step-button"
                  type="button"
                  aria-label={calendarNavigationLabel.next}
                  title={calendarNavigationLabel.next}
                  onClick={() => navigateCalendar(1)}
                >
                  <ChevronRight size={18} strokeWidth={2} aria-hidden="true" />
                </button>
              </div>
              <h2 aria-live="polite">{calendarPeriodTitle}</h2>
              {isCalendarRefreshing ? (
                <span className="calendar-sync-status" role="status">
                  <i aria-hidden="true" />
                  更新中
                </span>
              ) : null}
            </div>
            <label className="calendar-view-select">
              <select
                aria-label="日历视图"
                value={calendarView}
                onChange={(event) =>
                  setCalendarView(event.target.value as CalendarView)
                }
              >
                {(Object.keys(calendarViewLabels) as CalendarView[]).map(
                  (view) => (
                    <option key={view} value={view}>
                      {calendarViewLabels[view]}
                    </option>
                  )
                )}
              </select>
              <ChevronDown size={16} strokeWidth={2} aria-hidden="true" />
            </label>
          </div>

          {calendarView === 'month' ? (
            <div
              className="calendar-grid"
              role="grid"
              aria-label={`${calendarPeriodTitle}日历`}
              aria-busy={isCalendarRefreshing}
            >
              {weekdays.map((weekday) => (
                <div key={weekday} className="calendar-weekday">
                  {weekday}
                </div>
              ))}
              {Array.from({ length: 42 }).map((_, index) => {
                const rawDay = index - firstDay + 1
                const isCurrentMonth = rawDay >= 1 && rawDay <= daysInMonth
                const displayDay =
                  rawDay < 1
                    ? daysInPrevMonth + rawDay
                    : rawDay > daysInMonth
                      ? rawDay - daysInMonth
                      : rawDay
                const dayAgenda = isCurrentMonth
                  ? getAgendaForDate(new Date(year, month, displayDay))
                  : []
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
                      dayAgenda.length > 0 ? 'has-events' : ''
                    } ${!isCurrentMonth ? 'is-muted' : ''}`}
                  >
                    <span className="calendar-day-head">
                      <span
                        className={
                          isToday
                            ? 'calendar-day-number is-today'
                            : 'calendar-day-number'
                        }
                      >
                        {displayDay}
                      </span>
                      {dayAgenda.length > 0 && (
                        <span className="calendar-day-count">
                          {dayAgenda.length}
                        </span>
                      )}
                    </span>
                    <span className="calendar-day-agenda">
                      <CalendarAgendaList entries={dayAgenda} />
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
                  const dayAgenda = getAgendaForDate(date)
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
                      {dayAgenda.length === 0 ? (
                        <em>暂无安排</em>
                      ) : (
                        <CalendarAgendaList entries={dayAgenda} />
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
                {selectedEvents.map((event) => (
                  <article
                    key={event.id}
                    className={`calendar-timeline-event ${getCalendarKindClass(event.kind)}`}
                  >
                    <time>{formatEventRange(event)}</time>
                    <div>
                      <strong>{event.title}</strong>
                      {event.location && <span>{event.location}</span>}
                    </div>
                  </article>
                ))}
                {selectedCalendarTasks.map((task) => (
                  <article
                    key={task.calendar_key}
                    className={`calendar-day-task${task.done ? ' is-done' : ''}`}
                  >
                    <i
                      style={{
                        backgroundColor: getTaskColor(task.id, task.color),
                      }}
                      aria-hidden="true"
                    />
                    <div>
                      <strong>{task.title}</strong>
                      <span>{task.project ?? '未归属项目'} · 任务</span>
                    </div>
                  </article>
                ))}
                {selectedEvents.length === 0 &&
                selectedCalendarTasks.length === 0 ? (
                  <p className="empty-copy calendar-empty-agenda">
                    当天暂无安排
                  </p>
                ) : null}
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
            <h3>当日日程</h3>
            {selectedEvents.length === 0 ? (
              <p className="empty-copy calendar-empty-agenda">暂无日程</p>
            ) : (
              <div className="calendar-timeline">
                {selectedEvents.map((event) => (
                  <article
                    key={event.id}
                    className={`calendar-timeline-event ${getCalendarKindClass(event.kind)}`}
                  >
                    <time>{formatEventRange(event)}</time>
                    <div>
                      <strong>{event.title}</strong>
                      {event.location && <span>{event.location}</span>}
                    </div>
                  </article>
                ))}
              </div>
            )}
            <form
              className="calendar-event-create"
              onSubmit={(event) => {
                event.preventDefault()
                void handleAddEvent()
              }}
            >
              <input
                className="calendar-event-title"
                value={newEventTitle}
                onChange={(event) => setNewEventTitle(event.target.value)}
                placeholder="新增日程"
                aria-label="日程标题"
              />
              <div className="calendar-event-time-fields">
                <label>
                  <span>开始</span>
                  <input
                    type="time"
                    step="900"
                    aria-label="开始时间"
                    value={newEventStartTime}
                    onChange={(event) =>
                      setNewEventStartTime(event.target.value)
                    }
                  />
                </label>
                <span
                  className="calendar-event-time-separator"
                  aria-hidden="true"
                >
                  至
                </span>
                <label>
                  <span>结束</span>
                  <input
                    type="time"
                    step="900"
                    aria-label="结束时间"
                    value={newEventEndTime}
                    onChange={(event) => setNewEventEndTime(event.target.value)}
                  />
                </label>
              </div>
              {newEventStartTime &&
              newEventEndTime &&
              newEventEndTime <= newEventStartTime ? (
                <p className="calendar-event-time-error" role="alert">
                  结束时间必须晚于开始时间
                </p>
              ) : null}
              <button type="submit" disabled={!canCreateEvent}>
                添加日程
              </button>
            </form>
          </section>

          <section
            className="inspector-section calendar-task-flow"
            data-testid="calendar-today-task-flow"
          >
            <h3>{isSelectedToday ? '今日任务' : '当日任务'}</h3>
            {isSelectedToday && isTodayLoading ? (
              <div className="h-20 bg-fs-hover rounded-lg animate-pulse" />
            ) : calendarTasksQuery.isLoading ? (
              <div className="h-20 bg-fs-hover rounded-lg animate-pulse" />
            ) : inspectorTasks.length === 0 ? (
              <p className="empty-copy">当天还没有任务</p>
            ) : (
              <div className="row-stack">
                {inspectorTasks.map((task) => (
                  <TaskRow
                    key={`${task.id}:${task.occurrence_date ?? task.planned_date ?? ''}`}
                    task={task}
                    onToggle={handleToggleTask}
                    onOccurrenceToggle={handleToggleOccurrence}
                  />
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
                <span>
                  {note.project ?? 'Personal'} · {note.word_count ?? 128} 字
                </span>
              </button>
            ))}
          </section>
        </aside>
      </div>
    </div>
  )
}
