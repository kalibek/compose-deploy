import { useState, type FormEvent } from 'react'
import { api, errorMessage } from '../api/client'
import type { Registry, RegistryRequest } from '../api/types'
import ConfirmButton from '../components/ConfirmButton'
import { absoluteTime } from '../lib/format'
import { usePolling } from '../lib/usePolling'
import {
  btnDangerSm,
  btnDangerSolidSm,
  btnPrimary,
  btnSecondary,
  btnSecondarySm,
  card,
  code,
  errorBox,
  input,
  label,
} from '../lib/ui'

type Editing = { kind: 'new' } | { kind: 'edit'; registry: Registry } | null

function RegistryForm({
  editing,
  onDone,
  onCancel,
}: {
  editing: Exclude<Editing, null>
  onDone: () => void
  onCancel: () => void
}) {
  const existing = editing.kind === 'edit' ? editing.registry : null
  const [url, setUrl] = useState(existing?.url ?? '')
  const [username, setUsername] = useState(existing?.username ?? '')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!url.trim()) {
      setError('Registry URL is required')
      return
    }
    setBusy(true)
    setError(null)
    const body: RegistryRequest = { url: url.trim(), username: username.trim() }
    if (password) body.password = password
    try {
      if (existing) await api.updateRegistry(existing.id, body)
      else await api.createRegistry(body)
      onDone()
    } catch (err) {
      setError(errorMessage(err))
      setBusy(false)
    }
  }

  return (
    <form onSubmit={(e) => void submit(e)} className={`${card} space-y-3`} noValidate>
      <h2 className="text-sm font-semibold">{existing ? `Edit ${existing.url}` : 'Add registry'}</h2>
      <div className="grid gap-3 sm:grid-cols-3">
        <div>
          <label htmlFor="reg-url" className={label}>
            Registry URL
          </label>
          <input
            id="reg-url"
            className={`${input} font-mono`}
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            placeholder="ghcr.io"
            autoComplete="off"
            spellCheck={false}
            required
            disabled={busy}
            autoFocus
          />
        </div>
        <div>
          <label htmlFor="reg-user" className={label}>
            Username
          </label>
          <input
            id="reg-user"
            className={input}
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="off"
            spellCheck={false}
            disabled={busy}
          />
        </div>
        <div>
          <label htmlFor="reg-pass" className={label}>
            Password / token
          </label>
          <input
            id="reg-pass"
            type="password"
            className={`${input} font-mono`}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder={existing?.has_password ? '•••• unchanged' : ''}
            autoComplete="new-password"
            spellCheck={false}
            disabled={busy}
          />
        </div>
      </div>
      {error && <div className={errorBox}>{error}</div>}
      <div className="flex justify-end gap-2">
        <button type="button" className={btnSecondary} onClick={onCancel} disabled={busy}>
          Cancel
        </button>
        <button type="submit" className={btnPrimary} disabled={busy}>
          {busy ? 'Saving…' : existing ? 'Save' : 'Add registry'}
        </button>
      </div>
    </form>
  )
}

export default function Registries() {
  const { data, error: loadError, reload: load } = usePolling(api.listRegistries, 60000)
  const items = data ?? null
  const [actionError, setError] = useState<string | null>(null)
  const error = actionError ?? loadError
  const [editing, setEditing] = useState<Editing>(null)

  async function remove(r: Registry) {
    setError(null)
    try {
      await api.deleteRegistry(r.id)
      if (editing?.kind === 'edit' && editing.registry.id === r.id) setEditing(null)
      await load()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-2">
        <h1 className="text-xl font-semibold tracking-tight">Registries</h1>
        {editing?.kind !== 'new' && (
          <button type="button" className={btnPrimary} onClick={() => setEditing({ kind: 'new' })}>
            Add registry
          </button>
        )}
      </div>

      <p className="text-sm text-zinc-600 dark:text-zinc-400">
        Credentials are applied for every stack when pulling images. Use e.g. <code className={code}>ghcr.io</code>,{' '}
        <code className={code}>registry.gitlab.com</code>, or <code className={code}>https://index.docker.io/v1/</code>{' '}
        for Docker Hub. For tokens (GitHub, GitLab) use the token as the password.
      </p>

      {editing && (
        <RegistryForm
          key={editing.kind === 'edit' ? editing.registry.id : 'new'}
          editing={editing}
          onCancel={() => setEditing(null)}
          onDone={() => {
            setEditing(null)
            void load()
          }}
        />
      )}

      {error && <div className={errorBox}>{error}</div>}

      {items === null ? (
        !error && <p className="text-sm text-zinc-500">Loading…</p>
      ) : items.length === 0 ? (
        <div className={`${card} py-8 text-center text-sm text-zinc-500`}>No registries configured.</div>
      ) : (
        <div className="overflow-x-auto rounded-lg border border-zinc-200 bg-white shadow-sm dark:border-zinc-800 dark:bg-zinc-900">
          <table className="w-full text-left text-sm">
            <thead className="border-b border-zinc-200 text-xs text-zinc-500 uppercase dark:border-zinc-800 dark:text-zinc-400">
              <tr>
                <th className="px-4 py-2 font-medium">URL</th>
                <th className="px-4 py-2 font-medium">Username</th>
                <th className="hidden px-4 py-2 font-medium sm:table-cell">Password</th>
                <th className="hidden px-4 py-2 font-medium md:table-cell">Added</th>
                <th className="px-4 py-2" />
              </tr>
            </thead>
            <tbody className="divide-y divide-zinc-100 dark:divide-zinc-800">
              {items.map((r) => (
                <tr key={r.id}>
                  <td className="px-4 py-2 font-mono text-xs break-all">{r.url}</td>
                  <td className="px-4 py-2">{r.username || <span className="text-zinc-400">—</span>}</td>
                  <td className="hidden px-4 py-2 sm:table-cell">
                    {r.has_password ? '••••' : <span className="text-zinc-400">none</span>}
                  </td>
                  <td className="hidden px-4 py-2 text-xs text-zinc-500 md:table-cell">{absoluteTime(r.created_at)}</td>
                  <td className="px-4 py-2">
                    <div className="flex justify-end gap-1.5">
                      <button
                        type="button"
                        className={btnSecondarySm}
                        onClick={() => setEditing({ kind: 'edit', registry: r })}
                      >
                        Edit
                      </button>
                      <ConfirmButton
                        onConfirm={() => remove(r)}
                        className={btnDangerSm}
                        confirmClassName={btnDangerSolidSm}
                        confirmLabel="Confirm"
                      >
                        Delete
                      </ConfirmButton>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
