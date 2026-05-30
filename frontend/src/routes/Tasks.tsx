import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { getTasks, createTask, updateTask, type Task } from '../api/tasks'
import { TaskRow } from '../components/ui/TaskRow'

const projects = ['全部', '项目A', '技术', '工作', '个人']
const statuses = ['all', 'active', 'done'] as const
const statusLabels: Record<string, string> = { all: '全部', active: '未完成', done: '已完成' }

export default function Tasks() {
  const [project, setProject] = useState('')
  const [status, setStatus] = useState('all')
  const [newTitle, setNewTitle] = useState('')

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ['tasks', project, status],
    queryFn: () => getTasks({ project: project || undefined, status }),
  })

  async function handleToggle(id: string) {
    const task = data?.tasks.find((t) => t.id === id)
    if (!task) return
    await updateTask(id, { done: task.done ? 0 : 1 })
    refetch()
  }

  async function handleAdd() {
    if (!newTitle.trim()) return
    await createTask({ title: newTitle.trim() })
    setNewTitle('')
    refetch()
  }

  const tasksByScope: Record<string, Task[]> = {}
  data?.tasks.forEach((t) => {
    const scope = t.scope || 'daily'
    if (!tasksByScope[scope]) tasksByScope[scope] = []
    tasksByScope[scope].push(t)
  })

  if (isLoading) return <div className="grid gap-2">{Array.from({ length: 5 }).map((_, i) => <div key={i} className="h-10 bg-fs-hover rounded-md animate-pulse" />)}</div>
  if (error) return <div className="text-red-500 text-sm">加载失败</div>

  return (
    <div className="grid gap-5 max-w-[720px]">
      <div className="flex gap-2 flex-wrap">
        {projects.map((p) => (
          <button key={p} onClick={() => setProject(p === '全部' ? '' : p)}
            className={`border-0 rounded-md px-3 py-1.5 text-xs cursor-pointer transition-colors ${(p === '全部' && !project) || project === p ? 'bg-fs-accent text-white' : 'bg-fs-hover text-fs-text-secondary hover:bg-fs-border'}`}>
            {p}
          </button>
        ))}
      </div>

      <div className="flex gap-4 border-b border-fs-border pb-3">
        {statuses.map((s) => (
          <button key={s} onClick={() => setStatus(s)}
            className={`border-0 bg-transparent text-sm cursor-pointer pb-1.5 transition-colors ${status === s ? 'text-fs-accent font-semibold border-b-2 border-fs-accent -mb-[13px]' : 'text-fs-text-muted hover:text-fs-text'}`}>
            {statusLabels[s]}
          </button>
        ))}
      </div>

      {Object.entries(tasksByScope).map(([scope, tasks]) => (
        <div key={scope}>
          <span className="text-fs-text-muted text-[11px] font-semibold uppercase tracking-wider">{scope === 'daily' ? '今天' : scope === 'monthly' ? '本月' : '今年'}</span>
          <div className="grid gap-1 mt-2">
            {tasks.map((t) => <TaskRow key={t.id} task={t} onToggle={handleToggle} />)}
          </div>
        </div>
      ))}

      {(!data?.tasks || data.tasks.length === 0) && (
        <p className="text-fs-text-muted text-sm text-center py-8">暂无任务</p>
      )}

      <div className="flex gap-2 mt-4">
        <input
          value={newTitle}
          onChange={(e) => setNewTitle(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter') handleAdd() }}
          placeholder="添加新任务..."
          className="flex-1 border border-fs-border rounded-md px-3 py-2 text-sm outline-none focus:border-fs-accent transition-colors font-sans"
        />
        <button onClick={handleAdd} disabled={!newTitle.trim()} className="border-0 rounded-md px-4 py-2 text-sm bg-fs-accent text-white cursor-pointer hover:bg-fs-accent-hover transition-colors disabled:opacity-50">
          添加
        </button>
      </div>
    </div>
  )
}
