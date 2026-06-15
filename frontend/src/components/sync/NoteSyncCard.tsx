import { useState } from 'react'
import type { SyncState, SyncTarget } from '../../api/sync'
import {
  useNoteSyncState,
  useRestoreNotionDeletion,
  useRestoreObsidianDeletion,
  useSyncObsidianNote,
  useSyncTargets,
} from '../../hooks/useSync'

function obsidianStatusLabel(status: string | undefined) {
  if (status === 'synced') return '已同步'
  if (status === 'failed') return '同步失败'
  if (status === 'external_deleted') return 'Obsidian 已删除'
  if (status === 'pending') return '待同步'
  return '未同步'
}

function notionStatusLabel(status: string | undefined) {
  if (status === 'synced') return 'Synced'
  if (status === 'failed') return 'Sync failed'
  if (status === 'external_deleted') return 'Notion deleted'
  if (status === 'pending') return 'Pending sync'
  return 'Not synced'
}

export function NoteSyncCard({ noteID }: { noteID: string }) {
  const targetsQ = useSyncTargets()
  const obsidianTarget = targetsQ.data?.find((item) => item.type === 'obsidian')
  const notionTarget = targetsQ.data?.find((item) => item.type === 'notion')

  return (
    <>
      <ObsidianSyncCard noteID={noteID} target={obsidianTarget} targetsLoading={targetsQ.isLoading} />
      {notionTarget && <NotionSyncCard noteID={noteID} target={notionTarget} />}
    </>
  )
}

function ObsidianSyncCard({
  noteID,
  target,
  targetsLoading,
}: {
  noteID: string
  target: SyncTarget | undefined
  targetsLoading: boolean
}) {
  const stateQ = useNoteSyncState(noteID)
  const syncNote = useSyncObsidianNote(noteID)
  const restoreDeletion = useRestoreObsidianDeletion()
  const [restoreError, setRestoreError] = useState<string | null>(null)
  const state = stateQ.data
  const status = state?.status ?? 'unsynced'
  const isExternalDeleted = state?.status === 'external_deleted'

  async function handleSync() {
    await syncNote.mutateAsync()
  }

  async function handleRestore() {
    setRestoreError(null)
    try {
      await restoreDeletion.mutateAsync(noteID)
    } catch {
      setRestoreError('重新导出失败，请先执行双向同步后再试')
    }
  }

  return (
    <div className="sync-card">
      <div className="sync-card-header">
        <span>Obsidian</span>
        <strong className={`sync-card-status sync-card-status-${status}`}>{obsidianStatusLabel(state?.status)}</strong>
      </div>
      {target ? (
        <>
          <p>{target.vault_path}</p>
          {state?.external_path && <code>{state.external_path}</code>}
          {state?.error_message && <em>{state.error_message}</em>}
          {isExternalDeleted ? (
            <>
              <p>这篇笔记在 Obsidian 中已删除，FlowSpace 正在等待确认。</p>
              <button type="button" className="secondary-action" onClick={handleRestore} disabled={restoreDeletion.isPending}>
                {restoreDeletion.isPending ? '重新导出中' : '保留并重新导出'}
              </button>
              {restoreError && <em>{restoreError}</em>}
            </>
          ) : (
            <button type="button" className="secondary-action" onClick={handleSync} disabled={syncNote.isPending}>
              {syncNote.isPending ? '同步中' : '同步当前笔记'}
            </button>
          )}
        </>
      ) : (
        <p>{targetsLoading ? '正在读取同步配置' : '还没有配置 Obsidian Vault'}</p>
      )}
    </div>
  )
}

function NotionSyncCard({ noteID, target }: { noteID: string; target: SyncTarget }) {
  const stateQ = useNoteSyncState(noteID, 'notion')
  const restoreDeletion = useRestoreNotionDeletion()
  const [restoreError, setRestoreError] = useState<string | null>(null)
  const state = stateQ.data
  const status = state?.status ?? 'unsynced'
  const isExternalDeleted = state?.status === 'external_deleted'

  async function handleRestore() {
    setRestoreError(null)
    try {
      await restoreDeletion.mutateAsync(noteID)
    } catch {
      setRestoreError('Restore to Notion failed. Run bidirectional sync and try again.')
    }
  }

  return (
    <div className="sync-card">
      <div className="sync-card-header">
        <span>Notion</span>
        <strong className={`sync-card-status sync-card-status-${status}`}>{notionStatusLabel(state?.status)}</strong>
      </div>
      <p>{target.name}</p>
      <NotionExternalReference state={state} />
      {state?.error_message && <em>{state.error_message}</em>}
      {isExternalDeleted && (
        <>
          <p>This note was deleted in Notion. FlowSpace is waiting for deletion confirmation.</p>
          <button type="button" className="secondary-action" onClick={handleRestore} disabled={restoreDeletion.isPending}>
            {restoreDeletion.isPending ? 'Restoring to Notion' : 'Restore to Notion'}
          </button>
          {restoreError && <em>{restoreError}</em>}
        </>
      )}
    </div>
  )
}

function NotionExternalReference({ state }: { state: SyncState | null | undefined }) {
  if (!state?.external_path && !state?.external_url) return null

  return (
    <>
      {state.external_path && <code>{state.external_path}</code>}
      {state.external_url && (
        <a href={state.external_url} target="_blank" rel="noreferrer">
          Open Notion page
        </a>
      )}
    </>
  )
}
