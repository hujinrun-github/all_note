import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import RoadmapV2 from './RoadmapV2'
import * as hooks from '../hooks/useRoadmapV2'
import * as taskHooks from '../hooks/useTaskDomain'

vi.mock('../hooks/useRoadmapV2')
vi.mock('../hooks/useTaskDomain')
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
      data: {
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
      },
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
      isPending: false,
    } as never)
  })
  it('shows occurrence-derived progress without a node completion checkbox and creates multiple tasks under a node', async () => {
    const user = userEvent.setup()
    render(
      <QueryClientProvider client={new QueryClient()}>
        <MemoryRouter initialEntries={['/projects/p1/roadmap']}>
          <Routes>
            <Route
              path="/projects/:projectID/roadmap"
              element={<RoadmapV2 />}
            />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>
    )
    expect(screen.getByText('完成 1 / 3')).toBeInTheDocument()
    expect(screen.getByText('阻塞 1')).toBeInTheDocument()
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '在此节点添加任务' }))
    expect(
      screen.getByRole('button', { name: '创建关联任务' })
    ).toBeInTheDocument()
  })
})
