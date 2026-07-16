import { describe, expect, it } from 'vitest'
import { getTaskColor } from './taskColors'

describe('getTaskColor', () => {
  it('keeps a task color stable and assigns different colors to different tasks', () => {
    const first = getTaskColor('task-color-a')
    const second = getTaskColor('task-color-b')

    expect(getTaskColor('task-color-a')).toBe(first)
    expect(second).not.toBe(first)
  })

  it('prefers a color supplied by the task API', () => {
    expect(getTaskColor('task-explicit', '#3b82f6')).toBe('#3b82f6')
  })
})
