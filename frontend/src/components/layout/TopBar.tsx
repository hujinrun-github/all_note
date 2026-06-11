import { useLocation } from 'react-router-dom'
import { useUIStore } from '../../stores/ui'

const pageMeta: Record<string, { title: string; subtitle: string }> = {
  '/': { title: '今日', subtitle: '把任务、日程和最近笔记收束到一个可执行的视图' },
  '/notes': { title: '笔记库', subtitle: '按文件夹整理想法，快速回到最近更新的内容' },
  '/tasks': { title: '任务工作台', subtitle: '按项目、状态和节奏推进每天要完成的事情' },
  '/calendar': { title: '日历', subtitle: '查看月份安排，连接任务、提醒和关联笔记' },
  '/inbox': { title: '未整理捕获', subtitle: '把临时想法转为笔记、任务或日程' },
  '/search': { title: '全局搜索', subtitle: '跨笔记、任务和日程找回上下文' },
}

export function TopBar() {
  const { pathname } = useLocation()
  const setCaptureOpen = useUIStore((s) => s.setCaptureOpen)

  const meta = pathname.startsWith('/editor/')
    ? { title: '编辑器', subtitle: '专注写作，并把内容连接到任务和日程' }
    : (pageMeta[pathname] ?? pageMeta['/'])

  return (
    <div className="workspace-topbar">
      <div className="min-w-0">
        <h1 className="page-title">{meta.title}</h1>
        <p className="workspace-subtitle">{meta.subtitle}</p>
      </div>
      <div className="workspace-actions">
        <div className="workspace-search-hint">
          <SearchIcon />
          <span>搜索笔记、任务、日程</span>
          <kbd>⌘K</kbd>
        </div>
        <button onClick={() => setCaptureOpen(true)} className="primary-action">
          <PlusIcon />
          快速捕获
        </button>
      </div>
    </div>
  )
}

function SearchIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <circle cx="11" cy="11" r="7" />
      <path d="m16.5 16.5 4 4" />
    </svg>
  )
}

function PlusIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M12 5v14M5 12h14" />
    </svg>
  )
}
