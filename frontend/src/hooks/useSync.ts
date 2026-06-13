import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import * as syncApi from '../api/sync'

export function useSyncTargets() {
  return useQuery({
    queryKey: ['sync-targets'],
    queryFn: syncApi.getSyncTargets,
  })
}

export function useSaveSyncTarget() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: syncApi.saveSyncTarget,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['sync-targets'] }),
  })
}

export function useTestObsidianTarget() {
  return useMutation({ mutationFn: syncApi.testObsidianTarget })
}

export function useTestNotionTarget() {
  return useMutation({ mutationFn: (input: syncApi.SaveSyncTargetInput) => syncApi.testNotionTarget(input) })
}

export function useNoteSyncState(noteID: string | undefined, target?: syncApi.SyncTargetType) {
  return useQuery({
    queryKey: ['note-sync-state', noteID, target ?? 'obsidian'],
    queryFn: () => syncApi.getNoteSyncState(noteID!, target),
    enabled: Boolean(noteID),
  })
}

export function useObsidianDeletions() {
  return useQuery({
    queryKey: ['obsidian-deletions'],
    queryFn: syncApi.getObsidianDeletions,
  })
}

export function useNotionDeletions() {
  return useQuery({
    queryKey: ['notion-deletions'],
    queryFn: syncApi.getNotionDeletions,
  })
}

export function useSyncObsidianNote(noteID: string | undefined) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => syncApi.syncObsidianNote(noteID!),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['note-sync-state', noteID] })
      qc.invalidateQueries({ queryKey: ['sync-targets'] })
    },
  })
}

export function useSyncObsidianFolder() {
  return useMutation({ mutationFn: syncApi.syncObsidianFolder })
}

export function useSyncObsidianAll() {
  return useMutation({ mutationFn: syncApi.syncObsidianAll })
}

export function useSyncObsidianBidirectional() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: syncApi.syncObsidianBidirectional,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['notes'] })
      qc.invalidateQueries({ queryKey: ['note'] })
      qc.invalidateQueries({ queryKey: ['note-sync-state'] })
      qc.invalidateQueries({ queryKey: ['obsidian-deletions'] })
    },
  })
}

export function useSyncNotionBidirectional() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => syncApi.syncNotionBidirectional(),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['notes'] })
      qc.invalidateQueries({ queryKey: ['note'] })
      qc.invalidateQueries({ queryKey: ['note-sync-state'] })
      qc.invalidateQueries({ queryKey: ['notion-deletions'] })
    },
  })
}

export function useConfirmObsidianDeletion() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: syncApi.confirmObsidianDeletion,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['notes'] })
      qc.invalidateQueries({ queryKey: ['obsidian-deletions'] })
      qc.invalidateQueries({ queryKey: ['note-sync-state'] })
    },
  })
}

export function useConfirmNotionDeletion() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (noteID: string) => syncApi.confirmNotionDeletion(noteID),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['notes'] })
      qc.invalidateQueries({ queryKey: ['notion-deletions'] })
      qc.invalidateQueries({ queryKey: ['note-sync-state'] })
    },
  })
}

export function useRestoreObsidianDeletion() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: syncApi.restoreObsidianDeletion,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['obsidian-deletions'] })
      qc.invalidateQueries({ queryKey: ['note-sync-state'] })
    },
  })
}

export function useRestoreNotionDeletion() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (noteID: string) => syncApi.restoreNotionDeletion(noteID),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['notion-deletions'] })
      qc.invalidateQueries({ queryKey: ['note-sync-state'] })
    },
  })
}
