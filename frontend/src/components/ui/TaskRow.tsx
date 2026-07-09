export interface TaskData {
  id: string
  title: string
  project?: string
  due?: number
  planned_date?: string
  priority: number
  done: number
  scope: string
  execution_type?: 'single' | 'recurring'
  occurrence_date?: string
  occurrence_status?: 'open' | 'done' | 'skipped'
  recurrence_label?: string
}

export function TaskRow({
  task,
  onToggle,
  onOccurrenceToggle,
  onSelect,
  isSelected = false,
}: {
  task: TaskData
  onToggle: (id: string) => void
  onOccurrenceToggle?: (id: string, date: string, currentStatus: string) => void
  onSelect?: (id: string) => void
  isSelected?: boolean
}) {
  const isRecurring = task.execution_type === 'recurring'
  const isOccurrenceDone = isRecurring && task.occurrence_status === 'done'
  const isDone = !isRecurring && task.done === 1
  const showDone = isDone || isOccurrenceDone
  const actionLabel = showDone ? `重新打开 ${task.title}` : `完成 ${task.title}`
  const dateLabel = task.occurrence_date || task.planned_date || formatDueDate(task.due)

  function handleToggle() {
    if (isRecurring && task.occurrence_date && onOccurrenceToggle) {
      onOccurrenceToggle(task.id, task.occurrence_date, task.occurrence_status ?? 'open')
    } else {
      onToggle(task.id)
    }
  }

  function handleSelect() {
    if (onSelect) {
      onSelect(task.id)
      return
    }
    handleToggle()
  }

  return (
    <div className={`task-row group ${showDone ? 'is-done' : ''} ${isSelected ? 'is-selected' : ''}`}>
      <button
        type="button"
        onClick={handleToggle}
        aria-label={actionLabel}
        className={`task-row-check w-[20px] h-[20px] rounded-md border-2 grid place-items-center text-[10px] font-bold mt-px transition-all duration-150 ${
          showDone
            ? 'bg-fs-success border-fs-success text-white'
            : 'border-fs-border-hover text-transparent group-hover:border-fs-accent'
        }`}
      >
        {showDone ? '✓' : ''}
      </button>
      <button
        type="button"
        className="task-row-content grid gap-[3px] min-w-0"
        aria-label={onSelect ? `查看任务 ${task.title}` : actionLabel}
        onClick={handleSelect}
      >
        <div className="task-row-title-line">
          <strong
            className={`task-row-title text-[13px] leading-snug font-medium truncate ${
              showDone ? 'text-fs-text-disabled line-through' : 'text-fs-text'
            }`}
          >
            {task.title}
          </strong>
          {dateLabel && <span className="task-row-date">{dateLabel}</span>}
        </div>
        <small className="task-row-meta text-fs-text-muted text-xs flex gap-1.5 items-center">
          {task.recurrence_label && (
            <span className="task-recurrence-tag">{task.recurrence_label}</span>
          )}
          {task.project && (
            <span className="task-project-tag" aria-label={`所属项目：${task.project}`}>
              {task.project}
            </span>
          )}
          {showDone && !isRecurring && <span className="task-done-badge">已完成</span>}
        </small>
      </button>
      {task.priority === 1 && <span className="priority-mark">高</span>}
    </div>
  )
}

function formatDueDate(due?: number) {
  if (!due) return ''
  return new Date(due * 1000).toLocaleDateString('zh-CN', { month: 'short', day: 'numeric' })
}
