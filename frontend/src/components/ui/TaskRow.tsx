export interface TaskData {
  id: string; title: string; project?: string; due?: number; priority: number; done: number; scope: string
}

export function TaskRow({ task, onToggle }: { task: TaskData; onToggle: (id: string) => void }) {
  return (
    <button
      onClick={() => onToggle(task.id)}
      className="task-row group"
    >
      <span className={`w-[20px] h-[20px] rounded-md border-2 grid place-items-center text-[10px] font-bold mt-px transition-all duration-150 ${
        task.done
          ? 'bg-fs-success border-fs-success text-white'
          : 'border-fs-border-hover text-transparent group-hover:border-fs-accent'
      }`}>
        {task.done ? '✓' : ''}
      </span>
      <div className="grid gap-[3px] min-w-0">
        <strong className={`text-[13px] leading-snug font-medium truncate ${task.done ? 'text-fs-text-disabled line-through' : 'text-fs-text'}`}>
          {task.title}
        </strong>
        {(task.due || task.project) && (
          <small className="text-fs-text-muted text-xs flex gap-1.5 items-center">
            {task.due && new Date(task.due * 1000).toLocaleDateString('zh-CN', { month: 'short', day: 'numeric' })}
            {task.project && <span>· {task.project}</span>}
          </small>
        )}
      </div>
      {task.priority === 1 && <span className="priority-mark">高</span>}
    </button>
  )
}
