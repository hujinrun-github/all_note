import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as syncApi from '../api/sync'
import {
  useBidirectionalTarget,
  useConfirmNotionDeletion,
  useConfirmTargetDeletion,
  useDeleteNoteSyncBinding,
  useNoteSyncState,
  useNoteSyncBinding,
  useNotionDeletions,
  usePullTarget,
  usePushTarget,
  usePutNoteSyncBinding,
  useRestoreNotionDeletion,
  useRestoreTargetDeletion,
  useSyncNote,
  useSyncNotionBidirectional,
  useTargetDeletions,
  useTestNotionTarget,
} from './useSync'

vi.mock('../api/sync', () => ({
  getSyncTargets: vi.fn(),
  saveSyncTarget: vi.fn(),
  testObsidianTarget: vi.fn(),
  getNoteSyncBinding: vi.fn(),
  putNoteSyncBinding: vi.fn(),
  deleteNoteSyncBinding: vi.fn(),
  getNoteSyncState: vi.fn(),
  getObsidianDeletions: vi.fn(),
  syncNote: vi.fn(),
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
  pushTarget: vi.fn(),
  pullTarget: vi.fn(),
  bidirectionalTarget: vi.fn(),
  getTargetDeletions: vi.fn(),
  confirmTargetDeletion: vi.fn(),
  restoreTargetDeletion: vi.fn(),
  deleteSyncTarget: vi.fn(),
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

  it('keys note sync state by target and passes the target to the api', async () => {
    vi.mocked(syncApi.getNoteSyncState).mockResolvedValue({
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
    })
    const queryClient = createQueryClient()

    const { result } = renderHook(() => useNoteSyncState('note-1', 'notion'), {
      wrapper: createWrapper(queryClient),
    })

    await waitFor(() => expect(result.current.data?.external_id).toBe('page-1'))
    expect(syncApi.getNoteSyncState).toHaveBeenCalledWith('note-1', 'notion')
    expect(queryClient.getQueryData(['note-sync-state', 'note-1', 'notion'])).toEqual(result.current.data)
    expect(queryClient.getQueryData(['note-sync-state', 'note-1'])).toBeUndefined()
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

  it('exposes note sync binding query and mutations', async () => {
    vi.mocked(syncApi.getNoteSyncBinding).mockResolvedValue({
      binding: { note_id: 'note-1', target_id: 'target-1', created_at: 1, updated_at: 2 },
      target: {
        id: 'target-1',
        type: 'notion',
        name: 'Notion',
        vault_path: '',
        base_folder: '',
        config_json: '{}',
        enabled: true,
        auto_sync: false,
        is_default: true,
        created_at: 1,
        updated_at: 2,
      },
      candidates: [],
    })
    vi.mocked(syncApi.putNoteSyncBinding).mockResolvedValue({
      binding: { note_id: 'note-1', target_id: 'target-2', created_at: 1, updated_at: 3 },
      target: {
        id: 'target-2',
        type: 'obsidian',
        name: 'Vault',
        vault_path: 'D:/vault',
        base_folder: 'FlowSpace',
        config_json: '{}',
        enabled: true,
        auto_sync: false,
        is_default: false,
        created_at: 1,
        updated_at: 3,
      },
      changed_target: true,
    })
    vi.mocked(syncApi.deleteNoteSyncBinding).mockResolvedValue(undefined)
    vi.mocked(syncApi.syncNote).mockResolvedValue({ note_id: 'note-1', status: 'synced' })
    const queryClient = createQueryClient()

    const binding = renderHook(() => useNoteSyncBinding('note-1'), { wrapper: createWrapper(queryClient) })
    await waitFor(() => expect(binding.result.current.data?.binding?.target_id).toBe('target-1'))

    const putBinding = renderHook(() => usePutNoteSyncBinding('note-1'), { wrapper: createWrapper(queryClient) })
    await act(async () => {
      await putBinding.result.current.mutateAsync({
        target_id: 'target-2',
        expected_target_id: 'target-1',
        confirm_changed_target: true,
      })
    })

    const deleteBinding = renderHook(() => useDeleteNoteSyncBinding('note-1'), { wrapper: createWrapper(queryClient) })
    await act(async () => {
      await deleteBinding.result.current.mutateAsync({ expected_target_id: 'target-2', expected_updated_at: 3 })
    })

    const syncNote = renderHook(() => useSyncNote('note-1'), { wrapper: createWrapper(queryClient) })
    await act(async () => {
      await syncNote.result.current.mutateAsync()
    })

    expect(syncApi.getNoteSyncBinding).toHaveBeenCalledWith('note-1')
    expect(syncApi.putNoteSyncBinding).toHaveBeenCalledWith('note-1', {
      target_id: 'target-2',
      expected_target_id: 'target-1',
      confirm_changed_target: true,
    })
    expect(syncApi.deleteNoteSyncBinding).toHaveBeenCalledWith('note-1', {
      expected_target_id: 'target-2',
      expected_updated_at: 3,
    })
    expect(syncApi.syncNote).toHaveBeenCalledWith('note-1')
  })

  it('exposes target scoped sync and deletion hooks', async () => {
    vi.mocked(syncApi.pushTarget).mockResolvedValue({ synced: 1, failed: 0, items: [] })
    vi.mocked(syncApi.pullTarget).mockResolvedValue({ pulled: 1, pushed: 0, imported: 0, external_deleted: 0, failed: 0, items: [] })
    vi.mocked(syncApi.bidirectionalTarget).mockResolvedValue({
      pulled: 1,
      pushed: 1,
      imported: 0,
      external_deleted: 0,
      failed: 0,
      items: [],
    })
    vi.mocked(syncApi.getTargetDeletions).mockResolvedValue([{ note_id: 'note-1', title: 'Deleted', external_path: 'notion:page', last_synced_at: 1 }])
    vi.mocked(syncApi.confirmTargetDeletion).mockResolvedValue(undefined)
    vi.mocked(syncApi.restoreTargetDeletion).mockResolvedValue({ note_id: 'note-1', status: 'synced' })
    const queryClient = createQueryClient()

    const deletions = renderHook(() => useTargetDeletions('target-1'), { wrapper: createWrapper(queryClient) })
    await waitFor(() => expect(deletions.result.current.data?.[0]?.note_id).toBe('note-1'))

    const push = renderHook(() => usePushTarget(), { wrapper: createWrapper(queryClient) })
    await act(async () => {
      await push.result.current.mutateAsync('target-1')
    })

    const pull = renderHook(() => usePullTarget(), { wrapper: createWrapper(queryClient) })
    await act(async () => {
      await pull.result.current.mutateAsync('target-1')
    })

    const bidirectional = renderHook(() => useBidirectionalTarget(), { wrapper: createWrapper(queryClient) })
    await act(async () => {
      await bidirectional.result.current.mutateAsync('target-1')
    })

    const confirm = renderHook(() => useConfirmTargetDeletion(), { wrapper: createWrapper(queryClient) })
    await act(async () => {
      await confirm.result.current.mutateAsync({ targetID: 'target-1', noteID: 'note-1' })
    })

    const restore = renderHook(() => useRestoreTargetDeletion(), { wrapper: createWrapper(queryClient) })
    await act(async () => {
      await restore.result.current.mutateAsync({ targetID: 'target-1', noteID: 'note-1' })
    })

    expect(syncApi.pushTarget).toHaveBeenCalledWith('target-1')
    expect(syncApi.pullTarget).toHaveBeenCalledWith('target-1')
    expect(syncApi.bidirectionalTarget).toHaveBeenCalledWith('target-1')
    expect(syncApi.getTargetDeletions).toHaveBeenCalledWith('target-1')
    expect(syncApi.confirmTargetDeletion).toHaveBeenCalledWith('target-1', 'note-1')
    expect(syncApi.restoreTargetDeletion).toHaveBeenCalledWith('target-1', 'note-1')
  })
})
