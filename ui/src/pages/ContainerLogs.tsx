import { useEffect, useMemo, useState } from 'react'
import { useParams } from 'react-router'
import { containerLogsUrl } from '../api/client'
import ContainerHeader from '../components/ContainerHeader'
import LogViewer from '../components/LogViewer'
import { useContainer } from '../lib/useContainer'
import { btnSecondary, card, input } from '../lib/ui'

const MAX_LINES = 5000
const TAIL_OPTIONS = [100, 500, 2000, 10000]

type StreamState = 'connecting' | 'live' | 'ended' | 'error'

// `docker logs --timestamps` prefix, e.g. "2026-09-25T10:00:00.123456789Z ".
const TS_RE = /^\d{4}-\d{2}-\d{2}T\S+Z /

/** One log stream; remounted (via key) whenever its URL or the reconnect epoch changes. */
function LogStream({ url, follow, timestamps }: { url: string; follow: boolean; timestamps: boolean }) {
  const [lines, setLines] = useState<string[]>([])
  const [state, setState] = useState<StreamState>('connecting')
  const [endMsg, setEndMsg] = useState('')

  useEffect(() => {
    let pending: string[] = []
    let frame = 0
    // Batch incoming lines into one render per animation frame.
    const flush = () => {
      frame = 0
      const batch = pending
      pending = []
      setLines((prev) => {
        const next = prev.concat(batch)
        return next.length > MAX_LINES ? next.slice(next.length - MAX_LINES) : next
      })
    }
    const es = new EventSource(url)
    let ended = false
    es.onopen = () => setState('live')
    es.onmessage = (e: MessageEvent<string>) => {
      pending.push(e.data)
      if (!frame) frame = requestAnimationFrame(flush)
    }
    es.addEventListener('end', (e) => {
      ended = true
      es.close()
      setState('ended')
      setEndMsg((e as MessageEvent<string>).data)
    })
    es.onerror = () => {
      // EventSource would retry forever (re-sending the tail); stop and let the user reconnect.
      es.close()
      if (!ended) setState('error')
    }
    return () => {
      es.close()
      if (frame) cancelAnimationFrame(frame)
    }
  }, [url])

  const text = useMemo(
    () => (timestamps ? lines : lines.map((l) => l.replace(TS_RE, ''))).join('\n'),
    [lines, timestamps],
  )

  const label: Record<StreamState, string> = {
    connecting: 'connecting…',
    live: follow ? 'live' : 'loaded',
    ended: follow ? `stream ended${endMsg ? `: ${endMsg}` : ' (container stopped)'}` : endMsg || 'loaded',
    error: 'disconnected — use Reconnect',
  }
  const color =
    state === 'error'
      ? 'text-red-600 dark:text-red-400'
      : state === 'live'
        ? 'text-green-600 dark:text-green-400'
        : 'text-zinc-500'

  return (
    <>
      <div className={`mb-1 text-right text-xs ${color}`}>
        {label[state]}
        {lines.length >= MAX_LINES && ` · showing last ${MAX_LINES} lines`}
      </div>
      <LogViewer text={text} className="h-[70dvh]" empty={state === 'connecting' ? 'Loading…' : 'No log output.'} />
    </>
  )
}

export default function ContainerLogs() {
  const { id = '', cid = '' } = useParams()
  const info = useContainer(id, cid)
  const [tail, setTail] = useState(500)
  const [follow, setFollow] = useState(true)
  const [timestamps, setTimestamps] = useState(false)
  const [epoch, setEpoch] = useState(0)
  const url = containerLogsUrl(id, cid, tail, follow)

  return (
    <div className="space-y-4">
      <ContainerHeader
        stackId={id}
        cid={cid}
        title="Logs"
        info={info}
        actions={
          <button type="button" className={btnSecondary} onClick={() => setEpoch((n) => n + 1)}>
            Reconnect
          </button>
        }
      />

      <section className={card}>
        <div className="mb-2 flex flex-wrap items-center gap-x-4 gap-y-2 text-sm">
          <label className="flex items-center gap-2">
            Tail
            <select className={`${input} w-auto py-1`} value={tail} onChange={(e) => setTail(Number(e.target.value))}>
              {TAIL_OPTIONS.map((n) => (
                <option key={n} value={n}>
                  {n} lines
                </option>
              ))}
            </select>
          </label>
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} />
            Follow
          </label>
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={timestamps} onChange={(e) => setTimestamps(e.target.checked)} />
            Timestamps
          </label>
        </div>
        <LogStream key={`${url}#${epoch}`} url={url} follow={follow} timestamps={timestamps} />
      </section>
    </div>
  )
}
