import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { OccurrenceV2 } from '../../api/taskDomain'
import { OccurrenceRow } from './TaskDomainWorkspace'

const occurrence: OccurrenceV2 = {
  id: 'occurrence',
  task_id: 'task',
  title: '整理反馈',
  occurrence_key: 'once',
  execution_status: 'open',
  revision: 1,
  generated_schedule_revision: 1,
}

describe('OccurrenceRow actions', () => {
  it('activates a focused completion button without also opening the inspector', async () => {
    const select = vi.fn()
    const complete = vi.fn().mockResolvedValue(undefined)
    render(
      <OccurrenceRow
        occurrence={occurrence}
        onSelect={select}
        onComplete={complete}
      />
    )
    screen.getByRole('button', { name: '完成整理反馈' }).focus()
    await userEvent.keyboard('{Enter}')
    expect(complete).toHaveBeenCalledTimes(1)
    expect(select).not.toHaveBeenCalled()
  })

  it('shows a failure next to the task and allows retry', async () => {
    const complete = vi
      .fn()
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce(undefined)
    render(
      <OccurrenceRow
        occurrence={occurrence}
        onSelect={vi.fn()}
        onComplete={complete}
      />
    )
    await userEvent.click(screen.getByRole('button', { name: '完成整理反馈' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('完成失败')
    await userEvent.click(screen.getByRole('button', { name: '完成整理反馈' }))
    expect(complete).toHaveBeenCalledTimes(2)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
