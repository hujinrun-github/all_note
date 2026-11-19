import { useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  createJapanesePairingCode,
  getJapaneseIntegration,
  retryJapaneseEvent,
  revokeJapaneseConnection,
  saveJapaneseBinding,
  type JapaneseBindingInput,
} from '../api/japaneseIntegration'
import '../styles/japanese-integration.css'

const queryKey = ['settings', 'japanese-integration']
const labels: Record<string, string> = {
  received: '已送达，等待处理',
  processing: '处理中',
  retry_wait: '等待重试',
  applied: '已同步完成',
  already_done: '目标已完成',
  stale: '旧状态已忽略',
  needs_attention: '需要处理',
  source_not_completed: '来源已撤销，目标状态保留',
  not_completed: '来源未完成，目标状态保留',
  unbound: '尚未关联任务',
}
function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : '操作失败，请稍后重试。'
}

export default function JapaneseIntegrationSettings() {
  const client = useQueryClient()
  const [sourceKey, setSourceKey] = useState('')
  const [targetID, setTargetID] = useState(
    () =>
      new URLSearchParams(window.location.search).get('integration_task') ?? ''
  )
  const [notice, setNotice] = useState('')
  const [error, setError] = useState('')
  const status = useQuery({
    queryKey,
    queryFn: getJapaneseIntegration,
    retry: false,
    refetchInterval: () =>
      document.visibilityState === 'visible' ? 5000 : false,
  })
  const refresh = () => client.invalidateQueries({ queryKey })
  const pairing = useMutation({
    mutationFn: createJapanesePairingCode,
    onError: (err) => setError(errorMessage(err)),
  })
  const save = useMutation({
    mutationFn: (input: JapaneseBindingInput) => saveJapaneseBinding(input),
    onSuccess: (binding) => {
      setError('')
      setNotice(
        `关联已保存，${binding.effective_from} 起${binding.enabled ? '按学习日期自动完成对应执行实例' : '暂停自动完成'}。`
      )
      void refresh()
    },
    onError: (err) => setError(errorMessage(err)),
  })
  const revoke = useMutation({
    mutationFn: revokeJapaneseConnection,
    onSuccess: () => {
      setNotice('连接已停用，后续学习状态不会继续写入。')
      void refresh()
    },
    onError: (err) => setError(errorMessage(err)),
  })
  const retry = useMutation({
    mutationFn: retryJapaneseEvent,
    onSuccess: () => {
      setNotice('已提交重试，请查看处理结果。')
      void refresh()
    },
    onError: (err) => setError(errorMessage(err)),
  })

  const sources = (status.data?.catalogs ?? []).flatMap(
    ({ connection_id, catalog }) => {
      const plan = catalog.plan
      const connection = status.data?.connections.find(
        (item) =>
          item.connection_id === connection_id && item.state === 'active'
      )
      return plan && connection
        ? (plan.time_slots ?? []).flatMap((slot) =>
            (slot.tasks ?? []).map((task) => ({
              key: JSON.stringify([
                connection_id,
                plan.plan_id,
                slot.id,
                task.id,
              ]),
              connection_id,
              plan_id: plan.plan_id,
              slot_id: slot.id,
              task_id: task.id,
              timezone: plan.timezone,
              path: task.path,
              label: `${connection.source_user_name || connection.source_user_id} · ${plan.title} · ${slot.title} · ${task.title}`,
            }))
          )
        : []
    }
  )
  const source = sources.find((item) => item.key === sourceKey)
  const targets = (status.data?.targets ?? []).filter(
    (item) => !source || item.timezone === source.timezone
  )
  const target = targets.find((item) => item.task_id === targetID)
  const busy = save.isPending || revoke.isPending || retry.isPending
  function submit(event: FormEvent) {
    event.preventDefault()
    if (!source || !target) return
    setNotice('')
    setError('')
    save.mutate({
      connection_id: source.connection_id,
      plan_id: source.plan_id,
      slot_id: source.slot_id,
      task_id: source.task_id,
      target_task_id: target.task_id,
    })
  }
  function pause(input: JapaneseBindingInput) {
    setNotice('')
    setError('')
    save.mutate(input)
  }

  return (
    <div className="japanese-integration-settings">
      <section className="settings-card">
        <header>
          <h2>日语学习联动</h2>
          <p>
            在日语学习中完成当天任务后，自动勾选 FlowSpace 相同日期的执行实例。
          </p>
        </header>
        <p className="settings-hint">
          先生成配对码，在日语学习的“设置 → FlowSpace
          联动”中连接，再回来关联任务。
        </p>
        <div className="settings-actions">
          <button
            type="button"
            className="primary-button"
            disabled={pairing.isPending || status.isError || status.isLoading}
            onClick={() => {
              setError('')
              pairing.mutate()
            }}
          >
            生成配对码
          </button>
          <button
            type="button"
            className="secondary-button"
            onClick={() => {
              void refresh()
            }}
          >
            刷新状态
          </button>
        </div>
        {pairing.data ? (
          <div className="japanese-pairing-code" role="status">
            <code>{pairing.data.pairing_code}</code>
            <p>
              有效期至 {new Date(pairing.data.expires_at).toLocaleString()}
              。配对码仅用于连接，请勿公开分享。
            </p>
          </div>
        ) : null}
        {status.isLoading ? <p role="status">正在读取联动状态…</p> : null}
        {status.isError ? (
          <p className="settings-message is-error" role="alert">
            {errorMessage(status.error)}
          </p>
        ) : null}
        {(status.data?.connections ?? []).map((connection) => (
          <div
            className="japanese-integration-row"
            key={connection.connection_id}
          >
            <div>
              <strong>
                {connection.source_user_name || connection.source_user_id}
              </strong>
              <p className="settings-hint">
                {connection.state === 'active' ? '已连接' : '已停用'} ·{' '}
                {connection.source_instance_id}
              </p>
            </div>
            {connection.state === 'active' ? (
              <button
                type="button"
                className="secondary-button"
                disabled={busy}
                onClick={() => {
                  setError('')
                  revoke.mutate(connection.connection_id)
                }}
              >
                停用连接
              </button>
            ) : null}
          </div>
        ))}
      </section>
      <form className="settings-card" onSubmit={submit}>
        <header>
          <h2>关联学习任务</h2>
          <p>选择一个日语学习任务和一个已有的 FlowSpace 重复任务。</p>
        </header>
        <div className="settings-field-grid">
          <label>
            <span>日语学习任务</span>
            <select
              value={sourceKey}
              onChange={(event) => {
                setSourceKey(event.target.value)
                const selected = sources.find(
                  (item) => item.key === event.target.value
                )
                if (
                  !status.data?.targets.some(
                    (item) =>
                      item.task_id === targetID &&
                      item.timezone === selected?.timezone
                  )
                )
                  setTargetID('')
              }}
              required
            >
              <option value="">请选择来源任务</option>
              {sources.map((item) => (
                <option
                  key={`${item.connection_id}:${item.plan_id}:${item.slot_id}:${item.task_id}`}
                  value={item.key}
                >
                  {item.label}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span>FlowSpace 重复任务</span>
            <select
              value={targetID}
              onChange={(event) => setTargetID(event.target.value)}
              required
            >
              <option value="">请选择目标任务</option>
              {targets.map((item) => (
                <option key={item.task_id} value={item.task_id}>
                  {item.title}
                </option>
              ))}
            </select>
          </label>
        </div>
        {!status.isLoading && !status.isError && sources.length === 0 ? (
          <p className="settings-hint">
            连接后请在日语学习中保存带时段任务的计划，再刷新此处。
          </p>
        ) : null}
        {source ? (
          <p className="settings-hint">
            时区：{source.timezone}。仅列出同一时区的重复任务。
          </p>
        ) : null}
        {source && target ? (
          <p className="japanese-binding-preview">
            {source.label} → {target.title}
            。只完成相同学习日期的执行实例，不结束整项重复任务；默认从本次关联生效日起同步。
          </p>
        ) : null}
        <div className="settings-actions">
          <button
            type="submit"
            className="primary-button"
            disabled={!source || !target || busy}
          >
            保存关联
          </button>
        </div>
        {(status.data?.bindings ?? [])
          .filter((item) => !item.effective_to)
          .map((binding) => (
            <div className="japanese-integration-row" key={binding.id}>
              <div>
                <strong>{binding.target_title}</strong>
                <p className="settings-hint">
                  {binding.slot_id} / {binding.task_id} ·{' '}
                  {binding.enabled ? '自动完成已启用' : '关联已暂停'} ·{' '}
                  {binding.effective_from} 起
                </p>
              </div>
              {binding.enabled ? (
                <button
                  type="button"
                  className="secondary-button"
                  disabled={busy}
                  onClick={() =>
                    pause({
                      connection_id: binding.connection_id,
                      plan_id: binding.plan_id,
                      slot_id: binding.slot_id,
                      task_id: binding.task_id,
                      target_task_id: binding.target_task_id,
                      enabled: false,
                    })
                  }
                >
                  从明日起暂停关联
                </button>
              ) : null}
            </div>
          ))}
      </form>
      {error ? (
        <p className="settings-message is-error" role="alert">
          {error}
        </p>
      ) : null}
      {notice ? (
        <p className="settings-message" role="status">
          {notice}
        </p>
      ) : null}
      <section className="settings-card">
        <header>
          <h2>最近同步记录</h2>
          <p>
            已送达与已完成分别展示。撤销来源打卡后，已完成的 FlowSpace
            实例会保留。
          </p>
        </header>
        {(status.data?.events ?? []).length === 0 ? (
          <p className="settings-hint">还没有同步记录。</p>
        ) : null}
        {(status.data?.events ?? []).map((event) => (
          <div
            className="japanese-integration-row"
            key={`${event.connection_id}:${event.event_id}`}
          >
            <div>
              <strong>
                {event.study_date} · {event.source_task_id}
              </strong>
              <p>
                {!event.completed && event.status === 'applied'
                  ? '来源已撤销，目标状态保留'
                  : (labels[event.status] ?? '等待检查同步状态')}
              </p>
              {event.error_code ? (
                <p className="settings-hint">
                  请检查任务关联、计划变更和目标完成条件。
                </p>
              ) : null}
            </div>
            {event.status === 'needs_attention' && event.target_task_id ? (
              <button
                type="button"
                className="secondary-button"
                disabled={busy}
                onClick={() => {
                  setError('')
                  retry.mutate(event.event_id)
                }}
              >
                重试处理
              </button>
            ) : null}
          </div>
        ))}
      </section>
    </div>
  )
}
