import { useMemo, useState } from 'react'
import { KEEP_MARKER } from '../lib/dotenv'
import { rememberEnvMode, rowsToText, textToRows, type EnvDraft, type EnvMode } from '../lib/env'
import { code, errorBox } from '../lib/ui'
import KeyValueEditor from './KeyValueEditor'

interface Props {
  draft: EnvDraft
  onChange: (draft: EnvDraft) => void
  /** Variables already stored on the server (values unknown to the UI). */
  existingKeys: string[]
  disabled?: boolean
}

function ModeButton({ active, onClick, children }: { active: boolean; onClick: () => void; children: string }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={`rounded px-2 py-0.5 text-xs font-medium ${
        active
          ? 'bg-white text-zinc-900 shadow-sm dark:bg-zinc-700 dark:text-zinc-100'
          : 'text-zinc-600 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100'
      }`}
    >
      {children}
    </button>
  )
}

/** Environment variables as one-by-one rows or as `.env` text, with validation. */
export default function EnvEditor({ draft, onChange, existingKeys, disabled }: Props) {
  const [switchError, setSwitchError] = useState<string | null>(null)
  const errors = useMemo(
    () => (draft.mode === 'text' ? textToRows(draft.text, existingKeys).errors : []),
    [draft.mode, draft.text, existingKeys],
  )

  function setMode(mode: EnvMode) {
    if (mode === draft.mode) return
    if (mode === 'text') {
      onChange({ ...draft, mode, text: rowsToText(draft.rows) })
    } else {
      const parsed = textToRows(draft.text, existingKeys)
      if (parsed.errors.length > 0) {
        setSwitchError('Fix the errors below before switching to the list view.')
        return
      }
      onChange({ ...draft, mode, rows: parsed.rows })
    }
    setSwitchError(null)
    rememberEnvMode(mode)
  }

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between gap-2">
        <div className="inline-flex rounded-md bg-zinc-100 p-0.5 dark:bg-zinc-800" role="group" aria-label="Editor mode">
          <ModeButton active={draft.mode === 'rows'} onClick={() => setMode('rows')}>
            One by one
          </ModeButton>
          <ModeButton active={draft.mode === 'text'} onClick={() => setMode('text')}>
            .env text
          </ModeButton>
        </div>
        {draft.mode === 'text' && (
          <span className={`text-xs ${errors.length ? 'text-red-600 dark:text-red-400' : 'text-zinc-500'}`}>
            {errors.length ? `${errors.length} error${errors.length > 1 ? 's' : ''}` : 'valid'}
          </span>
        )}
      </div>

      {switchError && <div className={errorBox}>{switchError}</div>}

      {draft.mode === 'rows' ? (
        <KeyValueEditor rows={draft.rows} onChange={(rows) => onChange({ ...draft, rows })} disabled={disabled} />
      ) : (
        <>
          <textarea
            className={`block min-h-40 w-full resize-y rounded-md border bg-white px-2.5 py-2 font-mono text-xs leading-relaxed text-zinc-900 focus:outline-none focus:ring-1 dark:bg-zinc-900 dark:text-zinc-100 ${
              errors.length
                ? 'border-red-400 focus:border-red-500 focus:ring-red-500 dark:border-red-700'
                : 'border-zinc-300 focus:border-zinc-500 focus:ring-zinc-500 dark:border-zinc-700'
            }`}
            rows={Math.min(20, Math.max(6, draft.text.split('\n').length + 1))}
            value={draft.text}
            onChange={(e) => {
              setSwitchError(null)
              onChange({ ...draft, text: e.target.value })
            }}
            placeholder={'# one variable per line\nDB_HOST=db\nDB_PASSWORD=\'s3cr3t with spaces\''}
            disabled={disabled}
            spellCheck={false}
            autoComplete="off"
            aria-label="Environment variables in .env format"
            aria-invalid={errors.length > 0}
          />
          {errors.length > 0 && (
            <ul className="space-y-0.5 text-xs text-red-700 dark:text-red-400">
              {errors.slice(0, 10).map((e) => (
                <li key={`${e.line}:${e.message}`}>
                  Line {e.line}: {e.message}
                </li>
              ))}
              {errors.length > 10 && <li>…and {errors.length - 10} more</li>}
            </ul>
          )}
          <p className="text-xs text-zinc-500 dark:text-zinc-400">
            <code className={code}>KEY=value</code> per line; <code className={code}>#</code> comments,{' '}
            <code className={code}>'single'</code> (literal) and <code className={code}>"double"</code> quotes (
            <code className={code}>\" \\ \$</code> escapes) are supported, multi-line values are not.
            {existingKeys.length > 0 && (
              <>
                {' '}
                <code className={code}>{KEEP_MARKER}</code> keeps a saved variable’s current value — replace it to
                change the value, delete the line to remove the variable. Values you type here are shown in plain
                text.
              </>
            )}
          </p>
        </>
      )}
    </div>
  )
}
