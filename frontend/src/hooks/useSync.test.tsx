import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as syncApi from '../api/sync'
import {
  useConfirmNotionDeletion,
  useNotionDeletions,
  useRestoreNotionDeletion,
  useSyncNotionBidirectional,
  useTestNotionTarget,
} from './useSync'

vi.mock('../api/sync', () => ({
  getSyncTargets: vi.fn(),
  saveSyncTarget: vi.fn(),
  testObsidianTarget: vi.fn(),
  getNoteSyncState: vi.fn(),
  getObsidianDeletions: vi.fn(),
  syncObsidianNote: vi.fn(),
  syncObsidianFolder: vi.fn(),
  syncObsidianAll: vi.fn(),
  syncObsidianBidirectional: vi.fn(),
  confirmObsidianDeletion: vi.fn(),
  restoreObsidianDeletion: vi.fn(),
  testNotionTarget: vi.fn(),
  syncNotionBidirectional: vi.fn(),
  getNotionDeletions: vi.fn(),
  confirmNotionDeletion: vi.fn(),
  restoreNotionDeletion: vi.fn(),
}))

function createWrapper(queryClient: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  }
}

function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
}

describe('notion sync hooks', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('tests a notion target through the sync api', async () => {
    vi.mocked(syncApi.testNotionTarget).mockResolvedValue(undefined)
    const queryClient = createQueryClient()
    const input = {
      type: 'notion' as const,
      name: 'Personal Notion',
      vault_path: '',
      base_folder: '',
      config_json: JSON.stringify({ data_source_id: 'ds-123' }),
      enabled: true,
      auto_sync: false,
    }

    const { result } = renderHook(() => useTestNotionTarget(), { wrapper: createWrapper(queryClient) })
    await act(async () => {
      await result.current.mutateAsync(input)
    })

    expect(syncApi.testNotionTarget).toHaveBeenCalledWith(input)
  })

  it('loads notion deletion candidates', async () => {
    const items = [{ note_id: 'note-1', title: 'Deleted', external_path: 'notion:page-1', last_synced_at: 1 }]
    vi.mocked(syncApi.getNotionDeletions).mockResolvedValue(items)
    const queryClient = createQueryClient()

    const { result } = renderHook(() => useNotionDeletions(), { wrapper: createWrapper(queryClient) })

    await waitFor(() => expect(result.current.data).toEqual(items))
    expect(syncApi.getNotionDeletions).toHaveBeenCalled()
  })

  it('invalidates notion sync queries after mutations', async () => {
    vi.mocked(syncApi.syncNotionBidirectional).mockResolvedValue({
      pushed: 1,
      pulled: 0,
      conflict_pulled: 0,
      imported: 0,
      external_deleted: 0,
      unsupported: 0,
      failed: 0,
      items: [],
    })
    vi.mocked(syncApi.confirmNotionDeletion).mockResolvedValue(undefined)
    vi.mocked(syncApi.restoreNotionDeletion).mockResolvedValue({ note_id: 'note-1', status: 'restored' })
    const queryClient = createQueryClient()
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    const bidirectional = renderHook(() => useSyncNotionBidirectional(), { wrapper: createWrapper(queryClient) })
    await act(async () => {
      await bidirectional.result.current.mutateAsync()
    })

    const confirm = renderHook(() => useConfirmNotionDeletion(), { wrapper: createWrapper(queryClient) })
    await act(async () => {
      await confirm.result.current.mutateAsync('note-1')
    })

    const restore = renderHook(() => useRestoreNotionDeletion(), { wrapper: createWrapper(queryClient) })
    await act(async () => {
      await restore.result.current.mutateAsync('note-1')
    })

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['notes'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['note'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['note-sync-state'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['notion-deletions'] })
    expect(syncApi.confirmNotionDeletion).toHaveBeenCalledWith('note-1')
    expect(syncApi.restoreNotionDeletion).toHaveBeenCalledWith('note-1')
  })
})
