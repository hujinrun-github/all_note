import { type MouseEvent, useEffect, useMemo, useState } from 'react'
import type {
  SaveSyncTargetInput,
  SyncBatchResult,
  TargetSyncResult,
} from '../../api/sync'
import { parseSyncTagsInput, syncTagsInputFromConfig } from './syncTagInput'
import { SyncTagsField } from './SyncTagsField'
import {
  useConfirmTargetDeletion,
  usePullTarget,
  usePushTarget,
  useRestoreTargetDeletion,
  useSaveSyncTarget,
  useSyncTargets,
  useTargetDeletions,
  useTestNotionTarget,
} from '../../hooks/useSync'

const DEFAULT_TARGET_NAME = 'Personal Notion'
const DEFAULT_TITLE_PROPERTY = 'Name'
const NOTION_SYNC_DOC_PATH = `${import.meta.env.BASE_URL}docs/notion-sync.html`
const NOTION_SYNC_HELP_WINDOW = 'flowspace-notion-sync-help'
const NOTION_SYNC_HELP_WINDOW_FEATURES =
  'noopener,noreferrer,width=960,height=720,left=120,top=80'

type SyncMessage = {
  tone: 'neutral' | 'success' | 'error'
  text: string
}

type NotionConfig = {
  data_source_id: string
  token: string
  token_env: string
  token_set: boolean
  title_property: string
  required_tags: string[]
}

export function NotionSyncPanel() {
  const targetsQ = useSyncTargets()
  const saveTarget = useSaveSyncTarget()
  const testTarget = useTestNotionTarget()
  const syncAll = usePushTarget()
  const syncPull = usePullTarget()
  const confirmDeletion = useConfirmTargetDeletion()
  const restoreDeletion = useRestoreTargetDeletion()
  const notionTargets = useMemo(
    () => targetsQ.data?.filter((item) => item.type === 'notion') ?? [],
    [targetsQ.data]
  )
  const preferredTargetID = useMemo(
    () =>
      notionTargets.find((item) => item.is_default)?.id ??
      notionTargets[0]?.id ??
      null,
    [notionTargets]
  )
  const [editingTargetID, setEditingTargetID] = useState<string | null>(null)
  const [isCreatingTarget, setIsCreatingTarget] = useState(false)
  const [pendingTargetID, setPendingTargetID] = useState<string | null>(null)
  const target = useMemo(
    () =>
      isCreatingTarget
        ? undefined
        : notionTargets.find((item) => item.id === editingTargetID),
    [editingTargetID, isCreatingTarget, notionTargets]
  )
  const activeTargetID = isCreatingTarget
    ? undefined
    : (editingTargetID ?? undefined)
  const deletionsQ = useTargetDeletions(activeTargetID)

  const [name, setName] = useState(DEFAULT_TARGET_NAME)
  const [dataSourceID, setDataSourceID] = useState('')
  const [token, setToken] = useState('')
  const [tokenConfigured, setTokenConfigured] = useState(false)
  const [legacyTokenEnv, setLegacyTokenEnv] = useState('')
  const [titleProperty, setTitleProperty] = useState(DEFAULT_TITLE_PROPERTY)
  const [syncTags, setSyncTags] = useState('')
  const [autoSync, setAutoSync] = useState(false)
  const [message, setMessage] = useState<SyncMessage | null>(null)
  const [lastPushResult, setLastPushResult] = useState<SyncBatchResult | null>(
    null
  )
  const [lastPullResult, setLastPullResult] = useState<TargetSyncResult | null>(
    null
  )

  useEffect(() => {
    if (isCreatingTarget || pendingTargetID) return
    if (!preferredTargetID) {
      if (editingTargetID) setEditingTargetID(null)
      return
    }
    if (
      !editingTargetID ||
      !notionTargets.some((item) => item.id === editingTargetID)
    ) {
      setEditingTargetID(preferredTargetID)
    }
  }, [
    editingTargetID,
    isCreatingTarget,
    notionTargets,
    pendingTargetID,
    preferredTargetID,
  ])

  useEffect(() => {
    if (
      pendingTargetID &&
      notionTargets.some((item) => item.id === pendingTargetID)
    ) {
      setPendingTargetID(null)
    }
  }, [notionTargets, pendingTargetID])

  useEffect(() => {
    if (!target || isCreatingTarget) return
    const config = parseNotionConfig(target.config_json)
    setName(target.name || DEFAULT_TARGET_NAME)
    setDataSourceID(config.data_source_id ?? '')
    setToken('')
    setTokenConfigured(
      Boolean(config.token_set || config.token || config.token_env)
    )
    setLegacyTokenEnv(config.token_env)
    setTitleProperty(config.title_property || DEFAULT_TITLE_PROPERTY)
    setSyncTags(syncTagsInputFromConfig(target.config_json))
    setAutoSync(target.auto_sync)
  }, [isCreatingTarget, target])

  const payload = buildPayload({
    id: activeTargetID,
    name,
    dataSourceID,
    token,
    legacyTokenEnv,
    titleProperty,
    syncTags,
    autoSync,
  })
  const tokenStatus = token.trim()
    ? '待覆盖'
    : tokenConfigured
      ? '已设置'
      : '未设置'
  const tokenHelpText = token.trim()
    ? '保存后会用新 Token 覆盖当前设置。'
    : legacyTokenEnv
      ? `当前仍使用环境变量 ${legacyTokenEnv}；输入原始 Token 后会覆盖为页面保存的 Token。`
      : tokenConfigured
        ? '已保存的 Token 不会显示；留空会继续使用当前 Token，输入新 Token 会覆盖。'
        : '粘贴 Notion 集成的原始 Token，保存后不会回显。'

  const isBusy =
    saveTarget.isPending ||
    testTarget.isPending ||
    syncAll.isPending ||
    syncPull.isPending ||
    confirmDeletion.isPending ||
    restoreDeletion.isPending

  function resetFormForNewTarget() {
    setName(DEFAULT_TARGET_NAME)
    setDataSourceID('')
    setToken('')
    setTokenConfigured(false)
    setLegacyTokenEnv('')
    setTitleProperty(DEFAULT_TITLE_PROPERTY)
    setSyncTags('')
    setAutoSync(false)
    setLastPushResult(null)
    setLastPullResult(null)
  }

  function handleCreateTarget() {
    setIsCreatingTarget(true)
    setEditingTargetID(null)
    setPendingTargetID(null)
    resetFormForNewTarget()
    setMessage(null)
  }

  function handleSelectTarget(nextTargetID: string) {
    setMessage(null)
    setLastPushResult(null)
    setLastPullResult(null)
    if (!nextTargetID) {
      handleCreateTarget()
      return
    }
    setIsCreatingTarget(false)
    setPendingTargetID(null)
    setEditingTargetID(nextTargetID)
  }

  async function handleSave() {
    setMessage(null)
    const validationError = validateNotionSettings({
      name,
      dataSourceID,
      token,
      hasSavedToken: tokenConfigured,
      titleProperty,
      syncTags,
    })
    if (validationError) {
      setMessage({ tone: 'error', text: validationError })
      return
    }
    try {
      const savedTarget = await saveTarget.mutateAsync(payload)
      setIsCreatingTarget(false)
      if (savedTarget.id) {
        setPendingTargetID(savedTarget.id)
        setEditingTargetID(savedTarget.id)
      }
      if (token.trim()) {
        setToken('')
        setTokenConfigured(true)
        setLegacyTokenEnv('')
      }
      setMessage({
        tone: 'success',
        text: payload.id ? 'Notion 设置已保存' : 'Notion 同步目标已创建',
      })
    } catch (error) {
      setMessage({
        tone: 'error',
        text: isTargetIdentityLocked(error)
          ? 'Data Source ID 已被使用中的同步目标锁定'
          : '无法保存 Notion 设置',
      })
    }
  }

  async function handleTest() {
    setMessage(null)
    const validationError = validateNotionSettings({
      name,
      dataSourceID,
      token,
      hasSavedToken: tokenConfigured,
      titleProperty,
      syncTags,
    })
    if (validationError) {
      setMessage({ tone: 'error', text: validationError })
      return
    }
    try {
      await testTarget.mutateAsync(payload)
      setMessage({ tone: 'success', text: 'Notion 连接可用' })
    } catch (error) {
      setMessage({
        tone: 'error',
        text: errorMessageWithReason('无法使用当前配置连接 Notion', error),
      })
    }
  }

  async function handleSyncAll() {
    setMessage(null)
    setLastPushResult(null)
    setLastPullResult(null)
    if (!activeTargetID) {
      setMessage({ tone: 'error', text: '请先保存 Notion 同步目标' })
      return
    }
    try {
      const result = await syncAll.mutateAsync(activeTargetID)
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
    if (!activeTargetID) {
      setMessage({ tone: 'error', text: '请先保存 Notion 同步目标' })
      return
    }
    try {
      const result = await syncPull.mutateAsync(activeTargetID)
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
    if (!activeTargetID) return
    try {
      await confirmDeletion.mutateAsync({ targetID: activeTargetID, noteID })
      setMessage({ tone: 'success', text: '已确认删除 FlowSpace 笔记' })
    } catch {
      setMessage({ tone: 'error', text: '无法确认删除' })
    }
  }

  async function handleRestoreDeletion(noteID: string) {
    setMessage(null)
    if (!activeTargetID) return
    try {
      await restoreDeletion.mutateAsync({ targetID: activeTargetID, noteID })
      setMessage({ tone: 'success', text: 'Notion 页面已恢复' })
    } catch {
      setMessage({ tone: 'error', text: '无法恢复 Notion 页面' })
    }
  }

  return (
    <>
      <div className="sync-target-editor">
        <label className="sync-field">
          <span>编辑目标</span>
          <select
            aria-label="编辑 Notion 目标"
            value={isCreatingTarget ? '' : (editingTargetID ?? '')}
            onChange={(event) => handleSelectTarget(event.target.value)}
            disabled={isBusy || targetsQ.isLoading}
          >
            <option value="">新建 Notion 目标</option>
            {notionTargets.map((item) => (
              <option key={item.id} value={item.id}>
                {item.name || '未命名 Notion 目标'}
              </option>
            ))}
          </select>
        </label>
        <button
          type="button"
          className="secondary-action"
          onClick={handleCreateTarget}
          disabled={isBusy}
        >
          新增 Notion 目标
        </button>
      </div>

      <label className="sync-field">
        <span>目标名称</span>
        <input
          required
          value={name}
          onChange={(event) => setName(event.target.value)}
        />
      </label>

      <div className="sync-field">
        <div className="sync-field-heading">
          <label htmlFor="notion-data-source-id">
            数据库链接或 Data Source ID
          </label>
          <a
            className="sync-help-link"
            href={`${NOTION_SYNC_DOC_PATH}#data-source-id`}
            target="_blank"
            rel="noreferrer"
            aria-label="Data Source ID 说明"
            onClick={openNotionSyncHelp}
          >
            说明
          </a>
        </div>
        <input
          required
          id="notion-data-source-id"
          aria-label="Data Source ID"
          placeholder="粘贴 Notion 数据库链接或 Data Source ID"
          value={dataSourceID}
          onChange={(event) => setDataSourceID(event.target.value)}
        />
      </div>

      <div className="sync-field">
        <div className="sync-field-heading">
          <label htmlFor="notion-token">Notion Token（原始令牌）</label>
          <a
            className="sync-help-link"
            href={`${NOTION_SYNC_DOC_PATH}#token`}
            target="_blank"
            rel="noreferrer"
            aria-label="Notion Token 说明"
            onClick={openNotionSyncHelp}
          >
            说明
          </a>
        </div>
        <input
          id="notion-token"
          type="password"
          autoComplete="off"
          aria-label="Notion Token"
          aria-describedby="notion-token-help"
          placeholder={
            tokenConfigured
              ? '••••••••（已设置，输入新 Token 覆盖）'
              : '粘贴 ntn_... 原始 Token'
          }
          value={token}
          onChange={(event) => setToken(event.target.value)}
        />
        <p
          id="notion-token-help"
          className="sync-field-help sync-field-help-muted"
        >
          <span className="sync-token-status">{tokenStatus}</span>
          {tokenHelpText}
        </p>
      </div>

      <label className="sync-field">
        <span>标题属性</span>
        <input
          required
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
          {testTarget.isPending ? '正在测试 Notion 连接' : '测试 Notion 连接'}
        </button>
        <button
          type="button"
          className="secondary-action"
          onClick={handleSave}
          disabled={isBusy}
        >
          {saveTarget.isPending ? '正在保存 Notion 设置' : '保存 Notion 设置'}
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

function openNotionSyncHelp(event: MouseEvent<HTMLAnchorElement>) {
  event.preventDefault()
  const url = event.currentTarget.href
  const helpWindow = window.open(
    url,
    NOTION_SYNC_HELP_WINDOW,
    NOTION_SYNC_HELP_WINDOW_FEATURES
  )
  if (!helpWindow) {
    window.open(url, '_blank', 'noopener,noreferrer')
  }
}

function buildPayload({
  id,
  name,
  dataSourceID,
  token,
  legacyTokenEnv,
  titleProperty,
  syncTags,
  autoSync,
}: {
  id?: string
  name: string
  dataSourceID: string
  token: string
  legacyTokenEnv: string
  titleProperty: string
  syncTags: string
  autoSync: boolean
}): SaveSyncTargetInput {
  const config: Record<string, unknown> = {
    data_source_id: dataSourceID.trim(),
    title_property: titleProperty.trim(),
    required_tags: parseSyncTagsInput(syncTags),
  }
  const trimmedToken = token.trim()
  if (trimmedToken) {
    config.token = trimmedToken
  } else if (legacyTokenEnv.trim()) {
    config.token_env = legacyTokenEnv.trim()
  }

  return {
    id,
    type: 'notion',
    name: name.trim(),
    vault_path: '',
    base_folder: '',
    config_json: JSON.stringify(config),
    enabled: true,
    auto_sync: autoSync,
  }
}

function parseNotionConfig(raw: string | undefined): NotionConfig {
  const defaults = {
    data_source_id: '',
    token: '',
    token_env: '',
    token_set: false,
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
      token: typeof config.token === 'string' ? config.token : defaults.token,
      token_env:
        typeof config.token_env === 'string'
          ? config.token_env
          : defaults.token_env,
      token_set:
        typeof config.token_set === 'boolean'
          ? config.token_set
          : defaults.token_set,
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

function isTargetIdentityLocked(error: unknown) {
  return Boolean(
    error &&
    typeof error === 'object' &&
    'code' in error &&
    error.code === 'target_identity_locked'
  )
}

function validateNotionSettings({
  name,
  dataSourceID,
  token,
  hasSavedToken,
  titleProperty,
  syncTags,
}: {
  name: string
  dataSourceID: string
  token: string
  hasSavedToken: boolean
  titleProperty: string
  syncTags: string
}) {
  const missing = [
    { label: '目标名称', value: name },
    { label: '数据库链接或 Data Source ID', value: dataSourceID },
    { label: 'Notion Token', value: hasSavedToken ? 'saved' : token },
    { label: '标题属性', value: titleProperty },
  ]
    .filter((field) => !field.value.trim())
    .map((field) => field.label)
  if (parseSyncTagsInput(syncTags).length === 0) {
    missing.push('同步标签过滤')
  }

  if (missing.length) return `请填写${missing.join('、')}`
  return null
}

function errorMessageWithReason(prefix: string, error: unknown) {
  if (error instanceof Error && error.message.trim()) {
    return `${prefix}：${friendlyNotionError(error.message)}`
  }
  return prefix
}

function friendlyNotionError(message: string) {
  const trimmed = message.trim()
  if (
    /notion API error 401/i.test(trimmed) ||
    /API token is invalid/i.test(trimmed)
  ) {
    return 'Notion Token 无效。请从 Notion integration 重新复制原始 Token，粘贴到“Notion Token（原始令牌）”后保存并重新测试。'
  }
  if (/is a page, not a database/i.test(trimmed)) {
    return '当前粘贴的是 Notion 页面链接，不是数据库链接。请打开用于同步的 Notion 数据库本体，点击数据库右上角菜单的 Copy link，再粘贴到“数据库链接或 Data Source ID”。'
  }
  if (
    /notion API error 404/i.test(trimmed) &&
    /Could not find database/i.test(trimmed)
  ) {
    const integrationMatch = trimmed.match(/integration "([^"]+)"/i)
    const integrationName = integrationMatch?.[1] ?? '当前 Notion integration'
    return `Notion 数据库没有授权给 ${integrationName}，或当前链接不是原始数据库。请在 Notion 数据库右上角菜单进入 Connections，添加 ${integrationName}；如果这是 linked database，请打开原始数据库后再添加连接。`
  }
  return trimmed
}
