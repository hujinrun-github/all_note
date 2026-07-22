import { useMemo, useState, type CSSProperties, type DragEvent } from 'react'

import {
  TaskDomainAPIError,
  type CalendarEntryV2,
  type RecurrenceType,
  type TimingType,
} from '../api/taskDomain'
import {
  useCalendarEntries,
  useReopenOccurrenceMutation,
  useRescheduleOccurrenceMutation,
  useRescheduleThisAndFollowingMutation,
} from '../hooks/useTaskDomain'

type ScheduleScope = 'only-this' | 'this-and-following'

interface CalendarV2Props {
  initialDate?: string
  initialTimezone?: string
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

const weekHours = Array.from({ length: 15 }, (_, index) => index + 7)

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

function formatTime(value: string, timezone: string) {
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: timezone,
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  }).format(new Date(value))
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
}: CalendarV2Props) {
  const [weekAnchor, setWeekAnchor] = useState(initialDate)
  const [timezone, setTimezone] = useState(initialTimezone)
  const [selectedEntry, setSelectedEntry] =
    useState<CalendarEntryV2 | null>(null)
  const [draft, setDraft] = useState<ScheduleDraft | null>(null)
  const [offsetCandidates, setOffsetCandidates] = useState<
    Array<{ offset_seconds: number; utc: string }>
  >([])
  const [editorError, setEditorError] = useState('')

  const onlyThis = useRescheduleOccurrenceMutation()
  const thisAndFollowing = useRescheduleThisAndFollowingMutation()
  const reopen = useReopenOccurrenceMutation()
  const weekStart = mondayOf(weekAnchor)
  const weekEnd = addDays(weekStart, 7)
  const weekDates = useMemo(
    () => Array.from({ length: 7 }, (_, index) => addDays(weekStart, index)),
    [weekStart]
  )
  const entriesQuery = useCalendarEntries({
    from: weekStart,
    to: weekEnd,
    timezone,
  })
  const visibleEntries = (entriesQuery.data ?? []).filter(
    (entry) => entry.timing_type !== 'unscheduled'
  )
  const dateEntries = visibleEntries.filter(
    (entry) => entry.timing_type === 'date' && entry.planned_date
  )
  const timeEntries = visibleEntries.filter(
    (entry) =>
      entry.timing_type === 'time_block' &&
      entry.planned_date &&
      entry.planned_start_at &&
      entry.planned_end_at
  )

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

  function navigateWeek(direction: -1 | 1) {
    setWeekAnchor((current) => addDays(current, direction * 7))
  }

  return (
    <div className="calendar-v2-page">
      <header className="calendar-v2-heading">
        <div>
          <span className="calendar-v2-kicker">统一任务日历</span>
          <h1>日历</h1>
          <p>全天安排与时间块都来自任务实例；未安排任务不会出现在日历中。</p>
        </div>
        <label className="calendar-v2-timezone">
          <span>显示时区</span>
          <input
            value={timezone}
            onChange={(event) => setTimezone(event.target.value)}
            aria-label="显示时区"
          />
        </label>
      </header>

      <section className="surface-panel calendar-v2-shell">
        <div className="calendar-v2-toolbar">
          <div>
            <button type="button" onClick={() => navigateWeek(-1)}>
              上一周
            </button>
            <button type="button" onClick={() => setWeekAnchor(localDateValue())}>
              今天
            </button>
            <button type="button" onClick={() => navigateWeek(1)}>
              下一周
            </button>
          </div>
          <strong>
            {formatDate(weekStart)}–{formatDate(addDays(weekEnd, -1))}
          </strong>
          {entriesQuery.isFetching ? <span role="status">正在更新…</span> : null}
        </div>

        {entriesQuery.isError ? (
          <div className="domain-unavailable" role="alert">
            日历加载失败，请稍后重试。
          </div>
        ) : (
          <>
            <div className="calendar-v2-week-head" aria-hidden="true">
              <span />
              {weekDates.map((date) => (
                <strong key={date}>
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
                    className="calendar-v2-time-column"
                    onDragOver={(event) => event.preventDefault()}
                    onDrop={(event) => handleDrop(event, date)}
                  >
                    {weekHours.map((hour) => (
                      <i key={hour} aria-hidden="true" />
                    ))}
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
                                minuteOfDay(entry.planned_start_at!, timezone) -
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
        )}
      </section>

      {selectedEntry && draft ? (
        <div className="calendar-v2-editor-backdrop" onMouseDown={closeEditor}>
          <section
            className="calendar-v2-editor"
            role="dialog"
            aria-modal="true"
            aria-label={`编辑日程：${selectedEntry.task_title}`}
            onMouseDown={(event) => event.stopPropagation()}
          >
            <header>
              <div>
                <span>日程设置</span>
                <h2>{selectedEntry.task_title}</h2>
              </div>
              <button type="button" onClick={closeEditor} aria-label="关闭日程设置">
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
                      timingType: event.target.value as ScheduleDraft['timingType'],
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
                        updateDraft({ durationMinutes: Number(event.target.value) })
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

            {editorError ? <p className="calendar-v2-error">{editorError}</p> : null}
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
        </div>
      ) : null}
    </div>
  )
}
