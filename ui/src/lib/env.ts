import { parseDotenv, printDotenv, type DotenvError } from './dotenv'

export interface EnvRow {
  id: number
  key: string
  value: string
  /** Key already stored on the server (value unknown to the UI). */
  existing: boolean
  /** For existing rows: whether the user entered a new value. */
  touched: boolean
}

let nextId = 1
export function newRow(partial: Partial<EnvRow> = {}): EnvRow {
  return { id: nextId++, key: '', value: '', existing: false, touched: false, ...partial }
}

export function rowsFromKeys(keys: string[]): EnvRow[] {
  return keys.map((key) => newRow({ key, existing: true }))
}

/** Returns an error message or null. */
export function validateEnv(rows: EnvRow[]): string | null {
  const seen = new Set<string>()
  for (const r of rows) {
    const k = r.key.trim()
    if (!k) {
      if (r.value !== '') return 'Environment variable with a value is missing its name'
      continue
    }
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(k)) {
      return `Invalid environment variable name "${k}" (letters, digits and _; must not start with a digit)`
    }
    if (/[\r\n]/.test(r.value)) return `Value for "${k}" must not contain newlines`
    if (seen.has(k)) return `Duplicate environment variable "${k}"`
    seen.add(k)
  }
  return null
}

/** For create: plain map; blank-key rows are skipped. */
export function envForCreate(rows: EnvRow[]): Record<string, string> {
  const env: Record<string, string> = {}
  for (const r of rows) {
    const k = r.key.trim()
    if (k) env[k] = r.value
  }
  return env
}

/**
 * For update: existing untouched keys → null (keep stored value), edited or new
 * keys → string; removed keys are simply absent (the map replaces the key set).
 */
export function envForUpdate(rows: EnvRow[]): Record<string, string | null> {
  const env: Record<string, string | null> = {}
  for (const r of rows) {
    const k = r.key.trim()
    if (!k) continue
    env[k] = r.existing && !r.touched ? null : r.value
  }
  return env
}

/** Rows → `.env` text (untouched existing rows print as KEEP_MARKER). */
export function rowsToText(rows: EnvRow[]): string {
  const entries = rows
    .filter((r) => r.key.trim())
    .map((r) => ({ key: r.key.trim(), value: r.existing && !r.touched ? null : r.value }))
  return printDotenv(entries)
}

/** `.env` text → rows; `existingKeys` are the variables stored on the server. */
export function textToRows(text: string, existingKeys: string[]): { rows: EnvRow[]; errors: DotenvError[] } {
  const existing = new Set(existingKeys)
  const { entries, errors } = parseDotenv(text, existing)
  const rows = entries.map((e) =>
    e.value === null
      ? newRow({ key: e.key, existing: true })
      : newRow({ key: e.key, value: e.value, existing: existing.has(e.key), touched: existing.has(e.key) }),
  )
  return { rows, errors }
}

export type EnvMode = 'rows' | 'text'

/** Environment variables being edited, either as rows or as `.env` text. */
export interface EnvDraft {
  mode: EnvMode
  rows: EnvRow[]
  text: string
}

const MODE_KEY = 'compose-deploy.envMode'

/** Last used editor mode (a per-browser convenience). */
export function preferredEnvMode(): EnvMode {
  try {
    return localStorage.getItem(MODE_KEY) === 'text' ? 'text' : 'rows'
  } catch {
    return 'rows'
  }
}

export function rememberEnvMode(mode: EnvMode) {
  try {
    localStorage.setItem(MODE_KEY, mode)
  } catch {
    // storage unavailable (private mode etc.)
  }
}

export function newDraft(rows: EnvRow[], mode: EnvMode = preferredEnvMode()): EnvDraft {
  return { mode, rows, text: mode === 'text' ? rowsToText(rows) : '' }
}

/** The draft's rows and the first problem (null when valid). */
export function resolveDraft(d: EnvDraft, existingKeys: string[]): { rows: EnvRow[]; error: string | null } {
  if (d.mode === 'rows') return { rows: d.rows, error: validateEnv(d.rows) }
  const { rows, errors } = textToRows(d.text, existingKeys)
  const first = errors[0]
  return { rows, error: first ? `Environment variables, line ${first.line}: ${first.message}` : null }
}
