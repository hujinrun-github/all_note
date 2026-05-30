import { useLocation } from 'react-router-dom'
import { useUIStore } from '../../stores/ui'

const titles: Record<string, string> = {
  '/': '今日',
  '/notes': '笔记',
  '/tasks': '任务',
  '/calendar': '日历',
  '/inbox': '收件箱',
  '/search': '搜索',
}

export function TopBar() {
  const { pathname } = useLocation()
  const setCaptureOpen = useUIStore((s) => s.setCaptureOpen)

  const title = pathname.startsWith('/editor/') ? '编辑器' : (titles[pathname] ?? '今日')

  return (
    <div className="flex justify-between items-start gap-5">
      <h1 className="text-[28px] leading-tight font-bold mt-1">{title}</h1>
      <button
        onClick={() => setCaptureOpen(true)}
        className="text-[13px] px-4 py-1.5 rounded-md bg-fs-accent text-white border-0 cursor-pointer font-sans hover:bg-fs-accent-hover transition-colors"
      >
        + 快速捕获 (⌘⇧K)
      </button>
    </div>
  )
}
