import { useState } from 'react'
import { Link } from 'react-router'
import { api, errorMessage } from '../api/client'
import type { Stack } from '../api/types'
import StatusBadge from '../components/StatusBadge'
import { absoluteTime, isBusy, relativeTime, shortSha } from '../lib/format'
import { usePolling } from '../lib/usePolling'
import { btnPrimary, btnSecondarySm, card, errorBox } from '../lib/ui'

function repoLabel(url: string): string {
  return url.replace(/^https:\/\/(www\.)?github\.com\//, '').replace(/\.git$/, '')
}

function StackRow({ stack, onChanged }: { stack: Stack; onChanged: () => void }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function deploy() {
    setBusy(true)
    setError(null)
    try {
      await api.deployStack(stack.id)
      onChanged()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <li className="px-4 py-3">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <div className="min-w-0 flex-1 basis-56">
          <div className="flex items-center gap-2">
            <Link
              to={`/stacks/${stack.id}`}
              className="truncate font-medium text-zinc-900 hover:underline dark:text-zinc-100"
            >
              {stack.name}
            </Link>
            <StatusBadge status={stack.status} />
            {stack.waiting_sha && (
              <span className="text-xs text-sky-700 dark:text-sky-400" title={stack.waiting_reason}>
                {shortSha(stack.waiting_sha)} waiting for CI
              </span>
            )}
          </div>
          <div className="mt-0.5 truncate text-xs text-zinc-500 dark:text-zinc-400" title={stack.repo_url}>
            {stack.source === 'inline' ? (
              'compose editor'
            ) : (
              <>
                {repoLabel(stack.repo_url)}
                <span className="text-zinc-400 dark:text-zinc-500">@</span>
                {stack.branch}
              </>
            )}
          </div>
        </div>
        <div className="flex items-center gap-4 text-xs text-zinc-600 dark:text-zinc-400">
          <span className="font-mono" title={stack.last_sha || 'never deployed'}>
            {shortSha(stack.last_sha)}
          </span>
          <span className="w-20" title={absoluteTime(stack.last_deploy_at)}>
            {relativeTime(stack.last_deploy_at)}
          </span>
          <span
            className={`inline-flex items-center gap-1 ${stack.auto_deploy ? 'text-green-700 dark:text-green-400' : ''}`}
            title={stack.auto_deploy ? 'Auto-deploy enabled' : 'Auto-deploy disabled'}
          >
            <svg viewBox="0 0 16 16" className="h-3.5 w-3.5" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden>
              <path d="M13.5 8a5.5 5.5 0 1 1-1.6-3.9M13.5 2.5v2.5H11" />
            </svg>
            {stack.auto_deploy ? 'auto' : 'manual'}
          </span>
          <button
            type="button"
            className={btnSecondarySm}
            onClick={() => void deploy()}
            disabled={busy || isBusy(stack.status)}
          >
            {busy ? 'Queuing…' : 'Deploy'}
          </button>
        </div>
      </div>
      {error && <div className={`${errorBox} mt-2`}>{error}</div>}
    </li>
  )
}

export default function Stacks() {
  const { data, error, loading, reload } = usePolling(api.listStacks, 5000)

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-2">
        <h1 className="text-xl font-semibold tracking-tight">Stacks</h1>
        <Link to="/stacks/new" className={btnPrimary}>
          New stack
        </Link>
      </div>

      {error && <div className={errorBox}>{error}</div>}

      {loading && !data ? (
        <p className="text-sm text-zinc-500">Loading…</p>
      ) : data && data.length === 0 ? (
        <div className={`${card} py-10 text-center`}>
          <p className="text-sm text-zinc-600 dark:text-zinc-400">No stacks yet.</p>
          <Link to="/stacks/new" className={`${btnPrimary} mt-4`}>
            Create your first stack
          </Link>
        </div>
      ) : data ? (
        <ul className="divide-y divide-zinc-200 overflow-hidden rounded-lg border border-zinc-200 bg-white shadow-sm dark:divide-zinc-800 dark:border-zinc-800 dark:bg-zinc-900">
          {data.map((s) => (
            <StackRow key={s.id} stack={s} onChanged={() => void reload()} />
          ))}
        </ul>
      ) : null}
    </div>
  )
}
