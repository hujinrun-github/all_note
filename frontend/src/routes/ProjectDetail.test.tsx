import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { ProjectV2 } from '../api/taskDomain'
import * as taskHooks from '../hooks/useTaskDomain'
import ProjectDetail from './ProjectDetail'

vi.mock('../hooks/useTaskDomain')

const createTask = vi.fn()
const completeProject = vi.fn()
const cancelTask = vi.fn()
const updateTask = vi.fn()

describe('Project detail v2', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(taskHooks.useProject).mockReturnValue({
      data: project,
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    } as unknown as ReturnType<typeof taskHooks.useProject>)
    vi.mocked(taskHooks.useTaskDefinitions).mockReturnValue({
      data: [taskDefinition],
      isLoading: false,
    } as ReturnType<typeof taskHooks.useTaskDefinitions>)
    vi.mocked(taskHooks.useOccurrences).mockReturnValue({
      data: [openOccurrence],
      isLoading: false,
    } as ReturnType<typeof taskHooks.useOccurrences>)
    vi.mocked(taskHooks.useCreateTaskMutation).mockReturnValue({
      mutateAsync: createTask,
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useCreateTaskMutation>)
    vi.mocked(taskHooks.useCompleteProjectMutation).mockReturnValue({
      mutateAsync: completeProject,
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useCompleteProjectMutation>)
    vi.mocked(taskHooks.useCancelTaskMutation).mockReturnValue({
      mutateAsync: cancelTask,
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useCancelTaskMutation>)
    vi.mocked(taskHooks.useUpdateTaskDefinitionMutation).mockReturnValue({
      mutateAsync: updateTask,
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useUpdateTaskDefinitionMutation>)
    vi.mocked(taskHooks.useProjects).mockReturnValue({
      data: [project, { ...project, id: 'project-2', name: '后续项目', kind: 'standard' }],
      isLoading: false,
    } as ReturnType<typeof taskHooks.useProjects>)
  })

  it('creates a task in the current project and keeps definition state separate', async () => {
    renderDetail()
    const user = userEvent.setup()

    expect(screen.getByText('定义：进行中')).toBeVisible()
    expect(screen.getByText('执行：未开始')).toBeVisible()
    await user.type(screen.getByLabelText('任务标题'), '完成领域评审')
    await user.click(screen.getByRole('button', { name: '添加任务' }))

    expect(createTask).toHaveBeenCalledWith(
      expect.objectContaining({
        project_id: 'project-1',
        title: '完成领域评审',
      })
    )
  })

  it('only exposes Roadmap for learning projects', () => {
    renderDetail()
    expect(screen.getByRole('link', { name: '打开学习 Roadmap' })).toBeVisible()
  })

  it('requires an explicit cancel-or-move decision before completing a project with open occurrences', async () => {
    renderDetail()
    const user = userEvent.setup()

    await user.click(screen.getByRole('button', { name: '完成项目' }))

    expect(screen.getByRole('dialog', { name: '处理未完成执行实例' })).toBeVisible()
    expect(screen.getByRole('button', { name: '取消未完成实例并完成' })).toBeVisible()
    expect(screen.getByRole('button', { name: '迁移到其他项目' })).toBeVisible()
    expect(completeProject).not.toHaveBeenCalled()
  })

  it('cancels non-terminal task aggregates before completing the project', async () => {
    cancelTask.mockResolvedValue({
      task_revision: 5,
      schedule_revision: 2,
      occurrence_revisions: { 'occurrence-1': 6 },
    })
    completeProject.mockResolvedValue({
      project_id: 'project-1',
      project_revision: 4,
      status: 'completed',
    })
    renderDetail()
    const user = userEvent.setup()

    await user.click(screen.getByRole('button', { name: '完成项目' }))
    await user.click(screen.getByRole('button', { name: '取消未完成实例并完成' }))

    await waitFor(() => expect(cancelTask).toHaveBeenCalled())
    expect(cancelTask).toHaveBeenCalledWith({
      projectID: 'project-1',
      taskID: 'task-1',
      expectedRevisions: {
        expected_task_revision: 4,
        expected_schedule_revision: 2,
        expected_occurrence_revisions: { 'occurrence-1': 5 },
      },
    })
    expect(completeProject).toHaveBeenCalledWith({
      projectID: 'project-1',
      expectedRevision: { expected_project_revision: 3 },
    })
  })

  it('moves task definitions to the selected project before completing', async () => {
    updateTask.mockResolvedValue({ ...taskDefinition, project_id: 'project-2', revision: 5 })
    completeProject.mockResolvedValue({
      project_id: 'project-1',
      project_revision: 4,
      status: 'completed',
    })
    renderDetail()
    const user = userEvent.setup()

    await user.click(screen.getByRole('button', { name: '完成项目' }))
    await user.click(screen.getByRole('button', { name: '迁移到其他项目' }))
    await user.selectOptions(screen.getByLabelText('目标项目'), 'project-2')
    await user.click(screen.getByRole('button', { name: '迁移任务并完成' }))

    await waitFor(() => expect(updateTask).toHaveBeenCalled())
    expect(updateTask).toHaveBeenCalledWith({
      projectID: 'project-1',
      taskID: 'task-1',
      input: {
        project_id: 'project-2',
        expected_task_revision: 4,
        expected_schedule_revision: 2,
      },
    })
    expect(completeProject).toHaveBeenCalled()
  })
})

function renderDetail() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <MemoryRouter initialEntries={['/projects/project-1']}>
      <QueryClientProvider client={client}>
        <Routes>
          <Route path="/projects/:projectID" element={<ProjectDetail />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>
  )
}

const project: ProjectV2 = {
  id: 'project-1',
  name: '日语学习',
  kind: 'learning',
  horizon: 'long',
  status: 'active',
  revision: 3,
}

const taskDefinition: import('../api/taskDomain').TaskV2 = {
  id: 'task-1',
  project_id: 'project-1',
  title: '复习 N2 语法',
  priority: 1,
  sort_order: 0,
  lifecycle_status: 'active',
  revision: 4,
  schedule_revision: 2,
}

const openOccurrence: import('../api/taskDomain').OccurrenceV2 = {
  id: 'occurrence-1',
  task_id: 'task-1',
  occurrence_key: 'once',
  execution_status: 'open',
  revision: 5,
  generated_schedule_revision: 2,
}
