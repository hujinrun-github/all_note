import { Archive, AlertTriangle, Trash2, X } from 'lucide-react'
import { useEffect, useState } from 'react'

export interface TaskRetentionActionsProps {
  taskTitle: string
  archived: boolean
  onArchive?: () => Promise<unknown> | void
  onDelete?: () => Promise<unknown> | void
  busy?: boolean
}

type RetentionAction = 'archive' | 'delete'

export function TaskRetentionActions({
  taskTitle,
  archived,
  onArchive,
  onDelete,
  busy = false,
}: TaskRetentionActionsProps) {
  const [action, setAction] = useState<RetentionAction | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    setAction(null)
    setSubmitting(false)
    setError('')
  }, [taskTitle])

  async function confirm() {
    if (!action) return
    const command = action === 'archive' ? onArchive : onDelete
    if (!command) return
    setSubmitting(true)
    setError('')
    try {
      await command()
      setAction(null)
    } catch {
      setError(
        action === 'archive'
          ? '归档失败，请刷新任务后重试。'
          : '删除失败，请刷新任务后重试。'
      )
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <section className="td-retention-actions" aria-label="任务归档与删除">
      <header>
        <div>
          <strong>任务管理</strong>
          <span>归档可保留记录，删除后不可恢复</span>
        </div>
      </header>
      <div className="td-retention-buttons">
        {!archived ? (
          <button
            type="button"
            disabled={!onArchive || busy || submitting}
            onClick={() => {
              setError('')
              setAction('archive')
            }}
          >
            <Archive aria-hidden="true" />
            归档任务
          </button>
        ) : null}
        <button
          type="button"
          className="is-danger"
          disabled={!onDelete || busy || submitting}
          onClick={() => {
            setError('')
            setAction('delete')
          }}
        >
          <Trash2 aria-hidden="true" />
          永久删除
        </button>
      </div>
      {action ? (
        <div
          className={`td-retention-confirm is-${action}`}
          role="alertdialog"
          aria-modal="true"
          aria-labelledby={`td-retention-${action}-title`}
          aria-describedby={`td-retention-${action}-description`}
        >
          <button
            type="button"
            className="td-retention-confirm-close"
            aria-label="关闭确认"
            disabled={submitting}
            onClick={() => setAction(null)}
          >
            <X aria-hidden="true" />
          </button>
          <AlertTriangle aria-hidden="true" />
          <div>
            <strong id={`td-retention-${action}-title`}>
              {action === 'archive' ? '归档这个任务？' : '永久删除这个任务？'}
            </strong>
            <p id={`td-retention-${action}-description`}>
              {action === 'archive'
                ? `“${taskTitle}”将离开进行中的任务视图；尚未结束的执行实例会被取消，任务记录仍会保留。`
                : `“${taskTitle}”的计划、执行实例和执行历史都会被永久删除，此操作无法撤销。`}
            </p>
            <div>
              <button
                type="button"
                disabled={submitting}
                onClick={() => setAction(null)}
              >
                再想想
              </button>
              <button
                type="button"
                className={action === 'delete' ? 'is-danger' : 'is-primary'}
                disabled={submitting}
                onClick={() => void confirm()}
              >
                {submitting
                  ? '正在处理…'
                  : action === 'archive'
                    ? '确认归档'
                    : '永久删除'}
              </button>
            </div>
          </div>
        </div>
      ) : null}
      {error ? (
        <div className="td-inline-error" role="alert">
          {error}
        </div>
      ) : null}
    </section>
  )
}
