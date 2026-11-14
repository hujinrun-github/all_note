import { PlanningHeading } from '../components/planning/PlanningWorkspace'
import { Button } from '../components/ui/button'
import { Card } from '../components/ui/card'
import { NativeSelect } from '../components/ui/native-select'
import { useMemo, useState, type CSSProperties, type DragEvent } from 'react'
import {
  CalendarClock,
  Check,
  ChevronLeft,
  ChevronRight,
  Circle,
  Globe2,
  Plus,
} from 'lucide-react'

import {
  TaskDomainAPIError,
  type CalendarEntryV2,
  type OccurrenceV2,
  type TimingType,
} from '../api/taskDomain'
import {
  useCalendarEntries,
  useCompleteOccurrenceMutation,
  useOccurrences,
  useProjects,
  useReopenOccurrenceMutation,
  useRescheduleOccurrenceMutation,
  useRescheduleThisAndFollowingMutation,
} from '../hooks/useTaskDomain'
import { ScheduleCreateDialog } from '../components/taskDomain/ScheduleCreateDialog'
import { DetailPanel } from '../components/ui/DetailPanel'
import { useMediaQuery } from '../hooks/useMediaQuery'

type ScheduleScope = 'only-this' | 'this-and-following'
type CalendarView = 'week' | 'month' | 'year' | 'agenda'

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
}

type EditableEntry = Omit<
  CalendarEntryV2,
  'project_revision' | 'generated_schedule_revision'
>
type DropTarget = { date: string; minute?: number; allDay?: boolean }

interface CalendarCreateScheduleDefaults {
  startsOn: string
  startTime?: string
  endTime?: string
}

const dayMinutes = 24 * 60
const workStart = 7 * 60
const workEnd = 22 * 60
const defaultScheduleDuration = 30
const occurrenceDragType = 'text/task-occurrence-id'
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
    0,
    Math.min(dayMinutes - defaultScheduleDuration, snappedMinute)
  )
  return {
    startsOn,
    startTime: timeInputValue(startMinute),
    endTime: timeInputValue(
      Math.min(dayMinutes - 1, startMinute + defaultScheduleDuration)
    ),
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

function CalendarEntryCompletionButton({
  entry,
  pending,
  onComplete,
}: {
  entry: CalendarEntryV2
  pending: boolean
  onComplete: () => void
}) {
  const done = entry.execution_status === 'done'
  const canComplete = ['open', 'active', 'blocked'].includes(
    entry.execution_status
  )
  return (
    <button
      type="button"
      className={`calendar-entry-complete-toggle ${done ? 'is-done' : ''}`}
      aria-label={`完成${entry.task_title}`}
      aria-pressed={done}
      title={
        done ? '任务已完成' : canComplete ? '完成任务' : '当前状态不可完成'
      }
      disabled={!canComplete || pending}
      onClick={(event) => {
        event.stopPropagation()
        onComplete()
      }}
    >
      {done ? <Check aria-hidden="true" /> : <Circle aria-hidden="true" />}
    </button>
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

function dateInTimezone(value: Date | string, timezone: string) {
  return new Intl.DateTimeFormat('sv-SE', {
    timeZone: timezone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(new Date(value))
}

function minuteOfDay(time: string) {
  const [hours, minutes] = time.split(':').map(Number)
  return hours * 60 + minutes
}

function durationInMinutes(entry: EditableEntry) {
  if (!entry.planned_start_at || !entry.planned_end_at)
    return defaultScheduleDuration
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

interface AllDayEntryLayout {
  entry: CalendarEntryV2
  startIndex: number
  endIndex: number
  row: number
}

function layoutAllDayEntries(
  entries: CalendarEntryV2[],
  rangeStart: string,
  dayCount: number
) {
  const ranges = entries
    .flatMap<AllDayEntryLayout>((entry) => {
      if (!entry.planned_date) return []
      const exclusiveEnd =
        entry.all_day_end_date || addDays(entry.planned_date, 1)
      const rawStartIndex = daysBetween(rangeStart, entry.planned_date)
      const rawEndIndex = daysBetween(rangeStart, exclusiveEnd)
      if (rawEndIndex <= 0 || rawStartIndex >= dayCount) return []

      return [
        {
          entry,
          startIndex: Math.max(0, rawStartIndex),
          endIndex: Math.min(
            dayCount,
            Math.max(rawEndIndex, rawStartIndex + 1)
          ),
          row: 0,
        },
      ]
    })
    .sort(
      (left, right) =>
        left.startIndex - right.startIndex ||
        right.endIndex - left.endIndex ||
        left.entry.task_title.localeCompare(right.entry.task_title)
    )

  const rowEndIndexes: number[] = []
  for (const range of ranges) {
    const availableRow = rowEndIndexes.findIndex(
      (rowEndIndex) => rowEndIndex <= range.startIndex
    )
    const row = availableRow === -1 ? rowEndIndexes.length : availableRow
    range.row = row + 1
    rowEndIndexes[row] = range.endIndex
  }

  return {
    entries: ranges,
    rowCount: Math.max(1, rowEndIndexes.length),
  }
}

function offsetLabel(offsetSeconds: number) {
  const sign = offsetSeconds < 0 ? '-' : '+'
  const absolute = Math.abs(offsetSeconds)
  const hours = String(Math.floor(absolute / 3600)).padStart(2, '0')
  const minutes = String(Math.floor((absolute % 3600) / 60)).padStart(2, '0')
  return `UTC${sign}${hours}:${minutes}`
}

function draftForEntry(
  entry: EditableEntry,
  timezone: string,
  fallbackDate: string,
  target?: DropTarget
): ScheduleDraft {
  const entryDate = entry.planned_start_at
    ? dateInTimezone(entry.planned_start_at, timezone)
    : (entry.planned_date ?? fallbackDate)
  const plannedDate = target?.date ?? entryDate
  const allDayLength =
    entry.planned_date && entry.all_day_end_date
      ? Math.max(1, daysBetween(entry.planned_date, entry.all_day_end_date))
      : 1
  return {
    scope: 'only-this',
    timingType: target?.allDay
      ? 'date'
      : target?.minute !== undefined || entry.timing_type !== 'date'
        ? 'time_block'
        : 'date',
    plannedDate,
    allDayEndDate: addDays(plannedDate, allDayLength),
    localStartTime:
      target?.minute !== undefined
        ? timeInputValue(target.minute)
        : entry.planned_start_at
          ? formatTime(entry.planned_start_at, timezone)
          : '09:00',
    durationMinutes: durationInMinutes(entry),
  }
}

function movable(entry: EditableEntry) {
  return (
    entry.execution_status !== 'done' &&
    entry.execution_status !== 'skipped' &&
    entry.execution_status !== 'cancelled'
  )
}

function canEditSeries(entry: EditableEntry) {
  return entry.recurring && /^\d{4}-\d{2}-\d{2}$/.test(entry.occurrence_key)
}

function pendingEntry(item: OccurrenceV2): EditableEntry | null {
  if (!item.project_id || !item.task_revision || !item.schedule_revision)
    return null
  return {
    ...item,
    project_id: item.project_id,
    task_revision: item.task_revision,
    schedule_revision: item.schedule_revision,
    occurrence_id: item.id,
    task_title: item.title ?? '未命名任务',
    occurrence_revision: item.revision,
    timing_type: item.timing_type ?? 'unscheduled',
    timezone: item.timezone ?? defaultTimezone(),
    recurring: item.recurring ?? false,
  }
}

export default function CalendarV2({
  initialDate,
  initialTimezone = defaultTimezone(),
  initialView,
}: CalendarV2Props) {
  const mobile = useMediaQuery('(max-width: 820px)')
  const [anchorDate, setAnchorDate] = useState(
    () => initialDate ?? dateInTimezone(new Date(), initialTimezone)
  )
  const [calendarView, setCalendarView] = useState<CalendarView>(
    initialView ?? (mobile ? 'agenda' : 'week')
  )
  const [timezone, setTimezone] = useState(initialTimezone)
  const [selectedEntry, setSelectedEntry] = useState<EditableEntry | null>(null)
  const [draft, setDraft] = useState<ScheduleDraft | null>(null)
  const [offsetCandidates, setOffsetCandidates] = useState<
    Array<{ offset_seconds: number; utc: string }>
  >([])
  const [selectedOffsets, setSelectedOffsets] = useState<
    Record<string, number>
  >({})
  const [editorError, setEditorError] = useState('')
  const [completionError, setCompletionError] = useState('')
  const [pendingOpen, setPendingOpen] = useState(false)
  const [showEarly, setShowEarly] = useState(false)
  const [showLate, setShowLate] = useState(false)
  const [dropPreview, setDropPreview] = useState<{
    date: string
    minute: number
  } | null>(null)
  const [createScheduleDefaults, setCreateScheduleDefaults] =
    useState<CalendarCreateScheduleDefaults | null>(null)

  const projectsQuery = useProjects()
  const showPending = calendarView === 'week' && pendingOpen && !selectedEntry
  const unscheduledQuery = useOccurrences(
    { scope: 'unscheduled' },
    { enabled: calendarView === 'week' && pendingOpen }
  )
  const overdueQuery = useOccurrences(
    { scope: 'overdue', timezone },
    { enabled: calendarView === 'week' && pendingOpen }
  )
  const pendingEntries = useMemo(() => {
    const byID = new Map<string, EditableEntry>()
    for (const item of [
      ...(unscheduledQuery.data ?? []),
      ...(overdueQuery.data ?? []),
    ]) {
      const entry = pendingEntry(item)
      if (entry && movable(entry)) byID.set(entry.occurrence_id, entry)
    }
    return [...byID.values()]
  }, [unscheduledQuery.data, overdueQuery.data])
  const onlyThis = useRescheduleOccurrenceMutation()
  const thisAndFollowing = useRescheduleThisAndFollowingMutation()
  const complete = useCompleteOccurrenceMutation()
  const reopen = useReopenOccurrenceMutation()
  const todayValue = dateInTimezone(new Date(), timezone)
  const weekStart = mondayOf(anchorDate)
  const weekEnd = addDays(weekStart, 7)
  const weekDates = useMemo(
    () => Array.from({ length: 7 }, (_, index) => addDays(weekStart, index)),
    [weekStart]
  )
  const agendaEnd = addDays(anchorDate, 7)
  const agendaDates = useMemo(
    () => Array.from({ length: 7 }, (_, index) => addDays(anchorDate, index)),
    [anchorDate]
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
    calendarView === 'agenda'
      ? { from: anchorDate, to: agendaEnd }
      : calendarView === 'week'
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
  const timedByDate = useMemo(() => {
    const byDate = new Map<
      string,
      Array<{ entry: CalendarEntryV2; start: number; end: number }>
    >()
    for (const entry of visibleEntries) {
      if (
        entry.timing_type !== 'time_block' ||
        !entry.planned_start_at ||
        !entry.planned_end_at
      )
        continue
      const firstDate = dateInTimezone(entry.planned_start_at, timezone)
      const lastDate = dateInTimezone(entry.planned_end_at, timezone)
      const firstMinute = minuteOfDay(
        formatTime(entry.planned_start_at, timezone)
      )
      const lastMinute = minuteOfDay(formatTime(entry.planned_end_at, timezone))
      for (const date of weekDates) {
        if (
          date < firstDate ||
          date > lastDate ||
          (date === lastDate && date !== firstDate && lastMinute === 0)
        )
          continue
        const start = date === firstDate ? firstMinute : 0
        const end =
          date === lastDate ? Math.max(start + 1, lastMinute) : dayMinutes
        const blocks = byDate.get(date) ?? []
        blocks.push({ entry, start, end })
        byDate.set(date, blocks)
      }
    }
    return byDate
  }, [visibleEntries, timezone, weekDates])
  const hasEarly = [...timedByDate.values()].some((blocks) =>
    blocks.some((block) => block.start < workStart)
  )
  const hasLate = [...timedByDate.values()].some((blocks) =>
    blocks.some((block) => block.end > workEnd)
  )
  const timelineStart = hasEarly || showEarly ? 0 : workStart
  const timelineEnd = hasLate || showLate ? dayMinutes : workEnd
  const allDayLayout = useMemo(
    () => layoutAllDayEntries(dateEntries, weekStart, weekDates.length),
    [dateEntries, weekStart, weekDates.length]
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
    calendarView === 'agenda'
      ? formatWeekTitle(anchorDate, agendaEnd)
      : calendarView === 'week'
        ? formatWeekTitle(weekStart, weekEnd)
        : calendarView === 'month'
          ? formatMonthTitle(monthStart)
          : formatYearTitle(yearStart)
  const periodName =
    calendarView === 'week' || calendarView === 'agenda'
      ? '周'
      : calendarView === 'month'
        ? '月'
        : '年'
  const timezoneOptions = commonTimezones.includes(timezone)
    ? commonTimezones
    : [timezone, ...commonTimezones]
  const seriesEdit = draft?.scope === 'this-and-following'
  const editorTimezone =
    seriesEdit && selectedEntry ? selectedEntry.timezone : timezone
  const offsetDates = new Set(
    offsetCandidates.map((candidate) =>
      dateInTimezone(candidate.utc, editorTimezone)
    )
  )

  function openEditor(entry: EditableEntry, target?: DropTarget) {
    setSelectedEntry(entry)
    setDraft(draftForEntry(entry, timezone, anchorDate, target))
    setOffsetCandidates([])
    setSelectedOffsets({})
    setEditorError('')
  }

  function closeEditor() {
    setSelectedEntry(null)
    setDraft(null)
    setOffsetCandidates([])
    setSelectedOffsets({})
    setEditorError('')
  }

  function openCreateSchedule(defaults: CalendarCreateScheduleDefaults) {
    closeEditor()
    setCreateScheduleDefaults(defaults)
  }

  function updateDraft(update: Partial<ScheduleDraft>) {
    setDraft((current) => (current ? { ...current, ...update } : current))
    setOffsetCandidates([])
    setSelectedOffsets({})
    setEditorError('')
  }

  function changeScope(scope: ScheduleScope) {
    if (!selectedEntry || !draft) return
    if (selectedEntry.timezone !== timezone && selectedEntry.planned_start_at) {
      const zone = scope === 'only-this' ? timezone : selectedEntry.timezone
      updateDraft({
        scope,
        localStartTime: formatTime(selectedEntry.planned_start_at, zone),
      })
      return
    }
    updateDraft({ scope })
  }

  function handleDragStart(
    event: DragEvent<HTMLButtonElement>,
    entry: EditableEntry
  ) {
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData(occurrenceDragType, entry.occurrence_id)
  }

  function allowDrop(event: DragEvent<HTMLDivElement>) {
    if (!event.dataTransfer.types.includes(occurrenceDragType)) return false
    event.preventDefault()
    event.dataTransfer.dropEffect = 'move'
    return true
  }

  function handleDrop(event: DragEvent<HTMLDivElement>, target: DropTarget) {
    if (!allowDrop(event)) return
    setDropPreview(null)
    const id = event.dataTransfer.getData(occurrenceDragType)
    const entry =
      visibleEntries.find((item) => item.occurrence_id === id) ??
      pendingEntries.find((item) => item.occurrence_id === id)
    if (entry && movable(entry)) openEditor(entry, target)
  }

  function minuteAtPointer(
    event: DragEvent<HTMLDivElement>,
    start: number,
    end: number
  ) {
    const rect = event.currentTarget.getBoundingClientRect()
    const fraction = (event.clientY - rect.top) / rect.height
    const slot =
      Math.floor((fraction * (end - start)) / defaultScheduleDuration) *
      defaultScheduleDuration
    return Math.max(
      start,
      Math.min(end - defaultScheduleDuration, start + slot)
    )
  }

  async function submitSchedule() {
    if (!selectedEntry || !draft || !movable(selectedEntry)) return
    setEditorError('')
    const series = draft.scope === 'this-and-following'
    const offsets = Object.keys(selectedOffsets).length
      ? { selected_offsets: selectedOffsets }
      : {}
    try {
      if (series && selectedEntry.recurring) {
        await thisAndFollowing.mutateAsync({
          projectID: selectedEntry.project_id,
          taskID: selectedEntry.task_id,
          input: {
            expected_task_revision: selectedEntry.task_revision,
            expected_schedule_revision: selectedEntry.schedule_revision,
            effective_from: selectedEntry.occurrence_key,
            timing: {
              timing_type: draft.timingType,
              ...(draft.timingType === 'time_block'
                ? {
                    local_start_time: draft.localStartTime,
                    duration_minutes: draft.durationMinutes,
                  }
                : {}),
            },
            ...offsets,
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
            ...offsets,
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

  async function completeEntry(entry: CalendarEntryV2) {
    if (entry.execution_status === 'done' || complete.isPending) return
    setCompletionError('')
    try {
      await complete.mutateAsync({
        projectID: entry.project_id,
        taskID: entry.task_id,
        occurrenceID: entry.occurrence_id,
        expectedRevisions: {
          expected_task_revision: entry.task_revision,
          expected_schedule_revision: entry.schedule_revision,
          expected_occurrence_revisions: {
            [entry.occurrence_id]: entry.occurrence_revision,
          },
        },
      })
      if (selectedEntry?.occurrence_id === entry.occurrence_id) {
        closeEditor()
      }
    } catch {
      setCompletionError('完成任务失败，请稍后重试。')
    }
  }

  function changeCalendarView(nextView: CalendarView) {
    setCalendarView(nextView)
    closeEditor()
  }

  function navigatePeriod(direction: -1 | 1) {
    setAnchorDate((current) => {
      if (calendarView === 'week' || calendarView === 'agenda')
        return addDays(current, direction * 7)
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
    <section
      className="td-page td-calendar-page calendar-v2-page planning-page"
      aria-labelledby="calendar-heading"
    >
      <PlanningHeading
        id="calendar-heading"
        title="日历"
        description="查看时间安排，创建日程或调整已有任务。"
      >
        <aside className="calendar-v2-controls" aria-label="日历显示设置">
          <div className="calendar-v2-view-setting">
            <span className="calendar-v2-control-label">
              <strong>显示方式</strong>
              <small>切换时间尺度</small>
            </span>
            <div
              className="workspace-choice calendar-v2-view-switch"
              role="group"
              aria-label="日历视图"
            >
              {(
                [
                  ['agenda', '清单'],
                  ['week', '周'],
                  ['month', '月'],
                  ['year', '年'],
                ] as const
              ).map(([view, label]) => (
                <Button
                  variant="ghost"
                  key={view}
                  type="button"
                  aria-pressed={calendarView === view}
                  onClick={() => changeCalendarView(view)}
                >
                  {label}
                </Button>
              ))}
            </div>
          </div>
          <label className="calendar-v2-timezone">
            <span className="calendar-v2-timezone-label">
              <Globe2 size={14} aria-hidden="true" />
              显示时区
            </span>
            <NativeSelect
              value={timezone}
              onChange={(event) => {
                closeEditor()
                setTimezone(event.target.value)
              }}
              aria-label="显示时区"
            >
              {timezoneOptions.map((option) => (
                <option key={option} value={option}>
                  {option}
                </option>
              ))}
            </NativeSelect>
          </label>
        </aside>
      </PlanningHeading>

      <div
        className={`calendar-v2-workspace ${selectedEntry ? 'has-editor' : showPending ? 'has-pending' : ''}`}
        onDragEnd={() => setDropPreview(null)}
      >
        <Card
          className={`calendar-v2-shell is-${calendarView}-view gap-0 py-0 shadow-none`}
        >
          <div className="calendar-v2-toolbar">
            <div className="calendar-v2-navigation">
              <Button
                variant="outline"
                type="button"
                className="is-icon"
                onClick={() => navigatePeriod(-1)}
                aria-label={`上一${periodName}`}
                title={`上一${periodName}`}
              >
                <ChevronLeft size={16} aria-hidden="true" />
              </Button>
              <Button
                variant="outline"
                type="button"
                className="calendar-v2-today"
                onClick={() => {
                  closeEditor()
                  setAnchorDate(todayValue)
                }}
              >
                今天
              </Button>
              <Button
                variant="outline"
                type="button"
                className="is-icon"
                onClick={() => navigatePeriod(1)}
                aria-label={`下一${periodName}`}
                title={`下一${periodName}`}
              >
                <ChevronRight size={16} aria-hidden="true" />
              </Button>
              <Button
                type="button"
                className="calendar-v2-create"
                onClick={() => openCreateSchedule({ startsOn: anchorDate })}
              >
                <Plus size={15} aria-hidden="true" />
                新增日程
              </Button>
              {calendarView === 'week' ? (
                <Button
                  variant="outline"
                  type="button"
                  aria-expanded={showPending}
                  aria-controls={
                    showPending ? 'calendar-pending-tasks' : undefined
                  }
                  onClick={() => {
                    closeEditor()
                    setPendingOpen(!showPending)
                  }}
                >
                  <CalendarClock data-icon="inline-start" aria-hidden="true" />
                  待安排
                </Button>
              ) : null}
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

          {completionError ? (
            <div className="td-inline-error" role="alert">
              {completionError}
            </div>
          ) : null}

          {entriesQuery.isError ? (
            <div className="td-inline-error" role="alert">
              日历加载失败，请稍后重试。
            </div>
          ) : calendarView === 'agenda' ? (
            <div className="calendar-agenda" aria-label="日程清单">
              {agendaDates.map((date) => {
                const entries = entriesByDate.get(date) ?? []
                return (
                  <section
                    key={date}
                    className={`calendar-agenda-day ${date === todayValue ? 'is-today' : ''}`}
                  >
                    <header>
                      <div>
                        <time dateTime={date}>{formatDate(date)}</time>
                        <span>
                          {date === todayValue ? '今天' : formatWeekday(date)}
                        </span>
                      </div>
                      <button
                        type="button"
                        aria-label={`在${date}添加日程`}
                        onClick={() => openCreateSchedule({ startsOn: date })}
                      >
                        <Plus aria-hidden="true" />
                      </button>
                    </header>
                    {entries.map((entry) => (
                      <div
                        className="calendar-agenda-entry-frame"
                        key={entry.occurrence_id}
                      >
                        <button
                          type="button"
                          className={`calendar-agenda-entry is-${entry.execution_status}`}
                          aria-label={`编辑日程：${entry.task_title}`}
                          onClick={() => openEditor(entry)}
                        >
                          <time>
                            {entry.timing_type === 'date'
                              ? '全天'
                              : formatTime(entry.planned_start_at!, timezone)}
                          </time>
                          <span>
                            <strong>{entry.task_title}</strong>
                            <small>
                              {entry.timing_type === 'date'
                                ? allDayRangeLabel(entry)
                                : `至 ${formatTime(entry.planned_end_at!, timezone)}`}
                              {entry.location ? ` · ${entry.location}` : ''}
                            </small>
                          </span>
                          <ChevronRight aria-hidden="true" />
                        </button>
                        <CalendarEntryCompletionButton
                          entry={entry}
                          pending={complete.isPending}
                          onComplete={() => void completeEntry(entry)}
                        />
                      </div>
                    ))}
                    {entries.length === 0 && (
                      <p>
                        {entriesQuery.isLoading
                          ? '正在加载…'
                          : '暂无安排，留一点时间给自己。'}
                      </p>
                    )}
                  </section>
                )
              })}
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
                <div
                  className="calendar-v2-all-day-grid"
                  style={
                    {
                      '--calendar-all-day-rows': allDayLayout.rowCount,
                    } as CSSProperties
                  }
                >
                  {weekDates.map((date) => (
                    <div
                      key={date}
                      className="calendar-v2-drop-day"
                      onDragOver={allowDrop}
                      onDrop={(event) =>
                        handleDrop(event, { date, allDay: true })
                      }
                      aria-hidden="true"
                    />
                  ))}
                  {allDayLayout.entries.map(
                    ({ entry, startIndex, endIndex, row }) => {
                      const exclusiveEnd =
                        entry.all_day_end_date ||
                        addDays(entry.planned_date!, 1)
                      return (
                        <div
                          key={entry.occurrence_id}
                          className="calendar-v2-entry-frame is-all-day"
                          style={
                            {
                              '--calendar-column-start': startIndex + 1,
                              '--calendar-column-end': endIndex + 1,
                              '--calendar-row': row,
                            } as CSSProperties
                          }
                        >
                          <button
                            type="button"
                            className={`calendar-v2-entry is-all-day is-${entry.execution_status}`}
                            title={entry.task_title}
                            data-exclusive-end={exclusiveEnd}
                            data-calendar-row={row}
                            draggable={movable(entry)}
                            onDragStart={(event) =>
                              handleDragStart(event, entry)
                            }
                            onClick={() => openEditor(entry)}
                            aria-label={`编辑日程：${entry.task_title}`}
                          >
                            <strong>{entry.task_title}</strong>
                            <span>{allDayRangeLabel(entry)}</span>
                          </button>
                          <CalendarEntryCompletionButton
                            entry={entry}
                            pending={complete.isPending}
                            onComplete={() => void completeEntry(entry)}
                          />
                        </div>
                      )
                    }
                  )}
                  {allDayLayout.entries.length === 0 ? (
                    <p className="calendar-v2-empty-lane">本周没有全天安排</p>
                  ) : null}
                </div>
              </section>

              <section
                className="calendar-v2-time-section"
                role="region"
                aria-label="时间安排"
              >
                {!hasEarly ? (
                  <Button
                    variant="ghost"
                    className="calendar-v2-fold"
                    type="button"
                    aria-expanded={showEarly}
                    aria-controls="calendar-week-time-grid"
                    onClick={() => setShowEarly((current) => !current)}
                  >
                    <ChevronRight data-icon="inline-start" aria-hidden="true" />
                    凌晨 00:00–07:00
                  </Button>
                ) : null}
                <div
                  id="calendar-week-time-grid"
                  className="calendar-v2-time-grid"
                  style={
                    {
                      '--calendar-range-minutes': timelineEnd - timelineStart,
                    } as CSSProperties
                  }
                >
                  <div className="calendar-v2-hour-axis" aria-hidden="true">
                    {Array.from(
                      { length: (timelineEnd - timelineStart) / 60 },
                      (_, index) => (
                        <span key={index}>
                          {timeInputValue(timelineStart + index * 60)}
                        </span>
                      )
                    )}
                  </div>
                  {weekDates.map((date) => (
                    <div
                      key={date}
                      className={`calendar-v2-time-column ${date === todayValue ? 'is-today' : ''}`}
                      onDragOver={(event) => {
                        if (!allowDrop(event)) return
                        const minute = minuteAtPointer(
                          event,
                          timelineStart,
                          timelineEnd
                        )
                        setDropPreview((current) =>
                          current?.date === date && current.minute === minute
                            ? current
                            : { date, minute }
                        )
                      }}
                      onDragLeave={(event) => {
                        if (
                          !(event.relatedTarget instanceof Node) ||
                          !event.currentTarget.contains(event.relatedTarget)
                        ) {
                          setDropPreview(null)
                        }
                      }}
                      onDrop={(event) =>
                        handleDrop(event, {
                          date,
                          minute: minuteAtPointer(
                            event,
                            timelineStart,
                            timelineEnd
                          ),
                        })
                      }
                    >
                      {Array.from(
                        {
                          length:
                            (timelineEnd - timelineStart) /
                            defaultScheduleDuration,
                        },
                        (_, index) => {
                          const minute =
                            timelineStart + index * defaultScheduleDuration
                          const time = timeInputValue(minute)
                          return (
                            <button
                              key={minute}
                              type="button"
                              className="calendar-v2-create-slot"
                              style={
                                {
                                  '--calendar-slot-minute':
                                    minute - timelineStart,
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
                        }
                      )}
                      {timedByDate.get(date)?.map(({ entry, start, end }) => {
                        return (
                          <div
                            key={entry.occurrence_id}
                            className="calendar-v2-entry-frame is-time-block"
                            style={
                              {
                                '--calendar-start-minute':
                                  start - timelineStart,
                                '--calendar-duration-minute': end - start,
                              } as CSSProperties
                            }
                          >
                            <button
                              title={entry.task_title}
                              type="button"
                              className={`calendar-v2-entry is-time-block is-${entry.execution_status}`}
                              draggable={movable(entry)}
                              onDragStart={(event) =>
                                handleDragStart(event, entry)
                              }
                              onDragEnd={() => setDropPreview(null)}
                              onClick={() => openEditor(entry)}
                              aria-label={`编辑日程：${entry.task_title}，${formatDate(date)} ${timeInputValue(start)}`}
                            >
                              <time>
                                {timeInputValue(start)}–{timeInputValue(end)}
                              </time>
                              <strong>{entry.task_title}</strong>
                            </button>
                            <CalendarEntryCompletionButton
                              entry={entry}
                              pending={complete.isPending}
                              onComplete={() => void completeEntry(entry)}
                            />
                          </div>
                        )
                      })}
                      {dropPreview?.date === date &&
                      dropPreview.minute >= timelineStart &&
                      dropPreview.minute < timelineEnd ? (
                        <div
                          aria-hidden="true"
                          className="calendar-v2-drop-preview"
                          style={
                            {
                              '--calendar-slot-minute':
                                dropPreview.minute - timelineStart,
                            } as CSSProperties
                          }
                        >
                          {timeInputValue(dropPreview.minute)}
                        </div>
                      ) : null}
                    </div>
                  ))}
                </div>
                {!hasLate ? (
                  <Button
                    variant="ghost"
                    className="calendar-v2-fold"
                    type="button"
                    aria-expanded={showLate}
                    aria-controls="calendar-week-time-grid"
                    onClick={() => setShowLate((current) => !current)}
                  >
                    <ChevronRight data-icon="inline-start" aria-hidden="true" />
                    深夜 22:00–24:00
                  </Button>
                ) : null}
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
                      onDragOver={allowDrop}
                      onDrop={(event) => handleDrop(event, { date })}
                    >
                      <header>
                        <time dateTime={date}>{dayNumber(date)}</time>
                        {dayEntries.length > 0 ? (
                          <span>{dayEntries.length} 项</span>
                        ) : null}
                      </header>
                      <div className="calendar-v2-month-entries">
                        {dayEntries.slice(0, 3).map((entry) => (
                          <div
                            className="calendar-v2-month-entry-frame"
                            key={`${date}-${entry.occurrence_id}`}
                          >
                            <button
                              type="button"
                              className={`calendar-v2-month-entry is-${entry.timing_type} is-${entry.execution_status}`}
                              draggable={movable(entry)}
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
                            <CalendarEntryCompletionButton
                              entry={entry}
                              pending={complete.isPending}
                              onComplete={() => void completeEntry(entry)}
                            />
                          </div>
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
        </Card>

        {showPending && !selectedEntry ? (
          <aside
            id="calendar-pending-tasks"
            className="calendar-v2-pending"
            aria-labelledby="calendar-pending-heading"
          >
            <h2 id="calendar-pending-heading">待安排</h2>
            <p>拖到周历，或点击「安排到」选择时间。</p>
            {unscheduledQuery.isError || overdueQuery.isError ? (
              <p role="alert">
                部分任务加载失败。
                <button
                  type="button"
                  onClick={() => {
                    void unscheduledQuery.refetch()
                    void overdueQuery.refetch()
                  }}
                >
                  重试
                </button>
              </p>
            ) : null}
            {unscheduledQuery.isLoading || overdueQuery.isLoading ? (
              <p role="status">正在加载任务…</p>
            ) : null}
            {pendingEntries.length === 0 &&
            !unscheduledQuery.isLoading &&
            !overdueQuery.isLoading &&
            !unscheduledQuery.isError &&
            !overdueQuery.isError ? (
              <p>没有无日期或逾期的任务。</p>
            ) : null}
            <ul>
              {pendingEntries.map((entry) => {
                const project = projectsQuery.data?.find(
                  (item) => item.id === entry.project_id
                )
                return (
                  <li key={entry.occurrence_id}>
                    <button
                      type="button"
                      draggable
                      onDragStart={(event) => handleDragStart(event, entry)}
                      onClick={() =>
                        openEditor(entry, { date: anchorDate, minute: 9 * 60 })
                      }
                      aria-label={`安排到：${entry.task_title}`}
                    >
                      <strong>{entry.task_title}</strong>
                      <small>
                        {entry.timing_type === 'unscheduled'
                          ? '无日期'
                          : '逾期'}
                        {project ? ` · ${project.name}` : ''}
                      </small>
                      <span>安排到</span>
                    </button>
                  </li>
                )
              })}
            </ul>
          </aside>
        ) : null}

        {selectedEntry && draft ? (
          <DetailPanel
            className="calendar-v2-editor"
            desktopRole="dialog"
            label={`编辑日程：${selectedEntry.task_title}`}
            onClose={closeEditor}
          >
            <header>
              <div>
                <span>{seriesEdit ? '调整后续时间' : '安排本次执行'}</span>
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

            {!movable(selectedEntry) ? (
              <div className="calendar-v2-done-notice" role="alert">
                {selectedEntry.execution_status === 'done' ? (
                  <>
                    <p>已完成的任务不能移动，请先重新打开。</p>
                    <button
                      type="button"
                      onClick={() => void reopenSelectedEntry()}
                      disabled={reopen.isPending}
                    >
                      重新打开任务
                    </button>
                  </>
                ) : (
                  <p>此任务已跳过或取消，无法调整时间。</p>
                )}
              </div>
            ) : null}

            {canEditSeries(selectedEntry) ||
            selectedEntry.timing_type !== 'unscheduled' ? (
              <fieldset className="calendar-v2-scope">
                <legend>修改范围</legend>
                <label>
                  <input
                    type="radio"
                    name="schedule-scope"
                    checked={draft.scope === 'only-this'}
                    onChange={() => changeScope('only-this')}
                  />
                  仅本次
                </label>
                {canEditSeries(selectedEntry) ? (
                  <label>
                    <input
                      type="radio"
                      name="schedule-scope"
                      checked={draft.scope === 'this-and-following'}
                      onChange={() => changeScope('this-and-following')}
                    />
                    本次及以后
                  </label>
                ) : null}
              </fieldset>
            ) : null}

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
              {seriesEdit ? null : (
                <label>
                  <span>计划日期</span>
                  <input
                    type="date"
                    aria-label="计划日期"
                    value={draft.plannedDate}
                    onChange={(event) => {
                      const plannedDate = event.target.value
                      updateDraft({
                        plannedDate,
                        ...(plannedDate && plannedDate >= draft.allDayEndDate
                          ? { allDayEndDate: addDays(plannedDate, 1) }
                          : {}),
                      })
                    }}
                  />
                </label>
              )}
              {draft.timingType === 'date' ? (
                seriesEdit ? null : (
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
                )
              ) : (
                <>
                  <label>
                    <span>
                      开始时间 <small>{editorTimezone}</small>
                    </span>
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

            {seriesEdit && selectedEntry.recurring ? (
              <div className="calendar-v2-following-fields">
                从 {formatDate(selectedEntry.occurrence_key)}{' '}
                这次起调整时间，保留原重复规则。
              </div>
            ) : null}

            {offsetCandidates.length > 0 ? (
              <fieldset className="calendar-v2-offsets">
                <legend>这个本地时间出现了两次，请选择准确偏移</legend>
                {offsetCandidates.map((candidate) => {
                  const date = dateInTimezone(candidate.utc, editorTimezone)
                  return (
                    <label key={candidate.utc}>
                      <input
                        type="radio"
                        name={`selected-offset-${date}`}
                        checked={
                          selectedOffsets[date] === candidate.offset_seconds
                        }
                        onChange={() =>
                          setSelectedOffsets((current) => ({
                            ...current,
                            [date]: candidate.offset_seconds,
                          }))
                        }
                      />
                      {offsetLabel(candidate.offset_seconds)} · {candidate.utc}
                    </label>
                  )
                })}
              </fieldset>
            ) : null}

            {editorError ? (
              <p className="calendar-v2-error" role="alert">
                {editorError}
              </p>
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
                  !movable(selectedEntry) ||
                  onlyThis.isPending ||
                  thisAndFollowing.isPending ||
                  (!seriesEdit &&
                    (!draft.plannedDate ||
                      (draft.timingType === 'date' &&
                        draft.allDayEndDate <= draft.plannedDate))) ||
                  (draft.timingType === 'time_block' &&
                    (!draft.localStartTime ||
                      !Number.isInteger(draft.durationMinutes) ||
                      draft.durationMinutes < 1)) ||
                  [...offsetDates].some(
                    (date) => selectedOffsets[date] === undefined
                  )
                }
              >
                保存日程
              </button>
            </footer>
          </DetailPanel>
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
