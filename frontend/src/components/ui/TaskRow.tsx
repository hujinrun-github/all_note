export interface TaskData {
  id: string; title: string; project?: string; due?: number; priority: number; done: number; scope: string
}

export function TaskRow({ task, onToggle }: { task: TaskData; onToggle: (id: string) => void }) {
  return (
    <button
      onClick={() => onToggle(task.id)}
      className={`w-full border rounded-md px-2.5 py-2 grid grid-cols-[18px_1fr_auto] gap-2.5 items-start text-left cursor-pointer transition-colors hover:bg-fs-surface hover:border-fs-border ${
        task.done ? 'border-transparent bg-transparent' : 'border-transparent bg-transparent'
      }`}
    >
      <span className={`w-[18px] h-[18px] rounded border-2 grid place-items-center text-[10px] font-bold mt-0.5 ${
        task.done ? 'bg-fs-success border-fs-success text-white' : 'border-fs-border-hover text-transparent'
      }`}>
        {task.done ? '✓' : ''}
      </span>
      <div className="grid gap-[3px]">
        <strong className={`text-[13px] leading-snug font-medium ${task.done ? 'text-fs-text-disabled line-through' : ''}`}>
          {task.title}
        </strong>
        {task.due && (
          <small className="text-fs-text-muted text-xs flex gap-1.5 items-center">
            {new Date(task.due * 1000).toLocaleDateString('zh-CN', { month: 'short', day: 'numeric' })}
            {task.project && <span className="text-fs-text-muted">· {task.project}</span>}
          </small>
        )}
      </div>
      {task.priority === 1 && <span className="text-fs-warning text-[11px] font-semibold self-start">!!</span>}
    </button>
  )
}
