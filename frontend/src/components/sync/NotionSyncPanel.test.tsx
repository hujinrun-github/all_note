import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { APIError } from '../../api/client'
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
        token_set: true,
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
    vi.mocked(syncApi.pushTarget).mockResolvedValue({
      synced: 4,
      failed: 1,
      items: [],
    })
    vi.mocked(syncApi.pullTarget).mockResolvedValue({
      imported: 3,
      pulled: 2,
      conflict_pulled: 1,
      pushed: 0,
      unsupported: 5,
      external_deleted: 6,
      failed: 7,
      items: [],
    })
    vi.mocked(syncApi.getTargetDeletions).mockResolvedValue([])
    vi.mocked(syncApi.confirmTargetDeletion).mockResolvedValue(undefined)
    vi.mocked(syncApi.restoreTargetDeletion).mockResolvedValue({
      note_id: 'note-1',
      status: 'restored',
    })
  })

  it('rejects saving when required Notion settings are blank', async () => {
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue([
      {
        id: 'notion-empty',
        type: 'notion',
        name: '',
        vault_path: '',
        base_folder: '',
        config_json: JSON.stringify({
          data_source_id: '',
          title_property: '',
        }),
        enabled: true,
        auto_sync: false,
        is_default: true,
        created_at: 1,
        updated_at: 1,
      },
    ])
    renderPanel()
    const user = userEvent.setup()

    await waitFor(() =>
      expect(syncApi.getTargetDeletions).toHaveBeenCalledWith('notion-empty')
    )
    await user.clear(screen.getByLabelText('目标名称'))
    await user.clear(screen.getByLabelText('标题属性'))
    await user.click(screen.getByRole('button', { name: '保存 Notion 设置' }))

    expect(
      await screen.findByText(
        '请填写目标名称、数据库链接或 Data Source ID、Notion Token、标题属性、同步标签过滤'
      )
    ).toBeVisible()
    expect(syncApi.saveSyncTarget).not.toHaveBeenCalled()
  })

  it('tests with a raw Notion token in the request config', async () => {
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue([
      {
        id: 'notion-raw-token',
        type: 'notion',
        name: 'Personal Notion',
        vault_path: '',
        base_folder: '',
        config_json: JSON.stringify({
          data_source_id: 'ds-123',
          title_property: 'Name',
          required_tags: ['sync'],
        }),
        enabled: true,
        auto_sync: false,
        is_default: true,
        created_at: 1,
        updated_at: 1,
      },
    ])
    renderPanel()
    const user = userEvent.setup()

    await waitFor(() =>
      expect(screen.getByLabelText('Data Source ID')).toHaveValue('ds-123')
    )
    await user.type(screen.getByLabelText('Notion Token'), 'ntn_fake_raw_token')
    await user.click(screen.getByRole('button', { name: '测试 Notion 连接' }))

    await waitFor(() =>
      expect(syncApi.testNotionTarget).toHaveBeenCalledTimes(1)
    )
    const payload = vi.mocked(syncApi.testNotionTarget).mock.calls[0][0]
    expect(JSON.parse(payload.config_json ?? '{}')).toEqual({
      data_source_id: 'ds-123',
      token: 'ntn_fake_raw_token',
      title_property: 'Name',
      required_tags: ['sync'],
    })
  })

  it('shows the backend reason when Notion connection testing fails', async () => {
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue([
      {
        id: 'notion-missing-token',
        type: 'notion',
        name: 'Personal Notion',
        vault_path: '',
        base_folder: '',
        config_json: JSON.stringify({
          data_source_id: 'ds-123',
          token_set: true,
          title_property: 'Name',
          required_tags: ['sync'],
        }),
        enabled: true,
        auto_sync: false,
        is_default: true,
        created_at: 1,
        updated_at: 1,
      },
    ])
    vi.mocked(syncApi.testNotionTarget).mockRejectedValueOnce(
      new APIError(400, 'BAD_REQUEST', 'FLOWSPACE_NOTION_TOKEN is required')
    )
    renderPanel()
    const user = userEvent.setup()

    await waitFor(() =>
      expect(screen.getByLabelText('Data Source ID')).toHaveValue('ds-123')
    )
    await user.click(screen.getByRole('button', { name: '测试 Notion 连接' }))

    expect(
      await screen.findByText(
        '无法使用当前配置连接 Notion：FLOWSPACE_NOTION_TOKEN is required'
      )
    ).toBeVisible()
  })

  it('explains invalid Notion tokens in plain language', async () => {
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue([
      {
        id: 'notion-invalid-token',
        type: 'notion',
        name: 'Personal Notion',
        vault_path: '',
        base_folder: '',
        config_json: JSON.stringify({
          data_source_id: 'ds-123',
          token_set: true,
          title_property: 'Name',
          required_tags: ['sync'],
        }),
        enabled: true,
        auto_sync: false,
        is_default: true,
        created_at: 1,
        updated_at: 1,
      },
    ])
    vi.mocked(syncApi.testNotionTarget).mockRejectedValueOnce(
      new APIError(
        400,
        'BAD_REQUEST',
        'notion API error 401: API token is invalid.'
      )
    )
    renderPanel()
    const user = userEvent.setup()

    await waitFor(() =>
      expect(screen.getByLabelText('Data Source ID')).toHaveValue('ds-123')
    )
    await user.click(screen.getByRole('button', { name: '测试 Notion 连接' }))

    expect(
      await screen.findByText(
        '无法使用当前配置连接 Notion：Notion Token 无效。请从 Notion integration 重新复制原始 Token，粘贴到“Notion Token（原始令牌）”后保存并重新测试。'
      )
    ).toBeVisible()
  })

  it('explains missing Notion database sharing in plain language', async () => {
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue([
      {
        id: 'notion-unshared-database',
        type: 'notion',
        name: 'Personal Notion',
        vault_path: '',
        base_folder: '',
        config_json: JSON.stringify({
          data_source_id: '38fd2ee3-09c4-80aa-b4be-db93d1479505',
          token_set: true,
          title_property: 'Name',
          required_tags: ['sync'],
        }),
        enabled: true,
        auto_sync: false,
        is_default: true,
        created_at: 1,
        updated_at: 1,
      },
    ])
    vi.mocked(syncApi.testNotionTarget).mockRejectedValueOnce(
      new APIError(
        400,
        'BAD_REQUEST',
        'notion API error 404: Could not find database with ID: 38fd2ee3-09c4-80aa-b4be-db93d1479505. Make sure the relevant pages and databases are shared with your integration "all-note".'
      )
    )
    renderPanel()
    const user = userEvent.setup()

    await waitFor(() =>
      expect(screen.getByLabelText('Data Source ID')).toHaveValue(
        '38fd2ee3-09c4-80aa-b4be-db93d1479505'
      )
    )
    await user.click(screen.getByRole('button', { name: '测试 Notion 连接' }))

    expect(
      await screen.findByText(
        '无法使用当前配置连接 Notion：Notion 数据库没有授权给 all-note，或当前链接不是原始数据库。请在 Notion 数据库右上角菜单进入 Connections，添加 all-note；如果这是 linked database，请打开原始数据库后再添加连接。'
      )
    ).toBeVisible()
  })

  it('explains when a Notion page link is used instead of a database link', async () => {
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue([
      {
        id: 'notion-page-link',
        type: 'notion',
        name: 'Personal Notion',
        vault_path: '',
        base_folder: '',
        config_json: JSON.stringify({
          data_source_id: '38fd2ee3-09c4-80aa-b4be-db93d1479505',
          token_set: true,
          title_property: 'Name',
          required_tags: ['sync'],
        }),
        enabled: true,
        auto_sync: false,
        is_default: true,
        created_at: 1,
        updated_at: 1,
      },
    ])
    vi.mocked(syncApi.testNotionTarget).mockRejectedValueOnce(
      new APIError(
        400,
        'BAD_REQUEST',
        'notion API error 400: Provided database_id 38fd2ee3-09c4-80aa-b4be-db93d1479505 is a page, not a database. Use the pages API instead, or pass the ID of the database itself.'
      )
    )
    renderPanel()
    const user = userEvent.setup()

    await waitFor(() =>
      expect(screen.getByLabelText('Data Source ID')).toHaveValue(
        '38fd2ee3-09c4-80aa-b4be-db93d1479505'
      )
    )
    await user.click(screen.getByRole('button', { name: '测试 Notion 连接' }))

    expect(
      await screen.findByText(
        '无法使用当前配置连接 Notion：当前粘贴的是 Notion 页面链接，不是数据库链接。请打开用于同步的 Notion 数据库本体，点击数据库右上角菜单的 Copy link，再粘贴到“数据库链接或 Data Source ID”。'
      )
    ).toBeVisible()
  })

  it('saves notion config with data source id and raw token', async () => {
    renderPanel()
    const user = userEvent.setup()

    await user.type(await screen.findByLabelText('Data Source ID'), 'ds-123')
    await user.type(
      screen.getByLabelText('Notion Token'),
      'ntn_unit_test_token'
    )
    await user.type(screen.getByLabelText('添加同步标签'), 'sync{enter}')
    expect(screen.getByText('目标名称')).toBeVisible()
    expect(screen.getByText('数据库链接或 Data Source ID')).toBeVisible()
    expect(screen.getByText('Notion Token（原始令牌）')).toBeVisible()
    expect(screen.getByText('标题属性')).toBeVisible()
    expect(screen.getByLabelText('Notion Token')).toHaveAttribute(
      'type',
      'password'
    )
    expect(screen.getByLabelText('Notion Token')).toHaveValue(
      'ntn_unit_test_token'
    )
    expect(screen.getByLabelText('标题属性')).toHaveValue('Name')
    expect(
      screen.getByRole('link', { name: 'Data Source ID 说明' })
    ).toHaveAttribute('href', '/docs/notion-sync.html#data-source-id')
    expect(
      screen.getByRole('link', { name: 'Data Source ID 说明' })
    ).toHaveAttribute('target', '_blank')
    expect(
      screen.getByRole('link', { name: 'Notion Token 说明' })
    ).toHaveAttribute('href', '/docs/notion-sync.html#token')
    expect(
      screen.getByRole('link', { name: 'Notion Token 说明' })
    ).toHaveAttribute('target', '_blank')

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
      token: 'ntn_unit_test_token',
      title_property: 'Name',
      required_tags: ['sync'],
    })
    expect(payload).not.toHaveProperty('token')
  })

  it('creates an additional Notion target without overwriting the existing one', async () => {
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue([
      {
        id: 'notion-existing',
        type: 'notion',
        name: 'Personal Notion',
        vault_path: '',
        base_folder: '',
        config_json: JSON.stringify({
          data_source_id: 'ds-existing',
          token_set: true,
          title_property: 'Name',
          required_tags: ['notion'],
        }),
        enabled: true,
        auto_sync: false,
        is_default: true,
        created_at: 1,
        updated_at: 1,
      },
    ])
    vi.mocked(syncApi.saveSyncTarget).mockResolvedValueOnce({
      id: 'notion-work',
      type: 'notion',
      name: 'Work Notion',
      vault_path: '',
      base_folder: '',
      config_json: JSON.stringify({
        data_source_id: 'ds-work',
        token_set: true,
        title_property: 'Name',
        required_tags: ['work'],
      }),
      enabled: true,
      auto_sync: false,
      created_at: 2,
      updated_at: 2,
    })
    renderPanel()
    const user = userEvent.setup()

    await waitFor(() =>
      expect(screen.getByLabelText('Data Source ID')).toHaveValue('ds-existing')
    )
    await user.click(screen.getByRole('button', { name: '新增 Notion 目标' }))
    expect(screen.getByLabelText('Data Source ID')).toHaveValue('')
    expect(screen.getByLabelText('Notion Token')).toHaveValue('')

    await user.clear(screen.getByLabelText('目标名称'))
    await user.type(screen.getByLabelText('目标名称'), 'Work Notion')
    await user.type(screen.getByLabelText('Data Source ID'), 'ds-work')
    await user.type(screen.getByLabelText('Notion Token'), 'ntn_work_token')
    await user.type(screen.getByLabelText('添加同步标签'), 'work{enter}')
    await user.click(screen.getByRole('button', { name: '保存 Notion 设置' }))

    await waitFor(() => expect(syncApi.saveSyncTarget).toHaveBeenCalledTimes(1))
    const payload = vi.mocked(syncApi.saveSyncTarget).mock.calls[0][0]
    expect(payload.id).toBeUndefined()
    expect(payload.name).toBe('Work Notion')
    expect(JSON.parse(payload.config_json ?? '{}')).toEqual({
      data_source_id: 'ds-work',
      token: 'ntn_work_token',
      title_property: 'Name',
      required_tags: ['work'],
    })
  })

  it('switches between configured Notion targets before saving', async () => {
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue([
      {
        id: 'notion-personal',
        type: 'notion',
        name: 'Personal Notion',
        vault_path: '',
        base_folder: '',
        config_json: JSON.stringify({
          data_source_id: 'ds-personal',
          token_set: true,
          title_property: 'Name',
          required_tags: ['personal'],
        }),
        enabled: true,
        auto_sync: false,
        is_default: true,
        created_at: 1,
        updated_at: 1,
      },
      {
        id: 'notion-work',
        type: 'notion',
        name: 'Work Notion',
        vault_path: '',
        base_folder: '',
        config_json: JSON.stringify({
          data_source_id: 'ds-work',
          token_set: true,
          title_property: 'Name',
          required_tags: ['work'],
        }),
        enabled: true,
        auto_sync: true,
        created_at: 2,
        updated_at: 2,
      },
    ])
    renderPanel()
    const user = userEvent.setup()

    await waitFor(() =>
      expect(screen.getByLabelText('Data Source ID')).toHaveValue('ds-personal')
    )
    await user.selectOptions(
      screen.getByLabelText('编辑 Notion 目标'),
      'notion-work'
    )

    await waitFor(() =>
      expect(screen.getByLabelText('目标名称')).toHaveValue('Work Notion')
    )
    expect(screen.getByLabelText('Data Source ID')).toHaveValue('ds-work')
    expect(screen.getByText('#work')).toBeVisible()
    expect(screen.getByLabelText('保存笔记后自动同步')).toBeChecked()

    await user.clear(screen.getByLabelText('Data Source ID'))
    await user.type(screen.getByLabelText('Data Source ID'), 'ds-work-updated')
    await user.click(screen.getByRole('button', { name: '保存 Notion 设置' }))

    await waitFor(() => expect(syncApi.saveSyncTarget).toHaveBeenCalledTimes(1))
    const payload = vi.mocked(syncApi.saveSyncTarget).mock.calls[0][0]
    expect(payload.id).toBe('notion-work')
    expect(JSON.parse(payload.config_json ?? '{}')).toEqual({
      data_source_id: 'ds-work-updated',
      title_property: 'Name',
      required_tags: ['work'],
    })
  })

  it('saves comma-separated sync tags for Notion filtering', async () => {
    renderPanel()
    const user = userEvent.setup()

    await user.type(await screen.findByLabelText('Data Source ID'), 'ds-123')
    await user.type(
      screen.getByLabelText('Notion Token'),
      'ntn_tags_test_token'
    )
    expect(screen.getByText('只同步包含以下任一标签的笔记')).toBeVisible()
    expect(screen.getByText('至少填写一个同步标签')).toBeVisible()

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
      token: 'ntn_tags_test_token',
      title_property: 'Name',
      required_tags: ['sync', 'publish', 'work'],
    })
  })

  it('removes a selected sync tag before saving Notion settings', async () => {
    renderPanel()
    const user = userEvent.setup()

    await user.type(await screen.findByLabelText('Data Source ID'), 'ds-123')
    await user.type(
      screen.getByLabelText('Notion Token'),
      'ntn_remove_tag_token'
    )
    await user.type(
      screen.getByLabelText('添加同步标签'),
      'sync, publish{enter}'
    )
    await user.click(screen.getByRole('button', { name: '移除同步标签 sync' }))
    expect(screen.queryByText('#sync')).not.toBeInTheDocument()
    expect(screen.getByText('#publish')).toBeVisible()

    await user.click(screen.getByRole('button', { name: '保存 Notion 设置' }))

    await waitFor(() => expect(syncApi.saveSyncTarget).toHaveBeenCalledTimes(1))
    const payload = vi.mocked(syncApi.saveSyncTarget).mock.calls[0][0]
    expect(JSON.parse(payload.config_json ?? '{}')).toEqual({
      data_source_id: 'ds-123',
      token: 'ntn_remove_tag_token',
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
    expect(screen.getByLabelText('Notion Token')).toHaveValue('')
    expect(screen.getByLabelText('标题属性')).toHaveValue('Name')

    await user.type(screen.getByLabelText('Data Source ID'), 'ds-123')
    await user.type(
      screen.getByLabelText('Notion Token'),
      'ntn_normalized_token'
    )
    await user.type(screen.getByLabelText('添加同步标签'), 'sync{enter}')
    await user.click(screen.getByRole('button', { name: '保存 Notion 设置' }))

    await waitFor(() => expect(syncApi.saveSyncTarget).toHaveBeenCalledTimes(1))
    const payload = vi.mocked(syncApi.saveSyncTarget).mock.calls[0][0]
    expect(payload.id).toBe('target-bad-config')
    expect(JSON.parse(payload.config_json ?? '{}')).toEqual({
      data_source_id: 'ds-123',
      token: 'ntn_normalized_token',
      title_property: 'Name',
      required_tags: ['sync'],
    })
  })

  it('keeps an existing raw token hidden when editing', async () => {
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue([
      {
        id: 'notion-existing-token',
        type: 'notion',
        name: 'Personal Notion',
        vault_path: '',
        base_folder: '',
        config_json: JSON.stringify({
          data_source_id: 'ds-123',
          token_set: true,
          title_property: 'Name',
          required_tags: ['sync'],
        }),
        enabled: true,
        auto_sync: false,
        is_default: true,
        created_at: 1,
        updated_at: 1,
      },
    ])
    renderPanel()
    const user = userEvent.setup()

    await waitFor(() =>
      expect(screen.getByLabelText('Data Source ID')).toHaveValue('ds-123')
    )
    const tokenInput = screen.getByLabelText('Notion Token')
    expect(tokenInput).toHaveValue('')
    expect(tokenInput).toHaveAttribute(
      'placeholder',
      '••••••••（已设置，输入新 Token 覆盖）'
    )
    expect(screen.getByText('已设置')).toBeVisible()
    await user.click(screen.getByRole('button', { name: '保存 Notion 设置' }))

    await waitFor(() => expect(syncApi.saveSyncTarget).toHaveBeenCalledTimes(1))
    const payload = vi.mocked(syncApi.saveSyncTarget).mock.calls[0][0]
    expect(payload.id).toBe('notion-existing-token')
    expect(JSON.parse(payload.config_json ?? '{}')).toEqual({
      data_source_id: 'ds-123',
      title_property: 'Name',
      required_tags: ['sync'],
    })
  })

  it('overwrites an existing raw token only when a new token is entered', async () => {
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue([
      {
        id: 'notion-overwrite-token',
        type: 'notion',
        name: 'Personal Notion',
        vault_path: '',
        base_folder: '',
        config_json: JSON.stringify({
          data_source_id: 'ds-123',
          token_set: true,
          title_property: 'Name',
          required_tags: ['sync'],
        }),
        enabled: true,
        auto_sync: false,
        is_default: true,
        created_at: 1,
        updated_at: 1,
      },
    ])
    renderPanel()
    const user = userEvent.setup()

    await waitFor(() =>
      expect(screen.getByLabelText('Data Source ID')).toHaveValue('ds-123')
    )
    await user.type(
      screen.getByLabelText('Notion Token'),
      'ntn_replacement_token'
    )
    expect(screen.getByText('待覆盖')).toBeVisible()
    await user.click(screen.getByRole('button', { name: '保存 Notion 设置' }))

    await waitFor(() => expect(syncApi.saveSyncTarget).toHaveBeenCalledTimes(1))
    const payload = vi.mocked(syncApi.saveSyncTarget).mock.calls[0][0]
    expect(payload.id).toBe('notion-overwrite-token')
    expect(JSON.parse(payload.config_json ?? '{}')).toEqual({
      data_source_id: 'ds-123',
      token: 'ntn_replacement_token',
      title_property: 'Name',
      required_tags: ['sync'],
    })
  })

  it('opens Notion field help in a separate sized window', async () => {
    const openSpy = vi
      .spyOn(window, 'open')
      .mockReturnValue({ focus: vi.fn() } as unknown as Window)
    try {
      renderPanel()
      const user = userEvent.setup()

      await user.click(
        await screen.findByRole('link', { name: 'Data Source ID 说明' })
      )
      expect(openSpy).toHaveBeenNthCalledWith(
        1,
        expect.stringContaining('/docs/notion-sync.html#data-source-id'),
        'flowspace-notion-sync-help',
        expect.stringContaining('width=960')
      )
      expect(openSpy).toHaveBeenNthCalledWith(
        1,
        expect.any(String),
        expect.any(String),
        expect.stringContaining('height=720')
      )

      await user.click(screen.getByRole('link', { name: 'Notion Token 说明' }))
      expect(openSpy).toHaveBeenNthCalledWith(
        2,
        expect.stringContaining('/docs/notion-sync.html#token'),
        'flowspace-notion-sync-help',
        expect.stringContaining('width=960')
      )
    } finally {
      openSpy.mockRestore()
    }
  })

  it('separates pushing FlowSpace notes from manually pulling Notion notes', async () => {
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue([
      {
        id: 'notion-1',
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
        is_default: true,
        created_at: 1,
        updated_at: 1,
      },
    ])
    renderPanel()
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: '同步到 Notion' })
    )

    expect(syncApi.pushTarget).toHaveBeenCalledWith('notion-1')
    expect(syncApi.syncNotionAll).not.toHaveBeenCalled()
    expect(syncApi.syncNotionPull).not.toHaveBeenCalled()
    expect(await screen.findByText('已同步 4')).toBeVisible()
    expect(screen.getByText('失败 1')).toBeVisible()

    await user.click(screen.getByRole('button', { name: '从 Notion 手动拉取' }))

    expect(syncApi.pullTarget).toHaveBeenCalledWith('notion-1')
    expect(syncApi.syncNotionPull).not.toHaveBeenCalled()
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
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue([
      {
        id: 'notion-1',
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
        is_default: true,
        created_at: 1,
        updated_at: 1,
      },
    ])
    vi.mocked(syncApi.getTargetDeletions).mockResolvedValue([
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
    expect(syncApi.restoreTargetDeletion).toHaveBeenCalledWith(
      'notion-1',
      'note-1'
    )

    await user.click(
      screen.getByRole('button', { name: '确认删除 FlowSpace 笔记' })
    )
    expect(syncApi.confirmTargetDeletion).toHaveBeenCalledWith(
      'notion-1',
      'note-1'
    )
  })

  it('uses target scoped push and pull', async () => {
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue([
      {
        id: 'notion-1',
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
        is_default: true,
        created_at: 1,
        updated_at: 1,
      },
    ])
    renderPanel()
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: '同步到 Notion' })
    )
    await waitFor(() =>
      expect(syncApi.pushTarget).toHaveBeenCalledWith('notion-1')
    )
    expect(syncApi.syncNotionAll).not.toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: '从 Notion 手动拉取' }))
    await waitFor(() =>
      expect(syncApi.pullTarget).toHaveBeenCalledWith('notion-1')
    )
    expect(syncApi.syncNotionPull).not.toHaveBeenCalled()
  })

  it('shows data source id as locked when backend reports lock', async () => {
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue([
      {
        id: 'notion-locked',
        type: 'notion',
        name: 'Locked Notion',
        vault_path: '',
        base_folder: '',
        config_json: JSON.stringify({
          data_source_id: 'ds-locked',
          token_env: 'FLOWSPACE_NOTION_TOKEN',
          title_property: 'Name',
          required_tags: ['sync'],
        }),
        enabled: true,
        auto_sync: false,
        is_default: true,
        created_at: 1,
        updated_at: 1,
      },
    ])
    vi.mocked(syncApi.saveSyncTarget).mockRejectedValueOnce(
      new APIError(409, 'target_identity_locked', 'locked')
    )
    renderPanel()
    const user = userEvent.setup()

    await waitFor(() =>
      expect(screen.getByLabelText('Data Source ID')).toHaveValue('ds-locked')
    )
    await user.clear(screen.getByLabelText('Data Source ID'))
    await user.type(screen.getByLabelText('Data Source ID'), 'ds-new')
    await user.click(screen.getByRole('button', { name: '保存 Notion 设置' }))

    expect(
      await screen.findByText('Data Source ID 已被使用中的同步目标锁定')
    ).toBeVisible()
  })
})
