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
