import { useLocation, useNavigate } from 'react-router-dom'
import { useUIStore } from '../../stores/ui'

const quickTabs = [
  { label: '笔记', path: '/notes' },
  { label: '任务', path: '/tasks' },
  { label: '日程', path: '/calendar' },
]

export function QuickBar() {
  const setCaptureOpen = useUIStore((s) => s.setCaptureOpen)
  const navigate = useNavigate()
  const location = useLocation()

  return (
    <nav className="quick-command-bar" aria-label="快速导航">
      {quickTabs.map((tab) => (
        <button
          key={tab.path}
          type="button"
          onClick={() => navigate(tab.path)}
          className={location.pathname.startsWith(tab.path) ? 'is-tab-active' : ''}
        >
          {tab.label}
        </button>
      ))}
      <span />
      <button type="button" onClick={() => setCaptureOpen(true)} className="is-primary">
        快速捕获
      </button>
    </nav>
  )
}
