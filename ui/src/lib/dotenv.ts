// Parsing and printing of the `.env` text format used by the environment
// variable text editor. The accepted syntax is the single-line subset of
// docker compose's dotenv format:
//
//   # comment
//   KEY=value              unquoted; a " #" starts a trailing comment
//   export KEY=value       optional "export " prefix
//   KEY='literal value'    single quotes: taken literally
//   KEY="a \"b\" \\ \$"    double quotes: \\ \" \$ escapes
//   KEY=                   empty value
//   KEY=<unchanged>        keep the stored value of an existing variable
//
// Multi-line values are not supported (the server rejects newlines in values).

export const KEEP_MARKER = '<unchanged>'

const KEY_RE = /^[A-Za-z_][A-Za-z0-9_]*$/
// Values printed without quotes: no whitespace, quotes, #, $ or backslashes.
const BARE_RE = /^[A-Za-z0-9_./:@%+,=~^-]+$/

export interface DotenvEntry {
  key: string
  /** New value; null means "keep the stored value" (KEEP_MARKER). */
  value: string | null
  line: number
}

export interface DotenvError {
  line: number
  message: string
}

export interface DotenvResult {
  entries: DotenvEntry[]
  errors: DotenvError[]
}

/** Rest of a line after a closing quote: only whitespace or a # comment. */
function trailingOk(rest: string): boolean {
  const t = rest.trim()
  return t === '' || t.startsWith('#')
}

function parseValue(raw: string): { value: string; quoted: boolean } | { error: string } {
  const v = raw.replace(/^[ \t]+/, '')
  if (v.startsWith("'")) {
    const end = v.indexOf("'", 1)
    if (end < 0) return { error: 'unterminated single quote (multi-line values are not supported)' }
    if (!trailingOk(v.slice(end + 1))) return { error: 'unexpected text after the closing quote' }
    return { value: v.slice(1, end), quoted: true }
  }
  if (v.startsWith('"')) {
    let out = ''
    for (let i = 1; i < v.length; i++) {
      const c = v[i]
      if (c === '\\') {
        const n = v[i + 1]
        if (n === undefined) break
        if (n === 'n' || n === 'r') return { error: 'newlines in values are not supported' }
        out += n === '\\' || n === '"' || n === '$' ? n : c + n
        i++
        continue
      }
      if (c === '"') {
        if (!trailingOk(v.slice(i + 1))) return { error: 'unexpected text after the closing quote' }
        return { value: out, quoted: true }
      }
      out += c
    }
    return { error: 'unterminated double quote (multi-line values are not supported)' }
  }
  // Unquoted: a whitespace-preceded # starts a comment.
  const hash = v.search(/[ \t]#/)
  const value = (hash >= 0 ? v.slice(0, hash) : v).replace(/[ \t]+$/, '')
  return { value, quoted: false }
}

/**
 * Parses `.env` text. `existingKeys` are variables already stored on the
 * server; only those may use KEEP_MARKER.
 */
export function parseDotenv(text: string, existingKeys: ReadonlySet<string> = new Set()): DotenvResult {
  const entries: DotenvEntry[] = []
  const errors: DotenvError[] = []
  const seen = new Map<string, number>()
  text.split(/\r?\n/).forEach((rawLine, i) => {
    const line = i + 1
    let s = rawLine.trim()
    if (s === '' || s.startsWith('#')) return
    if (/^export[ \t]/.test(s)) s = s.slice(7).trimStart()
    const eq = s.indexOf('=')
    if (eq < 0) {
      errors.push({ line, message: 'expected KEY=value' })
      return
    }
    const key = s.slice(0, eq).trim()
    if (!KEY_RE.test(key)) {
      errors.push({
        line,
        message: key
          ? `invalid name "${key}" (letters, digits and _; must not start with a digit)`
          : 'missing variable name before "="',
      })
      return
    }
    const prev = seen.get(key)
    if (prev !== undefined) {
      errors.push({ line, message: `duplicate variable "${key}" (first defined on line ${prev})` })
      return
    }
    seen.set(key, line)
    const parsed = parseValue(s.slice(eq + 1))
    if ('error' in parsed) {
      errors.push({ line, message: `${key}: ${parsed.error}` })
      return
    }
    if (parsed.value.includes('\0')) {
      errors.push({ line, message: `${key}: value must be text` })
      return
    }
    if (!parsed.quoted && parsed.value === KEEP_MARKER) {
      if (!existingKeys.has(key)) {
        errors.push({ line, message: `${key}: ${KEEP_MARKER} only works for variables that are already saved` })
        return
      }
      entries.push({ key, value: null, line })
      return
    }
    entries.push({ key, value: parsed.value, line })
  })
  return { entries, errors }
}

/** Formats one value so that parseDotenv reads it back unchanged. */
export function quoteValue(v: string): string {
  if (v === '') return ''
  if (BARE_RE.test(v) && v !== KEEP_MARKER) return v
  if (!v.includes("'")) return `'${v}'`
  return `"${v.replace(/[\\"$]/g, (c) => `\\${c}`)}"`
}

/** Prints entries as `.env` text (value null → KEEP_MARKER). */
export function printDotenv(entries: { key: string; value: string | null }[]): string {
  return entries.map((e) => `${e.key}=${e.value === null ? KEEP_MARKER : quoteValue(e.value)}`).join('\n')
}
