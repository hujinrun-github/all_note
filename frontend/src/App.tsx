import { Suspense, useState } from 'react'
import { Outlet, useLocation } from 'react-router-dom'
import { useUIStore } from './stores/ui'
import { Sidebar } from './components/layout/Sidebar'
import { TopBar } from './components/layout/TopBar'
import { QuickCapture } from './components/QuickCapture'
import { useTaskDomainCapabilities } from './hooks/useTaskDomain'

export function App() {
  const captureOpen = useUIStore((s) => s.captureOpen)
  const location = useLocation()
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)
  const taskDomainCapability = useTaskDomainCapabilities()
  const isTaskRoute =
    location.pathname === '/' ||
    location.pathname.startsWith('/tasks') ||
    location.pathname.startsWith('/projects') ||
    location.pathname.startsWith('/calendar')
  const isV2TaskRoute =
    isTaskRoute && taskDomainCapability.data?.model_version === 'v2'
  const isRoadmapCanvasRoute =
    location.pathname.includes('/roadmap/nodes/') &&
    location.pathname.endsWith('/mind-map')

  return (
    <div className={`workspace-shell ${sidebarCollapsed ? 'is-sidebar-collapsed' : ''}`}>
      <Sidebar
        collapsed={sidebarCollapsed}
        onToggleCollapsed={() => setSidebarCollapsed((collapsed) => !collapsed)}
      />
      <main
        className={`workspace-main ${isTaskRoute ? 'is-task-route' : ''}${
          isRoadmapCanvasRoute ? ' is-roadmap-canvas-route' : ''
        }`}
      >
        {isRoadmapCanvasRoute ? null : <TopBar compact={isV2TaskRoute} />}
        <Suspense fallback={<div className="text-fs-text-muted">Loading...</div>}>
          <Outlet />
        </Suspense>
      </main>
      {captureOpen && <QuickCapture />}
    </div>
  )
}
