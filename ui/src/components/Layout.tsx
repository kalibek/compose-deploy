import { useState, type ReactNode } from 'react'
import { NavLink, useNavigate } from 'react-router'
import { api } from '../api/client'

function navCls({ isActive }: { isActive: boolean }) {
  return `rounded-md px-2.5 py-1.5 text-sm font-medium ${
    isActive
      ? 'bg-zinc-100 text-zinc-900 dark:bg-zinc-800 dark:text-zinc-100'
      : 'text-zinc-600 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100'
  }`
}

export default function Layout({ children, onLogout }: { children: ReactNode; onLogout: () => void }) {
  const navigate = useNavigate()
  const [busy, setBusy] = useState(false)

  async function logout() {
    setBusy(true)
    try {
      await api.logout()
    } catch {
      // ignore; we drop local auth state regardless
    } finally {
      setBusy(false)
      onLogout()
      navigate('/login', { replace: true })
    }
  }

  return (
    <div className="min-h-dvh">
      <header className="sticky top-0 z-10 border-b border-zinc-200 bg-white/90 backdrop-blur dark:border-zinc-800 dark:bg-zinc-950/90">
        <div className="mx-auto flex max-w-5xl items-center gap-2 px-4 py-2.5">
          <NavLink to="/stacks" className="mr-2 flex items-center gap-2 font-semibold tracking-tight">
            <svg viewBox="0 0 16 16" className="h-5 w-5 text-sky-600 dark:text-sky-400" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden>
              <path d="M8 1.5 14 5v6l-6 3.5L2 11V5z" />
              <path d="M2 5l6 3.5L14 5M8 8.5V14.5" />
            </svg>
            <span className="hidden sm:inline">compose-deploy</span>
          </NavLink>
          <nav className="flex items-center gap-1">
            <NavLink to="/stacks" className={navCls}>
              Stacks
            </NavLink>
            <NavLink to="/registries" className={navCls}>
              Registries
            </NavLink>
          </nav>
          <button
            type="button"
            onClick={() => void logout()}
            disabled={busy}
            className="ml-auto rounded-md px-2.5 py-1.5 text-sm text-zinc-600 hover:text-zinc-900 disabled:opacity-50 dark:text-zinc-400 dark:hover:text-zinc-100"
          >
            Logout
          </button>
        </div>
      </header>
      <main className="mx-auto max-w-5xl px-4 py-6">{children}</main>
    </div>
  )
}
