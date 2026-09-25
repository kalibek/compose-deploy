import { useState } from 'react'
import { newRow, type EnvRow } from '../lib/env'
import { btnIcon, btnSecondarySm, input } from '../lib/ui'

interface Props {
  rows: EnvRow[]
  onChange: (rows: EnvRow[]) => void
  disabled?: boolean
}

function EyeIcon({ open }: { open: boolean }) {
  return (
    <svg viewBox="0 0 16 16" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden>
      <path d="M1.5 8S4 3.5 8 3.5 14.5 8 14.5 8 12 12.5 8 12.5 1.5 8 1.5 8Z" />
      <circle cx="8" cy="8" r="2" />
      {!open && <path d="M2.5 2.5l11 11" />}
    </svg>
  )
}

export default function KeyValueEditor({ rows, onChange, disabled }: Props) {
  const [shown, setShown] = useState<Set<number>>(() => new Set())

  function update(id: number, patch: Partial<EnvRow>) {
    onChange(rows.map((r) => (r.id === id ? { ...r, ...patch } : r)))
  }

  function remove(id: number) {
    onChange(rows.filter((r) => r.id !== id))
  }

  function toggleShow(id: number) {
    setShown((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  return (
    <div className="space-y-2">
      {rows.length === 0 && (
        <p className="text-sm text-zinc-500 dark:text-zinc-400">No environment variables.</p>
      )}
      {rows.map((r) => {
        const keep = r.existing && !r.touched
        return (
          <div key={r.id} className="flex flex-col gap-1.5 sm:flex-row sm:items-center">
            <input
              className={`${input} font-mono sm:w-2/5`}
              placeholder="KEY"
              value={r.key}
              readOnly={r.existing}
              title={r.existing ? 'Existing key — remove it and add a new one to rename' : undefined}
              disabled={disabled}
              autoComplete="off"
              spellCheck={false}
              aria-label="Variable name"
              onChange={(e) => update(r.id, { key: e.target.value })}
            />
            <div className="flex flex-1 items-center gap-1.5">
              <div className="relative flex-1">
                <input
                  className={`${input} pr-9 font-mono`}
                  type={shown.has(r.id) ? 'text' : 'password'}
                  placeholder={keep ? '•••• unchanged' : 'value'}
                  value={r.value}
                  disabled={disabled}
                  autoComplete="new-password"
                  spellCheck={false}
                  aria-label={`Value for ${r.key || 'variable'}`}
                  onChange={(e) => update(r.id, { value: e.target.value, touched: true })}
                />
                <button
                  type="button"
                  className="absolute inset-y-0 right-0 flex w-8 items-center justify-center text-zinc-500 hover:text-zinc-800 dark:hover:text-zinc-200"
                  onClick={() => toggleShow(r.id)}
                  aria-label={shown.has(r.id) ? 'Hide value' : 'Show value'}
                  title={shown.has(r.id) ? 'Hide value' : 'Show value'}
                >
                  <EyeIcon open={shown.has(r.id)} />
                </button>
              </div>
              {r.existing && r.touched && (
                <button
                  type="button"
                  className={btnSecondarySm}
                  disabled={disabled}
                  onClick={() => update(r.id, { value: '', touched: false })}
                  title="Keep the stored value"
                >
                  Undo
                </button>
              )}
              <button
                type="button"
                className={btnIcon}
                disabled={disabled}
                onClick={() => remove(r.id)}
                aria-label={`Remove ${r.key || 'variable'}`}
                title="Remove"
              >
                <svg viewBox="0 0 16 16" className="h-3.5 w-3.5" fill="none" stroke="currentColor" strokeWidth="1.75" aria-hidden>
                  <path d="M4 4l8 8M12 4l-8 8" />
                </svg>
              </button>
            </div>
          </div>
        )
      })}
      <button
        type="button"
        className={btnSecondarySm}
        disabled={disabled}
        onClick={() => onChange([...rows, newRow()])}
      >
        + Add variable
      </button>
    </div>
  )
}
