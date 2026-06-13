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
    </QueryClientProvider>,
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
    vi.mocked(syncApi.restoreNotionDeletion).mockResolvedValue({ note_id: 'note-1', status: 'restored' })
  })

  it('saves notion config with data source id and token env but no raw token', async () => {
    renderPanel()
    const user = userEvent.setup()

    await user.type(await screen.findByLabelText('Data Source ID'), 'ds-123')
    expect(screen.getByLabelText('Token environment variable')).toHaveValue('FLOWSPACE_NOTION_TOKEN')
    expect(screen.getByLabelText('Title property')).toHaveValue('Name')

    await user.click(screen.getByRole('button', { name: 'Save Notion settings' }))

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
      }),
    )
    expect(JSON.parse(payload.config_json ?? '{}')).toEqual({
      data_source_id: 'ds-123',
      token_env: 'FLOWSPACE_NOTION_TOKEN',
      title_property: 'Name',
    })
    expect(JSON.stringify(payload)).not.toContain('secret')
    expect(payload).not.toHaveProperty('token')
  })

  it('runs notion bidirectional sync and shows summary counts', async () => {
    renderPanel()
    const user = userEvent.setup()

    await user.click(await screen.findByRole('button', { name: 'Run Notion bidirectional sync' }))

    expect(syncApi.syncNotionBidirectional).toHaveBeenCalledTimes(1)
    expect(await screen.findByText('Imported 3')).toBeVisible()
    expect(screen.getByText('Pulled 2')).toBeVisible()
    expect(screen.getByText('Conflict pulled 1')).toBeVisible()
    expect(screen.getByText('Pushed 4')).toBeVisible()
    expect(screen.getByText('Unsupported 5')).toBeVisible()
    expect(screen.getByText('External deleted 6')).toBeVisible()
    expect(screen.getByText('Failed 7')).toBeVisible()
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

    expect(await screen.findByText('Deleted Notion Note')).toBeVisible()
    expect(screen.getByText('notion:page-1')).toBeVisible()

    await user.click(screen.getByRole('button', { name: 'Restore Notion page' }))
    expect(syncApi.restoreNotionDeletion).toHaveBeenCalledWith('note-1')

    await user.click(screen.getByRole('button', { name: 'Confirm FlowSpace deletion' }))
    expect(syncApi.confirmNotionDeletion).toHaveBeenCalledWith('note-1')
  })
})
