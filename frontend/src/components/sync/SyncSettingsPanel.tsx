import { useState } from 'react'
import type { SyncTarget } from '../../api/sync'
import { useDeleteSyncTarget, useSaveSyncTarget, useSyncTargets } from '../../hooks/useSync'
import { NotionSyncPanel } from './NotionSyncPanel'
import { ObsidianSyncPanel } from './ObsidianSyncPanel'

type SyncTab = 'obsidian' | 'notion'

type SyncMessage = {
  tone: 'success' | 'error'
  text: string
}

function targetTypeLabel(type: SyncTarget['type']) {
  return type === 'notion' ? 'Notion' : 'Obsidian'
}

function syncTargetPayload(target: SyncTarget, isDefault: boolean) {
  return {
    id: target.id,
    type: target.type,
    name: target.name,
    vault_path: target.vault_path,
    base_folder: target.base_folder,
    config_json: target.config_json,
    enabled: target.enabled,
    auto_sync: target.auto_sync,
    is_default: isDefault,
  }
}

function syncErrorMessage(error: unknown) {
  if (error && typeof error === 'object' && 'code' in error) {
    if (error.code === 'target_identity_locked') return '同步目标已被使用，不能修改外部身份字段'
    if (error.code === 'target_in_use') return '同步目标已被使用，不能删除'
  }
  return '同步目标操作失败，请稍后重试'
}

export function SyncSettingsPanel({ onClose }: { onClose: () => void }) {
  const [activeTab, setActiveTab] = useState<SyncTab>('obsidian')
  const [message, setMessage] = useState<SyncMessage | null>(null)
  const targetsQ = useSyncTargets()
  const saveTarget = useSaveSyncTarget()
  const deleteTarget = useDeleteSyncTarget()

  async function handleMakeDefault(target: SyncTarget) {
    setMessage(null)
    try {
      await saveTarget.mutateAsync(syncTargetPayload(target, true))
      setMessage({ tone: 'success', text: `${target.name} 已设为默认同步目标` })
    } catch (error) {
      setMessage({ tone: 'error', text: syncErrorMessage(error) })
    }
  }

  async function handleDeleteTarget(target: SyncTarget) {
    setMessage(null)
    const confirmed = window.confirm(`确定删除同步目标 ${target.name} 吗？`)
    if (!confirmed) return
    try {
      await deleteTarget.mutateAsync(target.id)
      setMessage({ tone: 'success', text: `${target.name} 已删除` })
    } catch (error) {
      setMessage({ tone: 'error', text: syncErrorMessage(error) })
    }
  }

  return (
    <div className="sync-overlay" onClick={onClose}>
      <section className="sync-panel sync-panel-wide" onClick={(event) => event.stopPropagation()}>
        <header className="sync-panel-header">
          <div>
            <span>同步</span>
            <h2>同步设置</h2>
          </div>
          <button type="button" aria-label="关闭同步设置面板" onClick={onClose}>
            ×
          </button>
        </header>

        <div className="sync-actions" role="tablist" aria-label="同步目标类型">
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === 'obsidian'}
            className={activeTab === 'obsidian' ? 'primary-action' : 'secondary-action'}
            onClick={() => setActiveTab('obsidian')}
          >
            Obsidian
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === 'notion'}
            className={activeTab === 'notion' ? 'primary-action' : 'secondary-action'}
            onClick={() => setActiveTab('notion')}
          >
            Notion
          </button>
        </div>

        <section className="sync-deletions" aria-label="已配置同步目标">
          <strong>已配置同步目标</strong>
          {targetsQ.isLoading && <p className="sync-message sync-message-neutral">正在读取同步目标</p>}
          {targetsQ.data?.map((target) => (
            <div className="sync-deletion-item" key={target.id}>
              <div className="sync-deletion-copy">
                <span>{target.name}</span>
                <code>{targetTypeLabel(target.type)}</code>
                {target.is_default && <span className="sync-tags-status">默认</span>}
              </div>
              <div className="sync-deletion-actions">
                <button
                  type="button"
                  className="secondary-action"
                  aria-label={`设为默认 ${target.name}`}
                  onClick={() => void handleMakeDefault(target)}
                  disabled={!target.enabled || target.is_default || saveTarget.isPending}
                >
                  设为默认
                </button>
                <button
                  type="button"
                  className="danger-action"
                  aria-label={`删除同步目标 ${target.name}`}
                  onClick={() => void handleDeleteTarget(target)}
                  disabled={deleteTarget.isPending}
                >
                  删除
                </button>
              </div>
            </div>
          ))}
        </section>

        {message && <p className={`sync-message sync-message-${message.tone}`}>{message.text}</p>}

        <div role="tabpanel" aria-label={activeTab === 'obsidian' ? 'Obsidian 同步设置' : 'Notion 同步设置'}>
          {activeTab === 'obsidian' ? <ObsidianSyncPanel embedded /> : <NotionSyncPanel />}
        </div>
      </section>
    </div>
  )
}
