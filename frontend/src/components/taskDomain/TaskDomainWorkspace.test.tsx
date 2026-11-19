import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { OccurrenceV2 } from '../../api/taskDomain'
import { OccurrenceRow, TaskDefinitionInspector } from './TaskDomainWorkspace'

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
  it('links the task definition to Japanese integration with the explicit target', () => {
    render(
      <QueryClientProvider client={new QueryClient()}>
        <MemoryRouter>
          <TaskDefinitionInspector
            task={{
              id: 'task/japanese',
              project_id: 'learning',
              title: '听力练习',
              priority: 0,
              sort_order: 0,
              lifecycle_status: 'active',
              revision: 1,
              schedule_revision: 1,
            }}
            occurrences={[]}
            onClose={vi.fn()}
          />
        </MemoryRouter>
      </QueryClientProvider>
    )
    expect(
      screen.getByRole('link', { name: '设置日语任务自动完成' })
    ).toHaveAttribute(
      'href',
      '/settings?integration_task=task%2Fjapanese#japanese'
    )
  })
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
