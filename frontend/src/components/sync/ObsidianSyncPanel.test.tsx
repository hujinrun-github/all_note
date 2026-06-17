import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ObsidianSyncPanel } from './ObsidianSyncPanel'
import * as syncApi from '../../api/sync'

vi.mock('../../api/sync')

function renderPanel() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })

  return render(
    <QueryClientProvider client={queryClient}>
      <ObsidianSyncPanel embedded />
    </QueryClientProvider>
  )
}

describe('ObsidianSyncPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue([])
    vi.mocked(syncApi.getObsidianDeletions).mockResolvedValue([])
    vi.mocked(syncApi.saveSyncTarget).mockResolvedValue({
      id: 'target-1',
      type: 'obsidian',
      name: 'Obsidian Vault',
      vault_path: 'D:\\Vault',
      base_folder: 'FlowSpace Notes',
      config_json: '{}',
      enabled: true,
      auto_sync: false,
      created_at: 1,
      updated_at: 1,
    })
    vi.mocked(syncApi.testObsidianTarget).mockResolvedValue(undefined)
    vi.mocked(syncApi.syncObsidianAll).mockResolvedValue({
      synced: 2,
      failed: 1,
      items: [],
    })
    vi.mocked(syncApi.syncObsidianPull).mockResolvedValue({
      imported: 3,
      pulled: 2,
      pushed: 0,
      external_deleted: 1,
      failed: 0,
      items: [],
    })
    vi.mocked(syncApi.confirmObsidianDeletion).mockResolvedValue(undefined)
    vi.mocked(syncApi.restoreObsidianDeletion).mockResolvedValue({
      note_id: 'note-1',
      status: 'synced',
    })
  })

  it('separates pushing FlowSpace notes from manually pulling Obsidian notes', async () => {
    renderPanel()
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: '同步到 Obsidian' })
    )

    await waitFor(() =>
      expect(syncApi.syncObsidianAll).toHaveBeenCalledTimes(1)
    )
    expect(syncApi.syncObsidianPull).not.toHaveBeenCalled()
    expect(await screen.findByText('同步完成：成功 2，失败 1')).toBeVisible()

    await user.click(
      screen.getByRole('button', { name: '从 Obsidian 手动拉取' })
    )

    await waitFor(() =>
      expect(syncApi.syncObsidianPull).toHaveBeenCalledTimes(1)
    )
    expect(syncApi.syncObsidianBidirectional).not.toHaveBeenCalled()
    expect(
      await screen.findByText(
        '手动拉取完成：导入 3，从 Obsidian 更新 2，待确认删除 1，失败 0'
      )
    ).toBeVisible()
  })
  it('saves an explicit empty tag filter by default', async () => {
    renderPanel()
    const user = userEvent.setup()

    expect(await screen.findByText('只同步包含以下任一标签的笔记')).toBeVisible()
    expect(screen.getByText('留空时不会同步任何笔记')).toBeVisible()
    await user.click(
      screen.getByRole('button', { name: '保存 Obsidian 设置' })
    )

    await waitFor(() => expect(syncApi.saveSyncTarget).toHaveBeenCalledTimes(1))
    const payload = vi.mocked(syncApi.saveSyncTarget).mock.calls[0][0]
    expect(JSON.parse(payload.config_json ?? '{}')).toEqual({
      required_tags: [],
    })
  })

  it('saves comma-separated sync tags for Obsidian filtering', async () => {
    renderPanel()
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText('添加同步标签'),
      'sync, publish, #work{enter}'
    )
    expect(screen.getByText('#sync')).toBeVisible()
    expect(screen.getByText('#publish')).toBeVisible()
    expect(screen.getByText('#work')).toBeVisible()
    await user.click(
      screen.getByRole('button', { name: '保存 Obsidian 设置' })
    )

    await waitFor(() => expect(syncApi.saveSyncTarget).toHaveBeenCalledTimes(1))
    const payload = vi.mocked(syncApi.saveSyncTarget).mock.calls[0][0]
    expect(JSON.parse(payload.config_json ?? '{}')).toEqual({
      required_tags: ['sync', 'publish', 'work'],
    })
  })
})
