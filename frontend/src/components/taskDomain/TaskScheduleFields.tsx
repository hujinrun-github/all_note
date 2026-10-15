import type {
  RecurrenceType,
  ScheduleV2Input,
  TimingType,
} from '../../api/taskDomain'

export interface TaskScheduleDraft {
  recurrenceType: RecurrenceType
  /** Explicit weekly days are used by the workday/weekend presets. */
  weeklyDays?: number[]
  timingType: TimingType
  startsOn: string
  startTime: string
  endTime: string
}

export type RecurrenceSelection =
  | RecurrenceType
  | 'weekdays'
  | 'weekends'
  | 'custom'

const weekdayOptions = [
  { value: 1, shortLabel: '一', label: '星期一' },
  { value: 2, shortLabel: '二', label: '星期二' },
  { value: 3, shortLabel: '三', label: '星期三' },
  { value: 4, shortLabel: '四', label: '星期四' },
  { value: 5, shortLabel: '五', label: '星期五' },
  { value: 6, shortLabel: '六', label: '星期六' },
  { value: 0, shortLabel: '日', label: '星期日' },
]

export function createTaskScheduleDraft(
  timingType: TimingType = 'unscheduled'
): TaskScheduleDraft {
  const range = initialTimeRange()
  return {
    recurrenceType: 'none',
    timingType,
    startsOn: todayInputValue(),
    startTime: range.start,
    endTime: range.end,
  }
}

export function taskScheduleValidationError(draft: TaskScheduleDraft) {
  if (draft.timingType !== 'unscheduled' && draft.startsOn === '') {
    return '请选择任务的起始日期。'
  }
  if (
    draft.timingType === 'time_block' &&
    timeRangeMinutes(draft.startTime, draft.endTime) <= 0
  ) {
    return '请确保结束时间晚于开始时间。'
  }
  return ''
}

export function buildTaskScheduleInput(
  draft: TaskScheduleDraft,
  timezone = Intl.DateTimeFormat().resolvedOptions().timeZone
): ScheduleV2Input {
  const scheduled = draft.timingType !== 'unscheduled'
  return {
    recurrence_type: draft.recurrenceType,
    timing_type: draft.timingType,
    timezone,
    ...(scheduled
      ? {
          starts_on: draft.startsOn,
          ...(draft.timingType === 'time_block'
            ? {
                local_start_time: draft.startTime,
                duration_minutes: timeRangeMinutes(
                  draft.startTime,
                  draft.endTime
                ),
              }
            : {}),
          ...(draft.recurrenceType === 'none'
            ? {}
            : {
                rule: recurrenceRule(
                  draft.recurrenceType,
                  draft.startsOn,
                  draft.weeklyDays
                ),
              }),
        }
      : {}),
  }
}

export function recurrenceRule(
  recurrenceType: Exclude<RecurrenceType, 'none'>,
  startsOn: string,
  weeklyDays?: number[]
) {
  const date = new Date(`${startsOn}T12:00:00`)
  if (recurrenceType === 'weekly') {
    const selectedDays = normalizeWeeklyDays(weeklyDays ?? [])
    return {
      interval: 1,
      weekdays: selectedDays.length > 0 ? selectedDays : [date.getDay()],
    }
  }
  if (recurrenceType === 'monthly') {
    return { interval: 1, month_days: [date.getDate()] }
  }
  return { interval: 1 }
}

export function TaskScheduleFields({
  value,
  onChange,
  labelPrefix = '任务',
  className = '',
}: {
  value: TaskScheduleDraft
  onChange: (value: TaskScheduleDraft) => void
  labelPrefix?: string
  className?: string
}) {
  function update(next: Partial<TaskScheduleDraft>) {
    onChange({ ...value, ...next })
  }

  return (
    <div className={`task-schedule-fields ${className}`.trim()}>
      <TaskRecurrenceField
        value={value}
        onChange={onChange}
        labelPrefix={labelPrefix}
      />
      <label>
        <span>安排方式</span>
        <select
          aria-label={`${labelPrefix}安排方式`}
          value={value.timingType}
          onChange={(event) => {
            const timingType = event.target.value as TimingType
            update({
              timingType,
              ...(timingType === 'unscheduled'
                ? { recurrenceType: 'none' as const }
                : {}),
            })
          }}
        >
          <option value="unscheduled">暂不安排</option>
          <option value="date">指定日期（全天）</option>
          <option value="time_block">指定具体时间</option>
        </select>
      </label>
      {value.timingType !== 'unscheduled' ? (
        <label>
          <span>
            {value.recurrenceType === 'none' ? '执行日期' : '起始日期'}
          </span>
          <input
            aria-label={`${labelPrefix}${
              value.recurrenceType === 'none' ? '执行日期' : '重复起始日期'
            }`}
            type="date"
            value={value.startsOn}
            onChange={(event) => update({ startsOn: event.target.value })}
          />
        </label>
      ) : null}
      {value.timingType === 'time_block' ? (
        <div className="task-schedule-time-range">
          <label>
            <span>开始时间</span>
            <input
              aria-label={`${labelPrefix}开始时间`}
              type="time"
              value={value.startTime}
              onChange={(event) => {
                const startTime = event.target.value
                update({
                  startTime,
                  endTime:
                    timeRangeMinutes(startTime, value.endTime) > 0
                      ? value.endTime
                      : addMinutes(startTime, 60),
                })
              }}
            />
          </label>
          <span aria-hidden="true">至</span>
          <label>
            <span>结束时间</span>
            <input
              aria-label={`${labelPrefix}结束时间`}
              type="time"
              value={value.endTime}
              onChange={(event) => update({ endTime: event.target.value })}
            />
          </label>
        </div>
      ) : null}
      <small>{taskScheduleHint(value)}</small>
    </div>
  )
}

export function TaskRecurrenceField({
  value,
  onChange,
  labelPrefix = '任务',
  showStartDate = false,
}: {
  value: TaskScheduleDraft
  onChange: (value: TaskScheduleDraft) => void
  labelPrefix?: string
  showStartDate?: boolean
}) {
  const selection = recurrenceSelection(value)

  return (
    <>
      <label>
        <span>重复</span>
        <select
          aria-label={`${labelPrefix}重复方式`}
          value={selection}
          onChange={(event) => {
            const nextSelection = event.target.value as RecurrenceSelection
            const recurrenceType =
              nextSelection === 'weekdays' ||
              nextSelection === 'weekends' ||
              nextSelection === 'custom'
                ? 'weekly'
                : nextSelection
            onChange({
              ...value,
              recurrenceType,
              weeklyDays:
                nextSelection === 'weekdays'
                  ? [1, 2, 3, 4, 5]
                  : nextSelection === 'weekends'
                    ? [0, 6]
                    : nextSelection === 'custom'
                      ? value.weeklyDays?.length
                        ? value.weeklyDays
                        : [weekdayFromDate(value.startsOn)]
                      : undefined,
              timingType:
                recurrenceType !== 'none' && value.timingType === 'unscheduled'
                  ? 'date'
                  : value.timingType,
            })
          }}
        >
          <option value="none">不重复</option>
          <option value="daily">每天</option>
          <option value="weekdays">工作日</option>
          <option value="weekends">周末</option>
          <option value="weekly">每周</option>
          <option value="custom">自定义</option>
          <option value="monthly">每月</option>
        </select>
      </label>
      {selection === 'custom' ? (
        <WeekdayPicker
          days={value.weeklyDays ?? [weekdayFromDate(value.startsOn)]}
          onChange={(weeklyDays) => onChange({ ...value, weeklyDays })}
          labelPrefix={labelPrefix}
        />
      ) : null}
      {showStartDate && value.recurrenceType !== 'none' ? (
        <label>
          <span>起始日期</span>
          <input
            aria-label={`${labelPrefix}重复起始日期`}
            type="date"
            value={value.startsOn}
            onChange={(event) =>
              onChange({ ...value, startsOn: event.target.value })
            }
          />
        </label>
      ) : null}
    </>
  )
}

export function WeekdayPicker({
  days,
  onChange,
  labelPrefix = '任务',
}: {
  days: number[]
  onChange: (days: number[]) => void
  labelPrefix?: string
}) {
  const normalizedDays = normalizeWeeklyDays(days)

  return (
    <fieldset className="task-weekday-picker">
      <legend>选择周几</legend>
      <div role="group" aria-label={`${labelPrefix}重复星期`}>
        {weekdayOptions.map((weekday) => {
          const selected = normalizedDays.includes(weekday.value)
          return (
            <button
              key={weekday.value}
              type="button"
              className={selected ? 'is-active' : ''}
              aria-label={`${labelPrefix}重复${weekday.label}`}
              aria-pressed={selected}
              disabled={selected && normalizedDays.length === 1}
              onClick={() => {
                if (selected && normalizedDays.length === 1) return
                const nextDays = selected
                  ? normalizedDays.filter((day) => day !== weekday.value)
                  : [...normalizedDays, weekday.value]
                onChange(normalizeWeeklyDays(nextDays))
              }}
            >
              {weekday.shortLabel}
            </button>
          )
        })}
      </div>
    </fieldset>
  )
}

function recurrenceSelection(value: TaskScheduleDraft): RecurrenceSelection {
  if (value.recurrenceType !== 'weekly') return value.recurrenceType
  if (sameDays(value.weeklyDays, [1, 2, 3, 4, 5])) return 'weekdays'
  if (sameDays(value.weeklyDays, [0, 6])) return 'weekends'
  if (value.weeklyDays?.length) return 'custom'
  return 'weekly'
}

function sameDays(left: number[] | undefined, right: number[]) {
  if (!left || left.length !== right.length) return false
  const normalizedLeft = normalizeWeeklyDays(left)
  return normalizedLeft.every((day, index) => day === right[index])
}

function normalizeWeeklyDays(days: number[]) {
  return [...new Set(days.filter((day) => day >= 0 && day <= 6))].sort(
    (left, right) => left - right
  )
}

function weekdayFromDate(value: string) {
  const date = new Date(`${value}T12:00:00`)
  return Number.isNaN(date.getTime()) ? new Date().getDay() : date.getDay()
}

export function timeRangeMinutes(start: string, end: string) {
  return timeToMinutes(end) - timeToMinutes(start)
}

function taskScheduleHint(draft: TaskScheduleDraft) {
  if (draft.timingType === 'unscheduled') {
    return '任务会进入“无日期”，稍后仍可安排。'
  }
  const recurrenceText =
    draft.recurrenceType === 'daily'
      ? '每天重复'
      : draft.recurrenceType === 'weekly'
        ? weeklyRecurrenceText(draft.weeklyDays)
        : draft.recurrenceType === 'monthly'
          ? '每月重复'
          : '仅执行一次'
  if (draft.timingType === 'date') {
    return `${recurrenceText}，作为全天事项显示。`
  }
  return `${recurrenceText}，每次为 ${timeRangeMinutes(
    draft.startTime,
    draft.endTime
  )} 分钟的时间块。`
}

function weeklyRecurrenceText(days: number[] | undefined) {
  if (sameDays(days, [1, 2, 3, 4, 5])) return '工作日重复'
  if (sameDays(days, [0, 6])) return '周末重复'
  if (days?.length) {
    const labels = [1, 2, 3, 4, 5, 6, 0]
      .filter((day) => days.includes(day))
      .map((day) => ['日', '一', '二', '三', '四', '五', '六'][day])
    return `每周${labels.join('、')}重复`
  }
  return '每周重复'
}

function todayInputValue() {
  const now = new Date()
  const year = now.getFullYear()
  const month = String(now.getMonth() + 1).padStart(2, '0')
  const day = String(now.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

function initialTimeRange() {
  const now = new Date()
  const nextHour = now.getHours() + 1
  const start = `${String(nextHour >= 23 ? 9 : nextHour).padStart(2, '0')}:00`
  return { start, end: addMinutes(start, 60) }
}

function timeToMinutes(value: string) {
  const [hours, minutes] = value.split(':').map(Number)
  if (!Number.isFinite(hours) || !Number.isFinite(minutes)) return 0
  return hours * 60 + minutes
}

export function addMinutes(value: string, minutes: number) {
  const total = Math.min(timeToMinutes(value) + minutes, 23 * 60 + 59)
  const hours = Math.floor(total / 60)
  const remainingMinutes = total % 60
  return `${String(hours).padStart(2, '0')}:${String(remainingMinutes).padStart(2, '0')}`
}
