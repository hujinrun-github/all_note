import { useEffect, useMemo, useState } from 'react'
import {
  useSaveSyncTarget,
  useSyncObsidianAll,
  useSyncTargets,
  useTestObsidianTarget,
} from '../../hooks/useSync'

type SyncMessage = {
  tone: 'neutral' | 'success' | 'error'
  text: string
}

export function ObsidianSyncPanel({ onClose }: { onClose: () => void }) {
  const targetsQ = useSyncTargets()
  const saveTarget = useSaveSyncTarget()
  const testTarget = useTestObsidianTarget()
  const syncAll = useSyncObsidianAll()
  const target = useMemo(() => targetsQ.data?.find((item) => item.type === 'obsidian'), [targetsQ.data])

  const [name, setName] = useState('Obsidian Vault')
  const [vaultPath, setVaultPath] = useState('')
  const [baseFolder, setBaseFolder] = useState('FlowSpace Notes')
  const [autoSync, setAutoSync] = useState(false)
  const [message, setMessage] = useState<SyncMessage | null>(null)

  useEffect(() => {
    if (!target) return
    setName(target.name)
    setVaultPath(target.vault_path)
    setBaseFolder(target.base_folder)
    setAutoSync(target.auto_sync)
  }, [target])

  const payload = {
    id: target?.id,
    name,
    vault_path: vaultPath,
    base_folder: baseFolder,
    enabled: true,
    auto_sync: autoSync,
  }
  const isBusy = saveTarget.isPending || testTarget.isPending || syncAll.isPending

  async function handleSave() {
    setMessage(null)
    try {
      await saveTarget.mutateAsync(payload)
      setMessage({ tone: 'success', text: '同步设置已保存' })
    } catch {
      setMessage({ tone: 'error', text: '保存失败，请检查路径和后端服务' })
    }
  }

  async function handleTest() {
    setMessage(null)
    try {
      await testTarget.mutateAsync(payload)
      setMessage({ tone: 'success', text: '路径可用' })
    } catch {
      setMessage({ tone: 'error', text: '路径不可用或没有写入权限' })
    }
  }

  async function handleSyncAll() {
    setMessage(null)
    try {
      const result = await syncAll.mutateAsync()
      setMessage({ tone: 'success', text: `同步完成：成功 ${result.synced}，失败 ${result.failed}` })
    } catch {
      setMessage({ tone: 'error', text: '同步失败，请先保存并测试路径' })
    }
  }

  return (
    <div className="sync-overlay" onClick={onClose}>
      <section className="sync-panel" onClick={(event) => event.stopPropagation()}>
        <header className="sync-panel-header">
          <div>
            <span>Obsidian</span>
            <h2>本地 Vault 同步</h2>
          </div>
          <button type="button" aria-label="关闭同步面板" onClick={onClose}>
            ×
          </button>
        </header>

        <label className="sync-field">
          <span>目标名称</span>
          <input value={name} onChange={(event) => setName(event.target.value)} />
        </label>
        <label className="sync-field">
          <span>Vault 路径</span>
          <input
            value={vaultPath}
            onChange={(event) => setVaultPath(event.target.value)}
            placeholder="D:\\Obsidian\\MyVault"
          />
        </label>
        <label className="sync-field">
          <span>同步目录</span>
          <input value={baseFolder} onChange={(event) => setBaseFolder(event.target.value)} />
        </label>
        <label className="sync-toggle">
          <input
            type="checkbox"
            checked={autoSync}
            onChange={(event) => setAutoSync(event.target.checked)}
          />
          <span>保存笔记后自动同步</span>
        </label>

        {message && <p className={`sync-message sync-message-${message.tone}`}>{message.text}</p>}

        <footer className="sync-actions">
          <button type="button" className="secondary-action" onClick={handleTest} disabled={isBusy}>
            {testTarget.isPending ? '测试中' : '测试路径'}
          </button>
          <button type="button" className="secondary-action" onClick={handleSave} disabled={isBusy}>
            {saveTarget.isPending ? '保存中' : '保存设置'}
          </button>
          <button type="button" className="primary-action" onClick={handleSyncAll} disabled={isBusy}>
            {syncAll.isPending ? '同步中' : '同步全部'}
          </button>
        </footer>
      </section>
    </div>
  )
}
