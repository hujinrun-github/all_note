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
    const task = (data?.tasks ?? []).find((t) => t.id === id)
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
  const taskList = data?.tasks ?? [];
  (taskList as Task[]).forEach((t) => {
    const scope = t.scope || 'daily'
    if (!tasksByScope[scope]) tasksByScope[scope] = []
    tasksByScope[scope].push(t)
  })

  if (isLoading) return <Skeleton />
  if (error) return <div className="text-center py-12"><p className="text-fs-text-muted text-sm">加载失败</p></div>

  return (
    <div className="list-workspace">
      <aside className="filter-rail">
        <div className="filter-title">项目筛选</div>
        {projects.map((p) => (
          <button
            key={p}
            onClick={() => setProject(p === '全部' ? '' : p)}
            className={(p === '全部' && !project) || project === p ? 'is-active' : ''}
          >
            <span>{p}</span>
          </button>
        ))}
        <div className="rail-summary">
          <span>本周节奏</span>
          <strong>{data?.tasks.length ?? 0}</strong>
          <p>按日、月、年分组推进</p>
        </div>
      </aside>

      <section className="surface-panel list-panel">
        <div className="panel-heading">
          <div>
            <span>任务</span>
            <h2>任务工作台</h2>
          </div>
          <div className="segmented-tabs">
            {statuses.map((s) => (
              <button key={s} onClick={() => setStatus(s)} className={status === s ? 'is-active' : ''}>
                {statusLabels[s]}
              </button>
            ))}
          </div>
        </div>

        <div className="inline-create">
          <input
            value={newTitle}
            onChange={(e) => setNewTitle(e.target.value)}
            onKeyDown={(e) => { if (e.key === 'Enter') handleAdd() }}
            placeholder="新增任务"
          />
          <button onClick={handleAdd} disabled={!newTitle.trim()}>添加</button>
        </div>

        {Object.entries(tasksByScope).map(([scope, tasks]) => (
          <div key={scope} className="task-section">
            <span>{scope === 'daily' ? '今天' : scope === 'monthly' ? '本月' : '今年'}</span>
            <div className="row-stack">
              {tasks.map((t) => <TaskRow key={t.id} task={t} onToggle={handleToggle} />)}
            </div>
          </div>
        ))}

        {(!data?.tasks || data.tasks.length === 0) && (
          <p className="empty-copy">暂无任务</p>
        )}
      </section>
    </div>
  )
}

function Skeleton() {
  return (
    <div className="max-w-[780px] grid gap-4">
      <div className="flex gap-2">{Array.from({ length: 5 }).map((_, i) => <div key={i} className="h-8 w-16 bg-fs-hover rounded-full animate-pulse" />)}</div>
      <div className="grid gap-2">{Array.from({ length: 6 }).map((_, i) => <div key={i} className="h-10 bg-fs-hover rounded-lg animate-pulse" />)}</div>
    </div>
  )
}
