import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { listTaskProjects, type TaskProject } from '../api/tasks'
import type { ConvertInboxInput, InboxItem } from '../api/inbox'
import {
  useInboxList,
  useConvertInboxItem,
  useDeleteInboxItem,
  useBatchInbox,
} from '../hooks/useInbox'
import { dateInputToUnix, todayDateInputValue } from '../utils/taskForm'
import { formatTaskProjectOption } from '../utils/taskProjects'

const kinds = ['all', 'note', 'task', 'event', 'idea'] as const
const kindLabels: Record<(typeof kinds)[number], string> = {
  all: '全部',
  note: '笔记',
  task: '任务',
  event: '日程',
  idea: '想法',
}

function getKindLabel(kind: string) {
  return kindLabels[kind as keyof typeof kindLabels] ?? '捕获'
}

function getKindClass(kind: string) {
  return `is-${kind === 'note' || kind === 'task' || kind === 'event' || kind === 'idea' ? kind : 'idea'}`
}

export default function Inbox() {
  const [kind, setKind] = useState<(typeof kinds)[number]>('all')
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [activeItemID, setActiveItemID] = useState('')

  const { data, isLoading, error, refetch } = useInboxList({ page_size: 100 })
  const {
    data: taskProjects = [],
    isLoading: isProjectsLoading,
    isError: isProjectsError,
  } = useQuery({
    queryKey: ['task-projects'],
    queryFn: listTaskProjects,
  })
  const convertItem = useConvertInboxItem()
  const deleteItem = useDeleteInboxItem()
  const batchInbox = useBatchInbox()

  const items = data?.items ?? []
  const visibleItems =
    kind === 'all' ? items : items.filter((item) => item.kind === kind)
  const selectedItem =
    visibleItems.find((item) => item.id === activeItemID) ?? visibleItems[0]
  const allVisibleSelected =
    visibleItems.length > 0 &&
    visibleItems.every((item) => selected.has(item.id))

  function toggleSelect(id: string) {
    setSelected((current) => {
      const next = new Set(current)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  function toggleSelectAllVisible() {
    setSelected((current) => {
      const next = new Set(current)
      if (visibleItems.every((item) => next.has(item.id))) {
        visibleItems.forEach((item) => next.delete(item.id))
      } else {
        visibleItems.forEach((item) => next.add(item.id))
      }
      return next
    })
  }

  async function handleConvert(
    id: string,
    targetKind: string,
    input: Omit<ConvertInboxInput, 'kind'> = {}
  ) {
    await convertItem.mutateAsync({ id, kind: targetKind, ...input })
    setSelected((current) => {
      const next = new Set(current)
      next.delete(id)
      return next
    })
    if (activeItemID === id) setActiveItemID('')
    await refetch()
  }

  async function handleDelete(id: string) {
    await deleteItem.mutateAsync(id)
    setSelected((current) => {
      const next = new Set(current)
      next.delete(id)
      return next
    })
    if (activeItemID === id) setActiveItemID('')
    await refetch()
  }

  async function handleBatch(action: 'archive' | 'delete') {
    if (selected.size === 0) return
    await batchInbox.mutateAsync({ ids: Array.from(selected), action })
    setSelected(new Set())
    setActiveItemID('')
    await refetch()
  }

  if (isLoading) return <Skeleton />
  if (error) {
    return (
      <div className="empty-state">
        <strong>加载失败</strong>
        <p>收件箱暂时不可用，请稍后重试。</p>
      </div>
    )
  }

  return (
    <div className="inbox-page">
      <div className="inbox-workspace">
        <aside className="surface-panel inbox-filter-panel">
          <div className="inbox-overview">
            <span>待处理</span>
            <strong>{items.length}</strong>
            <p>把零散捕获整理成下一步行动</p>
          </div>
          <div className="inbox-filter-heading">
            <h2>捕获类型</h2>
            <span>{visibleItems.length} 条</span>
          </div>
          <nav className="inbox-kind-list" aria-label="捕获类型">
            {kinds.map((itemKind) => (
              <button
                key={itemKind}
                type="button"
                onClick={() => setKind(itemKind)}
                className={kind === itemKind ? 'is-active' : ''}
              >
                <span>
                  <i className={getKindClass(itemKind)} />
                  {kindLabels[itemKind]}
                </span>
                <em>
                  {itemKind === 'all'
                    ? items.length
                    : items.filter((item) => item.kind === itemKind).length}
                </em>
              </button>
            ))}
          </nav>
        </aside>

        <section className="surface-panel inbox-list-panel">
          <div className="panel-heading inbox-list-heading">
            <div>
              <h2>捕获列表</h2>
              <p>选择一条，在右侧补充任务信息</p>
            </div>
            <button
              type="button"
              className="secondary-action inbox-select-all"
              onClick={toggleSelectAllVisible}
              disabled={visibleItems.length === 0}
            >
              {allVisibleSelected ? '取消全选' : '全选'}
            </button>
          </div>

          <div className="capture-list">
            {visibleItems.map((item) => (
              <article
                key={item.id}
                className={`capture-row ${selected.has(item.id) ? 'is-selected' : ''} ${selectedItem?.id === item.id ? 'is-active' : ''}`}
                onClick={() => setActiveItemID(item.id)}
                aria-current={selectedItem?.id === item.id ? 'true' : undefined}
              >
                <input
                  type="checkbox"
                  aria-label={`选择 ${item.title}`}
                  checked={selected.has(item.id)}
                  onClick={(event) => event.stopPropagation()}
                  onChange={() => toggleSelect(item.id)}
                />
                <div className="capture-row-content">
                  <strong>{item.title}</strong>
                  {item.body ? <span>{item.body}</span> : null}
                  <p>
                    <em className={getKindClass(item.kind)}>
                      {getKindLabel(item.kind)}
                    </em>
                    <time>
                      {new Date(item.created_at * 1000).toLocaleString(
                        'zh-CN',
                        {
                          month: 'numeric',
                          day: 'numeric',
                          hour: '2-digit',
                          minute: '2-digit',
                        }
                      )}
                    </time>
                  </p>
                </div>
                <div
                  className="row-actions"
                  onClick={(event) => event.stopPropagation()}
                >
                  <button
                    type="button"
                    onClick={() => void handleConvert(item.id, 'note')}
                    disabled={convertItem.isPending}
                  >
                    转笔记
                  </button>
                  <button
                    type="button"
                    onClick={() => setActiveItemID(item.id)}
                    disabled={convertItem.isPending}
                  >
                    整理任务
                  </button>
                  <button
                    type="button"
                    onClick={() => void handleConvert(item.id, 'event')}
                    disabled={convertItem.isPending}
                  >
                    转日程
                  </button>
                  <button
                    type="button"
                    className="icon-danger"
                    aria-label={`删除 ${item.title}`}
                    title="删除捕获"
                    onClick={() => void handleDelete(item.id)}
                    disabled={deleteItem.isPending}
                  >
                    ×
                  </button>
                </div>
              </article>
            ))}
          </div>

          {visibleItems.length === 0 ? (
            <div className="inbox-empty-state">
              <strong>这里已经整理干净</strong>
              <p>
                {kind === 'all'
                  ? '新的快速捕获会出现在这里。'
                  : `暂无${kindLabels[kind]}类型的捕获。`}
              </p>
            </div>
          ) : null}

          {selected.size > 0 ? (
            <div className="inbox-batch-bar">
              <span>
                已选择 <strong>{selected.size}</strong> 项
              </span>
              <div>
                <button
                  type="button"
                  className="secondary-action"
                  onClick={() => void handleBatch('archive')}
                >
                  归档
                </button>
                <button
                  type="button"
                  className="danger-action"
                  onClick={() => void handleBatch('delete')}
                >
                  删除
                </button>
              </div>
            </div>
          ) : null}
        </section>

        <InboxTaskInspector
          key={selectedItem?.id ?? 'empty'}
          item={selectedItem}
          projects={taskProjects}
          projectsLoading={isProjectsLoading}
          projectsError={isProjectsError}
          pending={convertItem.isPending}
          onConvert={(id, input) => handleConvert(id, 'task', input)}
        />
      </div>
    </div>
  )
}

interface InboxTaskInspectorProps {
  item?: InboxItem
  projects: TaskProject[]
  projectsLoading: boolean
  projectsError: boolean
  pending: boolean
  onConvert: (
    id: string,
    input: Omit<ConvertInboxInput, 'kind'>
  ) => Promise<void>
}

function InboxTaskInspector({
  item,
  projects,
  projectsLoading,
  projectsError,
  pending,
  onConvert,
}: InboxTaskInspectorProps) {
  const defaultProjectID =
    projects.find((project) => project.id === 'personal')?.id ??
    projects[0]?.id ??
    'personal'
  const [title, setTitle] = useState(item?.title ?? '')
  const [projectID, setProjectID] = useState(defaultProjectID)
  const [dueDate, setDueDate] = useState(todayDateInputValue)
  const [priority, setPriority] = useState('1')
  const [content, setContent] = useState(item?.body ?? '')

  function resetDraft() {
    setTitle(item?.title ?? '')
    setProjectID(defaultProjectID)
    setDueDate(todayDateInputValue())
    setPriority('1')
    setContent(item?.body ?? '')
  }

  async function submitTask() {
    if (!item || !title.trim() || projectsLoading || projects.length === 0)
      return
    await onConvert(item.id, {
      title: title.trim(),
      content: content.trim(),
      project_id: projectID,
      due: dateInputToUnix(dueDate),
      priority: Number(priority),
    })
  }

  return (
    <aside className="surface-panel inbox-detail-panel">
      <div className="panel-heading is-compact inbox-detail-heading">
        <div>
          <h2>整理为任务</h2>
          <p>补全归属和下一步，再放入任务工作台</p>
        </div>
        <span>任务</span>
      </div>

      {!item ? (
        <div className="inbox-detail-empty">
          <strong>选择一条捕获</strong>
          <p>点击中间列表中的内容后，可在这里编辑并整理。</p>
        </div>
      ) : (
        <form
          className="inbox-convert-form"
          onSubmit={(event) => {
            event.preventDefault()
            void submitTask()
          }}
        >
          <div className="inbox-source-context">
            <span className={getKindClass(item.kind)}>
              {getKindLabel(item.kind)}
            </span>
            <time>
              {new Date(item.created_at * 1000).toLocaleString('zh-CN', {
                month: 'long',
                day: 'numeric',
                hour: '2-digit',
                minute: '2-digit',
              })}
            </time>
          </div>
          <label>
            <span>标题</span>
            <input
              value={title}
              onChange={(event) => setTitle(event.target.value)}
              placeholder="任务标题"
            />
          </label>
          <label>
            <span>项目</span>
            <select
              value={projectID}
              onChange={(event) => setProjectID(event.target.value)}
              disabled={projectsLoading || projects.length === 0}
            >
              {projects.length === 0 ? (
                <option value="personal">
                  {projectsLoading ? '正在加载项目...' : '暂无可用项目'}
                </option>
              ) : null}
              {projects.map((project) => (
                <option key={project.id} value={project.id}>
                  {formatTaskProjectOption(project)}
                </option>
              ))}
            </select>
            {projectsError ? (
              <small className="form-field-error">
                项目加载失败，请刷新后重试。
              </small>
            ) : null}
          </label>
          <div className="inbox-form-row">
            <label>
              <span>截止日期</span>
              <input
                type="date"
                value={dueDate}
                onChange={(event) => setDueDate(event.target.value)}
              />
            </label>
            <label>
              <span>优先级</span>
              <select
                value={priority}
                onChange={(event) => setPriority(event.target.value)}
              >
                <option value="0">低</option>
                <option value="1">中</option>
                <option value="2">高</option>
              </select>
            </label>
          </div>
          <label>
            <span>备注</span>
            <textarea
              value={content}
              onChange={(event) => setContent(event.target.value)}
              placeholder="补充下一步、背景或交付标准"
            />
          </label>
          <div className="form-actions inbox-form-actions">
            <button
              type="button"
              className="secondary-action"
              onClick={resetDraft}
            >
              重置
            </button>
            <button
              type="submit"
              className="primary-action"
              disabled={
                !title.trim() ||
                projectsLoading ||
                projects.length === 0 ||
                pending
              }
            >
              {pending ? '整理中...' : '确认整理'}
            </button>
          </div>
        </form>
      )}
    </aside>
  )
}

function Skeleton() {
  return (
    <div className="inbox-workspace">
      <aside className="surface-panel inbox-filter-panel" />
      <section className="surface-panel inbox-list-panel" />
      <aside className="surface-panel inbox-detail-panel" />
    </div>
  )
}
