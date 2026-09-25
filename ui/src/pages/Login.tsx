import { useState, type FormEvent } from 'react'
import { useLocation, useNavigate } from 'react-router'
import { ApiError, api, errorMessage } from '../api/client'
import { btnPrimary, errorBox, input, label } from '../lib/ui'

interface Props {
  onLogin: () => void
  initialError?: string | null
}

export default function Login({ onLogin, initialError }: Props) {
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(initialError ?? null)
  const [busy, setBusy] = useState(false)
  const navigate = useNavigate()
  const location = useLocation()
  const from = (location.state as { from?: string } | null)?.from

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!password) return
    setBusy(true)
    setError(null)
    try {
      await api.login(password)
      onLogin()
      navigate(from && from !== '/login' ? from : '/stacks', { replace: true })
    } catch (err) {
      if (err instanceof ApiError && err.status === 429) {
        setError('Too many login attempts. Please wait and try again.')
      } else if (err instanceof ApiError && err.status === 401) {
        setError('Invalid password')
      } else {
        setError(errorMessage(err))
      }
      setBusy(false)
    }
  }

  return (
    <div className="flex min-h-dvh items-center justify-center px-4">
      <form
        onSubmit={(e) => void submit(e)}
        className="w-full max-w-sm rounded-lg border border-zinc-200 bg-white p-6 shadow-sm dark:border-zinc-800 dark:bg-zinc-900"
      >
        <div className="mb-5 flex items-center gap-2">
          <svg viewBox="0 0 16 16" className="h-6 w-6 text-sky-600 dark:text-sky-400" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden>
            <path d="M8 1.5 14 5v6l-6 3.5L2 11V5z" />
            <path d="M2 5l6 3.5L14 5M8 8.5V14.5" />
          </svg>
          <h1 className="text-lg font-semibold tracking-tight">compose-deploy</h1>
        </div>
        <label htmlFor="password" className={label}>
          Admin password
        </label>
        <input
          id="password"
          type="password"
          className={input}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          autoComplete="current-password"
          autoFocus
          required
          disabled={busy}
        />
        {error && <div className={`${errorBox} mt-3`}>{error}</div>}
        <button type="submit" className={`${btnPrimary} mt-4 w-full`} disabled={busy || !password}>
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </div>
  )
}
