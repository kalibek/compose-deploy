import type { StackStatus } from '../api/types'

const styles: Record<StackStatus, { cls: string; dot: string }> = {
  idle: {
    cls: 'bg-zinc-100 text-zinc-700 ring-zinc-300 dark:bg-zinc-800 dark:text-zinc-300 dark:ring-zinc-700',
    dot: 'bg-zinc-400',
  },
  queued: {
    cls: 'bg-blue-50 text-blue-700 ring-blue-200 dark:bg-blue-950 dark:text-blue-300 dark:ring-blue-900',
    dot: 'bg-blue-500 animate-pulse',
  },
  deploying: {
    cls: 'bg-blue-50 text-blue-700 ring-blue-200 dark:bg-blue-950 dark:text-blue-300 dark:ring-blue-900',
    dot: 'bg-blue-500 animate-pulse',
  },
  success: {
    cls: 'bg-green-50 text-green-700 ring-green-200 dark:bg-green-950 dark:text-green-300 dark:ring-green-900',
    dot: 'bg-green-500',
  },
  failed: {
    cls: 'bg-red-50 text-red-700 ring-red-200 dark:bg-red-950 dark:text-red-300 dark:ring-red-900',
    dot: 'bg-red-500',
  },
  stopped: {
    cls: 'bg-amber-50 text-amber-800 ring-amber-200 dark:bg-amber-950 dark:text-amber-300 dark:ring-amber-900',
    dot: 'bg-amber-500',
  },
}

export default function StatusBadge({ status }: { status: StackStatus }) {
  const s = styles[status] ?? styles.idle
  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${s.cls}`}
    >
      <span className={`h-1.5 w-1.5 rounded-full ${s.dot}`} />
      {status}
    </span>
  )
}
