import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as hooks from '../hooks/useRoadmapV2'
import * as taskHooks from '../hooks/useTaskDomain'
import RoadmapV2 from './RoadmapV2'

vi.mock('../hooks/useRoadmapV2')
vi.mock('../hooks/useTaskDomain')

const roadmapModel = {
  id: 'r1',
  project_id: 'p1',
  title: 'Path',
  description: '',
  status: 'active',
  revision: 1,
  nodes: [
    {
      id: 'n1',
      project_id: 'p1',
      roadmap_id: 'r1',
      title: 'Basics',
      description: '',
      node_type: 'topic',
      position: 0,
      revision: 2,
      progress: {
        tasks: 2,
        total: 3,
        open: 1,
        active: 0,
        blocked: 1,
        done: 1,
        skipped: 0,
        cancelled: 0,
      },
    },
  ],
  edges: [],
} as const

function renderRoute() {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter initialEntries={['/projects/p1/roadmap']}>
        <Routes>
          <Route path="/projects/:projectID/roadmap" element={<RoadmapV2 />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  )
}

describe('RoadmapV2', () => {
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
    vi.mocked(taskHooks.useCreateTaskMutation).mockReturnValue({
      mutateAsync: vi.fn(),
      isPending: false,
    } as never)
    vi.mocked(hooks.useRoadmapV2).mockReturnValue({
      data: roadmapModel,
      isLoading: false,
      isError: false,
    } as never)
    vi.mocked(hooks.useCreateRoadmapNodeMutation).mockReturnValue({
      mutateAsync: vi.fn(),
      isPending: false,
    } as never)
    vi.mocked(hooks.useUpdateRoadmapNodeMutation).mockReturnValue({
      mutateAsync: vi.fn(),
      isPending: false,
    } as never)
    vi.mocked(hooks.useDeleteRoadmapNodeMutation).mockReturnValue({
      mutateAsync: vi.fn(),
      isPending: false,
    } as never)
    vi.mocked(hooks.useCreateRoadmapMutation).mockReturnValue({
      mutateAsync: vi.fn(),
      mutate: vi.fn(),
      isPending: false,
    } as never)
    vi.mocked(hooks.useGenerateRoadmapMutation).mockReturnValue({
      mutateAsync: vi.fn(),
      isPending: false,
    } as never)
  })

  it('shows occurrence-derived progress without a node completion checkbox and creates multiple tasks under a node', async () => {
    const user = userEvent.setup()
    renderRoute()

    expect(screen.getAllByText('33%')).toHaveLength(2)
    expect(screen.getByText('阻塞 1')).toBeInTheDocument()
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '添加任务' }))
    expect(
      screen.getByRole('button', { name: '创建关联任务' })
    ).toBeInTheDocument()
  })

  it('generates a complete roadmap from the empty state', async () => {
    const generate = vi.fn().mockResolvedValue({})
    vi.mocked(hooks.useRoadmapV2).mockReturnValue({
      data: null,
      isLoading: false,
      isError: false,
    } as never)
    vi.mocked(hooks.useGenerateRoadmapMutation).mockReturnValue({
      mutateAsync: generate,
      isPending: false,
    } as never)
    const user = userEvent.setup()

    renderRoute()

    await user.type(
      screen.getByRole('textbox', { name: '补充生成要求' }),
      '更重视可验证的实战产出'
    )
    await user.click(
      screen.getByRole('button', { name: '生成学习 Roadmap' })
    )

    expect(generate).toHaveBeenCalledWith({
      prompt: '更重视可验证的实战产出',
    })
  })

  it('regenerates an existing route when it has no linked tasks', async () => {
    const generate = vi.fn().mockResolvedValue({})
    vi.mocked(hooks.useRoadmapV2).mockReturnValue({
      data: {
        ...roadmapModel,
        nodes: [
          {
            ...roadmapModel.nodes[0],
            progress: {
              ...roadmapModel.nodes[0].progress,
              tasks: 0,
              total: 0,
              open: 0,
              blocked: 0,
              done: 0,
            },
          },
        ],
      },
      isLoading: false,
      isError: false,
    } as never)
    vi.mocked(hooks.useGenerateRoadmapMutation).mockReturnValue({
      mutateAsync: generate,
      isPending: false,
    } as never)
    const user = userEvent.setup()

    renderRoute()

    await user.click(
      screen.getByRole('button', { name: '重新生成路线' })
    )
    await user.type(
      screen.getByRole('textbox', { name: '补充生成要求' }),
      '每个阶段都要有作品'
    )
    await user.click(
      screen.getAllByRole('button', { name: '重新生成路线' })[1]
    )

    expect(generate).toHaveBeenCalledWith({
      prompt: '每个阶段都要有作品',
    })
  })

  it('protects an existing route that already has linked tasks', () => {
    renderRoute()

    expect(
      screen.getByRole('button', { name: '重新生成路线' })
    ).toBeDisabled()
  })
})
