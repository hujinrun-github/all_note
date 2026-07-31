import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import {
  TaskDomainRevisionConflictError,
  type ProjectV2,
} from '../api/taskDomain'
import * as notesApi from '../api/notes'
import * as roadmapHooks from '../hooks/useRoadmapV2'
import * as taskHooks from '../hooks/useTaskDomain'
import ProjectDetail from './ProjectDetail'

vi.mock('../hooks/useTaskDomain')
vi.mock('../hooks/useRoadmapV2')
vi.mock('../api/notes')

const createTask = vi.fn()
const activateProject = vi.fn()
const completeProject = vi.fn()
const cancelTask = vi.fn()
const publishTask = vi.fn()
const updateTask = vi.fn()
const generateRoadmap = vi.fn()
const updateProject = vi.fn()
const archiveProject = vi.fn()
const deleteProject = vi.fn()

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
    vi.mocked(taskHooks.useActivateProjectMutation).mockReturnValue({
      mutateAsync: activateProject,
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useActivateProjectMutation>)
    vi.mocked(taskHooks.useCompleteProjectMutation).mockReturnValue({
      mutateAsync: completeProject,
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useCompleteProjectMutation>)
    vi.mocked(taskHooks.useUpdateProjectMutation).mockReturnValue({
      mutateAsync: updateProject,
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useUpdateProjectMutation>)
    vi.mocked(taskHooks.useArchiveProjectMutation).mockReturnValue({
      mutateAsync: archiveProject,
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useArchiveProjectMutation>)
    vi.mocked(taskHooks.useDeleteProjectMutation).mockReturnValue({
      mutateAsync: deleteProject,
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useDeleteProjectMutation>)
    vi.mocked(taskHooks.useCancelTaskMutation).mockReturnValue({
      mutateAsync: cancelTask,
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.useCancelTaskMutation>)
    vi.mocked(taskHooks.useUpdateTaskDefinitionMutation).mockReturnValue({
      mutateAsync: updateTask,
      isPending: false,
    } as unknown as ReturnType<
      typeof taskHooks.useUpdateTaskDefinitionMutation
    >)
    vi.mocked(taskHooks.useProjects).mockReturnValue({
      data: [
        project,
        { ...project, id: 'project-2', name: '后续项目', kind: 'standard' },
      ],
      isLoading: false,
    } as ReturnType<typeof taskHooks.useProjects>)
    vi.mocked(taskHooks.usePublishTaskMutation).mockReturnValue({
      mutateAsync: publishTask,
      isPending: false,
    } as unknown as ReturnType<typeof taskHooks.usePublishTaskMutation>)
    vi.mocked(taskHooks.usePauseTaskMutation).mockReturnValue(
      idleMutation() as ReturnType<typeof taskHooks.usePauseTaskMutation>
    )
    vi.mocked(taskHooks.useResumeTaskMutation).mockReturnValue(
      idleMutation() as ReturnType<typeof taskHooks.useResumeTaskMutation>
    )
    vi.mocked(taskHooks.useRestoreTaskMutation).mockReturnValue(
      idleMutation() as ReturnType<typeof taskHooks.useRestoreTaskMutation>
    )
    vi.mocked(taskHooks.useArchiveTaskMutation).mockReturnValue(
      idleMutation() as ReturnType<typeof taskHooks.useArchiveTaskMutation>
    )
    vi.mocked(taskHooks.useDeleteTaskMutation).mockReturnValue(
      idleMutation() as ReturnType<typeof taskHooks.useDeleteTaskMutation>
    )
    vi.mocked(roadmapHooks.useRoadmapV2).mockReturnValue({
      data: null,
      isLoading: false,
      isError: false,
    } as ReturnType<typeof roadmapHooks.useRoadmapV2>)
    vi.mocked(roadmapHooks.useGenerateRoadmapMutation).mockReturnValue({
      mutateAsync: generateRoadmap,
      isPending: false,
    } as unknown as ReturnType<typeof roadmapHooks.useGenerateRoadmapMutation>)
  })

  it('creates a task in the current project and keeps definition state separate', async () => {
    renderDetail()
    const user = userEvent.setup()

    expect(screen.getByText('定义：进行中')).toBeVisible()
    expect(screen.getByLabelText('执行状态：未开始')).toBeVisible()
    await user.click(screen.getByRole('button', { name: '打开添加任务' }))
    await user.type(screen.getByLabelText('任务标题'), '完成领域评审')
    await user.click(screen.getByRole('button', { name: '添加任务' }))

    expect(createTask).toHaveBeenCalledWith(
      expect.objectContaining({
        project_id: 'project-1',
        title: '完成领域评审',
      })
    )
  })

  it('exposes Roadmap generation for learning projects without a route yet', () => {
    renderDetail()
    expect(
      screen.getAllByRole('button', { name: '生成学习 Roadmap' })[0]
    ).toBeVisible()
    expect(screen.getByRole('tab', { name: '学习路线' })).toBeVisible()
  })

  it('exposes completion requirements in a normal project task detail', async () => {
    const user = userEvent.setup()
    renderDetail()

    await user.click(screen.getByText('复习 N2 语法'))

    expect(screen.getByText('完成门槛')).toBeVisible()
    expect(screen.getByText('0 / 0')).toBeVisible()
    expect(screen.getByRole('button', { name: '添加必选项' })).toBeEnabled()
  })

  it('starts a planning project through the lifecycle command', async () => {
    vi.mocked(taskHooks.useProject).mockReturnValue({
      data: { ...project, status: 'planning' },
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    } as unknown as ReturnType<typeof taskHooks.useProject>)
    activateProject.mockResolvedValue({
      project_id: project.id,
      project_revision: project.revision + 1,
      status: 'active',
    })
    renderDetail()
    const user = userEvent.setup()

    expect(
      screen.queryByRole('button', { name: '完成项目' })
    ).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '开始项目' }))

    await waitFor(() =>
      expect(activateProject).toHaveBeenCalledWith({
        projectID: project.id,
        expectedRevision: {
          expected_project_revision: project.revision,
        },
      })
    )
  })

  it('shows a visible error when publishing a stale task definition', async () => {
    vi.mocked(taskHooks.useTaskDefinitions).mockReturnValue({
      data: [{ ...taskDefinition, lifecycle_status: 'draft' }],
      isLoading: false,
    } as ReturnType<typeof taskHooks.useTaskDefinitions>)
    publishTask.mockRejectedValue(
      new TaskDomainRevisionConflictError(
        'the resource changed',
        {
          expected_task_revision: 4,
          expected_schedule_revision: 2,
          expected_occurrence_revisions: {},
        },
        { task_revision: 5 }
      )
    )
    renderDetail()
    const user = userEvent.setup()

    await user.click(screen.getByText('复习 N2 语法'))
    await user.click(screen.getByRole('button', { name: '发布' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      '任务已在其他窗口更新，请刷新后重试。'
    )
  })

  it('submits optional generation guidance from the project page', async () => {
    generateRoadmap.mockResolvedValue({
      id: 'roadmap-1',
      project_id: 'project-1',
      title: '日语学习路线',
      description: '',
      status: 'active',
      revision: 1,
      nodes: [],
      edges: [],
    })
    renderDetail()
    const user = userEvent.setup()

    await user.click(
      screen.getAllByRole('button', { name: '生成学习 Roadmap' })[0]
    )
    const dialog = screen.getByRole('dialog', {
      name: '生成学习 Roadmap',
    })
    await user.type(
      within(dialog).getByRole('textbox', { name: '补充生成要求' }),
      '优先口语实战'
    )
    await user.click(
      within(dialog).getByRole('button', {
        name: '生成学习 Roadmap',
      })
    )

    await waitFor(() =>
      expect(generateRoadmap).toHaveBeenCalledWith({
        prompt: '优先口语实战',
      })
    )
  })

  it('keeps generation guidance and explains an AI timeout in Chinese', async () => {
    generateRoadmap.mockRejectedValue(
      new Error(
        'custom roadmap prompt could not be applied: decode AI response: context deadline exceeded'
      )
    )
    renderDetail()
    const user = userEvent.setup()

    await user.click(
      screen.getAllByRole('button', { name: '生成学习 Roadmap' })[0]
    )
    const dialog = screen.getByRole('dialog', {
      name: '生成学习 Roadmap',
    })
    const guidance = within(dialog).getByRole('textbox', {
      name: '补充生成要求',
    })
    await user.type(guidance, '优先覆盖训练 Infra')
    await user.click(
      within(dialog).getByRole('button', {
        name: '生成学习 Roadmap',
      })
    )

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'AI 生成超时。补充要求已保留，请稍后重试；若仍超时，可适当精简要求。'
    )
    expect(guidance).toHaveValue('优先覆盖训练 Infra')
  })

  it('opens each project action from the overflow menu', async () => {
    renderDetail()
    const user = userEvent.setup()

    await user.click(screen.getByRole('button', { name: '项目操作' }))
    const menu = screen.getByRole('menu', { name: '项目操作菜单' })
    expect(
      within(menu).getByRole('menuitem', { name: /编辑项目信息/ })
    ).toBeVisible()
    expect(
      within(menu).getByRole('menuitem', { name: /归档项目/ })
    ).toBeVisible()
    expect(
      within(menu).getByRole('menuitem', { name: /删除项目/ })
    ).toBeVisible()

    await user.click(
      within(menu).getByRole('menuitem', { name: /编辑项目信息/ })
    )
    expect(screen.getByRole('dialog', { name: '编辑项目信息' })).toBeVisible()
    expect(screen.getByRole('textbox', { name: '项目名称' })).toHaveValue(
      '日语学习'
    )
    await user.click(screen.getByRole('button', { name: '取消' }))

    await user.click(screen.getByRole('button', { name: '项目操作' }))
    await user.click(screen.getByRole('menuitem', { name: /归档项目/ }))
    expect(screen.getByRole('dialog', { name: '归档项目' })).toBeVisible()
    await user.click(screen.getByRole('button', { name: '取消' }))

    await user.click(screen.getByRole('button', { name: '项目操作' }))
    await user.click(screen.getByRole('menuitem', { name: /删除项目/ }))
    expect(screen.getByRole('dialog', { name: '删除项目' })).toBeVisible()
  })

  it('updates project settings through the overflow menu', async () => {
    updateProject.mockResolvedValue({
      ...project,
      name: '日语冲刺',
      horizon: 'short',
      revision: 4,
    })
    renderDetail()
    const user = userEvent.setup()

    await user.click(screen.getByRole('button', { name: '项目操作' }))
    await user.click(screen.getByRole('menuitem', { name: /编辑项目信息/ }))
    const nameInput = screen.getByRole('textbox', { name: '项目名称' })
    await user.clear(nameInput)
    await user.type(nameInput, '日语冲刺')
    await user.selectOptions(
      screen.getByRole('combobox', { name: '项目周期' }),
      'short'
    )
    await user.click(screen.getByRole('button', { name: '保存修改' }))

    await waitFor(() =>
      expect(updateProject).toHaveBeenCalledWith({
        projectID: 'project-1',
        input: {
          name: '日语冲刺',
          horizon: 'short',
          expected_project_revision: 3,
        },
      })
    )
  })

  it('requires an explicit cancel-or-move decision before completing a project with open occurrences', async () => {
    renderDetail()
    const user = userEvent.setup()

    await user.click(screen.getByRole('button', { name: '完成项目' }))

    expect(
      screen.getByRole('dialog', { name: '处理未完成执行实例' })
    ).toBeVisible()
    expect(
      screen.getByRole('button', { name: '取消未完成实例并完成' })
    ).toBeVisible()
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
    await user.click(
      screen.getByRole('button', { name: '取消未完成实例并完成' })
    )

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
    updateTask.mockResolvedValue({
      ...taskDefinition,
      project_id: 'project-2',
      revision: 5,
    })
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

  it('edits the task name, description, and attachment links', async () => {
    updateTask.mockResolvedValue({
      ...taskDefinition,
      title: '复习 N2 核心语法',
      description: '完成错题整理',
      attachment_links: [{ name: '复习资料', url: 'https://example.com/n2' }],
      revision: 5,
    })
    renderDetail()
    const user = userEvent.setup()

    await user.click(screen.getByText('复习 N2 语法'))
    await user.click(screen.getByRole('button', { name: '编辑任务' }))

    const title = screen.getByRole('textbox', { name: '任务名' })
    await user.clear(title)
    await user.type(title, '复习 N2 核心语法')
    await user.type(
      screen.getByRole('textbox', { name: '任务描述' }),
      '完成错题整理'
    )
    await user.click(screen.getByRole('button', { name: '添加附件链接' }))
    await user.type(
      screen.getByRole('textbox', { name: '附件 1 名称' }),
      '复习资料'
    )
    await user.type(
      screen.getByRole('textbox', { name: '附件 1 链接' }),
      'https://example.com/n2'
    )
    await user.click(screen.getByRole('button', { name: '保存任务' }))

    await waitFor(() =>
      expect(updateTask).toHaveBeenCalledWith({
        projectID: 'project-1',
        taskID: 'task-1',
        input: {
          title: '复习 N2 核心语法',
          description: '完成错题整理',
          attachment_links: [
            { name: '复习资料', url: 'https://example.com/n2' },
          ],
          expected_task_revision: 4,
          expected_schedule_revision: 2,
        },
      })
    )
  })

  it('lists notes linked through tasks in the project notes section', async () => {
    vi.mocked(taskHooks.useTaskDefinitions).mockReturnValue({
      data: [{ ...taskDefinition, task_note_id: 'note-1' }],
      isLoading: false,
    } as ReturnType<typeof taskHooks.useTaskDefinitions>)
    vi.mocked(notesApi.getNote).mockResolvedValue({
      id: 'note-1',
      title: 'N2 复习记录',
      body: '',
      folder_id: '__uncategorized',
      tags: '[]',
      projects: [],
      created_at: 1,
      updated_at: 2,
    })
    renderDetail()
    const user = userEvent.setup()

    await user.click(screen.getByRole('tab', { name: '笔记' }))

    expect(await screen.findByText('N2 复习记录')).toBeVisible()
    expect(screen.getByRole('link', { name: /N2 复习记录/ })).toHaveAttribute(
      'href',
      '/editor/note-1'
    )
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

function idleMutation() {
  return { mutateAsync: vi.fn(), isPending: false } as unknown
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
