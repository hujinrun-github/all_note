export interface TaskData {
  id: string
  title: string
  project?: string
  due?: number
  planned_date?: string
  priority: number
  done: number
  scope: string
}

export function TaskRow({ task, onToggle }: { task: TaskData; onToggle: (id: string) => void }) {
  const actionLabel = task.done ? `重新打开 ${task.title}` : `完成 ${task.title}`

  return (
    <button onClick={() => onToggle(task.id)} className="task-row group" aria-label={actionLabel}>
      <span
        className={`w-[20px] h-[20px] rounded-md border-2 grid place-items-center text-[10px] font-bold mt-px transition-all duration-150 ${
          task.done
            ? 'bg-fs-success border-fs-success text-white'
            : 'border-fs-border-hover text-transparent group-hover:border-fs-accent'
        }`}
      >
        {task.done ? '✓' : ''}
      </span>
      <div className="grid gap-[3px] min-w-0">
        <strong
          className={`text-[13px] leading-snug font-medium truncate ${
            task.done ? 'text-fs-text-disabled line-through' : 'text-fs-text'
          }`}
        >
          {task.title}
        </strong>
        {(task.due || task.planned_date || task.project) && (
          <small className="text-fs-text-muted text-xs flex gap-1.5 items-center">
            {task.planned_date && <span>{task.planned_date}</span>}
            {!task.planned_date && task.due && (
              <span>{new Date(task.due * 1000).toLocaleDateString('zh-CN', { month: 'short', day: 'numeric' })}</span>
            )}
            {task.project && <span>· {task.project}</span>}
            {task.done ? <span>· 已完成</span> : null}
          </small>
        )}
      </div>
      {task.priority === 1 && <span className="priority-mark">高</span>}
    </button>
  )
}
