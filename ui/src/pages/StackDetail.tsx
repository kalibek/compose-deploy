import { useCallback, useState, type ReactNode } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { api, errorMessage } from '../api/client'
import ConfirmButton from '../components/ConfirmButton'
import ContainersSection from '../components/ContainersSection'
import DeploymentsSection from '../components/DeploymentsSection'
import LogViewer from '../components/LogViewer'
import StatusBadge from '../components/StatusBadge'
import WebhookSetup from '../components/WebhookSetup'
import { absoluteTime, commitUrl, isBusy, relativeTime, shortSha } from '../lib/format'
import { usePolling } from '../lib/usePolling'
import {
  btnDanger,
  btnDangerSm,
  btnDangerSolid,
  btnDangerSolidSm,
  btnPrimary,
  btnSecondary,
  btnSecondarySm,
  card,
  code,
  errorBox,
  link,
} from '../lib/ui'

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[8rem_1fr] gap-2 py-1.5 text-sm sm:grid-cols-[10rem_1fr]">
      <dt className="text-zinc-500 dark:text-zinc-400">{label}</dt>
      <dd className="min-w-0 break-words">{children}</dd>
    </div>
  )
}

export default function StackDetail() {
  const { id = '' } = useParams()
  const navigate = useNavigate()

  const getStack = useCallback(() => api.getStack(id), [id])
  const getStatus = useCallback(() => api.stackStatus(id), [id])

  const stackQ = usePolling(getStack, 30000, id)
  // Poll fast while a deploy is in progress; the interval is derived from the last response.
  const [fast, setFast] = useState(false)
  const statusQ = usePolling(
    async () => {
      const s = await getStatus()
      setFast(isBusy(s.status))
      return s
    },
    fast ? 2000 : 10000,
    id,
  )

  const [actionError, setActionError] = useState<string | null>(null)
  const [pending, setPending] = useState<'deploy' | 'stop' | null>(null)
  const [downOnDelete, setDownOnDelete] = useState(false)
  const [newSecret, setNewSecret] = useState<string | null>(null)

  const stack = stackQ.data
  const st = statusQ.data

  async function run(kind: 'deploy' | 'stop') {
    setPending(kind)
    setActionError(null)
    try {
      if (kind === 'deploy') await api.deployStack(id)
      else await api.stopStack(id)
      setFast(true)
      await statusQ.reload()
    } catch (err) {
      setActionError(errorMessage(err))
    } finally {
      setPending(null)
    }
  }

  async function remove() {
    setActionError(null)
    try {
      await api.deleteStack(id, downOnDelete)
      navigate('/stacks', { replace: true })
    } catch (err) {
      setActionError(errorMessage(err))
    }
  }

  async function rotate() {
    setActionError(null)
    try {
      const res = await api.rotateWebhookSecret(id)
      setNewSecret(res.webhook_secret)
    } catch (err) {
      setActionError(errorMessage(err))
    }
  }

  if (!stack) {
    if (stackQ.error) {
      return (
        <div className="space-y-3">
          <div className={errorBox}>{stackQ.error}</div>
          <Link to="/stacks" className={link}>
            Back to stacks
          </Link>
        </div>
      )
    }
    return <p className="text-sm text-zinc-500">Loading…</p>
  }

  const status = st?.status ?? stack.status
  const lastSha = st?.last_sha ?? stack.last_sha
  const lastDeployAt = st ? st.last_deploy_at : stack.last_deploy_at
  const lastError = st?.last_error ?? stack.last_error
  const busy = isBusy(status)
  const inline = stack.source === 'inline'
  const lastCommitUrl = commitUrl(stack.repo_url, lastSha)

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <div className="min-w-0 flex-1">
          <Link to="/stacks" className="text-xs text-zinc-500 hover:underline dark:text-zinc-400">
            ← Stacks
          </Link>
          <div className="mt-0.5 flex items-center gap-2">
            <h1 className="truncate text-xl font-semibold tracking-tight">{stack.name}</h1>
            <StatusBadge status={status} />
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <button
            type="button"
            className={btnPrimary}
            onClick={() => void run('deploy')}
            disabled={pending !== null || busy}
          >
            {pending === 'deploy' ? 'Queuing…' : 'Deploy now'}
          </button>
          <ConfirmButton
            onConfirm={() => run('stop')}
            disabled={pending !== null || busy || status === 'stopped'}
            confirmLabel="Confirm stop"
          >
            {pending === 'stop' ? 'Stopping…' : 'Stop'}
          </ConfirmButton>
          <Link to={`/stacks/${stack.id}/edit`} className={btnSecondary}>
            Edit
          </Link>
        </div>
      </div>

      {actionError && <div className={errorBox}>{actionError}</div>}
      {stackQ.error && <div className={errorBox}>{stackQ.error}</div>}

      {stack.waiting_sha && !busy && (
        <div className="rounded-md border border-sky-200 bg-sky-50 px-3 py-2 text-sm text-sky-900 dark:border-sky-900 dark:bg-sky-950/40 dark:text-sky-200">
          <span className="font-medium">
            Commit <code className={code}>{shortSha(stack.waiting_sha)}</code> is waiting for its workflow:
          </span>{' '}
          {stack.waiting_reason}
        </div>
      )}

      {lastError && (
        <div className={errorBox}>
          <div className="mb-1 font-medium">Last deploy failed</div>
          <pre className="font-mono text-xs whitespace-pre-wrap break-all">{lastError}</pre>
        </div>
      )}

      <div className="grid gap-4 lg:grid-cols-2">
        <section className={card}>
          <h2 className="mb-2 text-sm font-semibold">Configuration</h2>
          <dl className="divide-y divide-zinc-100 dark:divide-zinc-800">
            {inline ? (
              <Row label="Source">compose editor</Row>
            ) : (
              <>
                <Row label="Repository">
                  <a href={stack.repo_url} target="_blank" rel="noreferrer" className={link}>
                    {stack.repo_url}
                  </a>
                </Row>
                <Row label="Branch">
                  <code className={code}>{stack.branch}</code>
                </Row>
                <Row label="Compose file">
                  <code className={code}>{stack.compose_path}</code>
                </Row>
                <Row label="Access token">{stack.has_token ? 'configured' : 'none (public repo)'}</Row>
              </>
            )}
            <Row label="Auto-deploy">
              {!stack.auto_deploy
                ? 'disabled'
                : inline
                  ? 'when the compose file is saved'
                  : stack.deploy_trigger === 'workflow'
                  ? `after ${stack.workflow_name ? `workflow “${stack.workflow_name}”` : 'all workflows'} succeed${stack.workflow_name ? 's' : ''}`
                  : 'on push'}
            </Row>
            <Row label="Environment">
              {stack.env_keys.length === 0 ? (
                <span className="text-zinc-500">none</span>
              ) : (
                <span className="flex flex-wrap gap-1">
                  {stack.env_keys.map((k) => (
                    <code key={k} className={code}>
                      {k}
                    </code>
                  ))}
                </span>
              )}
            </Row>
          </dl>
        </section>

        <section className={card}>
          <h2 className="mb-2 text-sm font-semibold">Deployment</h2>
          <dl className="divide-y divide-zinc-100 dark:divide-zinc-800">
            <Row label="Status">
              <StatusBadge status={status} />
            </Row>
            <Row label={inline ? 'Deployed revision' : 'Deployed commit'}>
              {inline ? (
                lastSha ? (
                  <span className="inline-flex flex-wrap items-center gap-2">
                    <code className={code} title={lastSha}>
                      {lastSha.slice(0, 12)}
                    </code>
                    {lastSha !== stack.content_sha && (
                      <span className="text-xs text-amber-700 dark:text-amber-400">saved changes not deployed</span>
                    )}
                  </span>
                ) : (
                  <span className="text-zinc-500">never deployed</span>
                )
              ) : lastSha ? (
                <span className="inline-flex items-center gap-2">
                  <code className={code} title={lastSha}>
                    {shortSha(lastSha)}
                  </code>
                  {lastCommitUrl && (
                    <a
                      href={lastCommitUrl}
                      target="_blank"
                      rel="noreferrer"
                      className={`${link} text-xs`}
                    >
                      view ↗
                    </a>
                  )}
                </span>
              ) : (
                <span className="text-zinc-500">never deployed</span>
              )}
            </Row>
            <Row label="Last deploy">
              <span title={absoluteTime(lastDeployAt)}>
                {relativeTime(lastDeployAt)}
                {lastDeployAt && (
                  <span className="ml-1 text-xs text-zinc-500">({absoluteTime(lastDeployAt)})</span>
                )}
              </span>
            </Row>
            <Row label="Created">{absoluteTime(stack.created_at)}</Row>
          </dl>
        </section>
      </div>

      <ContainersSection stackId={id} fast={busy} />

      <section className={card}>
        <div className="mb-2 flex items-center justify-between gap-2">
          <h2 className="text-sm font-semibold">Deploy log</h2>
          <span className="text-xs text-zinc-500">
            {statusQ.error ? (
              <span className="text-red-600 dark:text-red-400">{statusQ.error}</span>
            ) : busy ? (
              'live — refreshing every 2s'
            ) : (
              'refreshing every 10s'
            )}
          </span>
        </div>
        <LogViewer text={st?.last_log ?? ''} />
      </section>

      <DeploymentsSection stack={stack} fast={busy} />

      {inline && (
        <section className={card}>
          <div className="mb-2 flex items-center justify-between gap-2">
            <h2 className="text-sm font-semibold">Compose file</h2>
            <Link to={`/stacks/${stack.id}/edit`} className={btnSecondarySm}>
              Edit
            </Link>
          </div>
          <pre className="max-h-[28rem] overflow-auto rounded-md bg-zinc-50 p-3 font-mono text-xs leading-relaxed ring-1 ring-zinc-200 dark:bg-zinc-950 dark:ring-zinc-800">
            {stack.compose_content}
          </pre>
        </section>
      )}

      {!inline && (
        <section className={card}>
          <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
            <h2 className="text-sm font-semibold">GitHub webhook</h2>
            <ConfirmButton
              onConfirm={rotate}
              className={btnDangerSm}
              confirmClassName={btnDangerSolidSm}
              confirmLabel="Confirm rotate"
            >
              Rotate secret
            </ConfirmButton>
          </div>
          {newSecret && (
            <p className="mb-2 text-xs text-zinc-600 dark:text-zinc-400">
              Secret rotated. Update it in the GitHub webhook settings — the old secret no longer works.
            </p>
          )}
          <WebhookSetup
            webhookUrl={stack.webhook_url}
            secret={newSecret ?? undefined}
            branch={stack.branch}
            trigger={stack.deploy_trigger}
          />
        </section>
      )}

      <section className={`${card} border-red-200 dark:border-red-900/60`}>
        <h2 className="mb-2 text-sm font-semibold text-red-700 dark:text-red-400">Delete stack</h2>
        <p className="mb-3 text-sm text-zinc-600 dark:text-zinc-400">
          Removes the stack configuration and its working directory. Running containers are left alone unless you
          choose to stop them.
        </p>
        <div className="flex flex-wrap items-center gap-3">
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={downOnDelete} onChange={(e) => setDownOnDelete(e.target.checked)} />
            also run <code className={code}>docker compose down</code>
          </label>
          <ConfirmButton
            onConfirm={remove}
            className={btnDanger}
            confirmClassName={btnDangerSolid}
            confirmLabel="Confirm delete"
          >
            Delete
          </ConfirmButton>
        </div>
      </section>
    </div>
  )
}
