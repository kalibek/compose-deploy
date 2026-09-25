import { useLayoutEffect, useRef } from 'react'

/** Dark monospace log pane that sticks to the bottom unless the user scrolled up. */
export default function LogViewer({
  text,
  className = 'max-h-[28rem] min-h-40',
  empty = 'No log output yet.',
}: {
  text: string
  /** Sizing classes (height). */
  className?: string
  empty?: string
}) {
  const ref = useRef<HTMLPreElement>(null)
  const stick = useRef(true)

  useLayoutEffect(() => {
    const el = ref.current
    if (el && stick.current) el.scrollTop = el.scrollHeight
  }, [text])

  return (
    <pre
      ref={ref}
      onScroll={(e) => {
        const el = e.currentTarget
        stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24
      }}
      className={`${className} overflow-auto rounded-md bg-zinc-950 p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap break-all text-zinc-200 ring-1 ring-zinc-800`}
    >
      {text || <span className="text-zinc-500">{empty}</span>}
    </pre>
  )
}
