import { lazy, Suspense, useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { api, errorMessage } from '../api/client'
import type {
  CreateStackResponse,
  DeployTrigger,
  Stack,
  StackSource,
  UpdateStackRequest,
  ValidateComposeResponse,
} from '../api/types'
import EnvEditor from '../components/EnvEditor'
import WebhookSetup from '../components/WebhookSetup'
import {
  envForCreate,
  envForUpdate,
  newDraft,
  newRow,
  resolveDraft,
  rowsFromKeys,
  type EnvDraft,
  type EnvMode,
} from '../lib/env'
import { SLUG_RE } from '../lib/format'
import { btnPrimary, btnSecondary, btnSecondarySm, card, code, errorBox, hint, input, label, link } from '../lib/ui'

// CodeMirror is sizeable; load it only for inline stacks.
const ComposeEditor = lazy(() => import('../components/ComposeEditor'))

interface Props {
  mode: 'create' | 'edit'
}

interface FormState {
  name: string
  source: StackSource
  compose_content: string
  repo_url: string
  branch: string
  compose_path: string
  token: string
  clear_token: boolean
  auto_deploy: boolean
  deploy_trigger: DeployTrigger
  workflow_name: string
  env: EnvDraft
}

// GREETING is prefilled under "Environment variables" for new inline stacks and
// shown on the page nginx serves.
const sampleEnv = { key: 'GREETING', value: 'Hello from compose-deploy' }

const composeTemplate = `services:
  web:
    image: nginx:alpine
    ports:
      - "8080:80"
    environment:
      # Shown on the page at http://<host>:8080
      GREETING: \${GREETING:-Hello}
    # $$ escapes compose interpolation, so the shell expands GREETING at start.
    command:
      - sh
      - -c
      - printf '<h1>%s</h1>\\n' "$$GREETING" > /usr/share/nginx/html/index.html && exec nginx -g 'daemon off;'
    restart: unless-stopped
`

const noKeys: string[] = []

const emptyForm = (): FormState => ({
  name: '',
  source: 'git',
  compose_content: composeTemplate,
  repo_url: '',
  branch: 'main',
  compose_path: 'docker-compose.yml',
  token: '',
  clear_token: false,
  auto_deploy: true,
  deploy_trigger: 'push',
  workflow_name: '',
  env: newDraft([]),
})

const sampleDraft = (mode?: EnvMode) => newDraft([newRow(sampleEnv)], mode)

const isEnvEmpty = (d: EnvDraft) => resolveDraft(d, noKeys).rows.every((r) => !r.key.trim())

function isSampleEnv(d: EnvDraft): boolean {
  const rows = resolveDraft(d, noKeys).rows.filter((r) => r.key.trim())
  return rows.length === 1 && rows[0].key.trim() === sampleEnv.key && rows[0].value === sampleEnv.value
}

function formFromStack(s: Stack): FormState {
  return {
    name: s.name,
    source: s.source,
    compose_content: s.compose_content,
    repo_url: s.repo_url,
    branch: s.branch,
    compose_path: s.compose_path,
    token: '',
    clear_token: false,
    auto_deploy: s.auto_deploy,
    deploy_trigger: s.deploy_trigger,
    workflow_name: s.workflow_name,
    env: newDraft(rowsFromKeys(s.env_keys)),
  }
}

function validate(f: FormState, mode: Props['mode'], existingKeys: string[]): string | null {
  if (mode === 'create' && !SLUG_RE.test(f.name)) {
    return 'Name must be lowercase letters, digits and dashes, starting with a letter or digit (max 63 chars)'
  }
  if (f.source === 'inline') {
    if (!f.compose_content.trim()) return 'The compose file is empty'
    return resolveDraft(f.env, existingKeys).error
  }
  if (!/^https:\/\/\S+$/.test(f.repo_url.trim())) return 'Repository URL must start with https://'
  if (f.deploy_trigger === 'workflow' && !/^https:\/\/github\.com\/[^/\s]+\/[^/\s]+\/?$/i.test(f.repo_url.trim())) {
    return 'Deploying after a workflow requires a https://github.com/<owner>/<repo> repository'
  }
  const cp = f.compose_path.trim()
  if (cp.startsWith('/') || cp.split('/').includes('..')) {
    return 'Compose path must be relative to the repository root and must not contain ".."'
  }
  return resolveDraft(f.env, existingKeys).error
}

function Section({ title, children, aside }: { title: string; children: ReactNode; aside?: ReactNode }) {
  return (
    <section className={card}>
      <div className="mb-3 flex items-center justify-between gap-2">
        <h2 className="text-sm font-semibold text-zinc-800 dark:text-zinc-200">{title}</h2>
        {aside}
      </div>
      <div className="space-y-4">{children}</div>
    </section>
  )
}

function TokenHelp({ workflow }: { workflow: boolean }) {
  return (
    <div className="rounded-md border border-sky-200 bg-sky-50 p-3 text-xs leading-relaxed text-sky-900 dark:border-sky-900 dark:bg-sky-950/40 dark:text-sky-200">
      Create a GitHub fine-grained personal access token (
      <strong>Settings → Developer settings → Fine-grained tokens</strong>), Repository access:{' '}
      <strong>Only select repositories</strong> → this repo, Permissions: <strong>Contents → Read-only</strong>
      {workflow && (
        <>
          {' '}
          and <strong>Actions → Read-only</strong> (to check workflow runs)
        </>
      )}
      . Leave empty for public repos
      {workflow && ' (a token still raises the GitHub API rate limit)'}.{' '}
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

function SourceOption({
  checked,
  onSelect,
  title,
  description,
  disabled,
}: {
  checked: boolean
  onSelect: () => void
  title: string
  description: string
  disabled?: boolean
}) {
  return (
    <label
      className={`flex cursor-pointer items-start gap-2 rounded-md border p-3 text-sm ${
        checked
          ? 'border-zinc-900 bg-zinc-50 dark:border-zinc-300 dark:bg-zinc-800/60'
          : 'border-zinc-200 hover:border-zinc-400 dark:border-zinc-800 dark:hover:border-zinc-600'
      }`}
    >
      <input type="radio" name="source" className="mt-0.5" checked={checked} onChange={onSelect} disabled={disabled} />
      <span>
        <span className="font-medium">{title}</span>
        <span className="block text-xs text-zinc-500 dark:text-zinc-400">{description}</span>
      </span>
    </label>
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
  const [validation, setValidation] = useState<ValidateComposeResponse | null>(null)
  const [validating, setValidating] = useState(false)

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

  // Only on create: prefill the variable the sample compose file uses, and drop
  // it again if the user goes back to git without changing it.
  function setSource(source: StackSource) {
    setForm((f) => {
      let env = f.env
      if (source === 'inline' && f.compose_content === composeTemplate && isEnvEmpty(env)) env = sampleDraft(env.mode)
      if (source === 'git' && isSampleEnv(env)) env = newDraft([], env.mode)
      return { ...f, source, env }
    })
  }

  const inline = form.source === 'inline'
  const contentChanged = !!stack && form.compose_content !== stack.compose_content
  // The server deploys an inline stack by itself when a changed file is saved with auto-deploy on.
  const serverWillDeploy = inline && form.auto_deploy && contentChanged && stack?.status !== 'stopped'

  const existingKeys = stack?.env_keys ?? noKeys
  const envRows = () => resolveDraft(form.env, existingKeys).rows

  async function checkCompose() {
    const envError = resolveDraft(form.env, existingKeys).error
    if (envError) {
      setValidation({ valid: false, output: envError })
      return
    }
    setValidating(true)
    setValidation(null)
    try {
      setValidation(
        await api.validateCompose({
          content: form.compose_content,
          stack_id: stack?.id,
          env: stack ? envForUpdate(envRows()) : envForCreate(envRows()),
        }),
      )
    } catch (err) {
      setValidation({ valid: false, output: errorMessage(err) })
    } finally {
      setValidating(false)
    }
  }

  async function submit(e: FormEvent, deployAfter = false) {
    e.preventDefault()
    const v = validate(form, mode, existingKeys)
    if (v) {
      setError(v)
      return
    }
    setBusy(true)
    setError(null)
    try {
      if (mode === 'create') {
        if (inline) {
          const res = await api.createStack({
            name: form.name.trim(),
            source: 'inline',
            compose_content: form.compose_content,
            env: envForCreate(envRows()),
            auto_deploy: form.auto_deploy,
          })
          navigate(`/stacks/${res.stack.id}`)
          return
        }
        const token = form.token.trim()
        const res = await api.createStack({
          name: form.name.trim(),
          repo_url: form.repo_url.trim(),
          branch: form.branch.trim() || undefined,
          compose_path: form.compose_path.trim() || undefined,
          token: token || undefined,
          env: envForCreate(envRows()),
          auto_deploy: form.auto_deploy,
          deploy_trigger: form.deploy_trigger,
          workflow_name: form.workflow_name.trim(),
        })
        setCreated(res)
      } else if (stack) {
        let body: UpdateStackRequest
        if (inline) {
          body = {
            compose_content: form.compose_content,
            auto_deploy: form.auto_deploy,
            env: envForUpdate(envRows()),
          }
        } else {
          body = {
            repo_url: form.repo_url.trim(),
            branch: form.branch.trim() || 'main',
            compose_path: form.compose_path.trim() || 'docker-compose.yml',
            auto_deploy: form.auto_deploy,
            deploy_trigger: form.deploy_trigger,
            workflow_name: form.workflow_name.trim(),
            env: envForUpdate(envRows()),
          }
          if (form.clear_token) body.clear_token = true
          else if (form.token.trim()) body.token = form.token.trim()
        }
        await api.updateStack(stack.id, body)
        if (deployAfter && !serverWillDeploy) await api.deployStack(stack.id)
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
    return loadError ? <div className={errorBox}>{loadError}</div> : <p className="text-sm text-zinc-500">Loading…</p>
  }

  const nameInvalid = mode === 'create' && form.name !== '' && !SLUG_RE.test(form.name)
  const backTo = stack ? `/stacks/${stack.id}` : '/stacks'

  return (
    <form onSubmit={(e) => void submit(e)} className="mx-auto max-w-3xl space-y-4" noValidate>
      <div className="flex items-center justify-between gap-2">
        <h1 className="text-xl font-semibold tracking-tight">
          {mode === 'create' ? 'New stack' : `Edit ${stack?.name ?? ''}`}
        </h1>
        <Link to={backTo} className={link}>
          Cancel
        </Link>
      </div>

      {mode === 'create' && (
        <Section title="Stack">
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
          <div className="grid gap-2 sm:grid-cols-2">
            <SourceOption
              checked={!inline}
              onSelect={() => setSource('git')}
              title="Git repository"
              description="Deploy a compose file from a GitHub repository and follow a branch."
              disabled={busy}
            />
            <SourceOption
              checked={inline}
              onSelect={() => setSource('inline')}
              title="Compose editor"
              description="Write the compose file here; saving it redeploys the stack."
              disabled={busy}
            />
          </div>
        </Section>
      )}

      {inline ? (
        <Section
          title="Compose file"
          aside={
            <button type="button" className={btnSecondarySm} onClick={() => void checkCompose()} disabled={validating}>
              {validating ? 'Validating…' : 'Validate'}
            </button>
          }
        >
          <Suspense
            fallback={
              <div className="h-96 animate-pulse rounded-md bg-zinc-100 dark:bg-zinc-800" aria-label="Loading editor" />
            }
          >
            <ComposeEditor
              value={form.compose_content}
              onChange={(v) => {
                set('compose_content', v)
                setValidation(null)
              }}
              readOnly={busy}
            />
          </Suspense>
          {validation && (
            <div
              className={
                validation.valid
                  ? 'rounded-md border border-green-300 bg-green-50 px-3 py-2 text-sm text-green-800 dark:border-green-900 dark:bg-green-950/40 dark:text-green-300'
                  : errorBox
              }
            >
              <div className="font-medium">
                {validation.valid ? 'Compose file is valid.' : 'Compose file is invalid:'}
              </div>
              {validation.output && (
                <pre className="mt-1 font-mono text-xs whitespace-pre-wrap break-all">{validation.output}</pre>
              )}
            </div>
          )}
          <p className="-mt-2 text-xs text-zinc-500 dark:text-zinc-400">
            Checked with <code className={code}>docker compose config</code>, using the environment variables below.
            Relative paths (e.g. <code className={code}>./data:/data</code>) resolve to the stack’s directory under{' '}
            <code className={code}>DATA_DIR</code> — see the README’s path notes.
          </p>
        </Section>
      ) : (
        <Section title="Repository">
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
                  onChange={(e) =>
                    setForm((f) => ({
                      ...f,
                      clear_token: e.target.checked,
                      token: '',
                    }))
                  }
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
      )}

      <Section title="Environment variables">
        <p className="-mt-2 text-xs text-zinc-500 dark:text-zinc-400">
          Passed to <code className={code}>docker compose</code> (available for variable substitution in the compose
          file). Values are stored server-side and never shown again.
          {!inline && (
            <>
              {' '}
              <code className={code}>COMMIT_SHA</code> and <code className={code}>COMMIT_SHORT_SHA</code> (the deployed
              commit) are always set, e.g. <code className={code}>image: ghcr.io/acme/app:$&#123;COMMIT_SHA&#125;</code>
              .
            </>
          )}
        </p>
        <EnvEditor draft={form.env} onChange={(d) => set('env', d)} existingKeys={existingKeys} disabled={busy} />
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
              {inline
                ? 'Deploy when the stack is created and whenever a changed compose file is saved.'
                : 'Redeploy automatically when new commits land on the branch (polling and GitHub webhook).'}
            </span>
          </span>
        </label>
        {!inline && (
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
        )}
      </Section>

      {error && <div className={errorBox}>{error}</div>}

      <div className="flex flex-wrap items-center justify-end gap-2">
        {mode === 'edit' && inline && serverWillDeploy && (
          <span className="mr-auto text-xs text-zinc-500">
            Saving the changed compose file will redeploy the stack.
          </span>
        )}
        <Link to={backTo} className={btnSecondary}>
          Cancel
        </Link>
        {mode === 'edit' && inline && !serverWillDeploy && (
          <button type="button" className={btnSecondary} disabled={busy} onClick={(e) => void submit(e, true)}>
            Save & deploy
          </button>
        )}
        <button type="submit" className={btnPrimary} disabled={busy}>
          {busy
            ? 'Saving…'
            : mode === 'create'
              ? inline && form.auto_deploy
                ? 'Create & deploy'
                : 'Create stack'
              : serverWillDeploy
                ? 'Save & deploy'
                : 'Save changes'}
        </button>
      </div>
    </form>
  )
}
