import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { APIError } from '../../api/client'
import type { NoteSyncBindingResponse, SyncTarget } from '../../api/sync'
import * as syncApi from '../../api/sync'
import { NoteSyncCard } from './NoteSyncCard'

vi.mock('../../api/sync')

const targets: SyncTarget[] = [
  {
    id: 'obs-1',
    type: 'obsidian',
    name: 'Vault',
    vault_path: 'D:\\Vault',
    base_folder: 'FlowSpace Notes',
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
    config_json: '{"data_source_id":"ds-123"}',
    enabled: true,
    auto_sync: false,
    is_default: true,
    created_at: 1,
    updated_at: 1,
  },
]

function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
}

function renderCard() {
  const queryClient = createQueryClient()
  return render(<NoteSyncCard noteID="note-1" />, {
    wrapper: ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    ),
  })
}

async function findReadySelector() {
  await screen.findByRole('option', { name: 'Vault（Obsidian）' })
  return screen.getByRole('combobox', { name: '同步目标' })
}

function mockBinding(response: NoteSyncBindingResponse) {
  vi.mocked(syncApi.getNoteSyncBinding).mockResolvedValue(response)
}

function unboundResponse(): NoteSyncBindingResponse {
  return {
    binding: null,
    candidates: targets.map((target) => ({ target })),
  }
}

function boundResponse(target = targets[0]): NoteSyncBindingResponse {
  return {
    binding: { note_id: 'note-1', target_id: target.id, created_at: 10, updated_at: 77 },
    target,
    state: {
      note_id: 'note-1',
      target_id: target.id,
      external_path: target.type === 'notion' ? 'notion:page-1' : 'D:\\Vault\\FlowSpace Notes\\note.md',
      external_id: target.type === 'notion' ? 'page-1' : '',
      external_url: target.type === 'notion' ? 'https://www.notion.so/page-1' : '',
      content_hash: 'flow',
      external_hash: 'remote',
      external_mtime: 1800000000,
      last_direction: 'push',
      last_synced_at: 1800000000,
      status: 'synced',
      error_message: null,
    },
    candidates: targets.map((item) => ({ target: item })),
  }
}

describe('NoteSyncCard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue(targets)
    mockBinding(unboundResponse())
    vi.mocked(syncApi.putNoteSyncBinding).mockResolvedValue({
      binding: { note_id: 'note-1', target_id: 'notion-1', created_at: 10, updated_at: 88 },
      target: targets[1],
      changed_target: false,
    })
    vi.mocked(syncApi.deleteNoteSyncBinding).mockResolvedValue(undefined)
    vi.mocked(syncApi.syncNote).mockResolvedValue({ note_id: 'note-1', status: 'synced' })
    vi.mocked(syncApi.getNoteSyncState).mockResolvedValue(null)
    vi.mocked(syncApi.syncObsidianNote).mockResolvedValue({ note_id: 'note-1', status: 'synced' })
    vi.mocked(syncApi.restoreObsidianDeletion).mockResolvedValue({ note_id: 'note-1', status: 'restored' })
    vi.mocked(syncApi.restoreNotionDeletion).mockResolvedValue({ note_id: 'note-1', status: 'restored' })
  })

  it('renders single sync target selector', async () => {
    renderCard()

    const selector = await findReadySelector()
    expect(selector).toBeVisible()
    expect(screen.getAllByRole('combobox')).toHaveLength(1)
    expect(screen.getByRole('option', { name: '不同步' })).toBeVisible()
    expect(screen.getByRole('option', { name: 'Vault（Obsidian）' })).toBeVisible()
    expect(screen.getByRole('option', { name: 'Personal Notion（Notion）' })).toBeVisible()
  })

  it('unbound note defaults to do not sync', async () => {
    renderCard()

    expect(await findReadySelector()).toHaveValue('__none__')
    expect(screen.getByText('当前不同步')).toBeVisible()
  })

  it('selecting target creates binding', async () => {
    renderCard()

    await userEvent.selectOptions(await findReadySelector(), 'notion-1')

    await waitFor(() =>
      expect(syncApi.putNoteSyncBinding).toHaveBeenCalledWith('note-1', {
        target_id: 'notion-1',
      }),
    )
  })

  it('changing target requires confirmation', async () => {
    mockBinding(boundResponse(targets[0]))
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValueOnce(false).mockReturnValueOnce(true)
    renderCard()

    const selector = await findReadySelector()
    await waitFor(() => expect(selector).toHaveValue('obs-1'))
    await userEvent.selectOptions(selector, 'notion-1')
    expect(syncApi.putNoteSyncBinding).not.toHaveBeenCalled()

    await userEvent.selectOptions(selector, 'notion-1')
    await waitFor(() =>
      expect(syncApi.putNoteSyncBinding).toHaveBeenCalledWith('note-1', {
        target_id: 'notion-1',
        expected_target_id: 'obs-1',
        confirm_changed_target: true,
      }),
    )

    confirmSpy.mockRestore()
  })

  it('choosing do not sync deletes binding with expected fields', async () => {
    mockBinding(boundResponse(targets[0]))
    renderCard()

    const selector = await findReadySelector()
    await waitFor(() => expect(selector).toHaveValue('obs-1'))
    await userEvent.selectOptions(selector, '__none__')

    await waitFor(() =>
      expect(syncApi.deleteNoteSyncBinding).toHaveBeenCalledWith('note-1', {
        expected_target_id: 'obs-1',
        expected_updated_at: 77,
      }),
    )
  })

  it('sync button uses unified note endpoint', async () => {
    mockBinding(boundResponse(targets[0]))
    renderCard()

    await userEvent.click(await screen.findByRole('button', { name: '同步此笔记' }))

    await waitFor(() => expect(syncApi.syncNote).toHaveBeenCalledWith('note-1'))
    expect(syncApi.syncObsidianNote).not.toHaveBeenCalled()
  })

  it('mismatch response is shown in Chinese', async () => {
    vi.mocked(syncApi.putNoteSyncBinding).mockRejectedValue(
      new APIError(409, 'binding_mismatch', 'note is bound to another sync target'),
    )
    renderCard()

    await userEvent.selectOptions(await findReadySelector(), 'notion-1')

    expect(await screen.findByText('这篇笔记已经绑定到其他同步目标，请刷新后再试')).toBeVisible()
  })
})
