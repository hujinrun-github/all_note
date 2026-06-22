import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { APIError } from '../../api/client'
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
    vi.mocked(syncApi.pushTarget).mockResolvedValue({
      synced: 2,
      failed: 1,
      items: [],
    })
    vi.mocked(syncApi.pullTarget).mockResolvedValue({
      imported: 3,
      pulled: 2,
      pushed: 0,
      external_deleted: 1,
      failed: 0,
      items: [],
    })
    vi.mocked(syncApi.getTargetDeletions).mockResolvedValue([])
    vi.mocked(syncApi.confirmTargetDeletion).mockResolvedValue(undefined)
    vi.mocked(syncApi.restoreTargetDeletion).mockResolvedValue({
      note_id: 'note-1',
      status: 'synced',
    })
  })

  it('rejects saving when required Obsidian settings are blank', async () => {
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue([
      {
        id: 'obs-empty',
        type: 'obsidian',
        name: '',
        vault_path: '',
        base_folder: '',
        config_json: '{}',
        enabled: true,
        auto_sync: false,
        is_default: true,
        created_at: 1,
        updated_at: 1,
      },
    ])
    renderPanel()
    const user = userEvent.setup()

    await waitFor(() => expect(screen.getByLabelText('目标名称')).toHaveValue(''))
    await user.click(screen.getByRole('button', { name: '保存 Obsidian 设置' }))

    expect(await screen.findByText('请填写目标名称、Vault 路径、同步目录、同步标签过滤')).toBeVisible()
    expect(syncApi.saveSyncTarget).not.toHaveBeenCalled()
  })

  it('separates pushing FlowSpace notes from manually pulling Obsidian notes', async () => {
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue([
      {
        id: 'obs-1',
        type: 'obsidian',
        name: 'Work Vault',
        vault_path: 'D:\\Vault',
        base_folder: 'FlowSpace Notes',
        config_json: '{}',
        enabled: true,
        auto_sync: false,
        is_default: true,
        created_at: 1,
        updated_at: 1,
      },
    ])
    renderPanel()
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: '同步到 Obsidian' })
    )

    await waitFor(() => expect(syncApi.pushTarget).toHaveBeenCalledWith('obs-1'))
    expect(syncApi.syncObsidianAll).not.toHaveBeenCalled()
    expect(syncApi.syncObsidianPull).not.toHaveBeenCalled()
    expect(await screen.findByText('同步完成：成功 2，失败 1')).toBeVisible()

    await user.click(
      screen.getByRole('button', { name: '从 Obsidian 手动拉取' })
    )

    await waitFor(() => expect(syncApi.pullTarget).toHaveBeenCalledWith('obs-1'))
    expect(syncApi.syncObsidianPull).not.toHaveBeenCalled()
    expect(syncApi.syncObsidianBidirectional).not.toHaveBeenCalled()
    expect(
      await screen.findByText(
        '手动拉取完成：导入 3，从 Obsidian 更新 2，待确认删除 1，失败 0'
      )
    ).toBeVisible()
  })
  it('rejects saving when the Obsidian sync tag filter is blank', async () => {
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue([
      {
        id: 'obs-1',
        type: 'obsidian',
        name: 'Work Vault',
        vault_path: 'D:\\Vault',
        base_folder: 'FlowSpace Notes',
        config_json: '{}',
        enabled: true,
        auto_sync: false,
        is_default: true,
        created_at: 1,
        updated_at: 1,
      },
    ])
    renderPanel()
    const user = userEvent.setup()

    expect(await screen.findByText('只同步包含以下任一标签的笔记')).toBeVisible()
    await waitFor(() => expect(screen.getByLabelText('Vault 路径')).toHaveValue('D:\\Vault'))
    expect(screen.getByText('至少填写一个同步标签')).toBeVisible()
    await user.click(
      screen.getByRole('button', { name: '保存 Obsidian 设置' })
    )

    expect(await screen.findByText('请填写同步标签过滤')).toBeVisible()
    expect(syncApi.saveSyncTarget).not.toHaveBeenCalled()
  })

  it('saves comma-separated sync tags for Obsidian filtering', async () => {
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue([
      {
        id: 'obs-1',
        type: 'obsidian',
        name: 'Work Vault',
        vault_path: 'D:\\Vault',
        base_folder: 'FlowSpace Notes',
        config_json: '{}',
        enabled: true,
        auto_sync: false,
        is_default: true,
        created_at: 1,
        updated_at: 1,
      },
    ])
    renderPanel()
    const user = userEvent.setup()

    await waitFor(() => expect(screen.getByLabelText('Vault 路径')).toHaveValue('D:\\Vault'))
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

  it('uses target scoped deletion candidates', async () => {
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue([
      {
        id: 'obs-1',
        type: 'obsidian',
        name: 'Work Vault',
        vault_path: 'D:\\Vault',
        base_folder: 'FlowSpace Notes',
        config_json: '{}',
        enabled: true,
        auto_sync: false,
        is_default: true,
        created_at: 1,
        updated_at: 1,
      },
    ])
    vi.mocked(syncApi.getTargetDeletions).mockResolvedValue([
      {
        note_id: 'note-1',
        title: 'Deleted Obsidian Note',
        external_path: 'D:\\Vault\\note.md',
        last_synced_at: 1800000000,
      },
    ])
    renderPanel()
    const user = userEvent.setup()

    expect(await screen.findByText('Deleted Obsidian Note')).toBeVisible()
    expect(syncApi.getTargetDeletions).toHaveBeenCalledWith('obs-1')

    await user.click(screen.getByRole('button', { name: '保留并重新导出' }))
    expect(syncApi.restoreTargetDeletion).toHaveBeenCalledWith('obs-1', 'note-1')

    await user.click(screen.getByRole('button', { name: '确认删除' }))
    expect(syncApi.confirmTargetDeletion).toHaveBeenCalledWith('obs-1', 'note-1')
  })

  it('shows vault path as locked when backend reports lock', async () => {
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue([
      {
        id: 'obs-locked',
        type: 'obsidian',
        name: 'Locked Vault',
        vault_path: 'D:\\Vault',
        base_folder: 'FlowSpace Notes',
        config_json: JSON.stringify({ required_tags: ['sync'] }),
        enabled: true,
        auto_sync: false,
        is_default: true,
        created_at: 1,
        updated_at: 1,
      },
    ])
    vi.mocked(syncApi.saveSyncTarget).mockRejectedValueOnce(new APIError(409, 'target_identity_locked', 'locked'))
    renderPanel()
    const user = userEvent.setup()

    await waitFor(() => expect(screen.getByLabelText('Vault 路径')).toHaveValue('D:\\Vault'))
    await user.click(screen.getByRole('button', { name: '保存 Obsidian 设置' }))

    expect(await screen.findByText('Vault 路径已被使用中的同步目标锁定')).toBeVisible()
  })
})
