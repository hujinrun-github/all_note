import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  createJapanesePairingCode,
  getJapaneseIntegration,
  saveJapaneseBinding,
} from '../api/japaneseIntegration'
import JapaneseIntegrationSettings from './JapaneseIntegrationSettings'

vi.mock('../api/japaneseIntegration', () => ({
  getJapaneseIntegration: vi.fn(),
  createJapanesePairingCode: vi.fn(),
  saveJapaneseBinding: vi.fn(),
  revokeJapaneseConnection: vi.fn(),
  retryJapaneseEvent: vi.fn(),
}))

function renderPanel() {
  return render(
    <QueryClientProvider
      client={
        new QueryClient({
          defaultOptions: {
            queries: { retry: false },
            mutations: { retry: false },
          },
        })
      }
    >
      <JapaneseIntegrationSettings />
    </QueryClientProvider>
  )
}

const status = {
  connections: [
    {
      connection_id: 'c1',
      workspace_id: 'w1',
      actor_id: 'u1',
      source_instance_id: 'japanese-test',
      source_user_id: '1',
      source_user_name: '日语用户',
      key_id: 'v1',
      state: 'active',
      created_at: '',
    },
  ],
  catalogs: [
    {
      connection_id: 'c1',
      catalog: {
        schema_version: 1,
        source_instance_id: 'japanese-test',
        source_user_id: '1',
        catalog_revision: 1,
        plan: {
          plan_id: 'p1',
          revision: 1,
          title: 'N2 学习',
          timezone: 'Asia/Shanghai',
          start_date: '2026-11-19',
          end_date: '2027-06-30',
          weekdays: [1, 2, 3, 4, 5],
          time_slots: [
            {
              id: 'evening',
              title: '傍晚',
              time: '18:40',
              duration_minutes: 20,
              tasks: [
                {
                  id: 'listening',
                  title: '听力训练',
                  path: '/listening',
                  minutes: 20,
                },
              ],
            },
          ],
        },
      },
    },
  ],
  targets: [
    {
      task_id: 't1',
      title: '晚间听力练习',
      timezone: 'Asia/Shanghai',
      recurrence_type: 'weekly',
    },
  ],
  bindings: [],
  events: [],
}

describe('JapaneseIntegrationSettings', () => {
  beforeEach(() => {
    vi.mocked(getJapaneseIntegration).mockResolvedValue(structuredClone(status))
    vi.mocked(createJapanesePairingCode).mockResolvedValue({
      pairing_code: 'one-time-code',
      expires_at: '2026-11-19T12:00:00Z',
    })
    vi.mocked(saveJapaneseBinding).mockResolvedValue({
      id: 'b1',
      connection_id: 'c1',
      plan_id: 'p1',
      slot_id: 'evening',
      task_id: 'listening',
      target_task_id: 't1',
      target_title: '晚间听力练习',
      binding_version: 1,
      effective_from: '2026-11-19',
      schedule_revision: 1,
      source_fingerprint: 'fp',
      enabled: true,
    })
  })

  it('creates a one-time pairing code without exposing connection secrets', async () => {
    const user = userEvent.setup()
    renderPanel()
    await user.click(screen.getByRole('button', { name: '生成配对码' }))
    expect(await screen.findByText('one-time-code')).toBeVisible()
    expect(screen.getByText(/配对码仅用于连接/)).toBeVisible()
  })

  it('previews and saves explicit source to target binding', async () => {
    const user = userEvent.setup()
    renderPanel()
    await screen.findByRole('option', { name: /听力训练/ })
    await user.selectOptions(
      screen.getByLabelText('日语学习任务'),
      JSON.stringify(['c1', 'p1', 'evening', 'listening'])
    )
    await user.selectOptions(screen.getByLabelText('FlowSpace 重复任务'), 't1')
    expect(screen.getByText(/只完成相同学习日期的执行实例/)).toBeVisible()
    await user.click(screen.getByRole('button', { name: '保存关联' }))
    await waitFor(() =>
      expect(saveJapaneseBinding).toHaveBeenCalledWith({
        connection_id: 'c1',
        plan_id: 'p1',
        slot_id: 'evening',
        task_id: 'listening',
        target_task_id: 't1',
      })
    )
    expect(await screen.findByText(/关联已保存/)).toBeVisible()
  })

  it('shows a failure instead of an empty success state', async () => {
    vi.mocked(getJapaneseIntegration).mockRejectedValue(
      new Error('工作区暂不可用')
    )
    renderPanel()
    expect(await screen.findByRole('alert')).toHaveTextContent('工作区暂不可用')
  })

  it('handles a connected legacy plan without time slots', async () => {
    const legacy = structuredClone(status)
    Object.assign(legacy.catalogs[0].catalog.plan, { time_slots: null })
    vi.mocked(getJapaneseIntegration).mockResolvedValue(legacy)
    renderPanel()
    expect(await screen.findByText(/保存带时段任务的计划/)).toBeVisible()
    expect(screen.getByRole('button', { name: '保存关联' })).toBeDisabled()
  })
})
