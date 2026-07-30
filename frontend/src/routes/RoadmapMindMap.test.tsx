import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

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
} as const

function renderRoute() {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter
        initialEntries={['/projects/p1/roadmap/nodes/n1/mind-map']}
      >
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
      mutateAsync: vi.fn(),
      isPending: false,
    } as never)
  })

  it('renders current task data and keeps unsupported hierarchy explicit', () => {
    renderRoute()

    expect(
      screen.getByRole('heading', { name: 'Concurrency' })
    ).toBeInTheDocument()
    expect(
      screen.getByRole('textbox', { name: '任务标题' })
    ).toHaveValue('Concurrency model basics')
    expect(screen.getByText('Go memory model')).toBeInTheDocument()
    expect(
      screen.getByText(/当前按路线节点的一层关联任务展示/)
    ).toBeInTheDocument()
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

    await user.click(screen.getByRole('button', { name: '新建任务' }))
    await user.type(screen.getByRole('textbox', { name: '新任务标题' }), 'Channel lab')
    await user.click(screen.getByRole('button', { name: '创建任务' }))

    expect(create).toHaveBeenCalledWith(
      expect.objectContaining({
        project_id: 'p1',
        roadmap_node_id: 'n1',
        title: 'Channel lab',
      })
    )
  })
})
