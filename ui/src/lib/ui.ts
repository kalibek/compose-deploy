// Shared Tailwind class strings to keep the look consistent.
const btnBase =
  'inline-flex items-center justify-center gap-1.5 rounded-md font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-zinc-400 disabled:cursor-not-allowed disabled:opacity-50'
const md = 'px-3 py-1.5 text-sm'
const sm = 'px-2 py-1 text-xs'
const primary =
  'bg-zinc-900 text-white hover:bg-zinc-700 dark:bg-zinc-100 dark:text-zinc-900 dark:hover:bg-zinc-300'
const secondary =
  'border border-zinc-300 bg-white text-zinc-800 hover:bg-zinc-100 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-200 dark:hover:bg-zinc-800'
const danger =
  'border border-red-300 bg-white text-red-700 hover:bg-red-50 dark:border-red-900 dark:bg-zinc-900 dark:text-red-400 dark:hover:bg-red-950'
const dangerSolid = 'border border-red-600 bg-red-600 text-white hover:bg-red-700'

export const btnPrimary = `${btnBase} ${md} ${primary}`
export const btnSecondary = `${btnBase} ${md} ${secondary}`
export const btnDanger = `${btnBase} ${md} ${danger}`
export const btnDangerSolid = `${btnBase} ${md} ${dangerSolid}`
export const btnSecondarySm = `${btnBase} ${sm} ${secondary}`
export const btnDangerSm = `${btnBase} ${sm} ${danger}`
export const btnDangerSolidSm = `${btnBase} ${sm} ${dangerSolid}`
export const btnIcon = `${btnBase} h-8 w-8 shrink-0 ${secondary}`

export const input =
  'block w-full rounded-md border border-zinc-300 bg-white px-2.5 py-1.5 text-sm text-zinc-900 placeholder:text-zinc-400 focus:border-zinc-500 focus:outline-none focus:ring-1 focus:ring-zinc-500 disabled:opacity-60 read-only:bg-zinc-50 read-only:text-zinc-600 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-100 dark:placeholder:text-zinc-500 dark:read-only:bg-zinc-800/60 dark:read-only:text-zinc-300'
export const label = 'mb-1 block text-sm font-medium text-zinc-700 dark:text-zinc-300'
export const hint = 'mt-1 text-xs text-zinc-500 dark:text-zinc-400'
export const card =
  'rounded-lg border border-zinc-200 bg-white p-4 shadow-sm dark:border-zinc-800 dark:bg-zinc-900'
export const errorBox =
  'rounded-md border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-800 dark:border-red-900 dark:bg-red-950/50 dark:text-red-300'
export const link = 'text-sky-700 underline-offset-2 hover:underline dark:text-sky-400'
export const code = 'rounded bg-zinc-100 px-1 py-0.5 font-mono text-xs dark:bg-zinc-800'
