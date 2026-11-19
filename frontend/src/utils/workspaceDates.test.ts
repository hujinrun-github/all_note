import { describe, expect, it } from 'vitest'
import type { OccurrenceV2 } from '../api/taskDomain'
import {
  createWorkspaceDateWindows,
  createWorkspaceOccurrenceParams,
  matchesWorkspaceDateFilter,
} from './workspaceDates'

const shanghai = { date: '2026-11-19', timezone: 'Asia/Shanghai' }
const occurrence: OccurrenceV2 = {
  id: 'occurrence',
  task_id: 'task',
  occurrence_key: 'once',
  execution_status: 'open',
  revision: 1,
  generated_schedule_revision: 1,
}

function hours(range: { from: string; to: string }) {
  return (Date.parse(range.to) - Date.parse(range.from)) / 3_600_000
}

describe('workspace calendar windows', () => {
  it('anchors days, Monday weeks and natural months to the supplied local date', () => {
    expect(createWorkspaceDateWindows(shanghai)).toEqual({
      today: {
        from: '2026-11-18T16:00:00.000Z',
        to: '2026-11-19T16:00:00.000Z',
        timezone: 'Asia/Shanghai',
      },
      week: {
        from: '2026-11-15T16:00:00.000Z',
        to: '2026-11-22T16:00:00.000Z',
        timezone: 'Asia/Shanghai',
      },
      month: {
        from: '2026-10-31T16:00:00.000Z',
        to: '2026-11-30T16:00:00.000Z',
        timezone: 'Asia/Shanghai',
      },
    })
  })

  it('keeps Sunday in the previous Monday week across year boundaries', () => {
    const ranges = createWorkspaceDateWindows({
      date: '2026-01-04',
      timezone: 'UTC',
    })
    expect(ranges.week).toMatchObject({
      from: '2025-12-29T00:00:00.000Z',
      to: '2026-01-05T00:00:00.000Z',
    })
    expect(ranges.month).toMatchObject({
      from: '2026-01-01T00:00:00.000Z',
      to: '2026-02-01T00:00:00.000Z',
    })
  })

  it('retains leap dates and fractional timezone offsets', () => {
    expect(
      createWorkspaceDateWindows({
        date: '2028-02-29',
        timezone: 'Asia/Kathmandu',
      }).today
    ).toMatchObject({
      from: '2028-02-28T18:15:00.000Z',
      to: '2028-02-29T18:15:00.000Z',
    })
  })

  it.each([
    [
      '2026-03-08',
      23,
      167,
      743,
      '2026-03-08T05:00:00.000Z',
      '2026-03-09T04:00:00.000Z',
    ],
    [
      '2026-11-01',
      25,
      169,
      721,
      '2026-11-01T04:00:00.000Z',
      '2026-11-02T05:00:00.000Z',
    ],
  ])(
    'uses local calendar boundaries across DST on %s',
    (date, dayHours, weekHours, monthHours, from, to) => {
      const windows = createWorkspaceDateWindows({
        date,
        timezone: 'America/New_York',
      })
      expect(windows.today).toMatchObject({ from, to })
      expect(hours(windows.today)).toBe(dayHours)
      expect(hours(windows.week)).toBe(weekHours)
      expect(hours(windows.month)).toBe(monthHours)
    }
  )

  it('supports a half-hour DST transition', () => {
    const windows = createWorkspaceDateWindows({
      date: '2026-10-04',
      timezone: 'Australia/Lord_Howe',
    })
    expect(hours(windows.today)).toBe(23.5)
  })

  it('does not crash when a timezone skips midnight during DST', () => {
    expect(
      createWorkspaceDateWindows({
        date: '2026-09-06',
        timezone: 'America/Santiago',
      }).today
    ).toMatchObject({
      from: '2026-09-06T04:00:00.000Z',
      to: '2026-09-07T03:00:00.000Z',
    })
    expect(
      createWorkspaceDateWindows({
        date: '2026-03-29',
        timezone: 'Asia/Beirut',
      }).today
    ).toMatchObject({
      from: '2026-03-28T22:00:00.000Z',
      to: '2026-03-29T21:00:00.000Z',
    })
  })

  it('chooses the first midnight when a backward transition repeats it', () => {
    expect(
      createWorkspaceDateWindows({ date: '2021-10-29', timezone: 'Asia/Amman' })
        .today
    ).toMatchObject({
      from: '2021-10-28T21:00:00.000Z',
      to: '2021-10-29T22:00:00.000Z',
    })
  })

  it('rejects an entirely skipped date instead of producing an empty day', () => {
    expect(() =>
      createWorkspaceDateWindows({
        date: '2011-12-30',
        timezone: 'Pacific/Apia',
      })
    ).toThrow(RangeError)
  })

  it.each(['2026-02-30', '2026-1-01', '0000-01-01', '2026-11-19 '])(
    'rejects invalid calendar date %s',
    (date) => {
      expect(() =>
        createWorkspaceDateWindows({ date, timezone: 'UTC' })
      ).toThrow(RangeError)
    }
  )
})

describe('workspace occurrence query parameters', () => {
  it('uses explicit facet windows for today, week and month', () => {
    const windows = createWorkspaceDateWindows(shanghai)
    for (const view of ['today', 'week', 'month'] as const) {
      expect(
        createWorkspaceOccurrenceParams(view, shanghai, 'project')
      ).toEqual({
        ...windows[view],
        project_id: 'project',
        scope: view === 'today' ? 'today' : 'upcoming',
      })
    }
  })

  it('cuts overdue off at local midnight and keeps completed history unbounded', () => {
    expect(createWorkspaceOccurrenceParams('overdue', shanghai)).toEqual({
      scope: 'overdue',
      from: '2026-11-18T16:00:00.000Z',
      timezone: 'Asia/Shanghai',
    })
    expect(createWorkspaceOccurrenceParams('completed', shanghai)).toEqual({
      scope: 'all',
      execution_status: 'done',
      timezone: 'Asia/Shanghai',
    })
    expect(
      createWorkspaceOccurrenceParams('recurring', shanghai, 'project')
    ).toEqual({
      scope: 'all',
      recurring: true,
      project_id: 'project',
      timezone: 'Asia/Shanghai',
    })
    expect(createWorkspaceOccurrenceParams('unscheduled', shanghai)).toEqual({
      scope: 'unscheduled',
      timezone: 'Asia/Shanghai',
    })
    expect(createWorkspaceOccurrenceParams('inbox', shanghai, 'inbox')).toEqual(
      {
        scope: 'all',
        project_id: 'inbox',
        timezone: 'Asia/Shanghai',
      }
    )
  })
})

describe('secondary workspace date filters', () => {
  it('uses the requested timezone instead of UTC string dates for timed items', () => {
    expect(
      matchesWorkspaceDateFilter(
        {
          ...occurrence,
          planned_start_at: '2026-11-18T16:30:00Z',
          planned_end_at: '2026-11-18T17:30:00Z',
        },
        'today',
        shanghai
      )
    ).toBe(true)
    expect(
      matchesWorkspaceDateFilter(
        {
          ...occurrence,
          planned_start_at: '2026-11-19T16:00:00Z',
          planned_end_at: '2026-11-19T17:00:00Z',
        },
        'today',
        shanghai
      )
    ).toBe(false)
  })

  it('includes time blocks that overlap today while excluding an exact end boundary', () => {
    const item = { ...occurrence, planned_start_at: '2026-11-18T15:00:00Z' }
    expect(
      matchesWorkspaceDateFilter(
        { ...item, planned_end_at: '2026-11-18T16:30:00Z' },
        'today',
        shanghai
      )
    ).toBe(true)
    expect(
      matchesWorkspaceDateFilter(
        { ...item, planned_end_at: '2026-11-18T16:00:00Z' },
        'today',
        shanghai
      )
    ).toBe(false)
  })

  it('uses an exclusive all-day end for today and start dates for week/month', () => {
    const item = {
      ...occurrence,
      planned_date: '2026-11-15',
      all_day_end_date: '2026-11-20',
    }
    expect(matchesWorkspaceDateFilter(item, 'today', shanghai)).toBe(true)
    expect(matchesWorkspaceDateFilter(item, 'week', shanghai)).toBe(false)
    expect(matchesWorkspaceDateFilter(item, 'month', shanghai)).toBe(true)
    expect(
      matchesWorkspaceDateFilter(
        { ...item, all_day_end_date: '2026-11-19' },
        'today',
        shanghai
      )
    ).toBe(false)
  })

  it('keeps a seven-calendar-day content refinement distinct from the calendar week', () => {
    const item = { ...occurrence, planned_date: '2026-11-23' }
    expect(matchesWorkspaceDateFilter(item, 'week', shanghai)).toBe(false)
    expect(matchesWorkspaceDateFilter(item, 'next-7-days', shanghai)).toBe(true)
    expect(
      matchesWorkspaceDateFilter(
        { ...item, planned_date: '2026-11-26' },
        'next-7-days',
        shanghai
      )
    ).toBe(false)
  })

  it('does not treat a timed item without planned_date as unscheduled', () => {
    expect(
      matchesWorkspaceDateFilter(occurrence, 'unscheduled', shanghai)
    ).toBe(true)
    expect(
      matchesWorkspaceDateFilter(
        { ...occurrence, planned_start_at: '2026-11-19T04:00:00Z' },
        'unscheduled',
        shanghai
      )
    ).toBe(false)
    expect(matchesWorkspaceDateFilter(occurrence, 'today', shanghai)).toBe(
      false
    )
    expect(matchesWorkspaceDateFilter(occurrence, 'current', shanghai)).toBe(
      true
    )
  })
})
