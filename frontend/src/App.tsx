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
    <div className="h-screen grid grid-cols-[220px_minmax(0,1fr)] bg-fs-bg overflow-hidden max-[760px]:grid-cols-1">
      <Sidebar />
      <main className="min-w-0 min-h-0 pb-[110px] px-8 pt-7 grid gap-5 content-start relative max-[760px]:px-4 max-[760px]:pt-5 max-[760px]:pb-[120px]">
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
