import type { OccurrenceListParams, OccurrenceV2 } from '../api/taskDomain'
import type { WorkspaceQueryParams } from '../api/workspace'

export interface WorkspaceDateRange {
  from: string
  to: string
  timezone: string
}

export type WorkspaceOccurrenceView =
  | 'inbox'
  | 'today'
  | 'week'
  | 'month'
  | 'overdue'
  | 'unscheduled'
  | 'recurring'
  | 'completed'

export type WorkspaceDateFilter =
  | 'current'
  | 'today'
  | 'week'
  | 'month'
  | 'unscheduled'
  | 'next-7-days'

export interface WorkspaceDateWindows {
  today: WorkspaceDateRange
  week: WorkspaceDateRange
  month: WorkspaceDateRange
}

const windowsCache = new Map<string, WorkspaceDateWindows>()
const formattersCache = new Map<string, Intl.DateTimeFormat>()
const maxCachedWindows = 8

// Calendar arithmetic is deliberately separate from timezone conversion: a
// local day may contain 23 or 25 hours, and a week starts on Monday.
export function createWorkspaceDateWindows(
  params: WorkspaceQueryParams
): WorkspaceDateWindows {
  const key = JSON.stringify([params.date, params.timezone])
  const cached = windowsCache.get(key)
  if (cached) return cached
  const date = parseCalendarDate(params.date)
  const monday = shiftCalendarDate(date, -((date.getUTCDay() + 6) % 7))
  const month = calendarDate(date.getUTCFullYear(), date.getUTCMonth(), 1)
  const nextMonth = calendarDate(
    date.getUTCFullYear(),
    date.getUTCMonth() + 1,
    1
  )
  const range = (from: Date, to: Date): WorkspaceDateRange =>
    Object.freeze({
      from: zonedMidnight(from, params.timezone),
      to: zonedMidnight(to, params.timezone),
      timezone: params.timezone,
    })

  const today = range(date, shiftCalendarDate(date, 1))
  if (
    formatCalendarDate(
      localWallClock(Date.parse(today.from), timezoneFormatter(params.timezone))
    ) !== params.date
  ) {
    throw new RangeError(
      'Workspace date does not exist in the requested timezone'
    )
  }
  const result = Object.freeze({
    today,
    week: range(monday, shiftCalendarDate(monday, 7)),
    month: range(month, nextMonth),
  })
  if (windowsCache.size >= maxCachedWindows)
    windowsCache.delete(windowsCache.keys().next().value!)
  windowsCache.set(key, result)
  return result
}

// Every list receives the same reference date/timezone as its facets request.
// Completed totals are historical, so no implicit trailing 30-day range applies.
export function createWorkspaceOccurrenceParams(
  view: WorkspaceOccurrenceView,
  params: WorkspaceQueryParams,
  projectID?: string
): OccurrenceListParams {
  const base: OccurrenceListParams = {
    timezone: params.timezone,
    ...(projectID ? { project_id: projectID } : {}),
  }
  switch (view) {
    case 'today':
      return {
        ...base,
        scope: 'today',
        ...createWorkspaceDateWindows(params).today,
      }
    case 'week':
    case 'month':
      return {
        ...base,
        scope: 'upcoming',
        ...createWorkspaceDateWindows(params)[view],
      }
    case 'overdue':
      return {
        ...base,
        scope: 'overdue',
        from: createWorkspaceDateWindows(params).today.from,
      }
    case 'completed':
      return { ...base, scope: 'all', execution_status: 'done' }
    case 'recurring':
      return { ...base, scope: 'all', recurring: true }
    case 'unscheduled':
      return { ...base, scope: 'unscheduled' }
    case 'inbox':
      return { ...base, scope: 'all' }
  }
}

// Secondary filters narrow the displayed content. Today uses overlap, while
// week/month use the occurrence's start, matching the corresponding facets.
export function matchesWorkspaceDateFilter(
  occurrence: OccurrenceV2,
  filter: WorkspaceDateFilter,
  params: WorkspaceQueryParams
): boolean {
  if (filter === 'current') return true
  if (filter === 'unscheduled') {
    return !occurrence.planned_start_at && !occurrence.planned_date
  }
  const windows = createWorkspaceDateWindows(params)
  const range =
    filter === 'next-7-days'
      ? {
          from: windows.today.from,
          to: zonedMidnight(
            shiftCalendarDate(parseCalendarDate(params.date), 7),
            params.timezone
          ),
        }
      : windows[filter]

  if (occurrence.planned_start_at) {
    const start = Date.parse(occurrence.planned_start_at)
    const from = Date.parse(range.from)
    const to = Date.parse(range.to)
    if (filter === 'today' && occurrence.planned_end_at) {
      return start < to && Date.parse(occurrence.planned_end_at) > from
    }
    return start >= from && start < to
  }
  if (!occurrence.planned_date) return false
  const start = zonedMidnight(
    parseCalendarDate(occurrence.planned_date),
    params.timezone
  )
  if (filter === 'today') {
    const end = zonedMidnight(
      occurrence.all_day_end_date
        ? parseCalendarDate(occurrence.all_day_end_date)
        : shiftCalendarDate(parseCalendarDate(occurrence.planned_date), 1),
      params.timezone
    )
    return start < range.to && end > range.from
  }
  return start >= range.from && start < range.to
}

function parseCalendarDate(value: string): Date {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value)
  if (!match || Number(match[1]) < 1)
    throw new RangeError('Invalid workspace date')
  const result = calendarDate(
    Number(match[1]),
    Number(match[2]) - 1,
    Number(match[3])
  )
  if (formatCalendarDate(result) !== value)
    throw new RangeError('Invalid workspace date')
  return result
}

function calendarDate(year: number, month: number, day: number): Date {
  const result = new Date(0)
  result.setUTCFullYear(year, month, day)
  result.setUTCHours(0, 0, 0, 0)
  return result
}

function shiftCalendarDate(value: Date, days: number): Date {
  const result = new Date(value)
  result.setUTCDate(result.getUTCDate() + days)
  return result
}

function formatCalendarDate(value: Date): string {
  return `${String(value.getUTCFullYear()).padStart(4, '0')}-${String(value.getUTCMonth() + 1).padStart(2, '0')}-${String(value.getUTCDate()).padStart(2, '0')}`
}

// Resolve the offset at the requested local midnight, not the browser's current
// offset. Iterating also handles a boundary on the other side of a DST change.
function timezoneFormatter(timezone: string): Intl.DateTimeFormat {
  let formatter = formattersCache.get(timezone)
  if (!formatter) {
    formatter = new Intl.DateTimeFormat('en-GB', {
      timeZone: timezone,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hourCycle: 'h23',
    })
    if (formattersCache.size >= maxCachedWindows)
      formattersCache.delete(formattersCache.keys().next().value!)
    formattersCache.set(timezone, formatter)
  }
  return formatter
}

function localWallClock(instant: number, formatter: Intl.DateTimeFormat): Date {
  const parts = formatter.formatToParts(new Date(instant))
  const part = (type: Intl.DateTimeFormatPartTypes) =>
    Number(parts.find((item) => item.type === type)?.value)
  const wall = calendarDate(part('year'), part('month') - 1, part('day'))
  wall.setUTCHours(part('hour'), part('minute'), part('second'), 0)
  return wall
}

function zonedMidnight(date: Date, timezone: string): string {
  const formatter = timezoneFormatter(timezone)
  const visited = new Set<number>()
  let instant = date.getTime()
  for (let attempt = 0; attempt < 4; attempt += 1) {
    const wall = localWallClock(instant, formatter)
    const next = instant + date.getTime() - wall.getTime()
    if (next === instant) {
      // A backward transition can repeat midnight. Check the earlier offset
      // as well so the first occurrence of that midnight starts the day.
      const prior = shiftCalendarDate(new Date(instant), -2).getTime()
      const priorOffset = localWallClock(prior, formatter).getTime() - prior
      const earlier = date.getTime() - priorOffset
      if (
        earlier < instant &&
        localWallClock(earlier, formatter).getTime() === date.getTime()
      )
        instant = earlier
      return new Date(instant).toISOString()
    }
    // Some zones advance their clock at midnight. In that gap, use the first
    // valid time on the requested day instead of crashing the whole page.
    if (visited.has(next))
      return new Date(Math.max(instant, next)).toISOString()
    visited.add(instant)
    instant = next
  }
  throw new RangeError('Workspace date has no local midnight')
}
