import { lazy, Suspense, useEffect, useState } from 'react'
import { Navigate, Outlet, Route, Routes, useLocation } from 'react-router'
import { ApiError, api, errorMessage } from './api/client'
import Layout from './components/Layout'
import ContainerLogs from './pages/ContainerLogs'
import Login from './pages/Login'
import NotFound from './pages/NotFound'
import Registries from './pages/Registries'
import StackDetail from './pages/StackDetail'
import StackForm from './pages/StackForm'
import Stacks from './pages/Stacks'

// xterm.js is large; load the terminal page on demand.
const ContainerShell = lazy(() => import('./pages/ContainerShell'))

type AuthState = 'checking' | 'authed' | 'anon'

function RequireAuth({ auth, onLogout }: { auth: AuthState; onLogout: () => void }) {
  const location = useLocation()
  if (auth === 'anon') {
    return <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />
  }
  return (
    <Layout onLogout={onLogout}>
      <Outlet />
    </Layout>
  )
}

export default function App() {
  const [auth, setAuth] = useState<AuthState>('checking')
  const [authError, setAuthError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    api
      .me()
      .then(() => {
        if (!cancelled) setAuth('authed')
      })
      .catch((err: unknown) => {
        if (cancelled) return
        setAuth('anon')
        // 401 is the normal "not logged in" case; surface anything else on the login page.
        if (!(err instanceof ApiError && err.status === 401)) {
          setAuthError(errorMessage(err))
        }
      })
    return () => {
      cancelled = true
    }
  }, [])

  if (auth === 'checking') {
    return (
      <div className="flex min-h-dvh items-center justify-center text-sm text-zinc-500">Loading…</div>
    )
  }

  return (
    <Routes>
      <Route
        path="/login"
        element={
          auth === 'authed' ? (
            <Navigate to="/stacks" replace />
          ) : (
            <Login initialError={authError} onLogin={() => setAuth('authed')} />
          )
        }
      />
      <Route element={<RequireAuth auth={auth} onLogout={() => setAuth('anon')} />}>
        <Route index element={<Navigate to="/stacks" replace />} />
        <Route path="stacks" element={<Stacks />} />
        <Route path="stacks/new" element={<StackForm key="create" mode="create" />} />
        <Route path="stacks/:id" element={<StackDetail />} />
        <Route path="stacks/:id/edit" element={<StackForm key="edit" mode="edit" />} />
        <Route path="stacks/:id/containers/:cid/logs" element={<ContainerLogs />} />
        <Route path="stacks/:id/containers/:cid/shell" element={
            <Suspense fallback={<p className="text-sm text-zinc-500">Loading terminal…</p>}>
              <ContainerShell />
            </Suspense>
          } />
        <Route path="registries" element={<Registries />} />
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  )
}
