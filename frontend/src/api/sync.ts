import { api } from './client'

export interface SyncTarget {
  id: string
  type: 'obsidian'
  name: string
  vault_path: string
  base_folder: string
  enabled: boolean
  auto_sync: boolean
  created_at: number
  updated_at: number
}

export interface SaveSyncTargetInput {
  id?: string
  name: string
  vault_path: string
  base_folder: string
  enabled: boolean
  auto_sync: boolean
}

export interface SyncState {
  note_id: string
  target_id: string
  external_path: string
  content_hash: string
  last_synced_at: number | null
  status: 'synced' | 'pending' | 'failed'
  error_message: string | null
}

export interface SyncResultItem {
  note_id: string
  status: string
  external_path?: string
  error_message?: string
}

export interface SyncBatchResult {
  synced: number
  failed: number
  items: SyncResultItem[]
}

export async function getSyncTargets(): Promise<SyncTarget[]> {
  const res = await api.get<{ targets: SyncTarget[] }>('/api/sync/targets')
  return res.data.targets
}

export async function saveSyncTarget(input: SaveSyncTargetInput): Promise<SyncTarget> {
  const { id, ...body } = input
  const res = id
    ? await api.patch<{ target: SyncTarget }>(`/api/sync/targets/${id}`, body)
    : await api.post<{ target: SyncTarget }>('/api/sync/targets', body)
  return res.data.target
}

export async function testObsidianTarget(input: SaveSyncTargetInput): Promise<void> {
  const { id: _id, ...body } = input
  await api.post<{ ok: boolean }>('/api/sync/obsidian/test', body)
}

export async function syncObsidianNote(id: string): Promise<SyncResultItem> {
  const res = await api.post<{ item: SyncResultItem }>(`/api/sync/obsidian/notes/${id}`)
  return res.data.item
}

export async function syncObsidianFolder(folderID: string): Promise<SyncBatchResult> {
  const res = await api.post<{ result: SyncBatchResult }>(`/api/sync/obsidian/folders/${folderID}`)
  return res.data.result
}

export async function syncObsidianAll(): Promise<SyncBatchResult> {
  const res = await api.post<{ result: SyncBatchResult }>('/api/sync/obsidian/all')
  return res.data.result
}

export async function getNoteSyncState(id: string): Promise<SyncState | null> {
  const res = await api.get<{ state: SyncState | null }>(`/api/notes/${id}/sync-state`)
  return res.data.state
}
