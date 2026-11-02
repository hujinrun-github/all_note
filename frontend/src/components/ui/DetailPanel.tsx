import { useEffect, type ReactNode } from 'react'
import { useMediaQuery } from '../../hooks/useMediaQuery'
import { Modal } from './Modal'

export function DetailPanel({
  children,
  label,
  onClose,
  className = 'td-inspector',
  desktopRole = 'complementary',
  mediaQuery = '(max-width: 1180px)',
}: {
  children: ReactNode
  label: string
  onClose: () => void
  className?: string
  desktopRole?: 'complementary' | 'dialog'
  mediaQuery?: string
}) {
  const compact = useMediaQuery(mediaQuery)

  useEffect(() => {
    if (compact || desktopRole !== 'dialog') return

    function handleKeyDown(event: KeyboardEvent) {
      if (event.key !== 'Escape') return
      event.preventDefault()
      onClose()
    }

    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [compact, desktopRole, onClose])

  if (compact) {
    return (
      <Modal className="workspace-panel-dialog" label={label} onClose={onClose}>
        <div className={className}>{children}</div>
      </Modal>
    )
  }
  return (
    <aside className={className} role={desktopRole} aria-label={label}>
      {children}
    </aside>
  )
}
