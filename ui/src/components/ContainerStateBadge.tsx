import type { Container } from '../api/types'

const green = 'bg-green-50 text-green-700 ring-green-200 dark:bg-green-950 dark:text-green-300 dark:ring-green-900'
const red = 'bg-red-50 text-red-700 ring-red-200 dark:bg-red-950 dark:text-red-300 dark:ring-red-900'
const amber = 'bg-amber-50 text-amber-800 ring-amber-200 dark:bg-amber-950 dark:text-amber-300 dark:ring-amber-900'
const blue = 'bg-blue-50 text-blue-700 ring-blue-200 dark:bg-blue-950 dark:text-blue-300 dark:ring-blue-900'
const gray = 'bg-zinc-100 text-zinc-700 ring-zinc-300 dark:bg-zinc-800 dark:text-zinc-300 dark:ring-zinc-700'

function style(c: Pick<Container, 'state' | 'health'>): { cls: string; dot: string; label: string } {
  if (c.state === 'running') {
    if (c.health === 'unhealthy') return { cls: red, dot: 'bg-red-500', label: 'unhealthy' }
    if (c.health === 'starting') return { cls: blue, dot: 'bg-blue-500 animate-pulse', label: 'starting' }
    return { cls: green, dot: 'bg-green-500', label: c.health === 'healthy' ? 'healthy' : 'running' }
  }
  if (c.state === 'restarting') return { cls: amber, dot: 'bg-amber-500 animate-pulse', label: 'restarting' }
  if (c.state === 'paused') return { cls: amber, dot: 'bg-amber-500', label: 'paused' }
  if (c.state === 'dead') return { cls: red, dot: 'bg-red-500', label: 'dead' }
  if (c.state === 'exited') return { cls: gray, dot: 'bg-zinc-400', label: 'exited' }
  return { cls: gray, dot: 'bg-zinc-400', label: c.state || 'unknown' }
}

export default function ContainerStateBadge({ container }: { container: Pick<Container, 'state' | 'health'> }) {
  const s = style(container)
  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium whitespace-nowrap ring-1 ring-inset ${s.cls}`}
    >
      <span className={`h-1.5 w-1.5 rounded-full ${s.dot}`} />
      {s.label}
    </span>
  )
}
