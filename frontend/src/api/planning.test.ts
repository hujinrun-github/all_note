import { afterEach, describe, expect, it, vi } from 'vitest'
import { APIError } from './client'
import {
  commitPlanDraft,
  createPlanDraft,
  newPlanningRequestID,
  updatePlanDraft,
  uploadPlanAudio,
} from './planning'

afterEach(() => vi.restoreAllMocks())
describe('planning Web API contract', () => {
  it('uploads real MIME audio bytes and sends revision in the query with browser credentials', async () => {
    const fetchMock = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValue(
        new Response(
          JSON.stringify({ data: { draft: { id: 'draft-1', revision: 3 } } })
        )
      )
    const audio = new Blob(['webm-audio'], { type: 'audio/webm;codecs=opus' })
    await expect(uploadPlanAudio('draft-1', 2, audio)).resolves.toMatchObject({
      id: 'draft-1',
      revision: 3,
    })
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/plan-drafts/draft-1/audio?expected_revision=2',
      expect.objectContaining({
        method: 'PUT',
        credentials: 'include',
        headers: { 'Content-Type': 'audio/webm;codecs=opus' },
        body: audio,
      })
    )
  })
  it('keeps explicit project creation and idempotent request identity in the commit payload', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({
          data: {
            receipt: {
              draft_id: 'd1',
              project_id: 'p1',
              task_ids: ['t1'],
              occurrence_ids: [],
            },
          },
        })
      )
    )
    const input = {
      expected_revision: 4,
      request_id: 'request-commit',
      publish: false,
      allow_conflicts: false,
      new_project: {
        name: '学习',
        kind: 'learning' as const,
        horizon: 'short' as const,
      },
    }
    await expect(commitPlanDraft('d1', input)).resolves.toMatchObject({
      task_ids: ['t1'],
    })
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/plan-drafts/d1/commit',
      expect.objectContaining({ method: 'POST', body: JSON.stringify(input) })
    )
  })
  it('preserves revision conflicts as API errors instead of treating them as success', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({
          error: { code: 'planning_conflict', message: 'draft changed' },
        }),
        { status: 409 }
      )
    )
    await expect(
      updatePlanDraft('d1', { expected_revision: 2, input_text: 'updated' })
    ).rejects.toBeInstanceOf(APIError)
  })
  it('creates text drafts with explicit time context and request identity', async () => {
    const fetchMock = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValue(
        new Response(JSON.stringify({ data: { draft: { id: 'd1' } } }))
      )
    const input = {
      request_id: 'same-request',
      input_text: '明天完成',
      timezone: 'Asia/Shanghai',
      project_id: 'p1',
    }
    await createPlanDraft(input)
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/plan-drafts',
      expect.objectContaining({ body: JSON.stringify(input) })
    )
  })
  it('generates secure UUIDv4 request IDs when randomUUID is unavailable on HTTP pages', () => {
    const original = Object.getOwnPropertyDescriptor(crypto, 'randomUUID')
    Object.defineProperty(crypto, 'randomUUID', {
      configurable: true,
      value: undefined,
    })
    try {
      const secureRandom = vi.spyOn(crypto, 'getRandomValues')
      const id = newPlanningRequestID()
      expect(id).toMatch(
        /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/
      )
      expect(newPlanningRequestID()).not.toBe(id)
      expect(secureRandom).toHaveBeenCalledTimes(2)
    } finally {
      if (original) Object.defineProperty(crypto, 'randomUUID', original)
      else delete (crypto as Partial<Crypto>).randomUUID
    }
  })
})
