import { Fragment, useCallback, useState } from 'react'
import { api } from '../api/client'
import type { Deployment, DeploymentTrigger, Stack } from '../api/types'
import { absoluteTime, commitUrl, duration, relativeTime, shortSha } from '../lib/format'
import { usePolling } from '../lib/usePolling'
import { card, code, link } from '../lib/ui'
import LogViewer from './LogViewer'
import StatusBadge from './StatusBadge'

const triggerLabels: Record<DeploymentTrigger, string> = {
  manual: 'Manual',
  webhook: 'Webhook',
  poll: 'Poller',
  config: 'Compose saved',
}

function Version({ stack, d }: { stack: Stack; d: Deployment }) {
  if (d.action === 'stop') return <span className="text-zinc-400">—</span>
  if (!d.sha) return <span className="text-zinc-500">{d.status === 'deploying' ? 'resolving…' : 'unknown'}</span>
  if (stack.source === 'inline') {
    return (
      <code className={code} title={d.sha}>
        {d.sha.slice(0, 12)}
      </code>
    )
  }
  const url = commitUrl(stack.repo_url, d.sha)
  return (
    <span className="inline-flex items-center gap-1.5">
      <code className={code} title={d.sha}>
        {shortSha(d.sha)}
      </code>
      {url && (
        <a href={url} target="_blank" rel="noreferrer" className={`${link} text-xs`}>
          ↗
        </a>
      )}
    </span>
  )
}

/** Log of one deployment; polls while it is still running. */
function DeploymentLog({ stackId, d }: { stackId: string; d: Deployment }) {
  const get = useCallback(() => api.getDeployment(stackId, d.id), [stackId, d.id])
  const running = d.status === 'deploying'
  // Reload once more when the run finishes so the final log is shown.
  const q = usePolling(get, running ? 2000 : 3_600_000, `${stackId}/${d.id}/${d.status}`)
  return (
    <div className="space-y-2">
      {d.error && <pre className="font-mono text-xs whitespace-pre-wrap break-all text-red-700 dark:text-red-400">{d.error}</pre>}
      {q.error && <p className="text-xs text-red-600 dark:text-red-400">{q.error}</p>}
      <LogViewer text={q.data?.log ?? ''} className="max-h-80 min-h-24" empty={q.data ? 'No log output.' : 'Loading…'} />
    </div>
  )
}

/** History of deploy/stop runs: when, what triggered them and which version they deployed. */
export default function DeploymentsSection({ stack, fast }: { stack: Stack; fast: boolean }) {
  const stackId = String(stack.id)
  const list = useCallback(() => api.listDeployments(stackId), [stackId])
  const q = usePolling(list, fast ? 2000 : 10000, stackId)
  const [open, setOpen] = useState<number | null>(null)
  const deployments = q.data

  return (
    <section className={card}>
      <div className="mb-2 flex items-center justify-between gap-2">
        <h2 className="text-sm font-semibold">Deployment history</h2>
        {q.error ? (
          <span className="text-xs text-red-600 dark:text-red-400">{q.error}</span>
        ) : (
          deployments && deployments.length > 0 && <span className="text-xs text-zinc-500">last {deployments.length}</span>
        )}
      </div>

      {!deployments ? (
        !q.error && <p className="text-sm text-zinc-500">Loading…</p>
      ) : deployments.length === 0 ? (
        <p className="text-sm text-zinc-500">No deployments yet.</p>
      ) : (
        <div className="-mx-4 overflow-x-auto px-4">
          <table className="w-full min-w-[40rem] text-left text-sm">
            <thead className="text-xs text-zinc-500 dark:text-zinc-400">
              <tr className="border-b border-zinc-200 dark:border-zinc-800">
                <th className="py-1.5 pr-3 font-medium">Started</th>
                <th className="py-1.5 pr-3 font-medium">Action</th>
                <th className="py-1.5 pr-3 font-medium">Triggered by</th>
                <th className="py-1.5 pr-3 font-medium">{stack.source === 'inline' ? 'Revision' : 'Commit'}</th>
                <th className="py-1.5 pr-3 font-medium">Status</th>
                <th className="py-1.5 font-medium">Duration</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-zinc-100 dark:divide-zinc-800">
              {deployments.map((d) => {
                const isOpen = open === d.id
                return (
                  <Fragment key={d.id}>
                    <tr
                      className="cursor-pointer align-top hover:bg-zinc-50 dark:hover:bg-zinc-800/40"
                      onClick={() => setOpen(isOpen ? null : d.id)}
                      aria-expanded={isOpen}
                    >
                      <td className="py-2 pr-3 whitespace-nowrap" title={absoluteTime(d.started_at)}>
                        <span className="mr-1 inline-block w-3 text-zinc-400">{isOpen ? '▾' : '▸'}</span>
                        {relativeTime(d.started_at)}
                        <div className="ml-4 text-xs text-zinc-500">{absoluteTime(d.started_at)}</div>
                      </td>
                      <td className="py-2 pr-3">{d.action}</td>
                      <td className="py-2 pr-3">
                        <div>{triggerLabels[d.triggered_by] ?? d.triggered_by}</div>
                        {d.trigger_detail && <div className="text-xs text-zinc-500">{d.trigger_detail}</div>}
                      </td>
                      <td className="py-2 pr-3" onClick={(e) => e.stopPropagation()}>
                        <Version stack={stack} d={d} />
                      </td>
                      <td className="py-2 pr-3">
                        <StatusBadge status={d.status} />
                      </td>
                      <td className="py-2 text-xs whitespace-nowrap text-zinc-600 dark:text-zinc-400">
                        {duration(d.started_at, d.finished_at) || (d.status === 'deploying' ? 'running' : '—')}
                      </td>
                    </tr>
                    {isOpen && (
                      <tr>
                        <td colSpan={6} className="pt-1 pb-3">
                          <DeploymentLog stackId={stackId} d={d} />
                        </td>
                      </tr>
                    )}
                  </Fragment>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}
