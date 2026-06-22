import { useEffect, useMemo, useState } from 'react'
import {
  useConfirmTargetDeletion,
  usePullTarget,
  usePushTarget,
  useRestoreTargetDeletion,
  useSaveSyncTarget,
  useSyncTargets,
  useTargetDeletions,
  useTestObsidianTarget,
} from '../../hooks/useSync'
import {
  listLocalDirectories,
  type LocalDirectoryEntry,
  type LocalDirectoryList,
  type TargetSyncResult,
} from '../../api/sync'
import { parseSyncTagsInput, syncTagsInputFromConfig } from './syncTagInput'
import { SyncTagsField } from './SyncTagsField'

type SyncMessage = {
  tone: 'neutral' | 'success' | 'error'
  text: string
}

type ObsidianSyncPanelProps = {
  onClose?: () => void
  embedded?: boolean
}

export function ObsidianSyncPanel({
  onClose,
  embedded = false,
}: ObsidianSyncPanelProps) {
  const targetsQ = useSyncTargets()
  const saveTarget = useSaveSyncTarget()
  const testTarget = useTestObsidianTarget()
  const syncAll = usePushTarget()
  const syncPull = usePullTarget()
  const confirmDeletion = useConfirmTargetDeletion()
  const restoreDeletion = useRestoreTargetDeletion()
  const target = useMemo(
    () =>
      targetsQ.data?.find((item) => item.type === 'obsidian' && item.is_default) ??
      targetsQ.data?.find((item) => item.type === 'obsidian'),
    [targetsQ.data]
  )
  const deletionsQ = useTargetDeletions(target?.id)

  const [name, setName] = useState('Obsidian Vault')
  const [vaultPath, setVaultPath] = useState('')
  const [baseFolder, setBaseFolder] = useState('FlowSpace Notes')
  const [syncTags, setSyncTags] = useState('')
  const [autoSync, setAutoSync] = useState(false)
  const [message, setMessage] = useState<SyncMessage | null>(null)
  const [lastPullResult, setLastPullResult] =
    useState<TargetSyncResult | null>(null)
  const [directoryPickerOpen, setDirectoryPickerOpen] = useState(false)
  const [directoryList, setDirectoryList] = useState<LocalDirectoryList | null>(
    null
  )
  const [directoryRoots, setDirectoryRoots] = useState<LocalDirectoryEntry[]>(
    []
  )
  const [selectedDirectoryPath, setSelectedDirectoryPath] = useState('')
  const [directoryHistory, setDirectoryHistory] = useState<string[]>([])
  const [directorySearch, setDirectorySearch] = useState('')
  const [directoryLoading, setDirectoryLoading] = useState(false)
  const [directoryError, setDirectoryError] = useState<string | null>(null)

  useEffect(() => {
    if (!target) return
    setName(target.name)
    setVaultPath(target.vault_path)
    setBaseFolder(target.base_folder)
    setSyncTags(syncTagsInputFromConfig(target.config_json))
    setAutoSync(target.auto_sync)
  }, [target])

  const payload = {
    id: target?.id,
    name: name.trim(),
    vault_path: vaultPath.trim(),
    base_folder: baseFolder.trim(),
    config_json: JSON.stringify({
      required_tags: parseSyncTagsInput(syncTags),
    }),
    enabled: true,
    auto_sync: autoSync,
  }
  const isBusy =
    saveTarget.isPending ||
    testTarget.isPending ||
    syncAll.isPending ||
    syncPull.isPending ||
    confirmDeletion.isPending ||
    restoreDeletion.isPending

  const visibleDirectoryEntries = useMemo(() => {
    const entries = directoryList?.entries ?? []
    const query = directorySearch.trim().toLowerCase()
    if (!query) return entries
    return entries.filter((entry) => {
      return (
        entry.name.toLowerCase().includes(query) ||
        entry.path.toLowerCase().includes(query)
      )
    })
  }, [directoryList?.entries, directorySearch])

  const directoryBreadcrumbs = useMemo(() => {
    return buildDirectoryBreadcrumbs(directoryList?.current_path ?? '')
  }, [directoryList?.current_path])

  async function loadDirectory(
    path?: string,
    options: { pushHistory?: boolean } = {}
  ) {
    setDirectoryLoading(true)
    setDirectoryError(null)
    try {
      const previousPath = directoryList?.current_path
      const result = await listLocalDirectories(path)
      if (!result.current_path) {
        setDirectoryRoots(result.entries)
      }
      if (options.pushHistory && previousPath) {
        setDirectoryHistory((items) => [...items, previousPath])
      }
      setDirectoryList(result)
      setSelectedDirectoryPath(result.current_path || '')
      setDirectorySearch('')
    } catch {
      setDirectoryError('目录不可访问，请选择其它位置')
    } finally {
      setDirectoryLoading(false)
    }
  }

  async function handleOpenDirectoryPicker() {
    setDirectoryPickerOpen(true)
    setDirectoryHistory([])
    setDirectorySearch('')
    setDirectoryError(null)
    setDirectoryLoading(true)
    try {
      const roots = await listLocalDirectories()
      setDirectoryRoots(roots.entries)
      const initialPath = vaultPath.trim()
      if (initialPath) {
        const current = await listLocalDirectories(initialPath)
        setDirectoryList(current)
        setSelectedDirectoryPath(current.current_path)
      } else {
        setDirectoryList(roots)
        setSelectedDirectoryPath('')
      }
    } catch {
      setDirectoryError('目录不可访问，请选择其它位置')
    } finally {
      setDirectoryLoading(false)
    }
  }

  async function handleDirectoryBack() {
    const previousPath = directoryHistory[directoryHistory.length - 1]
    if (!previousPath) return
    setDirectoryHistory((items) => items.slice(0, -1))
    await loadDirectory(previousPath)
  }

  function handleUseSelectedDirectory() {
    const selectedPath = selectedDirectoryPath || directoryList?.current_path
    if (!selectedPath) return
    setVaultPath(selectedPath)
    setDirectoryPickerOpen(false)
    setMessage({
      tone: 'neutral',
      text: '已选择 Vault 路径，保存前可先测试路径',
    })
  }

  async function handleSave() {
    setMessage(null)
    const validationError = validateObsidianSettings({
      name,
      vaultPath,
      baseFolder,
      syncTags,
    })
    if (validationError) {
      setMessage({ tone: 'error', text: validationError })
      return
    }
    try {
      await saveTarget.mutateAsync(payload)
      setMessage({ tone: 'success', text: '同步设置已保存' })
    } catch (error) {
      setMessage({
        tone: 'error',
        text: isTargetIdentityLocked(error) ? 'Vault 路径已被使用中的同步目标锁定' : '保存失败，请检查路径和后端服务',
      })
    }
  }

  async function handleTest() {
    setMessage(null)
    const validationError = validateObsidianSettings({
      name,
      vaultPath,
      baseFolder,
      syncTags,
    })
    if (validationError) {
      setMessage({ tone: 'error', text: validationError })
      return
    }
    try {
      await testTarget.mutateAsync(payload)
      setMessage({ tone: 'success', text: '路径可用' })
    } catch {
      setMessage({ tone: 'error', text: '路径不可用或没有写入权限' })
    }
  }

  async function handleSyncAll() {
    setMessage(null)
    if (!target?.id) {
      setMessage({ tone: 'error', text: '请先保存 Obsidian 同步目标' })
      return
    }
    try {
      const result = await syncAll.mutateAsync(target.id)
      setMessage({
        tone: 'success',
        text: `同步完成：成功 ${result.synced}，失败 ${result.failed}`,
      })
    } catch {
      setMessage({ tone: 'error', text: '同步失败，请先保存并测试路径' })
    }
  }

  async function handlePullRemote() {
    setMessage(null)
    setLastPullResult(null)
    if (!target?.id) {
      setMessage({ tone: 'error', text: '请先保存 Obsidian 同步目标' })
      return
    }
    try {
      const result = await syncPull.mutateAsync(target.id)
      setLastPullResult(result)
      setMessage({
        tone: 'success',
        text: `手动拉取完成：导入 ${result.imported}，从 Obsidian 更新 ${result.pulled}，待确认删除 ${result.external_deleted}，失败 ${result.failed}`,
      })
    } catch {
      setMessage({
        tone: 'error',
        text: '手动拉取失败，请先保存并测试 Obsidian 路径',
      })
    }
  }

  async function handleConfirmDeletion(noteID: string) {
    setMessage(null)
    if (!target?.id) return
    try {
      await confirmDeletion.mutateAsync({ targetID: target.id, noteID })
      setLastPullResult(null)
      setMessage({ tone: 'success', text: '已确认删除该笔记' })
    } catch {
      setMessage({
        tone: 'error',
        text: '操作失败，请重新执行双向同步后再处理',
      })
    }
  }

  async function handleRestoreDeletion(noteID: string) {
    setMessage(null)
    if (!target?.id) return
    try {
      await restoreDeletion.mutateAsync({ targetID: target.id, noteID })
      setLastPullResult(null)
      setMessage({ tone: 'success', text: '已保留并重新导出该笔记' })
    } catch {
      setMessage({
        tone: 'error',
        text: '操作失败，请重新执行双向同步后再处理',
      })
    }
  }

  const content = (
    <>
      {!embedded && (
        <header className="sync-panel-header">
          <div>
            <span>Obsidian</span>
            <h2>本地 Vault 同步</h2>
          </div>
          <button type="button" aria-label="关闭同步面板" onClick={onClose}>
            ×
          </button>
        </header>
      )}

      <label className="sync-field">
        <span>目标名称</span>
        <input required value={name} onChange={(event) => setName(event.target.value)} />
      </label>
      <div className="sync-field">
        <span>Vault 路径</span>
        <div className="sync-path-picker-row">
          <input
            required
            value={vaultPath}
            readOnly
            placeholder="请选择 Obsidian Vault"
            aria-label="Vault 路径"
          />
          <button
            type="button"
            className="secondary-action"
            onClick={handleOpenDirectoryPicker}
            disabled={isBusy}
          >
            选择
          </button>
        </div>
      </div>
      <label className="sync-field">
        <span>同步目录</span>
        <input
          required
          value={baseFolder}
          onChange={(event) => setBaseFolder(event.target.value)}
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

      {directoryPickerOpen && (
        <div
          className="directory-picker"
          role="dialog"
          aria-label="选择 Vault 路径"
        >
          <header className="directory-picker-header">
            <div>
              <span>选择 Obsidian Vault</span>
              <strong>
                {selectedDirectoryPath
                  ? getDirectoryBaseName(selectedDirectoryPath)
                  : '常用位置'}
              </strong>
            </div>
            <button
              type="button"
              aria-label="关闭路径选择"
              onClick={() => setDirectoryPickerOpen(false)}
            >
              ×
            </button>
          </header>

          <div className="directory-picker-toolbar">
            <button
              type="button"
              aria-label="返回"
              title="返回"
              onClick={handleDirectoryBack}
              disabled={directoryLoading || directoryHistory.length === 0}
            >
              ‹
            </button>
            <button
              type="button"
              aria-label="上一级"
              title="上一级"
              onClick={() =>
                directoryList?.parent_path &&
                loadDirectory(directoryList.parent_path)
              }
              disabled={directoryLoading || !directoryList?.parent_path}
            >
              ↑
            </button>
            <button
              type="button"
              aria-label="刷新"
              title="刷新"
              onClick={() =>
                loadDirectory(directoryList?.current_path || undefined)
              }
              disabled={directoryLoading}
            >
              ↻
            </button>
            <div className="directory-picker-breadcrumbs" aria-label="当前路径">
              {directoryBreadcrumbs.length > 0 ? (
                directoryBreadcrumbs.map((crumb, index) => (
                  <button
                    type="button"
                    key={crumb.path}
                    onClick={() =>
                      loadDirectory(crumb.path, { pushHistory: true })
                    }
                    disabled={
                      directoryLoading ||
                      index === directoryBreadcrumbs.length - 1
                    }
                  >
                    {crumb.label}
                  </button>
                ))
              ) : (
                <span>常用位置</span>
              )}
            </div>
            <input
              value={directorySearch}
              onChange={(event) => setDirectorySearch(event.target.value)}
              placeholder="搜索文件夹"
              aria-label="搜索文件夹"
            />
          </div>

          {directoryError && (
            <p className="sync-message sync-message-error">{directoryError}</p>
          )}

          <div className="directory-picker-body">
            <aside className="directory-picker-sidebar" aria-label="常用位置">
              {directoryRoots.map((entry) => (
                <button
                  type="button"
                  key={entry.path}
                  className={
                    entry.path === directoryList?.current_path ? 'active' : ''
                  }
                  onClick={() =>
                    loadDirectory(entry.path, { pushHistory: true })
                  }
                  disabled={directoryLoading}
                >
                  <span className="directory-icon">▣</span>
                  <span>{entry.name}</span>
                </button>
              ))}
            </aside>

            <div className="directory-picker-main">
              <div className="directory-picker-columns" aria-hidden="true">
                <span>名称</span>
                <span>修改时间</span>
              </div>
              <div className="directory-picker-list">
                {directoryLoading && (
                  <span className="directory-picker-empty">加载中</span>
                )}
                {!directoryLoading &&
                  visibleDirectoryEntries.map((entry) => (
                    <button
                      type="button"
                      key={entry.path}
                      className={
                        selectedDirectoryPath === entry.path ? 'selected' : ''
                      }
                      onClick={() => setSelectedDirectoryPath(entry.path)}
                      onDoubleClick={() =>
                        loadDirectory(entry.path, { pushHistory: true })
                      }
                    >
                      <span className="directory-row-name">
                        <span className="directory-icon">▣</span>
                        <span>{entry.name}</span>
                      </span>
                      <span>
                        {formatDirectoryModifiedAt(entry.modified_at)}
                      </span>
                    </button>
                  ))}
                {!directoryLoading &&
                  directoryList &&
                  visibleDirectoryEntries.length === 0 && (
                    <span className="directory-picker-empty">
                      没有匹配的文件夹
                    </span>
                  )}
              </div>
            </div>
          </div>

          <footer className="directory-picker-actions">
            <code>
              {selectedDirectoryPath ||
                directoryList?.current_path ||
                '请选择一个文件夹'}
            </code>
            <button
              type="button"
              className="secondary-action"
              onClick={() => setDirectoryPickerOpen(false)}
            >
              取消
            </button>
            <button
              type="button"
              className="primary-action"
              onClick={handleUseSelectedDirectory}
              disabled={
                !(selectedDirectoryPath || directoryList?.current_path) ||
                directoryLoading
              }
            >
              选择此文件夹
            </button>
          </footer>
        </div>
      )}

      {lastPullResult && (
        <div className="sync-summary" aria-label="手动拉取结果">
          <span>导入 {lastPullResult.imported}</span>
          <span>Obsidian 更新 {lastPullResult.pulled}</span>
          <span>待确认删除 {lastPullResult.external_deleted}</span>
          <span>失败 {lastPullResult.failed}</span>
        </div>
      )}

      {Boolean(deletionsQ.data?.length) && (
        <div className="sync-deletions">
          <strong>Obsidian 已删除，等待确认</strong>
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
                  保留并重新导出
                </button>
                <button
                  type="button"
                  className="danger-action"
                  onClick={() => handleConfirmDeletion(item.note_id)}
                  disabled={isBusy}
                >
                  确认删除
                </button>
              </div>
            </div>
          ))}
        </div>
      )}

      <footer className="sync-actions">
        <button
          type="button"
          className="secondary-action"
          onClick={handleTest}
          disabled={isBusy}
        >
          {testTarget.isPending ? '测试中' : '测试路径'}
        </button>
        <button
          type="button"
          className="secondary-action"
          aria-label="保存 Obsidian 设置"
          onClick={handleSave}
          disabled={isBusy}
        >
          {saveTarget.isPending ? '保存中' : '保存设置'}
        </button>
        <button
          type="button"
          className="primary-action"
          onClick={handleSyncAll}
          disabled={isBusy}
        >
          {syncAll.isPending ? '同步到 Obsidian 中' : '同步到 Obsidian'}
        </button>
        <button
          type="button"
          className="secondary-action"
          onClick={handlePullRemote}
          disabled={isBusy}
        >
          {syncPull.isPending ? '从 Obsidian 拉取中' : '从 Obsidian 手动拉取'}
        </button>
      </footer>
    </>
  )

  if (embedded) return content

  const close = onClose ?? (() => undefined)

  return (
    <div className="sync-overlay" onClick={close}>
      <section
        className={`sync-panel${directoryPickerOpen ? ' sync-panel-wide' : ''}`}
        onClick={(event) => event.stopPropagation()}
      >
        {content}
      </section>
    </div>
  )
}

function buildDirectoryBreadcrumbs(path: string) {
  if (!path) return []

  const separator = path.includes('\\') ? '\\' : '/'
  const trimmed = path.replace(/[\\/]+$/, '')
  if (!trimmed) return [{ label: '/', path: '/' }]

  if (/^[A-Za-z]:/.test(trimmed)) {
    const root = `${trimmed.slice(0, 2)}\\`
    const rest = trimmed
      .slice(3)
      .split(/[\\/]+/)
      .filter(Boolean)
    const crumbs = [{ label: trimmed.slice(0, 2), path: root }]
    let current = root
    for (const part of rest) {
      current = current.endsWith(separator)
        ? `${current}${part}`
        : `${current}${separator}${part}`
      crumbs.push({ label: part, path: current })
    }
    return crumbs
  }

  const parts = trimmed.split(/[\\/]+/).filter(Boolean)
  const crumbs = [{ label: '/', path: '/' }]
  let current = ''
  for (const part of parts) {
    current = `${current}/${part}`
    crumbs.push({ label: part, path: current })
  }
  return crumbs
}

function getDirectoryBaseName(path: string) {
  const trimmed = path.replace(/[\\/]+$/, '')
  const parts = trimmed.split(/[\\/]+/).filter(Boolean)
  return parts[parts.length - 1] || trimmed || path
}

function formatDirectoryModifiedAt(value: number) {
  if (!value) return ''
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(value * 1000))
}

function isTargetIdentityLocked(error: unknown) {
  return Boolean(error && typeof error === 'object' && 'code' in error && error.code === 'target_identity_locked')
}

function validateObsidianSettings({
  name,
  vaultPath,
  baseFolder,
  syncTags,
}: {
  name: string
  vaultPath: string
  baseFolder: string
  syncTags: string
}) {
  const missing = [
    { label: '目标名称', value: name },
    { label: 'Vault 路径', value: vaultPath },
    { label: '同步目录', value: baseFolder },
  ]
    .filter((field) => !field.value.trim())
    .map((field) => field.label)
  if (parseSyncTagsInput(syncTags).length === 0) {
    missing.push('同步标签过滤')
  }

  return missing.length ? `请填写${missing.join('、')}` : null
}
