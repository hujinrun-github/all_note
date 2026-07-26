import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import {
  useCompleteOccurrenceMutation,
  useTaskDefinitions,
  useUpdateTaskDefinitionMutation,
} from '../hooks/useTaskDomain'
import { useTaskInbox } from '../hooks/useTaskInbox'
import Inbox from './Inbox'

vi.mock('../hooks/useTaskDomain', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../hooks/useTaskDomain')>()
  return {
    ...actual,
    useTaskDefinitions: vi.fn(),
    useUpdateTaskDefinitionMutation: vi.fn(),
    useCompleteOccurrenceMutation: vi.fn(),
  }
})

vi.mock('../hooks/useTaskInbox', () => ({
  useTaskInbox: vi.fn(),
}))

const updateTaskMock = vi.fn()
const completeOccurrenceMock = vi.fn()

function renderInbox() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <Inbox />
      </MemoryRouter>
    </QueryClientProvider>
  )
}

describe('V2 task inbox organizer', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    updateTaskMock.mockResolvedValue({})
    completeOccurrenceMock.mockResolvedValue({})

    vi.mocked(useTaskInbox).mockReturnValue({
      inboxProject: {
        id: 'system-inbox',
        name: '收件箱',
        kind: 'standard',
        horizon: 'short',
        status: 'active',
        system_role: 'inbox',
        revision: 1,
      },
      projectsQuery: {
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
            id: 'learning-1',
            name: '学习计划',
            kind: 'learning',
            horizon: 'short',
            status: 'active',
            revision: 2,
          },
          {
            id: 'completed-1',
            name: '已完成项目',
            kind: 'standard',
            horizon: 'short',
            status: 'completed',
            revision: 3,
          },
        ],
        isLoading: false,
        isError: false,
      },
      occurrencesQuery: {
        data: [
          {
            id: 'occurrence-1',
            task_id: 'task-1',
            project_id: 'system-inbox',
            occurrence_key: 'once',
            execution_status: 'open',
            revision: 4,
            generated_schedule_revision: 3,
            task_revision: 5,
            schedule_revision: 3,
            timing_type: 'unscheduled',
          },
        ],
        isLoading: false,
        isError: false,
      },
    } as unknown as ReturnType<typeof useTaskInbox>)

    vi.mocked(useTaskDefinitions).mockReturnValue({
      data: [
        {
          id: 'task-1',
          project_id: 'system-inbox',
          title: '整理新需求',
          description: '确认范围和下一步',
          priority: 2,
          sort_order: 0,
          lifecycle_status: 'published',
          revision: 5,
          schedule_revision: 3,
        },
      ],
      isLoading: false,
      isError: false,
    } as unknown as ReturnType<typeof useTaskDefinitions>)

    vi.mocked(useUpdateTaskDefinitionMutation).mockReturnValue({
      mutateAsync: updateTaskMock,
      isPending: false,
    } as unknown as ReturnType<typeof useUpdateTaskDefinitionMutation>)
    vi.mocked(useCompleteOccurrenceMutation).mockReturnValue({
      mutateAsync: completeOccurrenceMock,
      isPending: false,
    } as unknown as ReturnType<typeof useCompleteOccurrenceMutation>)
  })

  it('shows quick-captured V2 tasks and organizes one into a project', async () => {
    const user = userEvent.setup()
    renderInbox()

    expect(screen.getAllByText('整理新需求')).toHaveLength(2)
    expect(screen.getByText('确认范围和下一步')).toBeVisible()
    expect(
      screen.getByRole('option', { name: '学习计划 · 学习项目' })
    ).toBeVisible()
    expect(
      screen.queryByRole('option', { name: /已完成项目/ })
    ).not.toBeInTheDocument()

    await user.selectOptions(screen.getByLabelText('归入项目'), 'learning-1')
    await user.click(screen.getByRole('button', { name: /归入项目/ }))

    expect(updateTaskMock).toHaveBeenCalledWith({
      projectID: 'system-inbox',
      taskID: 'task-1',
      input: {
        expected_task_revision: 5,
        expected_schedule_revision: 3,
        project_id: 'learning-1',
      },
    })
  })
})
