import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { TaskV2 } from '../../api/taskDomain'
import * as taskHooks from '../../hooks/useTaskDomain'
import {
  TaskCompletionGate,
  taskCompletionProgress,
} from './TaskCompletionGate'

vi.mock('../../hooks/useTaskDomain')

const updateTask = vi.fn()

describe('Task completion gate', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    updateTask.mockImplementation(
      async ({ input }: taskHooks.UpdateTaskDefinitionVariables) => ({
        ...task,
        revision: task.revision + 1,
        completion_requirements: input.completion_requirements,
      })
    )
    vi.mocked(taskHooks.useUpdateTaskDefinitionMutation).mockReturnValue({
      mutateAsync: updateTask,
      isPending: false,
    } as unknown as ReturnType<
      typeof taskHooks.useUpdateTaskDefinitionMutation
    >)
  })

  it('persists progress and unlocks completion only after every item is done', async () => {
    const complete = vi.fn()
    const user = userEvent.setup()
    render(
      <TaskCompletionGate
        task={task}
        occurrence={occurrence}
        onComplete={complete}
        showCompleteAction
      />
    )

    expect(screen.getByText('1 / 2')).toBeVisible()
    expect(screen.getByRole('button', { name: '还需完成 1 项' })).toBeDisabled()

    await user.click(
      screen.getByRole('button', { name: '标记完成：看完交互演示' })
    )

    await waitFor(() =>
      expect(screen.getByRole('button', { name: '完成任务' })).toBeEnabled()
    )
    expect(updateTask).toHaveBeenCalledWith({
      projectID: 'project-1',
      taskID: 'task-1',
      input: {
        completion_requirements: [
          expect.objectContaining({ id: 'article-1', completed: true }),
          expect.objectContaining({ id: 'video-1', completed: true }),
        ],
        expected_task_revision: 4,
        expected_schedule_revision: 2,
      },
    })

    await user.click(screen.getByRole('button', { name: '完成任务' }))
    expect(complete).toHaveBeenCalledOnce()
  })

  it('adds a linked required item through the shared editor', async () => {
    const user = userEvent.setup()
    render(
      <TaskCompletionGate task={{ ...task, completion_requirements: [] }} />
    )

    await user.click(screen.getByRole('button', { name: '添加必选项' }))
    await user.selectOptions(screen.getByLabelText('必选项类型'), 'article')
    await user.type(screen.getByLabelText('必选项名称'), '读完设计复盘')
    await user.type(
      screen.getByLabelText('必选项链接'),
      'https://example.com/retrospective'
    )
    await user.click(screen.getByRole('button', { name: '保存必选项' }))

    expect(updateTask).toHaveBeenCalledWith({
      projectID: 'project-1',
      taskID: 'task-1',
      input: {
        completion_requirements: [
          expect.objectContaining({
            kind: 'article',
            title: '读完设计复盘',
            url: 'https://example.com/retrospective',
            completed: false,
          }),
        ],
        expected_task_revision: 4,
        expected_schedule_revision: 2,
      },
    })
  })

  it('reports remaining requirements for list-level completion guards', () => {
    expect(taskCompletionProgress(task)).toEqual({
      total: 2,
      completed: 1,
      remaining: 1,
    })
    expect(taskCompletionProgress(undefined)).toEqual({
      total: 0,
      completed: 0,
      remaining: 0,
    })
  })
})

const task: TaskV2 = {
  id: 'task-1',
  project_id: 'project-1',
  title: '完成交互设计',
  priority: 2,
  sort_order: 0,
  lifecycle_status: 'active',
  revision: 4,
  schedule_revision: 2,
  completion_requirements: [
    {
      id: 'article-1',
      kind: 'article',
      title: '读完设计规范',
      url: 'https://example.com/article',
      completed: true,
    },
    {
      id: 'video-1',
      kind: 'video',
      title: '看完交互演示',
      url: 'https://example.com/video',
      completed: false,
    },
  ],
}

const occurrence = {
  id: 'occurrence-1',
  task_id: 'task-1',
  occurrence_key: 'once',
  execution_status: 'open' as const,
  revision: 3,
  generated_schedule_revision: 2,
}
