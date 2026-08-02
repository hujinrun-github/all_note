import { useMemo, useState, type CSSProperties, type DragEvent } from 'react'
import { ChevronLeft, ChevronRight, Globe2, Plus } from 'lucide-react'

import {
  TaskDomainAPIError,
  type CalendarEntryV2,
  type RecurrenceType,
  type TimingType,
} from '../api/taskDomain'
import {
  useCalendarEntries,
  useProjects,
  useReopenOccurrenceMutation,
  useRescheduleOccurrenceMutation,
  useRescheduleThisAndFollowingMutation,
} from '../hooks/useTaskDomain'
import { ScheduleCreateDialog } from '../components/taskDomain/ScheduleCreateDialog'

type ScheduleScope = 'only-this' | 'this-and-following'
type CalendarView = 'week' | 'month' | 'year'

interface CalendarV2Props {
  initialDate?: string
  initialTimezone?: string
  initialView?: CalendarView
}

interface ScheduleDraft {
  scope: ScheduleScope
  timingType: Exclude<TimingType, 'unscheduled'>
  plannedDate: string
  allDayEndDate: string
  localStartTime: string
  durationMinutes: number
  recurrenceType: Exclude<RecurrenceType, 'none'>
  effectiveFrom: string
  generateThroughExclusive: string
  selectedOffsetSeconds?: number
}

interface CalendarCreateScheduleDefaults {
  startsOn: string
  startTime?: string
  endTime?: string
}

const weekHours = Array.from({ length: 15 }, (_, index) => index + 7)
const weekStartMinute = 7 * 60
const weekEndMinute = 22 * 60
const defaultScheduleDuration = 30
const weekTimeSlots = Array.from(
  { length: (weekEndMinute - weekStartMinute) / defaultScheduleDuration },
  (_, index) => weekStartMinute + index * defaultScheduleDuration
)
const calendarWeekdays = [
  '周一',
  '周二',
  '周三',
  '周四',
  '周五',
  '周六',
  '周日',
]
const commonTimezones = [
  'Asia/Shanghai',
  'Asia/Tokyo',
  'Europe/London',
  'America/New_York',
  'UTC',
]

function localDateValue(date = new Date()) {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

function parseLocalDate(value: string) {
  return new Date(`${value}T12:00:00`)
}

function addDays(value: string, days: number) {
  const date = parseLocalDate(value)
  date.setDate(date.getDate() + days)
  return localDateValue(date)
}

function timeInputValue(minuteOfDay: number) {
  const hours = Math.floor(minuteOfDay / 60)
  const minutes = minuteOfDay % 60
  return `${String(hours).padStart(2, '0')}:${String(minutes).padStart(2, '0')}`
}

export function createCalendarSlotDefaults(
  startsOn: string,
  requestedMinuteOfDay: number
): CalendarCreateScheduleDefaults {
  const snappedMinute =
    Math.round(requestedMinuteOfDay / defaultScheduleDuration) *
    defaultScheduleDuration
  const startMinute = Math.max(
    weekStartMinute,
    Math.min(weekEndMinute - defaultScheduleDuration, snappedMinute)
  )
  return {
    startsOn,
    startTime: timeInputValue(startMinute),
    endTime: timeInputValue(startMinute + defaultScheduleDuration),
  }
}

function addMonths(value: string, months: number) {
  const date = parseLocalDate(value)
  date.setDate(1)
  date.setMonth(date.getMonth() + months)
  return localDateValue(date)
}

function addYears(value: string, years: number) {
  const date = parseLocalDate(value)
  date.setFullYear(date.getFullYear() + years, 0, 1)
  return localDateValue(date)
}

function daysBetween(from: string, to: string) {
  return Math.round(
    (parseLocalDate(to).getTime() - parseLocalDate(from).getTime()) / 86_400_000
  )
}

function mondayOf(value: string) {
  const date = parseLocalDate(value)
  const weekday = date.getDay()
  date.setDate(date.getDate() - (weekday === 0 ? 6 : weekday - 1))
  return localDateValue(date)
}

function monthStartOf(value: string) {
  const date = parseLocalDate(value)
  date.setDate(1)
  return localDateValue(date)
}

function yearStartOf(value: string) {
  const date = parseLocalDate(value)
  date.setMonth(0, 1)
  return localDateValue(date)
}

function datesInRange(from: string, to: string) {
  return Array.from({ length: daysBetween(from, to) }, (_, index) =>
    addDays(from, index)
  )
}

function scrollPageToTop() {
  if (typeof document === 'undefined') return
  document.documentElement.scrollTop = 0
  document.body.scrollTop = 0
  const workspaceMain = document.querySelector<HTMLElement>('.workspace-main')
  if (workspaceMain) workspaceMain.scrollTop = 0
}

function monthGridRange(value: string) {
  const monthStart = monthStartOf(value)
  const nextMonthStart = addMonths(monthStart, 1)
  const from = mondayOf(monthStart)
  const to = addDays(mondayOf(addDays(nextMonthStart, -1)), 7)
  return { from, to }
}

function defaultTimezone() {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
}

function formatDate(value: string) {
  const date = parseLocalDate(value)
  return `${date.getMonth() + 1}月${date.getDate()}日`
}

function formatWeekday(value: string) {
  return new Intl.DateTimeFormat('zh-CN', { weekday: 'short' }).format(
    parseLocalDate(value)
  )
}

function formatWeekTitle(from: string, to: string) {
  const start = parseLocalDate(from)
  const end = parseLocalDate(addDays(to, -1))
  const startYear = start.getFullYear()
  const endYear = end.getFullYear()
  if (startYear !== endYear) {
    return `${startYear}年${formatDate(from)}–${endYear}年${formatDate(
      addDays(to, -1)
    )}`
  }
  if (start.getMonth() === end.getMonth()) {
    return `${startYear}年${start.getMonth() + 1}月${start.getDate()}日–${end.getDate()}日`
  }
  return `${startYear}年${formatDate(from)}–${formatDate(addDays(to, -1))}`
}

function formatMonthTitle(value: string) {
  const date = parseLocalDate(value)
  return `${date.getFullYear()}年${date.getMonth() + 1}月`
}

function formatYearTitle(value: string) {
  return `${parseLocalDate(value).getFullYear()}年`
}

function dayNumber(value: string) {
  return parseLocalDate(value).getDate()
}

function formatTime(value: string, timezone: string) {
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: timezone,
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  }).format(new Date(value))
}

function timezoneOffsetLabel(timezone: string, dateValue: string) {
  try {
    const part = new Intl.DateTimeFormat('en-US', {
      timeZone: timezone,
      timeZoneName: 'longOffset',
    })
      .formatToParts(parseLocalDate(dateValue))
      .find((candidate) => candidate.type === 'timeZoneName')?.value
    return (part ?? timezone).replace('GMT', 'UTC')
  } catch {
    return timezone
  }
}

function minuteOfDay(value: string, timezone: string) {
  const parts = new Intl.DateTimeFormat('en-GB', {
    timeZone: timezone,
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  }).formatToParts(new Date(value))
  const hour = Number(parts.find((part) => part.type === 'hour')?.value ?? 0)
  const minute = Number(
    parts.find((part) => part.type === 'minute')?.value ?? 0
  )
  return hour * 60 + minute
}

function durationInMinutes(entry: CalendarEntryV2) {
  if (!entry.planned_start_at || !entry.planned_end_at) return 60
  return Math.max(
    1,
    Math.round(
      (new Date(entry.planned_end_at).getTime() -
        new Date(entry.planned_start_at).getTime()) /
        60_000
    )
  )
}

function allDayRangeLabel(entry: CalendarEntryV2) {
  const start = entry.planned_date ?? ''
  const exclusiveEnd = entry.all_day_end_date || addDays(start, 1)
  const inclusiveEnd = addDays(exclusiveEnd, -1)
  return start === inclusiveEnd
    ? formatDate(start)
    : `${formatDate(start)}–${formatDate(inclusiveEnd)}`
}

function offsetLabel(offsetSeconds: number) {
  const sign = offsetSeconds < 0 ? '-' : '+'
  const absolute = Math.abs(offsetSeconds)
  const hours = String(Math.floor(absolute / 3600)).padStart(2, '0')
  const minutes = String(Math.floor((absolute % 3600) / 60)).padStart(2, '0')
  return `UTC${sign}${hours}:${minutes}`
}

function draftForEntry(entry: CalendarEntryV2): ScheduleDraft {
  const plannedDate = entry.planned_date ?? localDateValue()
  return {
    scope: 'only-this',
    timingType: entry.timing_type === 'time_block' ? 'time_block' : 'date',
    plannedDate,
    allDayEndDate: entry.all_day_end_date || addDays(plannedDate, 1),
    localStartTime: entry.planned_start_at
      ? formatTime(entry.planned_start_at, entry.timezone)
      : '09:00',
    durationMinutes: durationInMinutes(entry),
    recurrenceType: 'daily',
    effectiveFrom: plannedDate,
    generateThroughExclusive: addDays(plannedDate, 31),
  }
}

export default function CalendarV2({
  initialDate = localDateValue(),
  initialTimezone = defaultTimezone(),
  initialView = 'week',
}: CalendarV2Props) {
  const [anchorDate, setAnchorDate] = useState(initialDate)
  const [calendarView, setCalendarView] = useState<CalendarView>(initialView)
  const [timezone, setTimezone] = useState(initialTimezone)
  const [selectedEntry, setSelectedEntry] = useState<CalendarEntryV2 | null>(
    null
  )
  const [draft, setDraft] = useState<ScheduleDraft | null>(null)
  const [offsetCandidates, setOffsetCandidates] = useState<
    Array<{ offset_seconds: number; utc: string }>
  >([])
  const [editorError, setEditorError] = useState('')
  const [createScheduleDefaults, setCreateScheduleDefaults] =
    useState<CalendarCreateScheduleDefaults | null>(null)

  const projectsQuery = useProjects()
  const onlyThis = useRescheduleOccurrenceMutation()
  const thisAndFollowing = useRescheduleThisAndFollowingMutation()
  const reopen = useReopenOccurrenceMutation()
  const todayValue = localDateValue()
  const weekStart = mondayOf(anchorDate)
  const weekEnd = addDays(weekStart, 7)
  const weekDates = useMemo(
    () => Array.from({ length: 7 }, (_, index) => addDays(weekStart, index)),
    [weekStart]
  )
  const monthStart = monthStartOf(anchorDate)
  const monthRange = useMemo(() => monthGridRange(anchorDate), [anchorDate])
  const monthDates = useMemo(
    () => datesInRange(monthRange.from, monthRange.to),
    [monthRange.from, monthRange.to]
  )
  const yearStart = yearStartOf(anchorDate)
  const yearEnd = addYears(yearStart, 1)
  const yearMonths = useMemo(
    () =>
      Array.from({ length: 12 }, (_, monthIndex) =>
        addMonths(yearStart, monthIndex)
      ),
    [yearStart]
  )
  const yearMonthGrids = useMemo(
    () =>
      yearMonths.map((month) => {
        const from = mondayOf(month)
        return {
          month,
          dates: Array.from({ length: 42 }, (_, index) => addDays(from, index)),
        }
      }),
    [yearMonths]
  )
  const queryRange =
    calendarView === 'week'
      ? { from: weekStart, to: weekEnd }
      : calendarView === 'month'
        ? monthRange
        : { from: yearStart, to: yearEnd }
  const entriesQuery = useCalendarEntries({
    from: queryRange.from,
    to: queryRange.to,
    timezone,
  })
  const visibleEntries = useMemo(
    () =>
      (entriesQuery.data ?? []).filter(
        (entry) => entry.timing_type !== 'unscheduled'
      ),
    [entriesQuery.data]
  )
  const dateEntries = useMemo(
    () =>
      visibleEntries.filter(
        (entry) => entry.timing_type === 'date' && entry.planned_date
      ),
    [visibleEntries]
  )
  const timeEntries = useMemo(
    () =>
      visibleEntries.filter(
        (entry) =>
          entry.timing_type === 'time_block' &&
          entry.planned_date &&
          entry.planned_start_at &&
          entry.planned_end_at
      ),
    [visibleEntries]
  )
  const entriesByDate = useMemo(() => {
    const grouped = new Map<string, CalendarEntryV2[]>()
    for (const entry of visibleEntries) {
      if (!entry.planned_date) continue
      const exclusiveEnd =
        entry.timing_type === 'date'
          ? entry.all_day_end_date || addDays(entry.planned_date, 1)
          : addDays(entry.planned_date, 1)
      let date = entry.planned_date
      let safety = 0
      while (date < exclusiveEnd && safety < 370) {
        const dayEntries = grouped.get(date)
        if (dayEntries) dayEntries.push(entry)
        else grouped.set(date, [entry])
        date = addDays(date, 1)
        safety += 1
      }
    }
    for (const dayEntries of grouped.values()) {
      dayEntries.sort((left, right) => {
        if (left.timing_type !== right.timing_type) {
          return left.timing_type === 'date' ? -1 : 1
        }
        return (left.planned_start_at ?? '').localeCompare(
          right.planned_start_at ?? ''
        )
      })
    }
    return grouped
  }, [visibleEntries])
  const entryCountByMonth = useMemo(() => {
    const counts = new Map<string, number>()
    for (const entry of visibleEntries) {
      if (!entry.planned_date) continue
      const monthKey = entry.planned_date.slice(0, 7)
      counts.set(monthKey, (counts.get(monthKey) ?? 0) + 1)
    }
    return counts
  }, [visibleEntries])
  const periodTitle =
    calendarView === 'week'
      ? formatWeekTitle(weekStart, weekEnd)
      : calendarView === 'month'
        ? formatMonthTitle(monthStart)
        : formatYearTitle(yearStart)
  const periodName =
    calendarView === 'week' ? '周' : calendarView === 'month' ? '月' : '年'
  const timezoneOptions = commonTimezones.includes(timezone)
    ? commonTimezones
    : [timezone, ...commonTimezones]

  function openEditor(entry: CalendarEntryV2, plannedDate?: string) {
    const nextDraft = draftForEntry(entry)
    if (plannedDate) {
      nextDraft.plannedDate = plannedDate
      nextDraft.effectiveFrom = plannedDate
    }
    setSelectedEntry(entry)
    setDraft(nextDraft)
    setOffsetCandidates([])
    setEditorError('')
  }

  function closeEditor() {
    setSelectedEntry(null)
    setDraft(null)
    setOffsetCandidates([])
    setEditorError('')
  }

  function openCreateSchedule(defaults: CalendarCreateScheduleDefaults) {
    closeEditor()
    setCreateScheduleDefaults(defaults)
  }

  function updateDraft(update: Partial<ScheduleDraft>) {
    setDraft((current) => (current ? { ...current, ...update } : current))
  }

  function handleDragStart(
    event: DragEvent<HTMLButtonElement>,
    entry: CalendarEntryV2
  ) {
    if (entry.execution_status === 'done') {
      event.preventDefault()
      openEditor(entry)
      return
    }
    event.dataTransfer.setData('text/task-occurrence-id', entry.occurrence_id)
  }

  function handleDrop(event: DragEvent<HTMLDivElement>, plannedDate: string) {
    event.preventDefault()
    const occurrenceID = event.dataTransfer.getData('text/task-occurrence-id')
    const entry = visibleEntries.find(
      (candidate) => candidate.occurrence_id === occurrenceID
    )
    if (entry) openEditor(entry, plannedDate)
  }

  async function submitSchedule() {
    if (!selectedEntry || !draft || selectedEntry.execution_status === 'done')
      return
    setEditorError('')
    const selectedOffsets =
      draft.selectedOffsetSeconds === undefined
        ? undefined
        : { [draft.plannedDate]: draft.selectedOffsetSeconds }
    try {
      if (draft.scope === 'this-and-following' && selectedEntry.recurring) {
        await thisAndFollowing.mutateAsync({
          projectID: selectedEntry.project_id,
          taskID: selectedEntry.task_id,
          input: {
            expected_task_revision: selectedEntry.task_revision,
            expected_schedule_revision: selectedEntry.schedule_revision,
            effective_from: draft.effectiveFrom,
            generate_through_exclusive: draft.generateThroughExclusive,
            schedule: {
              recurrence_type: draft.recurrenceType,
              timing_type: draft.timingType,
              timezone,
              starts_on: draft.effectiveFrom,
              ...(draft.timingType === 'time_block'
                ? {
                    local_start_time: draft.localStartTime,
                    duration_minutes: draft.durationMinutes,
                  }
                : {}),
              rule: { interval: 1 },
            },
            ...(selectedOffsets ? { selected_offsets: selectedOffsets } : {}),
          },
        })
      } else {
        await onlyThis.mutateAsync({
          projectID: selectedEntry.project_id,
          taskID: selectedEntry.task_id,
          occurrenceID: selectedEntry.occurrence_id,
          input: {
            expected_task_revision: selectedEntry.task_revision,
            expected_schedule_revision: selectedEntry.schedule_revision,
            expected_occurrence_revision: selectedEntry.occurrence_revision,
            timing: {
              timing_type: draft.timingType,
              timezone,
              planned_date: draft.plannedDate,
              ...(draft.timingType === 'date'
                ? { all_day_end_date: draft.allDayEndDate }
                : {
                    local_start_time: draft.localStartTime,
                    duration_minutes: draft.durationMinutes,
                  }),
            },
            ...(selectedOffsets ? { selected_offsets: selectedOffsets } : {}),
          },
        })
      }
      closeEditor()
    } catch (error) {
      if (error instanceof TaskDomainAPIError) {
        const candidates = error.details?.offset_candidates ?? []
        if (candidates.length > 0) setOffsetCandidates(candidates)
        setEditorError(error.message)
      } else {
        setEditorError('保存日程失败，请稍后重试。')
      }
    }
  }

  async function reopenSelectedEntry() {
    if (!selectedEntry) return
    await reopen.mutateAsync({
      projectID: selectedEntry.project_id,
      taskID: selectedEntry.task_id,
      occurrenceID: selectedEntry.occurrence_id,
      expectedRevisions: {
        expected_task_revision: selectedEntry.task_revision,
        expected_schedule_revision: selectedEntry.schedule_revision,
        expected_occurrence_revisions: {
          [selectedEntry.occurrence_id]: selectedEntry.occurrence_revision,
        },
      },
    })
    closeEditor()
  }

  function changeCalendarView(nextView: CalendarView) {
    setCalendarView(nextView)
    closeEditor()
  }

  function navigatePeriod(direction: -1 | 1) {
    setAnchorDate((current) => {
      if (calendarView === 'week') return addDays(current, direction * 7)
      if (calendarView === 'month') return addMonths(current, direction)
      return addYears(current, direction)
    })
    closeEditor()
  }

  function drillIntoMonth(month: string) {
    setAnchorDate(month)
    setCalendarView('month')
    closeEditor()
    scrollPageToTop()
  }

  function drillIntoWeek(date: string) {
    setAnchorDate(date)
    setCalendarView('week')
    closeEditor()
    scrollPageToTop()
  }

  return (
    <section className="td-page td-calendar-page calendar-v2-page">
      <header className="td-page-header calendar-v2-heading">
        <div>
          <div className="td-title-line">
            <h1>日历</h1>
            <span>统一任务安排</span>
          </div>
          <p>全天安排与时间块都来自任务实例；未安排任务不会出现在日历中。</p>
        </div>
        <aside className="calendar-v2-controls" aria-label="日历显示设置">
          <div className="calendar-v2-view-setting">
            <span className="calendar-v2-control-label">
              <strong>显示方式</strong>
              <small>切换时间尺度</small>
            </span>
            <div
              className="calendar-v2-view-switch"
              role="group"
              aria-label="日历视图"
            >
              {(
                [
                  ['week', '周'],
                  ['month', '月'],
                  ['year', '年'],
                ] as const
              ).map(([view, label]) => (
                <button
                  key={view}
                  type="button"
                  className={calendarView === view ? 'is-active' : undefined}
                  aria-pressed={calendarView === view}
                  onClick={() => changeCalendarView(view)}
                >
                  {label}
                </button>
              ))}
            </div>
          </div>
          <label className="calendar-v2-timezone">
            <span>
              <Globe2 size={14} aria-hidden="true" />
              显示时区
            </span>
            <select
              value={timezone}
              onChange={(event) => setTimezone(event.target.value)}
              aria-label="显示时区"
            >
              {timezoneOptions.map((option) => (
                <option key={option} value={option}>
                  {option}
                </option>
              ))}
            </select>
          </label>
        </aside>
      </header>

      <div
        className={`calendar-v2-workspace ${selectedEntry ? 'has-editor' : ''}`}
      >
        <section className={`calendar-v2-shell is-${calendarView}-view`}>
          <div className="calendar-v2-toolbar">
            <div className="calendar-v2-navigation">
              <button
                type="button"
                className="is-icon"
                onClick={() => navigatePeriod(-1)}
                aria-label={`上一${periodName}`}
                title={`上一${periodName}`}
              >
                <ChevronLeft size={16} aria-hidden="true" />
              </button>
              <button
                type="button"
                className="calendar-v2-today"
                onClick={() => setAnchorDate(todayValue)}
              >
                今天
              </button>
              <button
                type="button"
                className="is-icon"
                onClick={() => navigatePeriod(1)}
                aria-label={`下一${periodName}`}
                title={`下一${periodName}`}
              >
                <ChevronRight size={16} aria-hidden="true" />
              </button>
              <button
                type="button"
                className="calendar-v2-create"
                onClick={() => openCreateSchedule({ startsOn: anchorDate })}
              >
                <Plus size={15} aria-hidden="true" />
                新增日程
              </button>
            </div>
            <strong aria-live="polite">{periodTitle}</strong>
            {entriesQuery.isFetching ? (
              <span className="calendar-v2-period-status" role="status">
                正在更新…
              </span>
            ) : (
              <span className="calendar-v2-period-status">
                {visibleEntries.length} 项安排
                <i aria-hidden="true" />
                {timezoneOffsetLabel(timezone, anchorDate)}
              </span>
            )}
          </div>

          {entriesQuery.isError ? (
            <div className="td-inline-error" role="alert">
              日历加载失败，请稍后重试。
            </div>
          ) : calendarView === 'week' ? (
            <>
              <div className="calendar-v2-week-head" aria-hidden="true">
                <span />
                {weekDates.map((date) => (
                  <strong
                    className={date === todayValue ? 'is-today' : undefined}
                    key={date}
                  >
                    <span>{formatWeekday(date)}</span>
                    {formatDate(date)}
                  </strong>
                ))}
              </div>

              <section
                className="calendar-v2-all-day"
                role="region"
                aria-label="全天安排"
              >
                <strong className="calendar-v2-lane-label">全天</strong>
                <div className="calendar-v2-all-day-grid">
                  {weekDates.map((date) => (
                    <div
                      key={date}
                      className="calendar-v2-drop-day"
                      onDragOver={(event) => event.preventDefault()}
                      onDrop={(event) => handleDrop(event, date)}
                      aria-hidden="true"
                    />
                  ))}
                  {dateEntries.map((entry) => {
                    const startIndex = Math.max(
                      0,
                      Math.min(6, daysBetween(weekStart, entry.planned_date!))
                    )
                    const exclusiveEnd =
                      entry.all_day_end_date || addDays(entry.planned_date!, 1)
                    const endIndex = Math.min(
                      7,
                      Math.max(
                        startIndex + 1,
                        daysBetween(weekStart, exclusiveEnd)
                      )
                    )
                    return (
                      <button
                        key={entry.occurrence_id}
                        type="button"
                        className={`calendar-v2-entry is-all-day is-${entry.execution_status}`}
                        style={
                          {
                            '--calendar-column-start': startIndex + 1,
                            '--calendar-column-end': endIndex + 1,
                          } as CSSProperties
                        }
                        data-exclusive-end={exclusiveEnd}
                        draggable={entry.execution_status !== 'done'}
                        onDragStart={(event) => handleDragStart(event, entry)}
                        onClick={() => openEditor(entry)}
                        aria-label={`编辑日程：${entry.task_title}`}
                      >
                        <strong>{entry.task_title}</strong>
                        <span>{allDayRangeLabel(entry)}</span>
                      </button>
                    )
                  })}
                  {dateEntries.length === 0 ? (
                    <p className="calendar-v2-empty-lane">本周没有全天安排</p>
                  ) : null}
                </div>
              </section>

              <section
                className="calendar-v2-time-section"
                role="region"
                aria-label="时间安排"
              >
                <div className="calendar-v2-time-grid">
                  <div className="calendar-v2-hour-axis" aria-hidden="true">
                    {weekHours.map((hour) => (
                      <span key={hour}>{String(hour).padStart(2, '0')}:00</span>
                    ))}
                  </div>
                  {weekDates.map((date) => (
                    <div
                      key={date}
                      className={`calendar-v2-time-column ${
                        date === todayValue ? 'is-today' : ''
                      }`}
                      onDragOver={(event) => event.preventDefault()}
                      onDrop={(event) => handleDrop(event, date)}
                    >
                      {weekHours.map((hour) => (
                        <i key={hour} aria-hidden="true" />
                      ))}
                      {weekTimeSlots.map((minute) => {
                        const time = timeInputValue(minute)
                        return (
                          <button
                            key={minute}
                            type="button"
                            className="calendar-v2-create-slot"
                            style={
                              {
                                '--calendar-slot-minute':
                                  minute - weekStartMinute,
                              } as CSSProperties
                            }
                            aria-label={`新增日程：${formatDate(date)} ${time}`}
                            title={`${time} 新增日程`}
                            onClick={() =>
                              openCreateSchedule(
                                createCalendarSlotDefaults(date, minute)
                              )
                            }
                          >
                            <span aria-hidden="true">＋ 新增</span>
                          </button>
                        )
                      })}
                      {timeEntries
                        .filter((entry) => entry.planned_date === date)
                        .map((entry) => (
                          <button
                            key={entry.occurrence_id}
                            type="button"
                            className={`calendar-v2-entry is-time-block is-${entry.execution_status}`}
                            style={
                              {
                                '--calendar-start-minute': Math.max(
                                  0,
                                  minuteOfDay(
                                    entry.planned_start_at!,
                                    timezone
                                  ) -
                                    7 * 60
                                ),
                                '--calendar-duration-minute': Math.max(
                                  30,
                                  durationInMinutes(entry)
                                ),
                              } as CSSProperties
                            }
                            draggable={entry.execution_status !== 'done'}
                            onDragStart={(event) =>
                              handleDragStart(event, entry)
                            }
                            onClick={() => openEditor(entry)}
                            aria-label={`编辑日程：${entry.task_title}`}
                          >
                            <time>
                              {formatTime(entry.planned_start_at!, timezone)}–
                              {formatTime(entry.planned_end_at!, timezone)}
                            </time>
                            <strong>{entry.task_title}</strong>
                          </button>
                        ))}
                    </div>
                  ))}
                </div>
              </section>
            </>
          ) : calendarView === 'month' ? (
            <section
              className="calendar-v2-month-view"
              role="grid"
              aria-label={`${periodTitle}日历`}
            >
              <div className="calendar-v2-month-weekdays" role="row">
                {calendarWeekdays.map((weekday) => (
                  <span key={weekday} role="columnheader">
                    {weekday}
                  </span>
                ))}
              </div>
              <div className="calendar-v2-month-grid">
                {monthDates.map((date) => {
                  const dayEntries = entriesByDate.get(date) ?? []
                  const isCurrentMonth =
                    date.slice(0, 7) === monthStart.slice(0, 7)
                  const isToday = date === todayValue
                  return (
                    <div
                      key={date}
                      className={`calendar-v2-month-day ${
                        isCurrentMonth ? '' : 'is-outside'
                      } ${isToday ? 'is-today' : ''}`}
                      role="gridcell"
                      onDragOver={(event) => event.preventDefault()}
                      onDrop={(event) => handleDrop(event, date)}
                    >
                      <header>
                        <time dateTime={date}>{dayNumber(date)}</time>
                        {dayEntries.length > 0 ? (
                          <span>{dayEntries.length} 项</span>
                        ) : null}
                      </header>
                      <div className="calendar-v2-month-entries">
                        {dayEntries.slice(0, 3).map((entry) => (
                          <button
                            key={`${date}-${entry.occurrence_id}`}
                            type="button"
                            className={`calendar-v2-month-entry is-${entry.timing_type} is-${entry.execution_status}`}
                            draggable={entry.execution_status !== 'done'}
                            onDragStart={(event) =>
                              handleDragStart(event, entry)
                            }
                            onClick={() => openEditor(entry)}
                            aria-label={`编辑日程：${entry.task_title}`}
                          >
                            {entry.timing_type === 'time_block' &&
                            entry.planned_start_at ? (
                              <time>
                                {formatTime(entry.planned_start_at, timezone)}
                              </time>
                            ) : (
                              <i aria-hidden="true" />
                            )}
                            <span>{entry.task_title}</span>
                          </button>
                        ))}
                        {dayEntries.length > 3 ? (
                          <span className="calendar-v2-more">
                            还有 {dayEntries.length - 3} 项
                          </span>
                        ) : null}
                      </div>
                    </div>
                  )
                })}
              </div>
            </section>
          ) : (
            <section
              className="calendar-v2-year-view"
              aria-label={`${periodTitle}总览`}
            >
              {yearMonthGrids.map(({ month, dates }) => (
                <article className="calendar-v2-year-month" key={month}>
                  <button
                    type="button"
                    className="calendar-v2-year-month-title"
                    onClick={() => drillIntoMonth(month)}
                    aria-label={`查看${formatMonthTitle(month)}`}
                  >
                    <strong>{parseLocalDate(month).getMonth() + 1}月</strong>
                    <span>
                      {entryCountByMonth.get(month.slice(0, 7)) ?? 0} 项
                    </span>
                  </button>
                  <div className="calendar-v2-year-weekdays" aria-hidden="true">
                    {calendarWeekdays.map((weekday) => (
                      <span key={weekday}>{weekday.slice(1)}</span>
                    ))}
                  </div>
                  <div className="calendar-v2-year-days">
                    {dates.map((date) => {
                      const isCurrentMonth =
                        date.slice(0, 7) === month.slice(0, 7)
                      const dayEntries = entriesByDate.get(date) ?? []
                      if (!isCurrentMonth) {
                        return <span key={date} aria-hidden="true" />
                      }
                      return (
                        <button
                          key={date}
                          type="button"
                          className={`${date === todayValue ? 'is-today' : ''} ${
                            dayEntries.length > 0 ? 'has-entries' : ''
                          }`}
                          onClick={() => drillIntoWeek(date)}
                          aria-label={`${formatMonthTitle(date)}${dayNumber(
                            date
                          )}日，${dayEntries.length}项安排`}
                          title={
                            dayEntries.length > 0
                              ? dayEntries
                                  .map((entry) => entry.task_title)
                                  .join('、')
                              : undefined
                          }
                        >
                          {dayNumber(date)}
                        </button>
                      )
                    })}
                  </div>
                </article>
              ))}
            </section>
          )}
        </section>

        {selectedEntry && draft ? (
          <section
            className="calendar-v2-editor"
            role="dialog"
            aria-modal="false"
            aria-label={`编辑日程：${selectedEntry.task_title}`}
          >
            <header>
              <div>
                <span>安排本次执行</span>
                <h2>{selectedEntry.task_title}</h2>
              </div>
              <button
                type="button"
                onClick={closeEditor}
                aria-label="关闭日程设置"
              >
                ×
              </button>
            </header>

            {selectedEntry.execution_status === 'done' ? (
              <div className="calendar-v2-done-notice" role="alert">
                <p>已完成的任务不能移动，请先重新打开。</p>
                <button
                  type="button"
                  onClick={() => void reopenSelectedEntry()}
                  disabled={reopen.isPending}
                >
                  重新打开任务
                </button>
              </div>
            ) : null}

            <fieldset className="calendar-v2-scope">
              <legend>修改范围</legend>
              <label>
                <input
                  type="radio"
                  name="schedule-scope"
                  checked={draft.scope === 'only-this'}
                  onChange={() => updateDraft({ scope: 'only-this' })}
                />
                仅本次
              </label>
              {selectedEntry.recurring ? (
                <label>
                  <input
                    type="radio"
                    name="schedule-scope"
                    checked={draft.scope === 'this-and-following'}
                    onChange={() =>
                      updateDraft({ scope: 'this-and-following' })
                    }
                  />
                  本次及以后
                </label>
              ) : null}
            </fieldset>

            <div className="calendar-v2-editor-grid">
              <label>
                <span>安排方式</span>
                <select
                  aria-label="安排方式"
                  value={draft.timingType}
                  onChange={(event) =>
                    updateDraft({
                      timingType: event.target
                        .value as ScheduleDraft['timingType'],
                    })
                  }
                >
                  <option value="date">全天</option>
                  <option value="time_block">时间块</option>
                </select>
              </label>
              <label>
                <span>计划日期</span>
                <input
                  type="date"
                  aria-label="计划日期"
                  value={draft.plannedDate}
                  onChange={(event) =>
                    updateDraft({ plannedDate: event.target.value })
                  }
                />
              </label>
              {draft.timingType === 'date' ? (
                <label>
                  <span>结束日期（不含）</span>
                  <input
                    type="date"
                    aria-label="结束日期（不含）"
                    min={addDays(draft.plannedDate, 1)}
                    value={draft.allDayEndDate}
                    onChange={(event) =>
                      updateDraft({ allDayEndDate: event.target.value })
                    }
                  />
                </label>
              ) : (
                <>
                  <label>
                    <span>开始时间</span>
                    <input
                      type="time"
                      aria-label="开始时间"
                      value={draft.localStartTime}
                      onChange={(event) =>
                        updateDraft({ localStartTime: event.target.value })
                      }
                    />
                  </label>
                  <label>
                    <span>时长（分钟）</span>
                    <input
                      type="number"
                      min={1}
                      aria-label="时长（分钟）"
                      value={draft.durationMinutes}
                      onChange={(event) =>
                        updateDraft({
                          durationMinutes: Number(event.target.value),
                        })
                      }
                    />
                  </label>
                </>
              )}
            </div>

            {draft.scope === 'this-and-following' && selectedEntry.recurring ? (
              <div className="calendar-v2-following-fields">
                <label>
                  <span>重复规则</span>
                  <select
                    aria-label="重复规则"
                    value={draft.recurrenceType}
                    onChange={(event) =>
                      updateDraft({
                        recurrenceType: event.target
                          .value as ScheduleDraft['recurrenceType'],
                      })
                    }
                  >
                    <option value="daily">每天</option>
                    <option value="weekly">每周</option>
                    <option value="monthly">每月</option>
                  </select>
                </label>
                <label>
                  <span>生效日期</span>
                  <input
                    type="date"
                    aria-label="生效日期"
                    value={draft.effectiveFrom}
                    onChange={(event) =>
                      updateDraft({ effectiveFrom: event.target.value })
                    }
                  />
                </label>
                <label>
                  <span>生成至（不含）</span>
                  <input
                    type="date"
                    aria-label="生成至（不含）"
                    value={draft.generateThroughExclusive}
                    onChange={(event) =>
                      updateDraft({
                        generateThroughExclusive: event.target.value,
                      })
                    }
                  />
                </label>
              </div>
            ) : null}

            {offsetCandidates.length > 0 ? (
              <fieldset className="calendar-v2-offsets">
                <legend>这个本地时间出现了两次，请选择准确偏移</legend>
                {offsetCandidates.map((candidate) => (
                  <label key={candidate.utc}>
                    <input
                      type="radio"
                      name="selected-offset"
                      checked={
                        draft.selectedOffsetSeconds === candidate.offset_seconds
                      }
                      onChange={() =>
                        updateDraft({
                          selectedOffsetSeconds: candidate.offset_seconds,
                        })
                      }
                    />
                    {offsetLabel(candidate.offset_seconds)} · {candidate.utc}
                  </label>
                ))}
              </fieldset>
            ) : null}

            {editorError ? (
              <p className="calendar-v2-error">{editorError}</p>
            ) : null}
            <footer>
              <button type="button" onClick={closeEditor}>
                取消
              </button>
              <button
                type="button"
                className="primary-action"
                onClick={() => void submitSchedule()}
                disabled={
                  selectedEntry.execution_status === 'done' ||
                  onlyThis.isPending ||
                  thisAndFollowing.isPending ||
                  (offsetCandidates.length > 0 &&
                    draft.selectedOffsetSeconds === undefined)
                }
              >
                保存日程
              </button>
            </footer>
          </section>
        ) : null}
      </div>

      {createScheduleDefaults ? (
        <ScheduleCreateDialog
          projects={projectsQuery.data ?? []}
          initialSchedule={createScheduleDefaults}
          timezone={timezone}
          onClose={() => setCreateScheduleDefaults(null)}
        />
      ) : null}
    </section>
  )
}
