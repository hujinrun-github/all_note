import { Suspense } from 'react'
import { Outlet } from 'react-router-dom'
import { useUIStore } from './stores/ui'
import { Sidebar } from './components/layout/Sidebar'
import { TopBar } from './components/layout/TopBar'
import { QuickBar } from './components/layout/QuickBar'
import { QuickCapture } from './components/QuickCapture'

export function App() {
  const captureOpen = useUIStore((s) => s.captureOpen)

  return (
    <div className="workspace-shell">
      <Sidebar />
      <main className="workspace-main">
        <TopBar />
        <Suspense fallback={<div className="text-fs-text-muted">Loading...</div>}>
          <Outlet />
        </Suspense>
      </main>
      <QuickBar />
      {captureOpen && <QuickCapture />}
    </div>
  )
}
