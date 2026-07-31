import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { TaskRetentionActions } from './TaskRetentionActions'

describe('TaskRetentionActions', () => {
  it('explains archive effects and requires an explicit confirmation', async () => {
    const archive = vi.fn().mockResolvedValue(undefined)
    const user = userEvent.setup()
    render(
      <TaskRetentionActions
        taskTitle="阅读架构文章"
        archived={false}
        onArchive={archive}
        onDelete={vi.fn()}
      />
    )

    await user.click(screen.getByRole('button', { name: '归档任务' }))
    const dialog = screen.getByRole('alertdialog', { name: '归档这个任务？' })
    expect(dialog).toHaveTextContent('尚未结束的执行实例会被取消')
    expect(archive).not.toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: '确认归档' }))
    expect(archive).toHaveBeenCalledTimes(1)
  })

  it('keeps permanent delete available for archived tasks and warns it cannot be undone', async () => {
    const remove = vi.fn().mockResolvedValue(undefined)
    const user = userEvent.setup()
    render(
      <TaskRetentionActions
        taskTitle="观看训练视频"
        archived
        onDelete={remove}
      />
    )

    expect(
      screen.queryByRole('button', { name: '归档任务' })
    ).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '永久删除' }))
    const dialog = screen.getByRole('alertdialog', {
      name: '永久删除这个任务？',
    })
    expect(dialog).toHaveTextContent('执行历史都会被永久删除')
    expect(dialog).toHaveTextContent('无法撤销')
    await user.click(within(dialog).getByRole('button', { name: '永久删除' }))
    expect(remove).toHaveBeenCalledTimes(1)
  })
})
