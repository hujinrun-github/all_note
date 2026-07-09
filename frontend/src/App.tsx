import { Suspense, useState } from 'react'
import { Outlet, useLocation } from 'react-router-dom'
import { useUIStore } from './stores/ui'
import { Sidebar } from './components/layout/Sidebar'
import { TopBar } from './components/layout/TopBar'
import { QuickCapture } from './components/QuickCapture'

export function App() {
  const captureOpen = useUIStore((s) => s.captureOpen)
  const location = useLocation()
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)
  const isTaskRoute = location.pathname.startsWith('/tasks')

  return (
    <div className={`workspace-shell ${sidebarCollapsed ? 'is-sidebar-collapsed' : ''}`}>
      <Sidebar
        collapsed={sidebarCollapsed}
        onToggleCollapsed={() => setSidebarCollapsed((collapsed) => !collapsed)}
      />
      <main className={`workspace-main ${isTaskRoute ? 'is-task-route' : ''}`}>
        <TopBar />
        <Suspense fallback={<div className="text-fs-text-muted">Loading...</div>}>
          <Outlet />
        </Suspense>
      </main>
      {captureOpen && <QuickCapture />}
    </div>
  )
}
