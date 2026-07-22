import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as taskHooks from '../../hooks/useTaskDomain'
import { TaskDomainGate } from './TaskDomainGate'

vi.mock('../../hooks/useTaskDomain')

describe('TaskDomainGate', () => {
  beforeEach(() => vi.clearAllMocks())

  it('keeps legacy workspaces on legacy screens', () => {
    vi.mocked(taskHooks.useTaskDomainCapabilities).mockReturnValue({
      data: { model_version: 'legacy', available: true },
      isLoading: false,
      isError: false,
    } as ReturnType<typeof taskHooks.useTaskDomainCapabilities>)

    render(<TaskDomainGate legacy={<div>旧页面</div>} v2={<div>新页面</div>} />)
    expect(screen.getByText('旧页面')).toBeVisible()
    expect(screen.queryByText('新页面')).not.toBeInTheDocument()
  })

  it('loads v2 only after the capability explicitly reports v2', () => {
    vi.mocked(taskHooks.useTaskDomainCapabilities).mockReturnValue({
      data: { model_version: 'v2', available: true },
      isLoading: false,
      isError: false,
    } as ReturnType<typeof taskHooks.useTaskDomainCapabilities>)

    render(<TaskDomainGate legacy={<div>旧页面</div>} v2={<div>新页面</div>} />)
    expect(screen.getByText('新页面')).toBeVisible()
  })

  it('does not silently infer legacy when capability resolution fails', () => {
    vi.mocked(taskHooks.useTaskDomainCapabilities).mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
    } as ReturnType<typeof taskHooks.useTaskDomainCapabilities>)

    render(<TaskDomainGate legacy={<div>旧页面</div>} v2={<div>新页面</div>} />)
    expect(screen.getByRole('alert')).toHaveTextContent('任务服务暂时不可用')
    expect(screen.queryByText('旧页面')).not.toBeInTheDocument()
  })
})
