import { useCallback } from 'react'
import { Link } from 'react-router'
import { api } from '../api/client'
import { usePolling } from '../lib/usePolling'
import { btnSecondarySm, card } from '../lib/ui'
import ContainerStateBadge from './ContainerStateBadge'

/** Containers of a stack's compose project with links to logs and a shell. */
export default function ContainersSection({ stackId, fast }: { stackId: string; fast: boolean }) {
  const list = useCallback(() => api.listContainers(stackId), [stackId])
  const q = usePolling(list, fast ? 2000 : 5000, stackId)
  const containers = q.data

  return (
    <section className={card}>
      <div className="mb-2 flex items-center justify-between gap-2">
        <h2 className="text-sm font-semibold">Containers</h2>
        {q.error ? (
          <span className="text-xs text-red-600 dark:text-red-400">{q.error}</span>
        ) : (
          containers && (
            <span className="text-xs text-zinc-500">
              {containers.filter((c) => c.state === 'running').length}/{containers.length} running
            </span>
          )
        )}
      </div>

      {!containers ? (
        !q.error && <p className="text-sm text-zinc-500">Loading…</p>
      ) : containers.length === 0 ? (
        <p className="text-sm text-zinc-500">No containers. Deploy the stack to create them.</p>
      ) : (
        <div className="-mx-4 overflow-x-auto px-4">
          <table className="w-full min-w-[40rem] text-left text-sm">
            <thead className="text-xs text-zinc-500 dark:text-zinc-400">
              <tr className="border-b border-zinc-200 dark:border-zinc-800">
                <th className="py-1.5 pr-3 font-medium">Service</th>
                <th className="py-1.5 pr-3 font-medium">State</th>
                <th className="py-1.5 pr-3 font-medium">Status</th>
                <th className="py-1.5 pr-3 font-medium">Image</th>
                <th className="py-1.5 pr-3 font-medium">Ports</th>
                <th className="py-1.5 font-medium" />
              </tr>
            </thead>
            <tbody className="divide-y divide-zinc-100 dark:divide-zinc-800">
              {containers.map((c) => (
                <tr key={c.id} className="align-top">
                  <td className="py-2 pr-3">
                    <div className="font-medium">{c.service || '—'}</div>
                    <div className="font-mono text-xs text-zinc-500" title={c.id}>
                      {c.name}
                    </div>
                  </td>
                  <td className="py-2 pr-3">
                    <ContainerStateBadge container={c} />
                  </td>
                  <td className="py-2 pr-3 text-xs text-zinc-600 dark:text-zinc-400">{c.status}</td>
                  <td className="max-w-[14rem] truncate py-2 pr-3 font-mono text-xs" title={c.image}>
                    {c.image}
                  </td>
                  <td className="max-w-[12rem] py-2 pr-3 font-mono text-xs break-words text-zinc-600 dark:text-zinc-400">
                    {c.ports || '—'}
                  </td>
                  <td className="py-2">
                    <div className="flex justify-end gap-1.5">
                      <Link to={`/stacks/${stackId}/containers/${c.id}/logs`} className={btnSecondarySm}>
                        Logs
                      </Link>
                      {c.state === 'running' ? (
                        <Link to={`/stacks/${stackId}/containers/${c.id}/shell`} className={btnSecondarySm}>
                          Shell
                        </Link>
                      ) : (
                        <button type="button" className={btnSecondarySm} disabled title="Container is not running">
                          Shell
                        </button>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}
