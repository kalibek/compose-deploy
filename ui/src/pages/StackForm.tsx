import { useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { api, errorMessage } from '../api/client'
import type { CreateStackResponse, DeployTrigger, Stack, UpdateStackRequest } from '../api/types'
import KeyValueEditor from '../components/KeyValueEditor'
import WebhookSetup from '../components/WebhookSetup'
import { envForCreate, envForUpdate, rowsFromKeys, validateEnv, type EnvRow } from '../lib/env'
import { SLUG_RE } from '../lib/format'
import { btnPrimary, btnSecondary, card, code, errorBox, hint, input, label, link } from '../lib/ui'

interface Props {
  mode: 'create' | 'edit'
}

interface FormState {
  name: string
  repo_url: string
  branch: string
  compose_path: string
  token: string
  clear_token: boolean
  auto_deploy: boolean
  deploy_trigger: DeployTrigger
  workflow_name: string
  env: EnvRow[]
}

const emptyForm: FormState = {
  name: '',
  repo_url: '',
  branch: 'main',
  compose_path: 'docker-compose.yml',
  token: '',
  clear_token: false,
  auto_deploy: true,
  deploy_trigger: 'push',
  workflow_name: '',
  env: [],
}

function formFromStack(s: Stack): FormState {
  return {
    name: s.name,
    repo_url: s.repo_url,
    branch: s.branch,
    compose_path: s.compose_path,
    token: '',
    clear_token: false,
    auto_deploy: s.auto_deploy,
    deploy_trigger: s.deploy_trigger,
    workflow_name: s.workflow_name,
    env: rowsFromKeys(s.env_keys),
  }
}

function validate(f: FormState, mode: Props['mode']): string | null {
  if (mode === 'create' && !SLUG_RE.test(f.name)) {
    return 'Name must be lowercase letters, digits and dashes, starting with a letter or digit (max 63 chars)'
  }
  if (!/^https:\/\/\S+$/.test(f.repo_url.trim())) return 'Repository URL must start with https://'
  if (f.deploy_trigger === 'workflow' && !/^https:\/\/github\.com\/[^/\s]+\/[^/\s]+\/?$/i.test(f.repo_url.trim())) {
    return 'Deploying after a workflow requires a https://github.com/<owner>/<repo> repository'
  }
  const cp = f.compose_path.trim()
  if (cp.startsWith('/') || cp.split('/').includes('..')) {
    return 'Compose path must be relative to the repository root and must not contain ".."'
  }
  return validateEnv(f.env)
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className={card}>
      <h2 className="mb-3 text-sm font-semibold text-zinc-800 dark:text-zinc-200">{title}</h2>
      <div className="space-y-4">{children}</div>
    </section>
  )
}

function TokenHelp({ workflow }: { workflow: boolean }) {
  return (
    <div className="rounded-md border border-sky-200 bg-sky-50 p-3 text-xs leading-relaxed text-sky-900 dark:border-sky-900 dark:bg-sky-950/40 dark:text-sky-200">
      Create a GitHub fine-grained personal access token (<strong>Settings → Developer settings → Fine-grained
      tokens</strong>), Repository access: <strong>Only select repositories</strong> → this repo, Permissions:{' '}
      <strong>Contents → Read-only</strong>
      {workflow && (
        <>
          {' '}
          and <strong>Actions → Read-only</strong> (to check workflow runs)
        </>
      )}
      . Leave empty for public repos{workflow && ' (a token still raises the GitHub API rate limit)'}.{' '}
      <a
        href="https://github.com/settings/personal-access-tokens/new"
        target="_blank"
        rel="noreferrer"
        className={link}
      >
        Create a token on GitHub ↗
      </a>
    </div>
  )
}

function CreatedPanel({ result }: { result: CreateStackResponse }) {
  const navigate = useNavigate()
  const { stack, webhook_secret } = result
  return (
    <div className="mx-auto max-w-2xl space-y-4">
      <h1 className="text-xl font-semibold tracking-tight">Stack “{stack.name}” created</h1>
      <p className="text-sm text-zinc-600 dark:text-zinc-400">
        {!stack.auto_deploy
          ? 'Auto-deploy is off — deploy it manually from the stack page.'
          : stack.deploy_trigger === 'workflow'
            ? 'It will be deployed automatically once the latest commit’s workflow succeeds. You can also deploy it right away from the stack page.'
            : 'It will be deployed automatically on the next poll. You can also deploy it right away from the stack page.'}
      </p>
      <Section title="GitHub webhook (optional)">
        <WebhookSetup
          webhookUrl={stack.webhook_url}
          secret={webhook_secret}
          branch={stack.branch}
          trigger={stack.deploy_trigger}
        />
      </Section>
      <div className="flex justify-end">
        <button type="button" className={btnPrimary} onClick={() => navigate(`/stacks/${stack.id}`)}>
          Go to stack
        </button>
      </div>
    </div>
  )
}

export default function StackForm({ mode }: Props) {
  const { id } = useParams()
  const navigate = useNavigate()
  const [form, setForm] = useState<FormState>(emptyForm)
  const [stack, setStack] = useState<Stack | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [created, setCreated] = useState<CreateStackResponse | null>(null)

  useEffect(() => {
    if (mode !== 'edit' || !id) return
    let cancelled = false
    api
      .getStack(id)
      .then((s) => {
        if (cancelled) return
        setStack(s)
        setForm(formFromStack(s))
      })
      .catch((err: unknown) => {
        if (!cancelled) setLoadError(errorMessage(err))
      })
    return () => {
      cancelled = true
    }
  }, [mode, id])

  function set<K extends keyof FormState>(key: K, value: FormState[K]) {
    setForm((f) => ({ ...f, [key]: value }))
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    const v = validate(form, mode)
    if (v) {
      setError(v)
      return
    }
    setBusy(true)
    setError(null)
    try {
      if (mode === 'create') {
        const token = form.token.trim()
        const res = await api.createStack({
          name: form.name.trim(),
          repo_url: form.repo_url.trim(),
          branch: form.branch.trim() || undefined,
          compose_path: form.compose_path.trim() || undefined,
          token: token || undefined,
          env: envForCreate(form.env),
          auto_deploy: form.auto_deploy,
          deploy_trigger: form.deploy_trigger,
          workflow_name: form.workflow_name.trim(),
        })
        setCreated(res)
      } else if (stack) {
        const body: UpdateStackRequest = {
          repo_url: form.repo_url.trim(),
          branch: form.branch.trim() || 'main',
          compose_path: form.compose_path.trim() || 'docker-compose.yml',
          auto_deploy: form.auto_deploy,
          deploy_trigger: form.deploy_trigger,
          workflow_name: form.workflow_name.trim(),
          env: envForUpdate(form.env),
        }
        if (form.clear_token) body.clear_token = true
        else if (form.token.trim()) body.token = form.token.trim()
        await api.updateStack(stack.id, body)
        navigate(`/stacks/${stack.id}`)
      }
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  if (created) return <CreatedPanel result={created} />

  if (mode === 'edit' && !stack) {
    return loadError ? (
      <div className={errorBox}>{loadError}</div>
    ) : (
      <p className="text-sm text-zinc-500">Loading…</p>
    )
  }

  const nameInvalid = mode === 'create' && form.name !== '' && !SLUG_RE.test(form.name)
  const backTo = stack ? `/stacks/${stack.id}` : '/stacks'

  return (
    <form onSubmit={(e) => void submit(e)} className="mx-auto max-w-2xl space-y-4" noValidate>
      <div className="flex items-center justify-between gap-2">
        <h1 className="text-xl font-semibold tracking-tight">
          {mode === 'create' ? 'New stack' : `Edit ${stack?.name ?? ''}`}
        </h1>
        <Link to={backTo} className={link}>
          Cancel
        </Link>
      </div>

      <Section title="Repository">
        {mode === 'create' && (
          <div>
            <label htmlFor="name" className={label}>
              Name
            </label>
            <input
              id="name"
              className={`${input} font-mono ${nameInvalid ? 'border-red-400 dark:border-red-700' : ''}`}
              value={form.name}
              onChange={(e) => set('name', e.target.value)}
              placeholder="my-app"
              autoComplete="off"
              spellCheck={false}
              required
              disabled={busy}
            />
            <p className={hint}>
              Lowercase letters, digits and dashes (e.g. <code className={code}>my-app</code>). Used as the compose
              project name; cannot be changed later.
            </p>
          </div>
        )}
        <div>
          <label htmlFor="repo_url" className={label}>
            GitHub repository URL
          </label>
          <input
            id="repo_url"
            type="url"
            className={input}
            value={form.repo_url}
            onChange={(e) => set('repo_url', e.target.value)}
            placeholder="https://github.com/owner/repo"
            autoComplete="off"
            spellCheck={false}
            required
            disabled={busy}
          />
        </div>
        <div className="grid gap-4 sm:grid-cols-2">
          <div>
            <label htmlFor="branch" className={label}>
              Branch
            </label>
            <input
              id="branch"
              className={input}
              value={form.branch}
              onChange={(e) => set('branch', e.target.value)}
              placeholder="main"
              autoComplete="off"
              spellCheck={false}
              disabled={busy}
            />
          </div>
          <div>
            <label htmlFor="compose_path" className={label}>
              Compose file path
            </label>
            <input
              id="compose_path"
              className={`${input} font-mono`}
              value={form.compose_path}
              onChange={(e) => set('compose_path', e.target.value)}
              placeholder="docker-compose.yml"
              autoComplete="off"
              spellCheck={false}
              disabled={busy}
            />
            <p className={hint}>Relative to the repository root.</p>
          </div>
        </div>
        <div>
          <label htmlFor="token" className={label}>
            Access token
          </label>
          <input
            id="token"
            type="password"
            className={`${input} font-mono`}
            value={form.token}
            onChange={(e) => set('token', e.target.value)}
            placeholder={
              mode === 'edit' && stack?.has_token ? '•••• unchanged' : 'github_pat_… (optional for public repos)'
            }
            autoComplete="new-password"
            spellCheck={false}
            disabled={busy || form.clear_token}
          />
          {mode === 'edit' && stack?.has_token && (
            <label className="mt-2 flex items-center gap-2 text-sm text-zinc-700 dark:text-zinc-300">
              <input
                type="checkbox"
                checked={form.clear_token}
                onChange={(e) => setForm((f) => ({ ...f, clear_token: e.target.checked, token: '' }))}
                disabled={busy}
              />
              Remove token
            </label>
          )}
          <div className="mt-2">
            <TokenHelp workflow={form.deploy_trigger === 'workflow'} />
          </div>
        </div>
      </Section>

      <Section title="Environment variables">
        <p className="-mt-2 text-xs text-zinc-500 dark:text-zinc-400">
          Passed to <code className={code}>docker compose</code> (available for variable substitution in the compose
          file). Values are stored server-side and never shown again. <code className={code}>COMMIT_SHA</code> and{' '}
          <code className={code}>COMMIT_SHORT_SHA</code> (the deployed commit) are always set, e.g.{' '}
          <code className={code}>image: ghcr.io/acme/app:$&#123;COMMIT_SHA&#125;</code>.
        </p>
        <KeyValueEditor rows={form.env} onChange={(rows) => set('env', rows)} disabled={busy} />
      </Section>

      <Section title="Deployment">
        <label className="flex items-start gap-2 text-sm">
          <input
            type="checkbox"
            className="mt-0.5"
            checked={form.auto_deploy}
            onChange={(e) => set('auto_deploy', e.target.checked)}
            disabled={busy}
          />
          <span>
            <span className="font-medium">Auto-deploy</span>
            <span className="block text-xs text-zinc-500 dark:text-zinc-400">
              Redeploy automatically when new commits land on the branch (polling and GitHub webhook).
            </span>
          </span>
        </label>
        <fieldset className={form.auto_deploy ? '' : 'opacity-60'} disabled={busy}>
          <legend className={label}>Deploy a new commit…</legend>
          <div className="space-y-2">
            <label className="flex items-start gap-2 text-sm">
              <input
                type="radio"
                name="deploy_trigger"
                className="mt-0.5"
                checked={form.deploy_trigger === 'push'}
                onChange={() => set('deploy_trigger', 'push')}
              />
              <span>
                <span className="font-medium">As soon as it is pushed</span>
                <span className="block text-xs text-zinc-500 dark:text-zinc-400">
                  For stacks whose images don’t depend on the commit (or are built by compose itself).
                </span>
              </span>
            </label>
            <label className="flex items-start gap-2 text-sm">
              <input
                type="radio"
                name="deploy_trigger"
                className="mt-0.5"
                checked={form.deploy_trigger === 'workflow'}
                onChange={() => set('deploy_trigger', 'workflow')}
              />
              <span>
                <span className="font-medium">After its GitHub Actions workflow succeeds</span>
                <span className="block text-xs text-zinc-500 dark:text-zinc-400">
                  Waits until the commit’s workflow runs finished successfully (e.g. images built and pushed), then
                  deploys exactly that commit. Failed or cancelled runs are not deployed.
                </span>
              </span>
            </label>
          </div>
          {form.deploy_trigger === 'workflow' && (
            <div className="mt-3 pl-6">
              <label htmlFor="workflow_name" className={label}>
                Workflow <span className="font-normal text-zinc-500">(optional)</span>
              </label>
              <input
                id="workflow_name"
                className={`${input} font-mono`}
                value={form.workflow_name}
                onChange={(e) => set('workflow_name', e.target.value)}
                placeholder="all workflows"
                autoComplete="off"
                spellCheck={false}
              />
              <p className={hint}>
                Name (<code className={code}>build</code>) or file (<code className={code}>build.yml</code>) of the
                workflow that builds your images. Empty = wait for every workflow run of the commit.
              </p>
            </div>
          )}
        </fieldset>
      </Section>

      {error && <div className={errorBox}>{error}</div>}

      <div className="flex justify-end gap-2">
        <Link to={backTo} className={btnSecondary}>
          Cancel
        </Link>
        <button type="submit" className={btnPrimary} disabled={busy}>
          {busy ? 'Saving…' : mode === 'create' ? 'Create stack' : 'Save changes'}
        </button>
      </div>
    </form>
  )
}
