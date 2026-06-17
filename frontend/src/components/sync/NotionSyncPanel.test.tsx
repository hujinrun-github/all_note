import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { NotionSyncPanel } from './NotionSyncPanel'
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
      <NotionSyncPanel />
    </QueryClientProvider>
  )
}

describe('NotionSyncPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue([])
    vi.mocked(syncApi.getNotionDeletions).mockResolvedValue([])
    vi.mocked(syncApi.saveSyncTarget).mockResolvedValue({
      id: 'target-1',
      type: 'notion',
      name: 'Personal Notion',
      vault_path: '',
      base_folder: '',
      config_json: JSON.stringify({
        data_source_id: 'ds-123',
        token_env: 'FLOWSPACE_NOTION_TOKEN',
        title_property: 'Name',
      }),
      enabled: true,
      auto_sync: false,
      created_at: 1,
      updated_at: 1,
    })
    vi.mocked(syncApi.testNotionTarget).mockResolvedValue(undefined)
    vi.mocked(syncApi.syncNotionAll).mockResolvedValue({
      synced: 4,
      failed: 1,
      items: [],
    })
    vi.mocked(syncApi.syncNotionPull).mockResolvedValue({
      imported: 3,
      pulled: 2,
      conflict_pulled: 1,
      pushed: 0,
      unsupported: 5,
      external_deleted: 6,
      failed: 7,
      items: [],
    })
    vi.mocked(syncApi.syncNotionBidirectional).mockResolvedValue({
      imported: 3,
      pulled: 2,
      conflict_pulled: 1,
      pushed: 4,
      unsupported: 5,
      external_deleted: 6,
      failed: 7,
      items: [],
    })
    vi.mocked(syncApi.confirmNotionDeletion).mockResolvedValue(undefined)
    vi.mocked(syncApi.restoreNotionDeletion).mockResolvedValue({
      note_id: 'note-1',
      status: 'restored',
    })
  })

  it('saves notion config with data source id and token env but no raw token', async () => {
    renderPanel()
    const user = userEvent.setup()

    await user.type(await screen.findByLabelText('Data Source ID'), 'ds-123')
    expect(screen.getByText('目标名称')).toBeVisible()
    expect(screen.getByText('Data Source ID（数据源 ID）')).toBeVisible()
    expect(screen.getByText('Token environment variable（令牌环境变量）')).toBeVisible()
    expect(screen.getByText('标题属性')).toBeVisible()
    expect(screen.getByLabelText('Token environment variable')).toHaveValue(
      'FLOWSPACE_NOTION_TOKEN'
    )
    expect(screen.getByLabelText('标题属性')).toHaveValue('Name')
    expect(screen.getByRole('link', { name: 'Data Source ID 说明' })).toHaveAttribute(
      'href',
      '/docs/notion-sync.html#data-source-id'
    )
    expect(screen.getByRole('link', { name: '令牌环境变量说明' })).toHaveAttribute(
      'href',
      '/docs/notion-sync.html#token-env'
    )

    await user.click(screen.getByRole('button', { name: '保存 Notion 设置' }))

    await waitFor(() => expect(syncApi.saveSyncTarget).toHaveBeenCalledTimes(1))
    const payload = vi.mocked(syncApi.saveSyncTarget).mock.calls[0][0]
    expect(payload).toEqual(
      expect.objectContaining({
        type: 'notion',
        name: 'Personal Notion',
        vault_path: '',
        base_folder: '',
        enabled: true,
        auto_sync: false,
      })
    )
    expect(JSON.parse(payload.config_json ?? '{}')).toEqual({
      data_source_id: 'ds-123',
      token_env: 'FLOWSPACE_NOTION_TOKEN',
      title_property: 'Name',
      required_tags: [],
    })
    expect(JSON.stringify(payload)).not.toContain('secret')
    expect(payload).not.toHaveProperty('token')
  })

  it('saves comma-separated sync tags for Notion filtering', async () => {
    renderPanel()
    const user = userEvent.setup()

    await user.type(await screen.findByLabelText('Data Source ID'), 'ds-123')
    expect(screen.getByText('只同步包含以下任一标签的笔记')).toBeVisible()
    expect(screen.getByText('留空时不会同步任何笔记')).toBeVisible()

    await user.type(
      screen.getByLabelText('添加同步标签'),
      'sync, publish, #work{enter}'
    )
    expect(screen.getByText('#sync')).toBeVisible()
    expect(screen.getByText('#publish')).toBeVisible()
    expect(screen.getByText('#work')).toBeVisible()
    await user.click(screen.getByRole('button', { name: '保存 Notion 设置' }))

    await waitFor(() => expect(syncApi.saveSyncTarget).toHaveBeenCalledTimes(1))
    const payload = vi.mocked(syncApi.saveSyncTarget).mock.calls[0][0]
    expect(JSON.parse(payload.config_json ?? '{}')).toEqual({
      data_source_id: 'ds-123',
      token_env: 'FLOWSPACE_NOTION_TOKEN',
      title_property: 'Name',
      required_tags: ['sync', 'publish', 'work'],
    })
  })

  it('removes a selected sync tag before saving Notion settings', async () => {
    renderPanel()
    const user = userEvent.setup()

    await user.type(await screen.findByLabelText('Data Source ID'), 'ds-123')
    await user.type(screen.getByLabelText('添加同步标签'), 'sync, publish{enter}')
    await user.click(screen.getByRole('button', { name: '移除同步标签 sync' }))
    expect(screen.queryByText('#sync')).not.toBeInTheDocument()
    expect(screen.getByText('#publish')).toBeVisible()

    await user.click(screen.getByRole('button', { name: '保存 Notion 设置' }))

    await waitFor(() => expect(syncApi.saveSyncTarget).toHaveBeenCalledTimes(1))
    const payload = vi.mocked(syncApi.saveSyncTarget).mock.calls[0][0]
    expect(JSON.parse(payload.config_json ?? '{}')).toEqual({
      data_source_id: 'ds-123',
      token_env: 'FLOWSPACE_NOTION_TOKEN',
      title_property: 'Name',
      required_tags: ['publish'],
    })
  })

  it('normalizes non-string config fields from an existing notion target', async () => {
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue([
      {
        id: 'target-bad-config',
        type: 'notion',
        name: 'Malformed Notion',
        vault_path: '',
        base_folder: '',
        config_json: JSON.stringify({
          data_source_id: 123,
          token_env: {},
          title_property: [],
          required_tags: 'sync',
        }),
        enabled: true,
        auto_sync: false,
        created_at: 1,
        updated_at: 1,
      },
    ])
    renderPanel()
    const user = userEvent.setup()

    await waitFor(() =>
      expect(screen.getByLabelText('目标名称')).toHaveValue('Malformed Notion')
    )
    expect(screen.getByLabelText('Data Source ID')).toHaveValue('')
    expect(screen.getByLabelText('Token environment variable')).toHaveValue(
      'FLOWSPACE_NOTION_TOKEN'
    )
    expect(screen.getByLabelText('标题属性')).toHaveValue('Name')

    await user.click(screen.getByRole('button', { name: '保存 Notion 设置' }))

    await waitFor(() => expect(syncApi.saveSyncTarget).toHaveBeenCalledTimes(1))
    const payload = vi.mocked(syncApi.saveSyncTarget).mock.calls[0][0]
    expect(payload.id).toBe('target-bad-config')
    expect(JSON.parse(payload.config_json ?? '{}')).toEqual({
      data_source_id: '',
      token_env: 'FLOWSPACE_NOTION_TOKEN',
      title_property: 'Name',
      required_tags: [],
    })
  })

  it('separates pushing FlowSpace notes from manually pulling Notion notes', async () => {
    renderPanel()
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: '同步到 Notion' })
    )

    expect(syncApi.syncNotionAll).toHaveBeenCalledTimes(1)
    expect(syncApi.syncNotionPull).not.toHaveBeenCalled()
    expect(await screen.findByText('已同步 4')).toBeVisible()
    expect(screen.getByText('失败 1')).toBeVisible()

    await user.click(screen.getByRole('button', { name: '从 Notion 手动拉取' }))

    expect(syncApi.syncNotionPull).toHaveBeenCalledTimes(1)
    expect(syncApi.syncNotionBidirectional).not.toHaveBeenCalled()
    expect(await screen.findByText('导入 3')).toBeVisible()
    expect(screen.getByText('Notion 更新 2')).toBeVisible()
    expect(screen.getByText('冲突覆盖 1')).toBeVisible()
    expect(screen.queryByText('Pushed 4')).not.toBeInTheDocument()
    expect(screen.getByText('不支持 5')).toBeVisible()
    expect(screen.getByText('待确认删除 6')).toBeVisible()
    expect(screen.getByText('失败 7')).toBeVisible()
  })

  it('renders notion deletion candidates with restore and confirm actions', async () => {
    vi.mocked(syncApi.getNotionDeletions).mockResolvedValue([
      {
        note_id: 'note-1',
        title: 'Deleted Notion Note',
        external_path: 'notion:page-1',
        last_synced_at: 1800000000,
      },
    ])
    renderPanel()
    const user = userEvent.setup()

    expect(await screen.findByText('Notion 已删除，等待确认')).toBeVisible()
    expect(screen.getByText('Deleted Notion Note')).toBeVisible()
    expect(screen.getByText('notion:page-1')).toBeVisible()

    await user.click(screen.getByRole('button', { name: '恢复 Notion 页面' }))
    expect(syncApi.restoreNotionDeletion).toHaveBeenCalledWith('note-1')

    await user.click(
      screen.getByRole('button', { name: '确认删除 FlowSpace 笔记' })
    )
    expect(syncApi.confirmNotionDeletion).toHaveBeenCalledWith('note-1')
  })
})
