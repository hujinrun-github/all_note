import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import {
  CheckCircle2,
  ChevronRight,
  CircleAlert,
  CircleX,
  Headphones,
  LoaderCircle,
  PanelRightClose,
  RotateCcw,
  Trash2,
  X,
} from 'lucide-react'
import type { ContentImport } from '../../api/contentImports'
import {
  useCancelContentImport,
  useContentImports,
  useDeleteContentImport,
  useRetryContentImport,
} from '../../hooks/useContentImports'

interface Props {
  open: boolean
  onOpen: () => void
  onClose: () => void
}

export function ContentImportTray({ open, onOpen, onClose }: Props) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const seenCompleted = useRef(new Set<string>())
  const importsQuery = useContentImports(true)
  const cancelImport = useCancelContentImport()
  const retryImport = useRetryContentImport()
  const deleteImport = useDeleteContentImport()
  const [pendingDeleteID, setPendingDeleteID] = useState('')
  const imports = importsQuery.data ?? []

  useEffect(() => {
    const completed = imports.filter(
      (item) =>
        item.status === 'completed' && !seenCompleted.current.has(item.id)
    )
    if (completed.length === 0) return
    completed.forEach((item) => seenCompleted.current.add(item.id))
    void queryClient.invalidateQueries({ queryKey: ['notes'] })
  }, [imports, queryClient])

  if (!open) {
    if (imports.length === 0) return null
    const activeCount = imports.filter(
      (item) => item.status === 'active'
    ).length
    return (
      <button
        type="button"
        className="content-import-tray-trigger"
        onClick={onOpen}
      >
        <Headphones />
        <span>
          {activeCount > 0 ? `${activeCount} 个导入进行中` : '查看播客导入'}
        </span>
        <ChevronRight />
      </button>
    )
  }

  return (
    <aside className="content-import-tray" aria-label="播客导入任务">
      <header>
        <div>
          <span>BACKGROUND TASKS</span>
          <h2>播客导入</h2>
        </div>
        <button type="button" onClick={onClose} aria-label="收起导入任务">
          <PanelRightClose />
        </button>
      </header>
      <div className="content-import-task-list">
        {importsQuery.isLoading ? (
          <p className="content-import-tray-empty">
            <LoaderCircle className="is-spinning" />
            正在读取任务
          </p>
        ) : null}
        {imports.map((item) => (
          <article
            key={item.id}
            className={`content-import-task is-${item.status}`}
          >
            <div className="content-import-task-heading">
              <span>{statusIcon(item)}</span>
              <div>
                <strong>{item.title || fallbackTitle(item)}</strong>
                <p>{item.podcast_title || sourceName(item)}</p>
              </div>
              <em>{statusLabel(item)}</em>
            </div>
            {item.status === 'active' ? (
              <div className="content-import-progress">
                <i style={{ width: `${item.progress}%` }} />
                <span>{stageLabel(item.stage)}</span>
                <b>{item.progress}%</b>
              </div>
            ) : null}
            {item.error_message ? (
              <div className="content-import-task-error" role="alert">
                <strong>
                  {isTextAIFailure(item) ? 'AI 整理失败' : '导入失败'}
                </strong>
                <span>{item.error_message}</span>
              </div>
            ) : null}
            <footer>
              <small>
                {item.summarize_with_ai ? '逐字稿 + AI 整理' : '仅完整逐字稿'}
              </small>
              <div>
                {item.status === 'active' ? (
                  <button
                    type="button"
                    onClick={() => cancelImport.mutate(item.id)}
                  >
                    <X />
                    取消
                  </button>
                ) : null}
                {(item.status === 'failed' || item.status === 'needs_review') &&
                item.retryable !== false ? (
                  <button
                    type="button"
                    disabled={retryImport.isPending}
                    onClick={() => retryImport.mutate(item.id)}
                  >
                    <RotateCcw />
                    {isTextAIFailure(item) ? '重试 AI 整理' : '重试'}
                  </button>
                ) : null}
                {item.status === 'completed' && item.result_note_id ? (
                  item.result_note_available !== false ? (
                    <button
                      type="button"
                      className="is-primary"
                      onClick={() =>
                        navigate(
                          `/editor/${encodeURIComponent(item.result_note_id!)}`
                        )
                      }
                    >
                      打开笔记
                      <ChevronRight />
                    </button>
                  ) : (
                    <span className="content-import-note-missing">
                      笔记已删除
                    </span>
                  )
                ) : null}
                {canDelete(item) && pendingDeleteID !== item.id ? (
                  <button
                    type="button"
                    aria-label={`删除导入记录：${item.title || fallbackTitle(item)}`}
                    onClick={() => {
                      deleteImport.reset()
                      setPendingDeleteID(item.id)
                    }}
                  >
                    <Trash2 />
                    删除
                  </button>
                ) : null}
              </div>
            </footer>
            {pendingDeleteID === item.id ? (
              <div
                className="content-import-delete-confirm"
                role="group"
                aria-label="确认删除导入记录"
              >
                <div className="content-import-delete-copy">
                  <p>
                    {item.result_note_id && item.result_note_available === false
                      ? '关联笔记已删除；将清理这条导入记录和逐字稿。'
                      : '仅删除导入记录和逐字稿，已生成的笔记会保留。'}
                  </p>
                  {deleteImport.isError &&
                  deleteImport.variables === item.id ? (
                    <span role="alert">
                      {deleteImport.error instanceof Error
                        ? deleteImport.error.message
                        : '删除失败，请重试'}
                    </span>
                  ) : null}
                </div>
                <div>
                  <button
                    type="button"
                    className="is-danger"
                    disabled={deleteImport.isPending}
                    onClick={() =>
                      deleteImport.mutate(item.id, {
                        onSuccess: () => setPendingDeleteID(''),
                      })
                    }
                  >
                    {deleteImport.isPending ? '正在删除' : '确认删除'}
                  </button>
                  <button
                    type="button"
                    disabled={deleteImport.isPending}
                    onClick={() => {
                      deleteImport.reset()
                      setPendingDeleteID('')
                    }}
                  >
                    保留
                  </button>
                </div>
              </div>
            ) : null}
          </article>
        ))}
        {!importsQuery.isLoading && imports.length === 0 ? (
          <p className="content-import-tray-empty">还没有导入任务</p>
        ) : null}
      </div>
    </aside>
  )
}

function fallbackTitle(item: ContentImport) {
  return item.status === 'active' ? '正在识别播客单集' : '播客导入任务'
}
function sourceName(item: ContentImport) {
  return item.source_type === 'apple'
    ? 'Apple Podcasts'
    : item.source_type === 'xiaoyuzhou'
      ? '小宇宙'
      : '等待解析来源'
}
function isTextAIFailure(item: ContentImport) {
  return (
    item.error_code?.startsWith('TEXT_AI_') ||
    item.error_code === 'IMPORT_OUTPUT_INVALID'
  )
}
function canDelete(item: ContentImport) {
  return item.status !== 'active'
}
function statusLabel(item: ContentImport) {
  return item.status === 'completed'
    ? '已完成'
    : item.status === 'failed' || item.status === 'needs_review'
      ? isTextAIFailure(item)
        ? 'AI 失败'
        : '未完成'
      : item.status === 'canceled'
        ? '已取消'
        : '处理中'
}
function statusIcon(item: ContentImport) {
  if (item.status === 'completed') return <CheckCircle2 />
  if (item.status === 'failed' || item.status === 'needs_review')
    return <CircleAlert />
  if (item.status === 'canceled') return <CircleX />
  return <LoaderCircle className="is-spinning" />
}
function stageLabel(stage: string) {
  return (
    (
      {
        queued: '等待开始',
        resolving: '正在解析链接',
        acquiring: '正在获取或转写逐字稿',
        summarizing: '正在用 AI 整理',
        publishing: '正在保存笔记',
      } as Record<string, string>
    )[stage] ?? '处理中'
  )
}
