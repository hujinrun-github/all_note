import { useMemo, useState } from 'react'
import type { NoteSyncBindingResponse, SyncState, SyncTarget } from '../../api/sync'
import {
  useDeleteNoteSyncBinding,
  useNoteSyncBinding,
  usePutNoteSyncBinding,
  useSyncNote,
  useSyncTargets,
} from '../../hooks/useSync'

const DO_NOT_SYNC = '__none__'

type SyncMessage = {
  tone: 'success' | 'error'
  text: string
}

const errorMessages: Record<string, string> = {
  binding_mismatch: '这篇笔记已经绑定到其他同步目标，请刷新后再试',
  default_target_missing: '还没有可用的默认同步目标，请先在同步设置里选择目标',
  binding_required: '这篇笔记当前设置为不同步，需要手动重新选择同步目标',
  target_identity_locked: '同步目标已被使用，不能修改外部身份字段，请新建同步目标或执行迁移',
  sync_binding_conflict: '同步绑定状态已变化，请刷新后再试',
}

function targetTypeLabel(type: SyncTarget['type']) {
  return type === 'notion' ? 'Notion' : 'Obsidian'
}

function targetOptionLabel(target: SyncTarget) {
  return `${target.name}（${targetTypeLabel(target.type)}）`
}

function statusLabel(status: SyncState['status'] | 'unsynced' | undefined) {
  if (status === 'synced') return '已同步'
  if (status === 'failed') return '同步失败'
  if (status === 'external_deleted') return '外部已删除'
  if (status === 'pending') return '待同步'
  return '未同步'
}

function errorCode(error: unknown) {
  if (error && typeof error === 'object' && 'code' in error && typeof error.code === 'string') {
    return error.code
  }
  return ''
}

function errorMessage(error: unknown) {
  const code = errorCode(error)
  if (code && errorMessages[code]) return errorMessages[code]
  return '同步操作失败，请稍后重试'
}

function compatibilityMessage(response: NoteSyncBindingResponse | undefined) {
  if (!response) return null
  if (response.binding_mismatch) {
    return response.bound_target_name
      ? `这篇笔记已经绑定到 ${response.bound_target_name}，请刷新后再试`
      : errorMessages.binding_mismatch
  }
  if (response.default_target_missing) return errorMessages.default_target_missing
  if (response.binding_required) return errorMessages.binding_required
  return null
}

function collectTargets(targets: SyncTarget[] | undefined, binding: NoteSyncBindingResponse | undefined) {
  const byID = new Map<string, SyncTarget>()
  for (const target of targets ?? []) {
    if (target.enabled) byID.set(target.id, target)
  }
  for (const candidate of binding?.candidates ?? []) {
    if (candidate.target.enabled) byID.set(candidate.target.id, candidate.target)
  }
  if (binding?.target) byID.set(binding.target.id, binding.target)
  return Array.from(byID.values())
}

export function NoteSyncCard({ noteID }: { noteID: string }) {
  const targetsQ = useSyncTargets()
  const bindingQ = useNoteSyncBinding(noteID)
  const putBinding = usePutNoteSyncBinding(noteID)
  const deleteBinding = useDeleteNoteSyncBinding(noteID)
  const syncNote = useSyncNote(noteID)
  const [message, setMessage] = useState<SyncMessage | null>(null)

  const bindingResponse = bindingQ.data
  const binding = bindingResponse?.binding ?? null
  const currentTarget = bindingResponse?.target ?? targetsQ.data?.find((target) => target.id === binding?.target_id)
  const targets = useMemo(() => collectTargets(targetsQ.data, bindingResponse), [targetsQ.data, bindingResponse])
  const selectedTargetID = binding?.target_id ?? DO_NOT_SYNC
  const status = bindingResponse?.state?.status ?? 'unsynced'
  const compatibility = compatibilityMessage(bindingResponse)
  const queryError = bindingQ.error ? errorMessage(bindingQ.error) : null
  const visibleMessage = message?.text ?? queryError ?? compatibility
  const messageTone = message?.tone ?? 'error'
  const isBusy = putBinding.isPending || deleteBinding.isPending || syncNote.isPending

  async function handleTargetChange(nextTargetID: string) {
    setMessage(null)
    if (nextTargetID === selectedTargetID) return

    try {
      if (nextTargetID === DO_NOT_SYNC) {
        if (!binding) return
        await deleteBinding.mutateAsync({
          expected_target_id: binding.target_id,
          expected_updated_at: binding.updated_at,
        })
        setMessage({ tone: 'success', text: '这篇笔记已设置为不同步' })
        return
      }

      const payload: {
        target_id: string
        expected_target_id?: string
        confirm_changed_target?: boolean
      } = { target_id: nextTargetID }

      if (binding && binding.target_id !== nextTargetID) {
        const nextTarget = targets.find((target) => target.id === nextTargetID)
        const confirmed = window.confirm(
          `这篇笔记已经绑定到 ${currentTarget ? targetOptionLabel(currentTarget) : '其他同步目标'}。要改为 ${
            nextTarget ? targetOptionLabel(nextTarget) : '新的同步目标'
          } 吗？`,
        )
        if (!confirmed) return
        payload.expected_target_id = binding.target_id
        payload.confirm_changed_target = true
      }

      await putBinding.mutateAsync(payload)
      setMessage({ tone: 'success', text: '同步目标已更新' })
    } catch (error) {
      setMessage({ tone: 'error', text: errorMessage(error) })
    }
  }

  async function handleSync() {
    setMessage(null)
    try {
      await syncNote.mutateAsync()
      setMessage({ tone: 'success', text: '同步完成' })
    } catch (error) {
      setMessage({ tone: 'error', text: errorMessage(error) })
    }
  }

  return (
    <div className="sync-card">
      <div className="sync-card-header">
        <span>笔记同步</span>
        <strong className={`sync-card-status sync-card-status-${status}`}>{statusLabel(status)}</strong>
      </div>

      <label className="sync-field">
        <span>同步目标</span>
        <select
          aria-label="同步目标"
          value={selectedTargetID}
          disabled={targetsQ.isLoading || bindingQ.isLoading || isBusy}
          onChange={(event) => void handleTargetChange(event.target.value)}
        >
          <option value={DO_NOT_SYNC}>不同步</option>
          {targets.map((target) => (
            <option key={target.id} value={target.id}>
              {targetOptionLabel(target)}
            </option>
          ))}
        </select>
      </label>

      <p>{currentTarget ? `当前同步目标：${targetOptionLabel(currentTarget)}` : '当前不同步'}</p>
      {!binding && <p className="sync-field-help">默认不同步，选择目标后才会推送 FlowSpace 笔记。</p>}
      {bindingResponse?.state?.external_path && <code>{bindingResponse.state.external_path}</code>}
      {bindingResponse?.state?.external_url && (
        <a href={bindingResponse.state.external_url} target="_blank" rel="noreferrer">
          打开外部页面
        </a>
      )}
      {bindingResponse?.state?.error_message && <em>{bindingResponse.state.error_message}</em>}
      {visibleMessage && <p className={`sync-message sync-message-${messageTone}`}>{visibleMessage}</p>}

      <button type="button" className="secondary-action" onClick={handleSync} disabled={!binding || isBusy}>
        {syncNote.isPending ? '同步中' : '同步此笔记'}
      </button>
    </div>
  )
}
