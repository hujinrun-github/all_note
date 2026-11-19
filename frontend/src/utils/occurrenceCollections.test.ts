import { describe, expect, it } from 'vitest'
import type { OccurrenceV2 } from '../api/taskDomain'
import {
  collectOccurrencesByTask,
  collectionScheduleRange,
  collectionStatusSummary,
} from './occurrenceCollections'

const item = (
  id: string,
  taskID: string,
  fields: Partial<OccurrenceV2> = {}
): OccurrenceV2 => ({
  id,
  task_id: taskID,
  occurrence_key: id,
  execution_status: 'open',
  revision: 1,
  generated_schedule_revision: 1,
  ...fields,
})

describe('occurrence collections', () => {
  it('groups by task identity while preserving server order and separate same-name tasks', () => {
    const first = item('a1', 'a', { title: '同名任务' })
    const second = item('b1', 'b', { title: '同名任务' })
    const third = item('a2', 'a')
    const rows = [first, second, third]
    expect(collectOccurrencesByTask(rows)).toEqual([
      { taskID: 'a', occurrences: [first, third] },
      { taskID: 'b', occurrences: [second] },
    ])
    expect(collectOccurrencesByTask(rows, false)).toHaveLength(3)
    expect(rows).toEqual([first, second, third])
  })
  it('uses true date boundaries despite unsorted input and distinguishes years', () => {
    const rows = [
      item('b', 'a', { planned_date: '2027-01-03' }),
      item('c', 'a', { planned_date: '2027-01-01' }),
      item('a', 'a', { planned_date: '2026-12-30' }),
    ]
    expect(collectionScheduleRange(rows)).toBe('2026年12月30日 – 2027年1月3日')
    expect(collectionScheduleRange([rows[1], rows[0]])).toBe('1月1日 – 1月3日')
  })
  it('uses occurrence-local calendar days for timestamp-only executions and discloses undated entries', () => {
    expect(
      collectionScheduleRange([
        item('a', 't', {
          planned_start_at: '2026-12-31T16:30:00Z',
          timezone: 'Asia/Shanghai',
        }),
        item('b', 't'),
      ])
    ).toBe('1月1日 · 1 次未安排')
    expect(collectionScheduleRange([item('a', 't'), item('b', 't')])).toBe(
      '未安排'
    )
  })
  it('summarizes actual execution states without treating pending work as completed', () => {
    expect(
      collectionStatusSummary([
        item('a', 't'),
        item('b', 't', { execution_status: 'blocked' }),
        item('c', 't', { execution_status: 'done' }),
      ])
    ).toBe('1 待执行 · 1 阻塞 · 1 已完成')
  })
})
