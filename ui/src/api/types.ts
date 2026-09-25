export type StackStatus = 'idle' | 'queued' | 'deploying' | 'success' | 'failed' | 'stopped'

/** push: deploy every new commit; workflow: deploy once the commit's GitHub Actions run(s) succeeded. */
export type DeployTrigger = 'push' | 'workflow'

export interface Stack {
  id: number
  name: string
  repo_url: string
  branch: string
  compose_path: string
  auto_deploy: boolean
  has_token: boolean
  env_keys: string[]
  webhook_url: string
  status: StackStatus
  last_sha: string
  last_deploy_at: string | null
  last_error: string
  deploy_trigger: DeployTrigger
  /** Workflow name or file to wait for ("" = all workflows). */
  workflow_name: string
  /** Newer commit waiting for its workflow ("" = none). */
  waiting_sha: string
  waiting_reason: string
  created_at: string
  updated_at: string
}

export interface CreateStackRequest {
  name: string
  repo_url: string
  branch?: string
  compose_path?: string
  token?: string
  env?: Record<string, string>
  auto_deploy: boolean
  deploy_trigger?: DeployTrigger
  workflow_name?: string
}

export interface CreateStackResponse {
  stack: Stack
  webhook_secret: string
}

export interface UpdateStackRequest {
  repo_url?: string
  branch?: string
  compose_path?: string
  auto_deploy?: boolean
  token?: string
  clear_token?: boolean
  /** Replaces the set of keys; null value = keep existing stored value. */
  env?: Record<string, string | null>
  deploy_trigger?: DeployTrigger
  workflow_name?: string
}

export interface StackStatusResponse {
  status: StackStatus
  last_sha: string
  last_deploy_at: string | null
  last_log: string
  last_error: string
}

export interface QueuedResponse {
  status: 'queued'
}

export interface WebhookSecretResponse {
  webhook_secret: string
}

export interface Registry {
  id: number
  url: string
  username: string
  has_password: boolean
  created_at: string
}

export interface RegistryRequest {
  url: string
  username: string
  password?: string
}

export type ContainerState = 'created' | 'running' | 'paused' | 'restarting' | 'removing' | 'exited' | 'dead'

export interface Container {
  id: string
  name: string
  service: string
  image: string
  state: ContainerState | string
  /** Human readable, e.g. "Up 2 hours (healthy)". */
  status: string
  health: 'healthy' | 'unhealthy' | 'starting' | ''
  ports: string
  created_at: string
}
