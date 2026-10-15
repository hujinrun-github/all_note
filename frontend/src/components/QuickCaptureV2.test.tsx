import { render, screen, waitFor } from '@testing-library/react'
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

  it('can create a task with a concrete date and time range', async () => {
    render(<QuickCaptureV2 />)
    const user = userEvent.setup()
    await user.type(screen.getByLabelText('快速捕获任务标题'), '安排评审')

    await user.selectOptions(
      screen.getByRole('combobox', { name: '任务安排方式' }),
      'time_block'
    )
    await user.clear(screen.getByLabelText('任务执行日期'))
    await user.type(screen.getByLabelText('任务执行日期'), '2026-08-03')
    await user.clear(screen.getByLabelText('任务开始时间'))
    await user.type(screen.getByLabelText('任务开始时间'), '14:30')
    await user.clear(screen.getByLabelText('任务结束时间'))
    await user.type(screen.getByLabelText('任务结束时间'), '16:00')
    await user.click(screen.getByRole('button', { name: '创建任务' }))

    expect(createTask).toHaveBeenCalledWith(
      expect.objectContaining({
        schedule: {
          recurrence_type: 'none',
          timing_type: 'time_block',
          timezone: expect.any(String),
          starts_on: '2026-08-03',
          local_start_time: '14:30',
          duration_minutes: 90,
        },
      })
    )
  })

  it('creates a weekly recurring task from quick capture', async () => {
    render(<QuickCaptureV2 />)
    const user = userEvent.setup()
    await user.type(screen.getByLabelText('快速捕获任务标题'), '每周复盘')

    await user.selectOptions(
      screen.getByRole('combobox', { name: '任务重复方式' }),
      'weekly'
    )
    await user.clear(screen.getByLabelText('任务重复起始日期'))
    await user.type(screen.getByLabelText('任务重复起始日期'), '2026-08-03')
    await user.click(screen.getByRole('button', { name: '创建任务' }))

    expect(createTask).toHaveBeenCalledWith(
      expect.objectContaining({
        schedule: {
          recurrence_type: 'weekly',
          timing_type: 'date',
          timezone: expect.any(String),
          starts_on: '2026-08-03',
          rule: { interval: 1, weekdays: [1] },
        },
      })
    )
  })

  it('supports workday and weekend recurring presets', async () => {
    render(<QuickCaptureV2 />)
    const user = userEvent.setup()
    await user.type(screen.getByLabelText('快速捕获任务标题'), '工作日练习')
    const recurrence = screen.getByRole('combobox', { name: '任务重复方式' })

    await user.selectOptions(recurrence, 'weekdays')
    await user.clear(screen.getByLabelText('任务重复起始日期'))
    await user.type(screen.getByLabelText('任务重复起始日期'), '2026-08-03')
    await user.click(screen.getByRole('button', { name: '创建任务' }))

    expect(createTask).toHaveBeenCalledWith(
      expect.objectContaining({
        schedule: expect.objectContaining({
          recurrence_type: 'weekly',
          rule: { interval: 1, weekdays: [1, 2, 3, 4, 5] },
        }),
      })
    )

    createTask.mockClear()
    await user.clear(screen.getByLabelText('快速捕获任务标题'))
    await user.type(screen.getByLabelText('快速捕获任务标题'), '周末整理')
    await user.selectOptions(recurrence, 'weekends')
    await user.click(screen.getByRole('button', { name: '创建任务' }))

    expect(createTask).toHaveBeenCalledWith(
      expect.objectContaining({
        schedule: expect.objectContaining({
          recurrence_type: 'weekly',
          rule: { interval: 1, weekdays: [0, 6] },
        }),
      })
    )
  })

  it('allows selecting individual weekdays for a recurring task', async () => {
    render(<QuickCaptureV2 />)
    const user = userEvent.setup()
    await user.type(screen.getByLabelText('快速捕获任务标题'), '周一周五复盘')
    const recurrence = screen.getByRole('combobox', { name: '任务重复方式' })

    await user.selectOptions(recurrence, 'weekly')
    await user.clear(screen.getByLabelText('任务重复起始日期'))
    await user.type(screen.getByLabelText('任务重复起始日期'), '2026-08-03')
    await user.selectOptions(recurrence, 'custom')
    await user.click(screen.getByRole('button', { name: '任务重复星期五' }))
    await user.click(screen.getByRole('button', { name: '创建任务' }))

    expect(createTask).toHaveBeenCalledWith(
      expect.objectContaining({
        schedule: expect.objectContaining({
          recurrence_type: 'weekly',
          rule: { interval: 1, weekdays: [1, 5] },
        }),
      })
    )
  })

  it('starts empty and does not create an example task', () => {
    render(<QuickCaptureV2 />)
    expect(screen.getByLabelText('快速捕获任务标题')).toHaveValue('')
    expect(screen.getByRole('button', { name: '创建任务' })).toBeDisabled()
  })

  it('retains input after a failed submission so it can be retried', async () => {
    createTask.mockRejectedValueOnce(new Error('offline'))
    render(<QuickCaptureV2 />)
    const user = userEvent.setup()
    await user.type(screen.getByLabelText('快速捕获任务标题'), '保留我的想法')
    await user.click(screen.getByRole('button', { name: '创建任务' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('创建失败')
    expect(screen.getByLabelText('快速捕获任务标题')).toHaveValue(
      '保留我的想法'
    )
    createTask.mockResolvedValueOnce({})
    await user.click(screen.getByRole('button', { name: '创建任务' }))
    await waitFor(() => expect(createTask).toHaveBeenCalledTimes(2))
  })
})
