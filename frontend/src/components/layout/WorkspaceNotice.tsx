import { useEffect } from 'react'
import { CheckCircle2, X } from 'lucide-react'
import { useUIStore } from '../../stores/ui'

export function WorkspaceNotice() {
  const notice = useUIStore((state) => state.notice)
  const dismiss = useUIStore((state) => state.dismissNotice)
  useEffect(() => {
    if (!notice) return
    const timer = window.setTimeout(dismiss, 5000)
    return () => window.clearTimeout(timer)
  }, [notice, dismiss])
  return notice ? (
    <div className="workspace-notice" role="status">
      <CheckCircle2 aria-hidden="true" />
      <span>{notice.message}</span>
      <button type="button" onClick={dismiss} aria-label="关闭提示">
        <X aria-hidden="true" />
      </button>
    </div>
  ) : null
}
