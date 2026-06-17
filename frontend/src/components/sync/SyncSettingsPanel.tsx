import { useState } from 'react'
import { NotionSyncPanel } from './NotionSyncPanel'
import { ObsidianSyncPanel } from './ObsidianSyncPanel'

type SyncTab = 'obsidian' | 'notion'

export function SyncSettingsPanel({ onClose }: { onClose: () => void }) {
  const [activeTab, setActiveTab] = useState<SyncTab>('obsidian')

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

        <div className="sync-actions" role="tablist" aria-label="同步目标">
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

        <div role="tabpanel" aria-label={activeTab === 'obsidian' ? 'Obsidian 同步设置' : 'Notion 同步设置'}>
          {activeTab === 'obsidian' ? <ObsidianSyncPanel embedded /> : <NotionSyncPanel />}
        </div>
      </section>
    </div>
  )
}
