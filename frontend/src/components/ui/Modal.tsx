import { useEffect, useRef, type ReactNode } from 'react'
import { createPortal } from 'react-dom'

// Native dialogs provide focus containment, Escape handling and an inert background.
export function Modal({
  children,
  onClose,
  label,
  labelledBy,
  className = '',
}: {
  children: ReactNode
  onClose: () => void
  label?: string
  labelledBy?: string
  className?: string
}) {
  const ref = useRef<HTMLDialogElement>(null)

  useEffect(() => {
    const dialog = ref.current!
    const previousFocus = document.activeElement as HTMLElement | null
    const previousOverflow = document.body.style.overflow
    dialog.showModal()
    dialog.querySelector<HTMLElement>('[data-modal-autofocus]')?.focus()
    document.body.style.overflow = 'hidden'
    return () => {
      dialog.close()
      document.body.style.overflow = previousOverflow
      if (previousFocus?.isConnected)
        previousFocus.focus({ preventScroll: true })
    }
  }, [])

  return createPortal(
    <dialog
      ref={ref}
      className={`workspace-modal ${className}`}
      aria-label={label}
      aria-labelledby={labelledBy}
      aria-modal="true"
      onCancel={(event) => {
        event.preventDefault()
        onClose()
      }}
    >
      {children}
    </dialog>,
    document.body
  )
}
