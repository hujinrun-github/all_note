import { describe, expect, it, vi } from 'vitest'
import { api } from './client'
import { getCalendarProjectSources, saveCalendarProjectSources } from './calendar'

vi.mock('./client', () => ({
  api: {
    get: vi.fn(),
    put: vi.fn(),
  },
}))

describe('calendar project source api', () => {
  it('loads project sources from the backend calendar endpoint', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: {
        sources: [
          {
            project_id: 'personal',
            name: 'Personal',
            type: 'personal',
            enabled: true,
            default: true,
            color: '#c4742f',
            order_index: 0,
          },
        ],
        available_projects: [],
      },
    })

    const result = await getCalendarProjectSources()

    expect(api.get).toHaveBeenCalledWith('/api/calendar/project-sources')
    expect(result.sources[0]?.project_id).toBe('personal')
  })

  it('saves all configurable source states', async () => {
    const response = {
      sources: [],
      available_projects: [
        {
          project_id: 'learning-1',
          name: 'N2',
          type: 'learning',
          enabled: false,
          default: false,
          color: '',
          order_index: 10,
        },
      ],
    }
    vi.mocked(api.put).mockResolvedValue({ data: response })

    const result = await saveCalendarProjectSources({
      sources: [
        {
          project_id: 'learning-1',
          enabled: false,
          color: '',
          order_index: 10,
        },
      ],
    })

    expect(api.put).toHaveBeenCalledWith('/api/calendar/project-sources', {
      sources: [
        {
          project_id: 'learning-1',
          enabled: false,
          color: '',
          order_index: 10,
        },
      ],
    })
    expect(result).toBe(response)
  })
})
