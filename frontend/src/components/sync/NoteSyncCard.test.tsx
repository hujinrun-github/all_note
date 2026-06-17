import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as syncApi from '../../api/sync'
import { NoteSyncCard } from './NoteSyncCard'

vi.mock('../../api/sync')

function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
}

function renderCard() {
  return render(
    <QueryClientProvider client={createQueryClient()}>
      <NoteSyncCard noteID="note-1" />
    </QueryClientProvider>,
  )
}

function mockTargets() {
  vi.mocked(syncApi.getSyncTargets).mockResolvedValue([
    {
      id: 'obs-1',
      type: 'obsidian',
      name: 'Vault',
      vault_path: 'D:\\Vault',
      base_folder: 'FlowSpace Notes',
      config_json: '{}',
      enabled: true,
      auto_sync: false,
      created_at: 1,
      updated_at: 1,
    },
    {
      id: 'notion-1',
      type: 'notion',
      name: 'Personal Notion',
      vault_path: '',
      base_folder: '',
      config_json: '{"data_source_id":"ds-123"}',
      enabled: true,
      auto_sync: false,
      created_at: 1,
      updated_at: 1,
    },
  ])
}

describe('NoteSyncCard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockTargets()
    vi.mocked(syncApi.getNoteSyncState).mockResolvedValue(null)
    vi.mocked(syncApi.syncObsidianNote).mockResolvedValue({ note_id: 'note-1', status: 'synced' })
    vi.mocked(syncApi.restoreObsidianDeletion).mockResolvedValue({ note_id: 'note-1', status: 'restored' })
    vi.mocked(syncApi.restoreNotionDeletion).mockResolvedValue({ note_id: 'note-1', status: 'restored' })
  })

  it('renders obsidian and notion target cards when both targets exist', async () => {
    renderCard()

    expect(await screen.findByText('Obsidian')).toBeVisible()
    expect(await screen.findByText('Notion')).toBeVisible()
  })

  it('shows notion synced state with status and page link', async () => {
    vi.mocked(syncApi.getNoteSyncState).mockImplementation(async (_id, target) => {
      if (target === 'notion') {
        return {
          note_id: 'note-1',
          target_id: 'notion-1',
          external_path: 'notion:page-1',
          external_id: 'page-1',
          external_url: 'https://www.notion.so/page-1',
          content_hash: 'flow',
          external_hash: 'notion',
          external_mtime: 1800000000,
          last_direction: 'pull',
          last_synced_at: 1800000000,
          status: 'synced',
          error_message: null,
        }
      }
      return null
    })

    renderCard()

    expect(await screen.findByText('Notion')).toBeVisible()
    expect(await screen.findByText('已同步')).toBeVisible()
    expect(await screen.findByRole('link', { name: '打开 Notion 页面' })).toHaveAttribute(
      'href',
      'https://www.notion.so/page-1',
    )
  })

  it('shows notion deletion state with restore action without hiding obsidian card', async () => {
    vi.mocked(syncApi.getNoteSyncState).mockImplementation(async (_id, target) => {
      if (target === 'notion') {
        return {
          note_id: 'note-1',
          target_id: 'notion-1',
          external_path: 'notion:page-1',
          external_id: 'page-1',
          external_url: 'https://www.notion.so/page-1',
          content_hash: 'flow',
          external_hash: 'notion',
          external_mtime: 1800000000,
          last_direction: 'delete_detected',
          last_synced_at: 1800000000,
          status: 'external_deleted',
          error_message: null,
        }
      }
      return null
    })

    renderCard()

    expect(await screen.findByText('Notion 已删除')).toBeVisible()
    expect(screen.getByText('Obsidian')).toBeVisible()
    expect(screen.getByRole('button', { name: '恢复到 Notion' })).toBeVisible()
  })

  it('loads sync state separately for obsidian and notion targets', async () => {
    renderCard()

    await waitFor(() => expect(syncApi.getNoteSyncState).toHaveBeenCalledWith('note-1', 'notion'))
    expect(syncApi.getNoteSyncState).toHaveBeenCalledWith('note-1', undefined)
  })
})
