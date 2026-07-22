import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { TaskDomainAPIError, type CalendarEntryV2 } from '../api/taskDomain'
import {
  useCalendarEntries,
  useReopenOccurrenceMutation,
  useRescheduleOccurrenceMutation,
  useRescheduleThisAndFollowingMutation,
} from '../hooks/useTaskDomain'
import CalendarV2 from './CalendarV2'

vi.mock('../hooks/useTaskDomain', () => ({
  useCalendarEntries: vi.fn(),
  useReopenOccurrenceMutation: vi.fn(),
  useRescheduleOccurrenceMutation: vi.fn(),
  useRescheduleThisAndFollowingMutation: vi.fn(),
}))

const entries: CalendarEntryV2[] = [
  {
    project_id: 'project-1',
    project_revision: 2,
    task_id: 'task-all-day',
    task_revision: 7,
    schedule_revision: 5,
    task_title: '发布准备',
    occurrence_id: 'occurrence-all-day',
    occurrence_key: 'once',
    occurrence_revision: 11,
    generated_schedule_revision: 3,
    execution_status: 'open',
    timing_type: 'date',
    timezone: 'Asia/Shanghai',
    recurring: false,
    planned_date: '2026-07-22',
    all_day_end_date: '2026-07-25',
  },
  {
    project_id: 'project-1',
    project_revision: 2,
    task_id: 'task-time',
    task_revision: 8,
    schedule_revision: 6,
    task_title: '设计评审',
    occurrence_id: 'occurrence-time',
    occurrence_key: '2026-07-23',
    occurrence_revision: 12,
    generated_schedule_revision: 6,
    execution_status: 'active',
    timing_type: 'time_block',
    timezone: 'Asia/Shanghai',
    recurring: true,
    planned_date: '2026-07-23',
    planned_start_at: '2026-07-23T01:30:00Z',
    planned_end_at: '2026-07-23T02:30:00Z',
  },
  {
    project_id: 'project-1',
    project_revision: 2,
    task_id: 'task-done',
    task_revision: 4,
    schedule_revision: 2,
    task_title: '已完成回顾',
    occurrence_id: 'occurrence-done',
    occurrence_key: 'once',
    occurrence_revision: 9,
    generated_schedule_revision: 2,
    execution_status: 'done',
    timing_type: 'date',
    timezone: 'Asia/Shanghai',
    recurring: false,
    planned_date: '2026-07-24',
    all_day_end_date: '2026-07-25',
  },
  {
    project_id: 'project-1',
    project_revision: 2,
    task_id: 'task-unscheduled',
    task_revision: 1,
    schedule_revision: 1,
    task_title: '不应出现在日历',
    occurrence_id: 'occurrence-unscheduled',
    occurrence_key: 'once',
    occurrence_revision: 1,
    generated_schedule_revision: 1,
    execution_status: 'open',
    timing_type: 'unscheduled',
    timezone: 'Asia/Shanghai',
    recurring: false,
  },
]

const onlyThisMock = vi.fn()
const followingMock = vi.fn()
const reopenMock = vi.fn()

function mutationResult(mutateAsync: ReturnType<typeof vi.fn>) {
  return {
    mutateAsync,
    isPending: false,
    error: null,
    reset: vi.fn(),
  } as never
}

function renderCalendar() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <CalendarV2 initialDate="2026-07-23" initialTimezone="Asia/Shanghai" />
    </QueryClientProvider>
  )
}

describe('CalendarV2', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    onlyThisMock.mockResolvedValue({
      task_revision: 7,
      schedule_revision: 5,
      occurrence_revision: 12,
    })
    followingMock.mockResolvedValue({
      task_revision: 8,
      schedule_revision: 6,
      schedule_version: 4,
    })
    reopenMock.mockResolvedValue({
      task_revision: 4,
      schedule_revision: 2,
      occurrence_revisions: { 'occurrence-done': 10 },
    })
    vi.mocked(useCalendarEntries).mockReturnValue({
      data: entries,
      isLoading: false,
      isFetching: false,
      isError: false,
    } as ReturnType<typeof useCalendarEntries>)
    vi.mocked(useRescheduleOccurrenceMutation).mockReturnValue(
      mutationResult(onlyThisMock)
    )
    vi.mocked(useRescheduleThisAndFollowingMutation).mockReturnValue(
      mutationResult(followingMock)
    )
    vi.mocked(useReopenOccurrenceMutation).mockReturnValue(
      mutationResult(reopenMock)
    )
  })

  afterEach(() => vi.useRealTimers())

  it('renders date occurrences in the all-day lane, time blocks in the grid, and hides unscheduled work', () => {
    renderCalendar()

    const allDayLane = screen.getByRole('region', { name: '全天安排' })
    const timeGrid = screen.getByRole('region', { name: '时间安排' })
    const allDayEntry = within(allDayLane).getByRole('button', {
      name: /编辑日程：发布准备/,
    })

    expect(allDayEntry).toHaveTextContent('发布准备')
    expect(allDayEntry).toHaveTextContent('7月22日–7月24日')
    expect(allDayEntry).toHaveAttribute('data-exclusive-end', '2026-07-25')
    expect(
      within(timeGrid).getByRole('button', { name: /编辑日程：设计评审/ })
    ).toHaveTextContent('09:30–10:30')
    expect(screen.queryByText('不应出现在日历')).not.toBeInTheDocument()
  })

  it('reschedules only the selected occurrence by default with independent revisions', async () => {
    const user = userEvent.setup()
    renderCalendar()

    await user.click(
      screen.getByRole('button', { name: /编辑日程：发布准备/ })
    )
    expect(screen.getByRole('radio', { name: '仅本次' })).toBeChecked()
    fireEvent.change(screen.getByLabelText('计划日期'), {
      target: { value: '2026-07-24' },
    })
    await user.click(screen.getByRole('button', { name: '保存日程' }))

    expect(onlyThisMock).toHaveBeenCalledWith({
      projectID: 'project-1',
      taskID: 'task-all-day',
      occurrenceID: 'occurrence-all-day',
      input: {
        expected_task_revision: 7,
        expected_schedule_revision: 5,
        expected_occurrence_revision: 11,
        timing: {
          timing_type: 'date',
          timezone: 'Asia/Shanghai',
          planned_date: '2026-07-24',
          all_day_end_date: '2026-07-25',
        },
      },
    })
  })

  it('changes a recurring schedule only after choosing this-and-following explicitly', async () => {
    const user = userEvent.setup()
    renderCalendar()

    await user.click(
      screen.getByRole('button', { name: /编辑日程：设计评审/ })
    )
    await user.click(screen.getByRole('radio', { name: '本次及以后' }))
    await user.selectOptions(screen.getByLabelText('重复规则'), 'daily')
    fireEvent.change(screen.getByLabelText('生效日期'), {
      target: { value: '2026-07-24' },
    })
    fireEvent.change(screen.getByLabelText('生成至（不含）'), {
      target: { value: '2026-08-24' },
    })
    await user.click(screen.getByRole('button', { name: '保存日程' }))

    expect(onlyThisMock).not.toHaveBeenCalled()
    expect(followingMock).toHaveBeenCalledWith({
      projectID: 'project-1',
      taskID: 'task-time',
      input: expect.objectContaining({
        expected_task_revision: 8,
        expected_schedule_revision: 6,
        effective_from: '2026-07-24',
        generate_through_exclusive: '2026-08-24',
        schedule: expect.objectContaining({
          recurrence_type: 'daily',
          timing_type: 'time_block',
          timezone: 'Asia/Shanghai',
          starts_on: '2026-07-24',
          local_start_time: '09:30',
          duration_minutes: 60,
          rule: { interval: 1 },
        }),
      }),
    })
  })

  it('prevents moving done occurrences and offers the explicit reopen command', async () => {
    const user = userEvent.setup()
    renderCalendar()

    const doneEntry = screen.getByRole('button', {
      name: /编辑日程：已完成回顾/,
    })
    expect(doneEntry).toHaveAttribute('draggable', 'false')
    await user.click(doneEntry)

    expect(screen.getByRole('alert')).toHaveTextContent(
      '已完成的任务不能移动，请先重新打开'
    )
    expect(screen.getByRole('button', { name: '保存日程' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: '重新打开任务' }))

    expect(reopenMock).toHaveBeenCalledWith({
      projectID: 'project-1',
      taskID: 'task-done',
      occurrenceID: 'occurrence-done',
      expectedRevisions: {
        expected_task_revision: 4,
        expected_schedule_revision: 2,
        expected_occurrence_revisions: { 'occurrence-done': 9 },
      },
    })
  })

  it('asks for a concrete UTC offset when the selected local time is ambiguous', async () => {
    const user = userEvent.setup()
    onlyThisMock
      .mockRejectedValueOnce(
        new TaskDomainAPIError(
          400,
          'ambiguous_local_time',
          '请选择这个本地时间对应的时区偏移',
          false,
          {
            offset_candidates: [
              { offset_seconds: -14400, utc: '2026-11-01T05:30:00Z' },
              { offset_seconds: -18000, utc: '2026-11-01T06:30:00Z' },
            ],
          }
        )
      )
      .mockResolvedValueOnce({
        task_revision: 8,
        schedule_revision: 6,
        occurrence_revision: 13,
      })
    renderCalendar()

    await user.click(
      screen.getByRole('button', { name: /编辑日程：设计评审/ })
    )
    fireEvent.change(screen.getByLabelText('计划日期'), {
      target: { value: '2026-11-01' },
    })
    await user.click(screen.getByRole('button', { name: '保存日程' }))

    const offsetChoice = await screen.findByRole('radio', {
      name: /UTC-05:00/,
    })
    await user.click(offsetChoice)
    await user.click(screen.getByRole('button', { name: '保存日程' }))

    expect(onlyThisMock).toHaveBeenLastCalledWith(
      expect.objectContaining({
        input: expect.objectContaining({
          selected_offsets: { '2026-11-01': -18000 },
        }),
      })
    )
  })

  it('queries the visible week with an exclusive end and the selected timezone', () => {
    renderCalendar()

    expect(useCalendarEntries).toHaveBeenCalledWith({
      from: '2026-07-20',
      to: '2026-07-27',
      timezone: 'Asia/Shanghai',
    })
  })
})
