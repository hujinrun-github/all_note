import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as taskHooks from '../hooks/useTaskDomain'
import { QuickCaptureV2 } from './QuickCaptureV2'

vi.mock('../hooks/useTaskDomain')

describe('QuickCaptureV2', () => {
  const createTask = vi.fn()

  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(taskHooks.useProjects).mockReturnValue({
      data: [
        {
          id: 'system-inbox',
          name: '收件箱',
          kind: 'standard',
          horizon: 'short',
          status: 'active',
          system_role: 'inbox',
          revision: 1,
        },
        {
          id: 'learning-project',
          name: '日语学习',
          kind: 'learning',
          horizon: 'long',
          status: 'planning',
          revision: 2,
        },
        {
          id: 'completed-project',
          name: '已结束项目',
          kind: 'standard',
          horizon: 'short',
          status: 'completed',
          revision: 3,
        },
      ],
      isLoading: false,
      isError: false,
    } as ReturnType<typeof taskHooks.useProjects>)
    vi.mocked(taskHooks.useCreateTaskMutation).mockReturnValue({
      mutateAsync: createTask,
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useCreateTaskMutation>)
  })

  it('defaults to the inbox and creates the task there', async () => {
    render(<QuickCaptureV2 />)
    const user = userEvent.setup()

    expect(screen.getByRole('combobox', { name: '归属项目' })).toHaveValue(
      'system-inbox'
    )
    await user.clear(screen.getByLabelText('快速捕获任务标题'))
    await user.type(screen.getByLabelText('快速捕获任务标题'), '记录评审结论')
    await user.click(screen.getByRole('button', { name: '创建任务' }))

    expect(createTask).toHaveBeenCalledWith(
      expect.objectContaining({
        project_id: 'system-inbox',
        title: '记录评审结论',
      })
    )
  })

  it('creates the task in the selected available project', async () => {
    render(<QuickCaptureV2 />)
    const user = userEvent.setup()
    const projectSelect = screen.getByRole('combobox', { name: '归属项目' })

    expect(projectSelect).toHaveDisplayValue('收件箱 · 系统收件箱')
    expect(
      screen.queryByRole('option', { name: /已结束项目/ })
    ).not.toBeInTheDocument()
    await user.selectOptions(projectSelect, 'learning-project')
    await user.clear(screen.getByLabelText('快速捕获任务标题'))
    await user.type(screen.getByLabelText('快速捕获任务标题'), '完成听力练习')
    await user.click(screen.getByRole('button', { name: '创建任务' }))

    expect(createTask).toHaveBeenCalledWith(
      expect.objectContaining({
        project_id: 'learning-project',
        title: '完成听力练习',
      })
    )
  })
})
