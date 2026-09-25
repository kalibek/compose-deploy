import type {
  Container,
  CreateStackRequest,
  CreateStackResponse,
  QueuedResponse,
  Registry,
  RegistryRequest,
  Stack,
  StackStatusResponse,
  UpdateStackRequest,
  ValidateComposeRequest,
  ValidateComposeResponse,
  WebhookSecretResponse,
} from './types'

export class ApiError extends Error {
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

interface RequestOptions {
  method?: string
  body?: unknown
  /** Skip the global 401 → /login redirect (used by login and the auth probe). */
  noAuthRedirect?: boolean
}

function redirectToLogin() {
  if (window.location.pathname !== '/login') {
    window.location.assign('/login')
  }
}

async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const method = opts.method ?? 'GET'
  const headers: Record<string, string> = { Accept: 'application/json' }
  // The backend requires JSON on every mutating request (CSRF guard), even body-less ones.
  if (method !== 'GET' && method !== 'HEAD') {
    headers['Content-Type'] = 'application/json'
  }
  let body: BodyInit | undefined
  if (opts.body !== undefined) {
    body = JSON.stringify(opts.body)
  }

  let res: Response
  try {
    res = await fetch(`/api${path}`, {
      method,
      headers,
      body,
      credentials: 'include',
    })
  } catch {
    throw new ApiError(0, 'Network error: could not reach the server')
  }

  if (res.status === 401 && !opts.noAuthRedirect) {
    redirectToLogin()
  }

  if (!res.ok) {
    let message = `${res.status} ${res.statusText}`.trim()
    try {
      const data: unknown = await res.json()
      if (data && typeof data === 'object' && 'error' in data && typeof data.error === 'string') {
        message = data.error
      }
    } catch {
      // non-JSON error body; keep status text
    }
    throw new ApiError(res.status, message)
  }

  if (res.status === 204) return undefined as T
  const text = await res.text()
  return (text ? JSON.parse(text) : undefined) as T
}

export function errorMessage(err: unknown): string {
  if (err instanceof Error) return err.message
  return String(err)
}

export const api = {
  login: (password: string) =>
    request<void>('/auth/login', { method: 'POST', body: { password }, noAuthRedirect: true }),
  logout: () => request<void>('/auth/logout', { method: 'POST', noAuthRedirect: true }),
  me: () => request<{ authenticated: boolean }>('/auth/me', { noAuthRedirect: true }),

  listStacks: () => request<Stack[]>('/stacks'),
  getStack: (id: number | string) => request<Stack>(`/stacks/${id}`),
  createStack: (body: CreateStackRequest) =>
    request<CreateStackResponse>('/stacks', { method: 'POST', body }),
  updateStack: (id: number | string, body: UpdateStackRequest) =>
    request<Stack>(`/stacks/${id}`, { method: 'PUT', body }),
  deleteStack: (id: number | string, down: boolean) =>
    request<void>(`/stacks/${id}${down ? '?down=true' : ''}`, { method: 'DELETE' }),
  deployStack: (id: number | string) =>
    request<QueuedResponse>(`/stacks/${id}/deploy`, { method: 'POST' }),
  stopStack: (id: number | string) =>
    request<QueuedResponse>(`/stacks/${id}/stop`, { method: 'POST' }),
  stackStatus: (id: number | string) => request<StackStatusResponse>(`/stacks/${id}/status`),
  rotateWebhookSecret: (id: number | string) =>
    request<WebhookSecretResponse>(`/stacks/${id}/webhook-secret`, { method: 'POST' }),

  validateCompose: (body: ValidateComposeRequest) =>
    request<ValidateComposeResponse>('/compose/validate', { method: 'POST', body }),
  listContainers: (id: number | string) => request<Container[]>(`/stacks/${id}/containers`),

  listRegistries: () => request<Registry[]>('/registries'),
  createRegistry: (body: RegistryRequest) =>
    request<Registry>('/registries', { method: 'POST', body }),
  updateRegistry: (id: number, body: RegistryRequest) =>
    request<Registry>(`/registries/${id}`, { method: 'PUT', body }),
  deleteRegistry: (id: number) => request<void>(`/registries/${id}`, { method: 'DELETE' }),
}

/** Server-Sent Events URL streaming `docker logs` of a stack container. */
export function containerLogsUrl(stackId: number | string, containerId: string, tail: number, follow: boolean): string {
  return `/api/stacks/${stackId}/containers/${containerId}/logs?tail=${tail}&follow=${follow}`
}

/** WebSocket URL for an interactive shell in a stack container. */
export function containerExecUrl(stackId: number | string, containerId: string, cols: number, rows: number): string {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${window.location.host}/api/stacks/${stackId}/containers/${containerId}/exec?cols=${cols}&rows=${rows}`
}
