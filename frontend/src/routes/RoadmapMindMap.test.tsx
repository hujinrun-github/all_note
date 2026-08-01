import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('@xyflow/react', async () => {
  const React = await import('react')

  interface TestNode {
    id: string
    type?: string
    selected?: boolean
    data: Record<string, unknown>
  }

  interface TestFlowProps {
    nodes: TestNode[]
    nodeTypes: Record<string, React.ComponentType<Record<string, unknown>>>
    onNodeClick?: (event: React.MouseEvent, node: TestNode) => void
    onNodeContextMenu?: (event: React.MouseEvent, node: TestNode) => void
    children?: React.ReactNode
  }

  return {
    Background: () => null,
    Handle: () => null,
    MiniMap: () => null,
    Position: { Left: 'left', Right: 'right' },
    ReactFlow: ({
      nodes,
      nodeTypes,
      onNodeClick,
      onNodeContextMenu,
      children,
    }: TestFlowProps) => (
      <div role="application">
        {nodes.map((node) => {
          const NodeView = nodeTypes[node.type ?? '']
          return NodeView ? (
            <div
              key={node.id}
              onClick={(event) => onNodeClick?.(event, node)}
              onContextMenu={(event) => onNodeContextMenu?.(event, node)}
            >
              <NodeView {...node} />
            </div>
          ) : null
        })}
        {children}
      </div>
    ),
    useEdgesState: (initial: unknown[]) => {
      const [edges, setEdges] = React.useState(initial)
      return [edges, setEdges, () => undefined]
    },
    useNodesState: (initial: TestNode[]) => {
      const [nodes, setNodes] = React.useState(initial)
      return [nodes, setNodes, () => undefined]
    },
  }
})

import * as roadmapHooks from '../hooks/useRoadmapV2'
import * as taskHooks from '../hooks/useTaskDomain'
import RoadmapMindMap from './RoadmapMindMap'

vi.mock('../hooks/useRoadmapV2')
vi.mock('../hooks/useTaskDomain')

const roadmap = {
  id: 'r1',
  project_id: 'p1',
  title: 'Backend Path',
  description: '',
  status: 'active',
  revision: 1,
  nodes: [
    {
      id: 'n1',
      project_id: 'p1',
      roadmap_id: 'r1',
      title: 'Concurrency',
      description: '',
      node_type: 'topic',
      position: 0,
      revision: 1,
      progress: {
        tasks: 1,
        total: 2,
        open: 1,
        active: 1,
        blocked: 0,
        done: 1,
        skipped: 0,
        cancelled: 0,
      },
    },
  ],
  edges: [],
} as const

const task = {
  id: 't1',
  project_id: 'p1',
  roadmap_node_id: 'n1',
  title: 'Concurrency model basics',
  description: 'Compare worker pools and channels.',
  priority: 2,
  sort_order: 0,
  lifecycle_status: 'active',
  revision: 1,
  schedule_revision: 1,
  attachment_links: [
    { name: 'Go memory model', url: 'https://go.dev/ref/mem' },
  ],
  completion_requirements: [
    {
      id: 'article-1',
      kind: 'article',
      title: 'Read Go memory model',
      url: 'https://go.dev/ref/mem',
      completed: true,
    },
    {
      id: 'video-1',
      kind: 'video',
      title: 'Watch worker pool video',
      completed: false,
    },
  ],
} as const

function renderRoute() {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter initialEntries={['/projects/p1/roadmap/nodes/n1/mind-map']}>
        <Routes>
          <Route
            path="/projects/:projectID/roadmap/nodes/:roadmapNodeID/mind-map"
            element={<RoadmapMindMap />}
          />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  )
}

describe('RoadmapMindMap', () => {
  beforeEach(() => {
    vi.mocked(taskHooks.useProject).mockReturnValue({
      data: {
        id: 'p1',
        name: 'Learning',
        kind: 'learning',
        horizon: 'long',
        status: 'active',
        revision: 1,
      },
      isLoading: false,
      isError: false,
    } as never)
    vi.mocked(roadmapHooks.useRoadmapV2).mockReturnValue({
      data: roadmap,
      isLoading: false,
      isError: false,
    } as never)
    vi.mocked(taskHooks.useTaskDefinitions).mockReturnValue({
      data: [task],
      isLoading: false,
      isError: false,
    } as never)
    vi.mocked(taskHooks.useOccurrences).mockReturnValue({
      data: [
        {
          id: 'o1',
          task_id: 't1',
          project_id: 'p1',
          occurrence_key: '2026-07-30',
          execution_status: 'active',
          revision: 1,
          generated_schedule_revision: 1,
          planned_date: '2026-07-30',
        },
      ],
      isLoading: false,
      isError: false,
    } as never)
    vi.mocked(taskHooks.useCreateTaskMutation).mockReturnValue({
      mutateAsync: vi.fn().mockResolvedValue({
        task,
        occurrences: [],
      }),
      isPending: false,
    } as never)
    vi.mocked(taskHooks.useUpdateTaskDefinitionMutation).mockReturnValue({
      mutateAsync: vi.fn().mockResolvedValue(task),
      isPending: false,
    } as never)
    vi.mocked(taskHooks.useCancelTaskMutation).mockReturnValue({
      mutateAsync: vi.fn().mockResolvedValue({}),
      isPending: false,
    } as never)
    vi.mocked(taskHooks.useArchiveTaskMutation).mockReturnValue(
      idleMutation() as never
    )
    vi.mocked(taskHooks.useDeleteTaskMutation).mockReturnValue(
      idleMutation() as never
    )
    vi.mocked(taskHooks.useCompleteOccurrenceMutation).mockReturnValue({
      mutateAsync: vi.fn().mockResolvedValue({}),
      isPending: false,
    } as never)
    vi.mocked(taskHooks.useStartOccurrenceMutation).mockReturnValue(
      idleMutation() as never
    )
    vi.mocked(taskHooks.useBlockOccurrenceMutation).mockReturnValue(
      idleMutation() as never
    )
    vi.mocked(taskHooks.useUnblockOccurrenceMutation).mockReturnValue(
      idleMutation() as never
    )
    vi.mocked(taskHooks.useSkipOccurrenceMutation).mockReturnValue(
      idleMutation() as never
    )
    vi.mocked(taskHooks.useCancelOccurrenceMutation).mockReturnValue(
      idleMutation() as never
    )
    vi.mocked(taskHooks.useReopenOccurrenceMutation).mockReturnValue(
      idleMutation() as never
    )
    vi.mocked(taskHooks.useRescheduleOccurrenceMutation).mockReturnValue(
      idleMutation() as never
    )
  })

  it('opens task details only after selecting a node', async () => {
    const user = userEvent.setup()
    renderRoute()

    expect(
      screen.getByRole('heading', { name: 'Concurrency' })
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('textbox', { name: '任务标题' })
    ).not.toBeInTheDocument()

    await user.click(screen.getByText('Concurrency model basics'))

    expect(screen.getByRole('textbox', { name: '任务标题' })).toHaveValue(
      'Concurrency model basics'
    )
    expect(screen.getByText('Go memory model')).toBeInTheDocument()
    expect(screen.getByText('完成门槛')).toBeInTheDocument()
    expect(screen.getByText('1 / 2')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '还需完成 1 项' })).toBeDisabled()
  })

  it('persists required-item progress before unlocking completion', async () => {
    const update = vi
      .fn()
      .mockImplementation(
        async ({ input }: taskHooks.UpdateTaskDefinitionVariables) => ({
          ...task,
          revision: 2,
          completion_requirements: input.completion_requirements,
        })
      )
    vi.mocked(taskHooks.useUpdateTaskDefinitionMutation).mockReturnValue({
      mutateAsync: update,
      isPending: false,
    } as never)
    const user = userEvent.setup()
    renderRoute()

    await user.click(screen.getByText('Concurrency model basics'))
    await user.click(
      screen.getByRole('button', {
        name: '标记完成：Watch worker pool video',
      })
    )

    expect(update).toHaveBeenCalledWith({
      projectID: 'p1',
      taskID: 't1',
      input: {
        expected_task_revision: 1,
        expected_schedule_revision: 1,
        completion_requirements: [
          expect.objectContaining({ id: 'article-1', completed: true }),
          expect.objectContaining({ id: 'video-1', completed: true }),
        ],
      },
    })
    expect(screen.getByRole('button', { name: '完成任务' })).toBeEnabled()
  })

  it('adds and persists attachment links from the mind-map inspector', async () => {
    const update = vi.fn().mockResolvedValue({
      ...task,
      revision: 2,
      attachment_links: [
        ...task.attachment_links,
        { name: 'Worker pool guide', url: 'https://example.com/worker-pool' },
      ],
    })
    vi.mocked(taskHooks.useUpdateTaskDefinitionMutation).mockReturnValue({
      mutateAsync: update,
      isPending: false,
    } as never)
    const user = userEvent.setup()
    renderRoute()

    await user.click(screen.getByText('Concurrency model basics'))
    await user.click(screen.getByRole('button', { name: '添加附件链接' }))

    expect(screen.getByRole('textbox', { name: '附件 1 名称' })).toHaveValue(
      'Go memory model'
    )
    await user.type(
      screen.getByRole('textbox', { name: '附件 2 名称' }),
      'Worker pool guide'
    )
    await user.type(
      screen.getByRole('textbox', { name: '附件 2 链接' }),
      'https://example.com/worker-pool'
    )
    await user.click(screen.getByRole('button', { name: '保存修改' }))

    expect(update).toHaveBeenCalledWith({
      projectID: 'p1',
      taskID: 't1',
      input: {
        title: 'Concurrency model basics',
        description: 'Compare worker pools and channels.',
        priority: 2,
        attachment_links: [
          { name: 'Go memory model', url: 'https://go.dev/ref/mem' },
          {
            name: 'Worker pool guide',
            url: 'https://example.com/worker-pool',
          },
        ],
        expected_task_revision: 1,
        expected_schedule_revision: 1,
      },
    })
  })

  it('keeps invalid attachment links in the editor with a visible error', async () => {
    const update = vi.fn().mockResolvedValue(task)
    vi.mocked(taskHooks.useUpdateTaskDefinitionMutation).mockReturnValue({
      mutateAsync: update,
      isPending: false,
    } as never)
    const user = userEvent.setup()
    renderRoute()

    await user.click(screen.getByText('Concurrency model basics'))
    await user.click(screen.getByRole('button', { name: '添加附件链接' }))
    await user.type(
      screen.getByRole('textbox', { name: '附件 2 名称' }),
      'Local file'
    )
    await user.type(
      screen.getByRole('textbox', { name: '附件 2 链接' }),
      'file:///tmp/guide.pdf'
    )
    await user.click(screen.getByRole('button', { name: '保存修改' }))

    expect(screen.getByRole('alert')).toHaveTextContent(
      '请填写资料名称和有效的 http(s) 链接。'
    )
    expect(update).not.toHaveBeenCalled()
  })

  it('starts an open occurrence from the execution status control', async () => {
    const start = vi.fn().mockResolvedValue({})
    vi.mocked(taskHooks.useOccurrences).mockReturnValue({
      data: [
        {
          id: 'o1',
          task_id: 't1',
          project_id: 'p1',
          occurrence_key: '2026-07-30',
          execution_status: 'open',
          revision: 3,
          generated_schedule_revision: 1,
          planned_date: '2026-07-30',
          task_revision: 4,
          schedule_revision: 2,
        },
      ],
      isLoading: false,
      isError: false,
    } as never)
    vi.mocked(taskHooks.useStartOccurrenceMutation).mockReturnValue({
      mutateAsync: start,
      isPending: false,
    } as never)
    const user = userEvent.setup()
    renderRoute()

    await user.click(screen.getByText('Concurrency model basics'))
    await user.selectOptions(
      screen.getByRole('combobox', { name: '执行状态' }),
      'active'
    )

    expect(start).toHaveBeenCalledWith({
      projectID: 'p1',
      taskID: 't1',
      occurrenceID: 'o1',
      expectedRevisions: {
        expected_task_revision: 4,
        expected_schedule_revision: 2,
        expected_occurrence_revisions: { o1: 3 },
      },
    })
  })

  it('schedules a learning task time block from the mind-map inspector', async () => {
    const reschedule = vi.fn().mockResolvedValue({})
    vi.mocked(taskHooks.useOccurrences).mockReturnValue({
      data: [
        {
          id: 'o1',
          task_id: 't1',
          project_id: 'p1',
          occurrence_key: 'unscheduled',
          execution_status: 'open',
          revision: 3,
          generated_schedule_revision: 2,
          timing_type: 'unscheduled',
          timezone: 'Asia/Shanghai',
          task_revision: 4,
          schedule_revision: 2,
        },
      ],
      isLoading: false,
      isError: false,
    } as never)
    vi.mocked(taskHooks.useRescheduleOccurrenceMutation).mockReturnValue({
      mutateAsync: reschedule,
      isPending: false,
    } as never)
    const user = userEvent.setup()
    renderRoute()

    await user.click(screen.getByText('Concurrency model basics'))
    await user.click(screen.getByRole('button', { name: '安排时间' }))
    await user.selectOptions(
      screen.getByLabelText('学习任务安排方式'),
      'time_block'
    )
    await user.clear(screen.getByLabelText('学习任务执行日期'))
    await user.type(screen.getByLabelText('学习任务执行日期'), '2026-08-03')
    await user.clear(screen.getByLabelText('学习任务开始时间'))
    await user.type(screen.getByLabelText('学习任务开始时间'), '14:30')
    await user.clear(screen.getByLabelText('学习任务结束时间'))
    await user.type(screen.getByLabelText('学习任务结束时间'), '16:00')
    await user.click(screen.getByRole('button', { name: '保存并同步日历' }))

    expect(reschedule).toHaveBeenCalledWith({
      projectID: 'p1',
      taskID: 't1',
      occurrenceID: 'o1',
      input: {
        expected_task_revision: 4,
        expected_schedule_revision: 2,
        expected_occurrence_revision: 3,
        timing: {
          timing_type: 'time_block',
          timezone: 'Asia/Shanghai',
          planned_date: '2026-08-03',
          local_start_time: '14:30',
          duration_minutes: 90,
        },
      },
    })
    expect(screen.getByText('已保存，并同步到日历')).toBeInTheDocument()
  })

  it('collects blocking details and keeps completion locked by requirements', async () => {
    const block = vi.fn().mockResolvedValue({})
    vi.mocked(taskHooks.useBlockOccurrenceMutation).mockReturnValue({
      mutateAsync: block,
      isPending: false,
    } as never)
    const user = userEvent.setup()
    renderRoute()

    await user.click(screen.getByText('Concurrency model basics'))
    const status = screen.getByRole('combobox', { name: '执行状态' })
    expect(
      screen.getByRole('option', { name: '已完成（还差 1 项）' })
    ).toBeDisabled()

    await user.selectOptions(status, 'blocked')
    await user.type(screen.getByLabelText('阻塞原因'), '等待评审')
    await user.type(screen.getByLabelText('阻塞后的下一步'), '明天提醒评审人')
    await user.click(screen.getByRole('button', { name: '确认阻塞' }))

    expect(block).toHaveBeenCalledWith({
      projectID: 'p1',
      taskID: 't1',
      occurrenceID: 'o1',
      expectedRevisions: {
        expected_task_revision: 1,
        expected_schedule_revision: 1,
        expected_occurrence_revisions: { o1: 1 },
      },
      blockedReason: '等待评审',
      nextAction: '明天提醒评审人',
    })
  })

  it('creates a task directly under the current roadmap node', async () => {
    const create = vi.fn().mockResolvedValue({
      task: { ...task, id: 't2', title: 'Channel lab' },
      occurrences: [],
    })
    vi.mocked(taskHooks.useCreateTaskMutation).mockReturnValue({
      mutateAsync: create,
      isPending: false,
    } as never)
    const user = userEvent.setup()
    renderRoute()

    await user.click(screen.getByRole('button', { name: '添加任务' }))
    await user.type(
      screen.getByRole('textbox', { name: '新任务标题' }),
      'Channel lab'
    )
    await user.keyboard('{Enter}')

    expect(create).toHaveBeenCalledWith(
      expect.objectContaining({
        project_id: 'p1',
        roadmap_node_id: 'n1',
        title: 'Channel lab',
      })
    )
  })

  it('dismisses inline task creation with Escape', async () => {
    const user = userEvent.setup()
    renderRoute()

    await user.click(screen.getByRole('button', { name: '添加任务' }))
    await user.type(
      screen.getByRole('textbox', { name: '新任务标题' }),
      'Draft'
    )
    await user.keyboard('{Escape}')

    expect(
      screen.queryByRole('textbox', { name: '新任务标题' })
    ).not.toBeInTheDocument()
  })

  it('renames the selected task with the Space shortcut', async () => {
    const rename = vi.fn().mockResolvedValue(task)
    vi.mocked(taskHooks.useUpdateTaskDefinitionMutation).mockReturnValue({
      mutateAsync: rename,
      isPending: false,
    } as never)
    const user = userEvent.setup()
    renderRoute()

    await user.click(screen.getByText('Concurrency model basics'))
    await user.keyboard(' ')
    const input = screen.getByRole('textbox', { name: '编辑任务标题' })
    await user.clear(input)
    await user.type(input, 'Concurrency patterns{Enter}')

    expect(rename).toHaveBeenCalledWith(
      expect.objectContaining({
        projectID: 'p1',
        taskID: 't1',
        input: expect.objectContaining({ title: 'Concurrency patterns' }),
      })
    )
  })
})

function idleMutation() {
  return { mutateAsync: vi.fn().mockResolvedValue({}), isPending: false }
}
