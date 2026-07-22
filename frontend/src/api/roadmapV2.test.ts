import { beforeEach, describe, expect, it, vi } from 'vitest'
import { deleteRoadmapNode, getRoadmapV2 } from './roadmapV2'

describe('roadmap v2 api', () => {
  beforeEach(() => vi.restoreAllMocks())
  it('reads derived progress and sends expected revision on delete', async () => {
    const fetchMock = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            data: {
              roadmap: {
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
                    title: 'Node',
                    description: '',
                    node_type: 'topic',
                    position: 0,
                    revision: 3,
                    progress: {
                      tasks: 2,
                      total: 2,
                      open: 1,
                      active: 0,
                      blocked: 1,
                      done: 0,
                      skipped: 0,
                      cancelled: 0,
                    },
                  },
                ],
                edges: [],
              },
            },
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } }
        )
      )
    const roadmap = await getRoadmapV2('p1')
    expect(roadmap?.nodes[0].progress.blocked).toBe(1)
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }))
    await deleteRoadmapNode('r1', 'n1', 3)
    expect(fetchMock.mock.calls[1][0].toString()).toContain(
      'expected_revision=3'
    )
  })
})
