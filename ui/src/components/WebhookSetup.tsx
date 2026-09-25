import type { DeployTrigger } from '../api/types'
import CopyButton from './CopyButton'
import { payloadUrl } from '../lib/format'
import { code } from '../lib/ui'

interface Props {
  webhookUrl: string
  /** Plaintext secret — only available right after create/rotate. */
  secret?: string
  branch: string
  trigger: DeployTrigger
}

function Field({ label, value, mono = true }: { label: string; value: string; mono?: boolean }) {
  return (
    <div>
      <div className="mb-1 text-xs font-medium text-zinc-500 dark:text-zinc-400">{label}</div>
      <div className="flex items-center gap-2">
        <code
          className={`min-w-0 flex-1 truncate rounded-md border border-zinc-200 bg-zinc-50 px-2 py-1.5 text-xs dark:border-zinc-800 dark:bg-zinc-950 ${mono ? 'font-mono' : ''}`}
          title={value}
        >
          {value}
        </code>
        <CopyButton text={value} />
      </div>
    </div>
  )
}

/** Webhook secret (shown once) + GitHub webhook setup instructions. */
export default function WebhookSetup({ webhookUrl, secret, branch, trigger }: Props) {
  return (
    <div className="space-y-3">
      {secret && (
        <div className="rounded-md border border-amber-300 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-200">
          <p className="mb-2 font-medium">
            Copy the webhook secret now — it will not be shown again.
          </p>
          <Field label="Webhook secret" value={secret} />
        </div>
      )}
      <Field label="Payload URL" value={payloadUrl(webhookUrl)} />
      <ol className="list-decimal space-y-1 pl-5 text-sm text-zinc-700 dark:text-zinc-300">
        <li>
          In the GitHub repository open <strong>Settings → Webhooks → Add webhook</strong>.
        </li>
        <li>
          <strong>Payload URL</strong>: the URL above (must be reachable from GitHub).
        </li>
        <li>
          <strong>Content type</strong>: <code className={code}>application/json</code>
        </li>
        <li>
          <strong>Secret</strong>: {secret ? 'the webhook secret above' : 'the secret shown when the stack was created (rotate it to get a new one)'}.
        </li>
        {trigger === 'workflow' ? (
          <li>
            <strong>Which events</strong>: “Let me select individual events” → check <strong>Workflow runs</strong> (you
            can uncheck Pushes).
          </li>
        ) : (
          <li>
            <strong>Which events</strong>: “Just the <code className={code}>push</code> event”.
          </li>
        )}
      </ol>
      <p className="text-xs text-zinc-500 dark:text-zinc-400">
        {trigger === 'workflow' ? (
          <>
            A successful workflow run for <code className={code}>{branch}</code> triggers a deploy of that commit when
            auto-deploy is enabled.
          </>
        ) : (
          <>
            Pushes to <code className={code}>{branch}</code> trigger a redeploy when auto-deploy is enabled.
          </>
        )}{' '}
        The branch is also polled periodically, so the webhook is optional but makes deploys immediate.
      </p>
    </div>
  )
}
