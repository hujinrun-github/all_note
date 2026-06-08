import { useState } from 'react'
import { useNoteSyncState, useRestoreObsidianDeletion, useSyncObsidianNote, useSyncTargets } from '../../hooks/useSync'

function syncStatusLabel(status: string | undefined) {
  if (status === 'synced') return '已同步'
  if (status === 'failed') return '同步失败'
  if (status === 'external_deleted') return 'Obsidian 已删除'
  if (status === 'pending') return '待同步'
  return '未同步'
}

export function NoteSyncCard({ noteID }: { noteID: string }) {
  const targetsQ = useSyncTargets()
  const stateQ = useNoteSyncState(noteID)
  const syncNote = useSyncObsidianNote(noteID)
  const restoreDeletion = useRestoreObsidianDeletion()
  const [restoreError, setRestoreError] = useState<string | null>(null)
  const target = targetsQ.data?.find((item) => item.type === 'obsidian')
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
        <strong className={`sync-card-status sync-card-status-${status}`}>{syncStatusLabel(state?.status)}</strong>
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
        <p>{targetsQ.isLoading ? '正在读取同步配置' : '还没有配置 Obsidian Vault'}</p>
      )}
    </div>
  )
}
