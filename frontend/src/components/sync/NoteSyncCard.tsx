import { useNoteSyncState, useSyncObsidianNote, useSyncTargets } from '../../hooks/useSync'

function syncStatusLabel(status: string | undefined) {
  if (status === 'synced') return '已同步'
  if (status === 'failed') return '同步失败'
  if (status === 'pending') return '待同步'
  return '未同步'
}

export function NoteSyncCard({ noteID }: { noteID: string }) {
  const targetsQ = useSyncTargets()
  const stateQ = useNoteSyncState(noteID)
  const syncNote = useSyncObsidianNote(noteID)
  const target = targetsQ.data?.find((item) => item.type === 'obsidian')
  const state = stateQ.data
  const status = state?.status ?? 'unsynced'

  async function handleSync() {
    await syncNote.mutateAsync()
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
          <button type="button" className="secondary-action" onClick={handleSync} disabled={syncNote.isPending}>
            {syncNote.isPending ? '同步中' : '同步当前笔记'}
          </button>
        </>
      ) : (
        <p>{targetsQ.isLoading ? '正在读取同步配置' : '还没有配置 Obsidian Vault'}</p>
      )}
    </div>
  )
}
