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
      ],
      isLoading: false,
      isError: false,
    } as ReturnType<typeof taskHooks.useProjects>)
    vi.mocked(taskHooks.useCreateTaskMutation).mockReturnValue({
      mutateAsync: createTask,
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useCreateTaskMutation>)
  })

  it('makes the inbox destination explicit and creates the task there', async () => {
    render(<QuickCaptureV2 />)
    const user = userEvent.setup()

    expect(screen.getByText('将进入：收件箱（系统项目）')).toBeVisible()
    await user.clear(screen.getByLabelText('快速捕获任务标题'))
    await user.type(screen.getByLabelText('快速捕获任务标题'), '记录评审结论')
    await user.click(screen.getByRole('button', { name: '创建到收件箱' }))

    expect(createTask).toHaveBeenCalledWith(
      expect.objectContaining({
        project_id: 'system-inbox',
        title: '记录评审结论',
      })
    )
  })
})
