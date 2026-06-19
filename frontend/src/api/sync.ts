import { api } from './client'

export type SyncTargetType = 'obsidian' | 'notion'

export interface SyncTarget {
  id: string
  type: SyncTargetType
  name: string
  vault_path: string
  base_folder: string
  config_json: string
  enabled: boolean
  auto_sync: boolean
  is_default?: boolean
  created_at: number
  updated_at: number
}

export interface SaveSyncTargetInput {
  id?: string
  type?: SyncTargetType
  name: string
  vault_path: string
  base_folder: string
  config_json?: string
  enabled: boolean
  auto_sync: boolean
  is_default?: boolean
}

export type SyncStateStatus = 'synced' | 'pending' | 'failed' | 'external_deleted'

export interface SyncState {
  note_id: string
  target_id: string
  external_path: string
  external_id: string
  external_url: string
  content_hash: string
  external_hash: string
  external_mtime: number | null
  last_direction: string
  last_synced_at: number | null
  status: SyncStateStatus
  error_message: string | null
}

export interface SyncResultItem {
  note_id: string
  status: string
  external_path?: string
  external_id?: string
  external_url?: string
  error_message?: string
}

export interface ObsidianBidirectionalResult {
  pushed: number
  pulled: number
  imported: number
  external_deleted: number
  failed: number
  items: SyncResultItem[]
}

export interface NotionBidirectionalResult {
  pushed: number
  pulled: number
  conflict_pulled: number
  imported: number
  external_deleted: number
  unsupported: number
  failed: number
  items: SyncResultItem[]
}

export interface ExternalDeletedNote {
  note_id: string
  title: string
  external_path: string
  last_synced_at: number | null
}

export interface LocalDirectoryEntry {
  name: string
  path: string
  modified_at: number
}

export interface LocalDirectoryList {
  current_path: string
  parent_path?: string
  entries: LocalDirectoryEntry[]
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

export async function listLocalDirectories(path?: string): Promise<LocalDirectoryList> {
  const res = await api.get<{ directory: LocalDirectoryList }>('/api/system/directories', path ? { path } : undefined)
  return res.data.directory
}

export async function saveSyncTarget(input: SaveSyncTargetInput): Promise<SyncTarget> {
  const { id } = input
  const body = syncTargetPayload(input)
  const res = id
    ? await api.patch<{ target: SyncTarget }>(`/api/sync/targets/${id}`, body)
    : await api.post<{ target: SyncTarget }>('/api/sync/targets', body)
  return res.data.target
}

export async function testObsidianTarget(input: SaveSyncTargetInput): Promise<void> {
  const body = {
    name: input.name,
    vault_path: input.vault_path,
    base_folder: input.base_folder,
    enabled: input.enabled,
    auto_sync: input.auto_sync,
  }
  await api.post<{ ok: boolean }>('/api/sync/obsidian/test', body)
}

export async function testNotionTarget(input: SaveSyncTargetInput): Promise<void> {
  await api.post<{ ok: boolean }>('/api/sync/notion/test', syncTargetPayload(input))
}

export async function syncObsidianNote(id: string): Promise<SyncResultItem> {
  const res = await api.post<{ item: SyncResultItem }>(`/api/sync/obsidian/notes/${encodeURIComponent(id)}`)
  return res.data.item
}

export async function syncObsidianFolder(folderID: string): Promise<SyncBatchResult> {
  const res = await api.post<{ result: SyncBatchResult }>(`/api/sync/obsidian/folders/${encodeURIComponent(folderID)}`)
  return res.data.result
}

export async function syncObsidianAll(): Promise<SyncBatchResult> {
  const res = await api.post<{ result: SyncBatchResult }>('/api/sync/obsidian/all')
  return res.data.result
}

export async function syncObsidianPull(): Promise<ObsidianBidirectionalResult> {
  const res = await api.post<{ result: ObsidianBidirectionalResult }>('/api/sync/obsidian/pull')
  return res.data.result
}

export async function syncObsidianBidirectional(): Promise<ObsidianBidirectionalResult> {
  const res = await api.post<{ result: ObsidianBidirectionalResult }>('/api/sync/obsidian/bidirectional')
  return res.data.result
}

export async function syncNotionAll(): Promise<SyncBatchResult> {
  const res = await api.post<{ result: SyncBatchResult }>('/api/sync/notion/all')
  return res.data.result
}

export async function syncNotionPull(): Promise<NotionBidirectionalResult> {
  const res = await api.post<{ result: NotionBidirectionalResult }>('/api/sync/notion/pull')
  return res.data.result
}

export async function syncNotionBidirectional(): Promise<NotionBidirectionalResult> {
  const res = await api.post<{ result: NotionBidirectionalResult }>('/api/sync/notion/bidirectional')
  return res.data.result
}

export async function getObsidianDeletions(): Promise<ExternalDeletedNote[]> {
  const res = await api.get<{ items: ExternalDeletedNote[] }>('/api/sync/obsidian/deletions')
  return res.data.items
}

export async function getNotionDeletions(): Promise<ExternalDeletedNote[]> {
  const res = await api.get<{ items: ExternalDeletedNote[] }>('/api/sync/notion/deletions')
  return res.data.items
}

export async function confirmObsidianDeletion(noteID: string): Promise<void> {
  await api.post(`/api/sync/obsidian/deletions/${encodeURIComponent(noteID)}/confirm`)
}

export async function confirmNotionDeletion(noteID: string): Promise<void> {
  await api.post(`/api/sync/notion/deletions/${encodeURIComponent(noteID)}/confirm`)
}

export async function restoreObsidianDeletion(noteID: string): Promise<SyncResultItem> {
  const res = await api.post<{ item: SyncResultItem }>(`/api/sync/obsidian/deletions/${encodeURIComponent(noteID)}/restore`)
  return res.data.item
}

export async function restoreNotionDeletion(noteID: string): Promise<SyncResultItem> {
  const res = await api.post<{ item: SyncResultItem }>(`/api/sync/notion/deletions/${encodeURIComponent(noteID)}/restore`)
  return res.data.item
}

export async function getNoteSyncState(id: string, target?: SyncTargetType): Promise<SyncState | null> {
  const res = await api.get<{ state: SyncState | null }>(
    `/api/notes/${encodeURIComponent(id)}/sync-state`,
    target ? { target } : undefined,
  )
  return res.data.state
}

function syncTargetPayload(input: SaveSyncTargetInput) {
  const payload: {
    type: SyncTargetType | undefined
    name: string
    vault_path: string
    base_folder: string
    config_json: string | undefined
    enabled: boolean
    auto_sync: boolean
    is_default?: boolean
  } = {
    type: input.type,
    name: input.name,
    vault_path: input.vault_path,
    base_folder: input.base_folder,
    config_json: input.config_json,
    enabled: input.enabled,
    auto_sync: input.auto_sync,
  }
  if (input.is_default !== undefined) {
    payload.is_default = input.is_default
  }
  return payload
}
