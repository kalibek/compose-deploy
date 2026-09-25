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

export const SLUG_RE = /^[a-z0-9][a-z0-9-]{0,62}$/

export function isBusy(status: string | undefined): boolean {
  return status === 'queued' || status === 'deploying'
}

export function payloadUrl(webhookUrl: string): string {
  return window.location.origin + webhookUrl
}
