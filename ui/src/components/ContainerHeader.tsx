import type { ReactNode } from 'react'
import { Link } from 'react-router'
import type { ContainerInfo } from '../lib/useContainer'
import { errorBox } from '../lib/ui'
import ContainerStateBadge from './ContainerStateBadge'

/** Title row for per-container pages: back link, name, state and actions. */
export default function ContainerHeader({
  stackId,
  cid,
  title,
  info: { container, loaded, error },
  actions,
}: {
  stackId: string
  cid: string
  title: string
  info: ContainerInfo
  actions?: ReactNode
}) {
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        <div className="min-w-0 flex-1">
          <Link to={`/stacks/${stackId}`} className="text-xs text-zinc-500 hover:underline dark:text-zinc-400">
            ← Stack
          </Link>
          <div className="mt-0.5 flex flex-wrap items-center gap-2">
            <h1 className="truncate text-xl font-semibold tracking-tight">
              {title} <span className="font-mono text-base font-normal">{container?.name ?? cid.slice(0, 12)}</span>
            </h1>
            {container && <ContainerStateBadge container={container} />}
            {container && <span className="text-xs text-zinc-500">{container.status}</span>}
          </div>
        </div>
        {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
      </div>
      {error && <div className={errorBox}>{error}</div>}
      {loaded && !container && !error && (
        <div className={errorBox}>Container not found — it may have been removed by a redeploy.</div>
      )}
    </div>
  )
}
