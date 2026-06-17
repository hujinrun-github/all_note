import { useEffect, useMemo, useState } from 'react'
import type {
  SaveSyncTargetInput,
  NotionBidirectionalResult,
  SyncBatchResult,
} from '../../api/sync'
import { parseSyncTagsInput, syncTagsInputFromConfig } from './syncTagInput'
import { SyncTagsField } from './SyncTagsField'
import {
  useConfirmNotionDeletion,
  useNotionDeletions,
  useRestoreNotionDeletion,
  useSaveSyncTarget,
  useSyncNotionAll,
  useSyncNotionPull,
  useSyncTargets,
  useTestNotionTarget,
} from '../../hooks/useSync'

const DEFAULT_TARGET_NAME = 'Personal Notion'
const DEFAULT_TOKEN_ENV = 'FLOWSPACE_NOTION_TOKEN'
const DEFAULT_TITLE_PROPERTY = 'Name'
const NOTION_SYNC_DOC_PATH = `${import.meta.env.BASE_URL}docs/notion-sync.html`

type SyncMessage = {
  tone: 'neutral' | 'success' | 'error'
  text: string
}

type NotionConfig = {
  data_source_id: string
  token_env: string
  title_property: string
  required_tags: string[]
}

export function NotionSyncPanel() {
  const targetsQ = useSyncTargets()
  const saveTarget = useSaveSyncTarget()
  const testTarget = useTestNotionTarget()
  const syncAll = useSyncNotionAll()
  const syncPull = useSyncNotionPull()
  const deletionsQ = useNotionDeletions()
  const confirmDeletion = useConfirmNotionDeletion()
  const restoreDeletion = useRestoreNotionDeletion()
  const target = useMemo(
    () => targetsQ.data?.find((item) => item.type === 'notion'),
    [targetsQ.data]
  )

  const [name, setName] = useState(DEFAULT_TARGET_NAME)
  const [dataSourceID, setDataSourceID] = useState('')
  const [tokenEnv, setTokenEnv] = useState(DEFAULT_TOKEN_ENV)
  const [titleProperty, setTitleProperty] = useState(DEFAULT_TITLE_PROPERTY)
  const [syncTags, setSyncTags] = useState('')
  const [autoSync, setAutoSync] = useState(false)
  const [message, setMessage] = useState<SyncMessage | null>(null)
  const [lastPushResult, setLastPushResult] = useState<SyncBatchResult | null>(
    null
  )
  const [lastPullResult, setLastPullResult] =
    useState<NotionBidirectionalResult | null>(null)

  useEffect(() => {
    if (!target) return
    const config = parseNotionConfig(target.config_json)
    setName(target.name || DEFAULT_TARGET_NAME)
    setDataSourceID(config.data_source_id ?? '')
    setTokenEnv(config.token_env || DEFAULT_TOKEN_ENV)
    setTitleProperty(config.title_property || DEFAULT_TITLE_PROPERTY)
    setSyncTags(syncTagsInputFromConfig(target.config_json))
    setAutoSync(target.auto_sync)
  }, [target])

  const payload = buildPayload({
    id: target?.id,
    name,
    dataSourceID,
    tokenEnv,
    titleProperty,
    syncTags,
    autoSync,
  })

  const isBusy =
    saveTarget.isPending ||
    testTarget.isPending ||
    syncAll.isPending ||
    syncPull.isPending ||
    confirmDeletion.isPending ||
    restoreDeletion.isPending

  async function handleSave() {
    setMessage(null)
    try {
      await saveTarget.mutateAsync(payload)
      setMessage({ tone: 'success', text: 'Notion 设置已保存' })
    } catch {
      setMessage({ tone: 'error', text: '无法保存 Notion 设置' })
    }
  }

  async function handleTest() {
    setMessage(null)
    try {
      await testTarget.mutateAsync(payload)
      setMessage({ tone: 'success', text: 'Notion 连接可用' })
    } catch {
      setMessage({
        tone: 'error',
        text: '无法使用当前配置连接 Notion',
      })
    }
  }

  async function handleSyncAll() {
    setMessage(null)
    setLastPushResult(null)
    setLastPullResult(null)
    try {
      const result = await syncAll.mutateAsync()
      setLastPushResult(result)
      setMessage({ tone: 'success', text: 'FlowSpace 笔记已同步到 Notion' })
    } catch {
      setMessage({
        tone: 'error',
        text: '无法同步 FlowSpace 笔记到 Notion',
      })
    }
  }

  async function handlePullRemote() {
    setMessage(null)
    setLastPushResult(null)
    setLastPullResult(null)
    try {
      const result = await syncPull.mutateAsync()
      setLastPullResult(result)
      setMessage({
        tone: 'success',
        text: '已从 Notion 手动拉取到 FlowSpace',
      })
    } catch {
      setMessage({
        tone: 'error',
        text: '无法从 Notion 拉取笔记',
      })
    }
  }

  async function handleConfirmDeletion(noteID: string) {
    setMessage(null)
    try {
      await confirmDeletion.mutateAsync(noteID)
      setMessage({ tone: 'success', text: '已确认删除 FlowSpace 笔记' })
    } catch {
      setMessage({ tone: 'error', text: '无法确认删除' })
    }
  }

  async function handleRestoreDeletion(noteID: string) {
    setMessage(null)
    try {
      await restoreDeletion.mutateAsync(noteID)
      setMessage({ tone: 'success', text: 'Notion 页面已恢复' })
    } catch {
      setMessage({ tone: 'error', text: '无法恢复 Notion 页面' })
    }
  }

  return (
    <>
      <label className="sync-field">
        <span>目标名称</span>
        <input value={name} onChange={(event) => setName(event.target.value)} />
      </label>

      <div className="sync-field">
        <div className="sync-field-heading">
          <label htmlFor="notion-data-source-id">
            Data Source ID（数据源 ID）
          </label>
          <a
            className="sync-help-link"
            href={`${NOTION_SYNC_DOC_PATH}#data-source-id`}
            target="_blank"
            rel="noreferrer"
            aria-label="Data Source ID 说明"
          >
            说明
          </a>
        </div>
        <input
          id="notion-data-source-id"
          aria-label="Data Source ID"
          value={dataSourceID}
          onChange={(event) => setDataSourceID(event.target.value)}
        />
      </div>

      <div className="sync-field">
        <div className="sync-field-heading">
          <label htmlFor="notion-token-env">
            Token environment variable（令牌环境变量）
          </label>
          <a
            className="sync-help-link"
            href={`${NOTION_SYNC_DOC_PATH}#token-env`}
            target="_blank"
            rel="noreferrer"
            aria-label="令牌环境变量说明"
          >
            说明
          </a>
        </div>
        <input
          id="notion-token-env"
          aria-label="Token environment variable"
          value={tokenEnv}
          onChange={(event) => setTokenEnv(event.target.value)}
        />
      </div>

      <label className="sync-field">
        <span>标题属性</span>
        <input
          value={titleProperty}
          onChange={(event) => setTitleProperty(event.target.value)}
        />
      </label>

      <SyncTagsField value={syncTags} onChange={setSyncTags} />

      <label className="sync-toggle">
        <input
          type="checkbox"
          checked={autoSync}
          onChange={(event) => setAutoSync(event.target.checked)}
        />
        <span>保存笔记后自动同步</span>
      </label>

      {message && (
        <p className={`sync-message sync-message-${message.tone}`}>
          {message.text}
        </p>
      )}

      {lastPushResult && (
        <div className="sync-summary" aria-label="Notion 推送同步结果">
          <span>已同步 {lastPushResult.synced}</span>
          <span>失败 {lastPushResult.failed}</span>
        </div>
      )}

      {lastPullResult && (
        <div className="sync-summary" aria-label="Notion 手动拉取结果">
          <span>导入 {lastPullResult.imported}</span>
          <span>Notion 更新 {lastPullResult.pulled}</span>
          <span>冲突覆盖 {lastPullResult.conflict_pulled}</span>
          <span>不支持 {lastPullResult.unsupported}</span>
          <span>待确认删除 {lastPullResult.external_deleted}</span>
          <span>失败 {lastPullResult.failed}</span>
        </div>
      )}

      {Boolean(deletionsQ.data?.length) && (
        <div className="sync-deletions">
          <strong>Notion 已删除，等待确认</strong>
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
                  恢复 Notion 页面
                </button>
                <button
                  type="button"
                  className="danger-action"
                  onClick={() => handleConfirmDeletion(item.note_id)}
                  disabled={isBusy}
                >
                  确认删除 FlowSpace 笔记
                </button>
              </div>
            </div>
          ))}
        </div>
      )}

      {deletionsQ.isLoading && (
        <p className="sync-message sync-message-neutral">
          正在读取 Notion 删除候选
        </p>
      )}

      <footer className="sync-actions">
        <button
          type="button"
          className="secondary-action"
          onClick={handleTest}
          disabled={isBusy}
        >
          {testTarget.isPending
            ? '正在测试 Notion 连接'
            : '测试 Notion 连接'}
        </button>
        <button
          type="button"
          className="secondary-action"
          onClick={handleSave}
          disabled={isBusy}
        >
          {saveTarget.isPending
            ? '正在保存 Notion 设置'
            : '保存 Notion 设置'}
        </button>
        <button
          type="button"
          className="primary-action"
          onClick={handleSyncAll}
          disabled={isBusy}
        >
          {syncAll.isPending ? '正在同步到 Notion' : '同步到 Notion'}
        </button>
        <button
          type="button"
          className="secondary-action"
          onClick={handlePullRemote}
          disabled={isBusy}
        >
          {syncPull.isPending ? '正在从 Notion 拉取' : '从 Notion 手动拉取'}
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
  syncTags,
  autoSync,
}: {
  id?: string
  name: string
  dataSourceID: string
  tokenEnv: string
  titleProperty: string
  syncTags: string
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
      required_tags: parseSyncTagsInput(syncTags),
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
    required_tags: [],
  }
  if (!raw) return defaults
  try {
    const parsed = JSON.parse(raw) as unknown
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed))
      return defaults
    const config = parsed as Record<string, unknown>
    return {
      data_source_id:
        typeof config.data_source_id === 'string'
          ? config.data_source_id
          : defaults.data_source_id,
      token_env:
        typeof config.token_env === 'string'
          ? config.token_env
          : defaults.token_env,
      title_property:
        typeof config.title_property === 'string'
          ? config.title_property
          : defaults.title_property,
      required_tags: Array.isArray(config.required_tags)
        ? config.required_tags.filter(
            (tag): tag is string => typeof tag === 'string'
          )
        : defaults.required_tags,
    }
  } catch {
    return defaults
  }
}
