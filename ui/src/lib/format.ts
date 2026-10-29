export function shortSha(sha: string): string {
  return sha ? sha.slice(0, 7) : '—'
}

export function relativeTime(iso: string | null, now: number = Date.now()): string {
  if (!iso) return 'never'
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return 'unknown'
  const diff = Math.round((now - t) / 1000)
  const abs = Math.abs(diff)
  const suffix = diff >= 0 ? 'ago' : 'from now'
  if (abs < 10) return 'just now'
  if (abs < 60) return `${abs}s ${suffix}`
  if (abs < 3600) return `${Math.floor(abs / 60)}m ${suffix}`
  if (abs < 86400) return `${Math.floor(abs / 3600)}h ${suffix}`
  if (abs < 86400 * 30) return `${Math.floor(abs / 86400)}d ${suffix}`
  return new Date(t).toLocaleDateString()
}

export function absoluteTime(iso: string | null): string {
  if (!iso) return ''
  const t = Date.parse(iso)
  return Number.isNaN(t) ? iso : new Date(t).toLocaleString()
}

/** "1m 05s"-style duration between two ISO timestamps ("" if unknown). */
export function duration(fromIso: string, toIso: string | null): string {
  if (!toIso) return ''
  const ms = Date.parse(toIso) - Date.parse(fromIso)
  if (Number.isNaN(ms) || ms < 0) return ''
  const s = Math.round(ms / 1000)
  if (s < 60) return `${s}s`
  return `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, '0')}s`
}

/** GitHub commit page for sha, or null for non-GitHub repositories. */
export function commitUrl(repoUrl: string, sha: string): string | null {
  if (!repoUrl.startsWith('https://github.com/') || !sha) return null
  return `${repoUrl.replace(/\.git$/, '')}/commit/${sha}`
}

export const SLUG_RE = /^[a-z0-9][a-z0-9-]{0,62}$/

export function isBusy(status: string | undefined): boolean {
  return status === 'queued' || status === 'deploying'
}

export function payloadUrl(webhookUrl: string): string {
  return window.location.origin + webhookUrl
}
