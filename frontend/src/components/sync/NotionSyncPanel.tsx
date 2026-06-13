import { useEffect, useMemo, useState } from 'react'
import type { SaveSyncTargetInput, NotionBidirectionalResult } from '../../api/sync'
import {
  useConfirmNotionDeletion,
  useNotionDeletions,
  useRestoreNotionDeletion,
  useSaveSyncTarget,
  useSyncNotionBidirectional,
  useSyncTargets,
  useTestNotionTarget,
} from '../../hooks/useSync'

const DEFAULT_TARGET_NAME = 'Personal Notion'
const DEFAULT_TOKEN_ENV = 'FLOWSPACE_NOTION_TOKEN'
const DEFAULT_TITLE_PROPERTY = 'Name'

type SyncMessage = {
  tone: 'neutral' | 'success' | 'error'
  text: string
}

type NotionConfig = {
  data_source_id: string
  token_env: string
  title_property: string
}

export function NotionSyncPanel() {
  const targetsQ = useSyncTargets()
  const saveTarget = useSaveSyncTarget()
  const testTarget = useTestNotionTarget()
  const syncBoth = useSyncNotionBidirectional()
  const deletionsQ = useNotionDeletions()
  const confirmDeletion = useConfirmNotionDeletion()
  const restoreDeletion = useRestoreNotionDeletion()
  const target = useMemo(() => targetsQ.data?.find((item) => item.type === 'notion'), [targetsQ.data])

  const [name, setName] = useState(DEFAULT_TARGET_NAME)
  const [dataSourceID, setDataSourceID] = useState('')
  const [tokenEnv, setTokenEnv] = useState(DEFAULT_TOKEN_ENV)
  const [titleProperty, setTitleProperty] = useState(DEFAULT_TITLE_PROPERTY)
  const [autoSync, setAutoSync] = useState(false)
  const [message, setMessage] = useState<SyncMessage | null>(null)
  const [lastBidirectionalResult, setLastBidirectionalResult] = useState<NotionBidirectionalResult | null>(null)

  useEffect(() => {
    if (!target) return
    const config = parseNotionConfig(target.config_json)
    setName(target.name || DEFAULT_TARGET_NAME)
    setDataSourceID(config.data_source_id ?? '')
    setTokenEnv(config.token_env || DEFAULT_TOKEN_ENV)
    setTitleProperty(config.title_property || DEFAULT_TITLE_PROPERTY)
    setAutoSync(target.auto_sync)
  }, [target])

  const payload = buildPayload({
    id: target?.id,
    name,
    dataSourceID,
    tokenEnv,
    titleProperty,
    autoSync,
  })

  const isBusy =
    saveTarget.isPending ||
    testTarget.isPending ||
    syncBoth.isPending ||
    confirmDeletion.isPending ||
    restoreDeletion.isPending

  async function handleSave() {
    setMessage(null)
    try {
      await saveTarget.mutateAsync(payload)
      setMessage({ tone: 'success', text: 'Notion settings saved' })
    } catch {
      setMessage({ tone: 'error', text: 'Unable to save Notion settings' })
    }
  }

  async function handleTest() {
    setMessage(null)
    try {
      await testTarget.mutateAsync(payload)
      setMessage({ tone: 'success', text: 'Notion connection available' })
    } catch {
      setMessage({ tone: 'error', text: 'Unable to connect to Notion with this configuration' })
    }
  }

  async function handleSyncBidirectional() {
    setMessage(null)
    setLastBidirectionalResult(null)
    try {
      const result = await syncBoth.mutateAsync()
      setLastBidirectionalResult(result)
      setMessage({ tone: 'success', text: 'Notion bidirectional sync completed' })
    } catch {
      setMessage({ tone: 'error', text: 'Notion bidirectional sync failed' })
    }
  }

  async function handleConfirmDeletion(noteID: string) {
    setMessage(null)
    try {
      await confirmDeletion.mutateAsync(noteID)
      setMessage({ tone: 'success', text: 'FlowSpace deletion confirmed' })
    } catch {
      setMessage({ tone: 'error', text: 'Unable to confirm deletion' })
    }
  }

  async function handleRestoreDeletion(noteID: string) {
    setMessage(null)
    try {
      await restoreDeletion.mutateAsync(noteID)
      setMessage({ tone: 'success', text: 'Notion page restored' })
    } catch {
      setMessage({ tone: 'error', text: 'Unable to restore Notion page' })
    }
  }

  return (
    <>
      <label className="sync-field">
        <span>Target name</span>
        <input value={name} onChange={(event) => setName(event.target.value)} />
      </label>

      <label className="sync-field">
        <span>Data Source ID</span>
        <input value={dataSourceID} onChange={(event) => setDataSourceID(event.target.value)} />
      </label>

      <label className="sync-field">
        <span>Token environment variable</span>
        <input value={tokenEnv} onChange={(event) => setTokenEnv(event.target.value)} />
      </label>

      <label className="sync-field">
        <span>Title property</span>
        <input value={titleProperty} onChange={(event) => setTitleProperty(event.target.value)} />
      </label>

      <label className="sync-toggle">
        <input type="checkbox" checked={autoSync} onChange={(event) => setAutoSync(event.target.checked)} />
        <span>Auto sync after saving notes</span>
      </label>

      {message && <p className={`sync-message sync-message-${message.tone}`}>{message.text}</p>}

      {lastBidirectionalResult && (
        <div className="sync-summary" aria-label="Notion bidirectional sync result">
          <span>Imported {lastBidirectionalResult.imported}</span>
          <span>Pulled {lastBidirectionalResult.pulled}</span>
          <span>Conflict pulled {lastBidirectionalResult.conflict_pulled}</span>
          <span>Pushed {lastBidirectionalResult.pushed}</span>
          <span>Unsupported {lastBidirectionalResult.unsupported}</span>
          <span>External deleted {lastBidirectionalResult.external_deleted}</span>
          <span>Failed {lastBidirectionalResult.failed}</span>
        </div>
      )}

      {Boolean(deletionsQ.data?.length) && (
        <div className="sync-deletions">
          <strong>Notion deletion candidates</strong>
          {deletionsQ.data!.map((item) => (
            <div className="sync-deletion-item" key={item.note_id}>
              <div className="sync-deletion-copy">
                <span>{item.title}</span>
                <code>{item.external_path}</code>
              </div>
              <div className="sync-deletion-actions">
                <button
                  type="button"
                  className="secondary-action"
                  onClick={() => handleRestoreDeletion(item.note_id)}
                  disabled={isBusy}
                >
                  Restore Notion page
                </button>
                <button
                  type="button"
                  className="danger-action"
                  onClick={() => handleConfirmDeletion(item.note_id)}
                  disabled={isBusy}
                >
                  Confirm FlowSpace deletion
                </button>
              </div>
            </div>
          ))}
        </div>
      )}

      {deletionsQ.isLoading && <p className="sync-message sync-message-neutral">Loading Notion deletion candidates</p>}

      <footer className="sync-actions">
        <button type="button" className="secondary-action" onClick={handleTest} disabled={isBusy}>
          {testTarget.isPending ? 'Testing Notion connection' : 'Test Notion connection'}
        </button>
        <button type="button" className="secondary-action" onClick={handleSave} disabled={isBusy}>
          {saveTarget.isPending ? 'Saving Notion settings' : 'Save Notion settings'}
        </button>
        <button type="button" className="primary-action" onClick={handleSyncBidirectional} disabled={isBusy}>
          {syncBoth.isPending ? 'Running Notion bidirectional sync' : 'Run Notion bidirectional sync'}
        </button>
      </footer>
    </>
  )
}

function buildPayload({
  id,
  name,
  dataSourceID,
  tokenEnv,
  titleProperty,
  autoSync,
}: {
  id?: string
  name: string
  dataSourceID: string
  tokenEnv: string
  titleProperty: string
  autoSync: boolean
}): SaveSyncTargetInput {
  return {
    id,
    type: 'notion',
    name: name.trim() || DEFAULT_TARGET_NAME,
    vault_path: '',
    base_folder: '',
    config_json: JSON.stringify({
      data_source_id: dataSourceID.trim(),
      token_env: tokenEnv.trim() || DEFAULT_TOKEN_ENV,
      title_property: titleProperty.trim() || DEFAULT_TITLE_PROPERTY,
    }),
    enabled: true,
    auto_sync: autoSync,
  }
}

function parseNotionConfig(raw: string | undefined): NotionConfig {
  const defaults = {
    data_source_id: '',
    token_env: DEFAULT_TOKEN_ENV,
    title_property: DEFAULT_TITLE_PROPERTY,
  }
  if (!raw) return defaults
  try {
    const parsed = JSON.parse(raw) as unknown
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return defaults
    const config = parsed as Record<string, unknown>
    return {
      data_source_id: typeof config.data_source_id === 'string' ? config.data_source_id : defaults.data_source_id,
      token_env: typeof config.token_env === 'string' ? config.token_env : defaults.token_env,
      title_property: typeof config.title_property === 'string' ? config.title_property : defaults.title_property,
    }
  } catch {
    return defaults
  }
}
