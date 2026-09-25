import { useEffect, useState, type ReactNode } from 'react'
import { btnDanger, btnDangerSolid } from '../lib/ui'

interface Props {
  onConfirm: () => void | Promise<void>
  children: ReactNode
  confirmLabel?: ReactNode
  disabled?: boolean
  className?: string
  confirmClassName?: string
  /** Auto-cancel the armed state after this many ms. */
  timeoutMs?: number
}

/** Two-click confirm: first click arms, second click (within timeout) fires. */
export default function ConfirmButton({
  onConfirm,
  children,
  confirmLabel = 'Click again to confirm',
  disabled,
  className = btnDanger,
  confirmClassName = btnDangerSolid,
  timeoutMs = 4000,
}: Props) {
  const [armed, setArmed] = useState(false)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (!armed) return
    const t = window.setTimeout(() => setArmed(false), timeoutMs)
    return () => window.clearTimeout(t)
  }, [armed, timeoutMs])

  async function handleClick() {
    if (!armed) {
      setArmed(true)
      return
    }
    setArmed(false)
    setBusy(true)
    try {
      await onConfirm()
    } finally {
      setBusy(false)
    }
  }

  return (
    <button
      type="button"
      className={armed ? confirmClassName : className}
      disabled={disabled || busy}
      onClick={() => void handleClick()}
      onBlur={() => setArmed(false)}
    >
      {armed ? confirmLabel : children}
    </button>
  )
}
