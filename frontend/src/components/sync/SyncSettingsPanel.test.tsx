import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { APIError } from '../../api/client'
import type { SyncTarget } from '../../api/sync'
import * as syncApi from '../../api/sync'
import { SyncSettingsPanel } from './SyncSettingsPanel'

vi.mock('../../api/sync')

vi.mock('./ObsidianSyncPanel', () => ({
  ObsidianSyncPanel: () => <div>Obsidian 面板</div>,
}))

vi.mock('./NotionSyncPanel', () => ({
  NotionSyncPanel: () => <div>Notion 面板</div>,
}))

const targets: SyncTarget[] = [
  {
    id: 'obs-1',
    type: 'obsidian',
    name: 'Work Vault',
    vault_path: 'D:\\Vault',
    base_folder: 'FlowSpace',
    config_json: '{}',
    enabled: true,
    auto_sync: false,
    is_default: true,
    created_at: 1,
    updated_at: 1,
  },
  {
    id: 'notion-1',
    type: 'notion',
    name: 'Personal Notion',
    vault_path: '',
    base_folder: '',
    config_json: '{"data_source_id":"ds-1"}',
    enabled: true,
    auto_sync: false,
    is_default: false,
    created_at: 1,
    updated_at: 1,
  },
]

function renderPanel() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })

  return render(<SyncSettingsPanel onClose={vi.fn()} />, {
    wrapper: ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    ),
  })
}

describe('SyncSettingsPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue(targets)
    vi.mocked(syncApi.saveSyncTarget).mockImplementation(async (input) => ({
      ...targets.find((target) => target.id === input.id)!,
      ...input,
      id: input.id ?? 'new-target',
      type: input.type ?? 'obsidian',
      config_json: input.config_json ?? '{}',
      is_default: input.is_default ?? false,
      created_at: 1,
      updated_at: 2,
    }))
    vi.mocked(syncApi.deleteSyncTarget).mockResolvedValue(undefined)
  })

  it('uses Chinese labels for the sync settings modal', async () => {
    const onClose = vi.fn()
    const user = userEvent.setup()

    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<SyncSettingsPanel onClose={onClose} />, {
      wrapper: ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
      ),
    })

    expect(screen.getByText('同步')).toBeVisible()
    expect(screen.getByRole('heading', { name: '同步设置' })).toBeVisible()
    expect(screen.getByRole('tablist', { name: '同步目标类型' })).toBeVisible()
    expect(screen.getByRole('tabpanel', { name: 'Obsidian 同步设置' })).toBeVisible()

    await user.click(screen.getByRole('tab', { name: 'Notion' }))
    expect(screen.getByRole('tabpanel', { name: 'Notion 同步设置' })).toBeVisible()

    await user.click(screen.getByRole('button', { name: '关闭同步设置面板' }))
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('marks one default target per type', async () => {
    renderPanel()

    expect(await screen.findByText('Work Vault')).toBeVisible()
    expect(screen.getByText('默认')).toBeVisible()

    await userEvent.click(screen.getByRole('button', { name: '设为默认 Personal Notion' }))

    await waitFor(() => expect(syncApi.saveSyncTarget).toHaveBeenCalledTimes(1))
    expect(vi.mocked(syncApi.saveSyncTarget).mock.calls[0][0]).toEqual(
      expect.objectContaining({
        id: 'notion-1',
        type: 'notion',
        is_default: true,
      }),
    )
  })

  it('shows target identity locked error', async () => {
    vi.mocked(syncApi.saveSyncTarget).mockRejectedValueOnce(new APIError(409, 'target_identity_locked', 'locked'))
    renderPanel()

    await userEvent.click(await screen.findByRole('button', { name: '设为默认 Personal Notion' }))

    expect(await screen.findByText('同步目标已被使用，不能修改外部身份字段')).toBeVisible()
  })

  it('deletes unused target', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValueOnce(true)
    renderPanel()

    await userEvent.click(await screen.findByRole('button', { name: '删除同步目标 Personal Notion' }))

    await waitFor(() => expect(syncApi.deleteSyncTarget).toHaveBeenCalledWith('notion-1'))
    confirmSpy.mockRestore()
  })
})
