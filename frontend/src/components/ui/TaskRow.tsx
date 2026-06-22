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
}: {
  task: TaskData
  onToggle: (id: string) => void
  onOccurrenceToggle?: (id: string, date: string, currentStatus: string) => void
}) {
  const isRecurring = task.execution_type === 'recurring'
  const isOccurrenceDone = isRecurring && task.occurrence_status === 'done'
  const isDone = !isRecurring && task.done === 1
  const showDone = isDone || isOccurrenceDone
  const actionLabel = showDone ? `重新打开 ${task.title}` : `完成 ${task.title}`

  function handleClick() {
    if (isRecurring && task.occurrence_date && onOccurrenceToggle) {
      onOccurrenceToggle(task.id, task.occurrence_date, task.occurrence_status ?? 'open')
    } else {
      onToggle(task.id)
    }
  }

  return (
    <button onClick={handleClick} className="task-row group" aria-label={actionLabel}>
      <span
        className={`w-[20px] h-[20px] rounded-md border-2 grid place-items-center text-[10px] font-bold mt-px transition-all duration-150 ${
          showDone
            ? 'bg-fs-success border-fs-success text-white'
            : 'border-fs-border-hover text-transparent group-hover:border-fs-accent'
        }`}
      >
        {showDone ? '✓' : ''}
      </span>
      <div className="grid gap-[3px] min-w-0">
        <strong
          className={`text-[13px] leading-snug font-medium truncate ${
            showDone ? 'text-fs-text-disabled line-through' : 'text-fs-text'
          }`}
        >
          {task.title}
        </strong>
        <small className="task-row-meta text-fs-text-muted text-xs flex gap-1.5 items-center">
          {(task.occurrence_date || task.planned_date) && (
            <span>{task.occurrence_date || task.planned_date}</span>
          )}
          {!task.occurrence_date && !task.planned_date && task.due && (
            <span>{new Date(task.due * 1000).toLocaleDateString('zh-CN', { month: 'short', day: 'numeric' })}</span>
          )}
          {task.recurrence_label && (
            <span className="task-recurrence-tag">{task.recurrence_label}</span>
          )}
          {task.project && (
            <span className="task-project-tag" aria-label={`所属项目：${task.project}`}>
              {task.project}
            </span>
          )}
          {showDone && !isRecurring && <span>· 已完成</span>}
        </small>
      </div>
      {task.priority === 1 && <span className="priority-mark">高</span>}
    </button>
  )
}
