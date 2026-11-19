import { lazy, Suspense } from 'react'
import { useUIStore } from '../stores/ui'
import { QuickCaptureV2 } from './QuickCaptureV2'

const PlanCapture = lazy(() => import('./planning/PlanCapture'))

export function QuickCapture() {
  const mode = useUIStore((state) => state.captureMode)
  return mode === 'plan' ? (
    <Suspense fallback={null}>
      <PlanCapture />
    </Suspense>
  ) : (
    <QuickCaptureV2 />
  )
}
