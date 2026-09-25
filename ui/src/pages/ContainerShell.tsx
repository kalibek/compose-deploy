import { FitAddon } from '@xterm/addon-fit'
import { Terminal } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'
import { useEffect, useRef, useState } from 'react'
import { useParams } from 'react-router'
import { containerExecUrl } from '../api/client'
import ContainerHeader from '../components/ContainerHeader'
import { useContainer } from '../lib/useContainer'
import { btnSecondary, card } from '../lib/ui'

type SessionState = { kind: 'connecting' } | { kind: 'open' } | { kind: 'closed'; reason: string }

/** One terminal + WebSocket session; remounted (via key) for a new session. */
function TerminalSession({
  stackId,
  cid,
  onState,
}: {
  stackId: string
  cid: string
  onState: (s: SessionState) => void
}) {
  const hostRef = useRef<HTMLDivElement>(null)
  const onStateRef = useRef(onState)
  useEffect(() => {
    onStateRef.current = onState
  })

  useEffect(() => {
    const host = hostRef.current
    if (!host) return
    const term = new Terminal({
      cursorBlink: true,
      fontSize: 13,
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
      scrollback: 5000,
      theme: { background: '#09090b' },
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(host)
    fit.fit()
    term.focus()

    const enc = new TextEncoder()
    const ws = new WebSocket(containerExecUrl(stackId, cid, term.cols, term.rows))
    ws.binaryType = 'arraybuffer'
    ws.onopen = () => onStateRef.current({ kind: 'open' })
    ws.onmessage = (e: MessageEvent<ArrayBuffer | string>) => {
      term.write(typeof e.data === 'string' ? e.data : new Uint8Array(e.data))
    }
    ws.onclose = (e) => {
      const reason = e.reason || (e.code === 1006 ? 'connection lost' : `code ${e.code}`)
      onStateRef.current({ kind: 'closed', reason })
      term.write(`\r\n\x1b[2m[session closed: ${reason}]\x1b[0m\r\n`)
    }
    // Terminal input goes as binary frames; text frames are control messages.
    const sendBytes = (bytes: Uint8Array) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(bytes.slice().buffer)
    }
    const onData = term.onData((d) => sendBytes(enc.encode(d)))
    const onBinary = term.onBinary((d) => sendBytes(Uint8Array.from(d, (ch) => ch.charCodeAt(0))))
    const onResize = term.onResize(({ cols, rows }) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: 'resize', cols, rows }))
    })

    const ro = new ResizeObserver(() => {
      try {
        fit.fit()
      } catch {
        // host detached
      }
    })
    ro.observe(host)

    return () => {
      ro.disconnect()
      onData.dispose()
      onBinary.dispose()
      onResize.dispose()
      ws.onclose = null
      ws.close()
      term.dispose()
    }
  }, [stackId, cid])

  return <div ref={hostRef} className="h-full w-full" />
}

export default function ContainerShell() {
  const { id = '', cid = '' } = useParams()
  const info = useContainer(id, cid)
  const running = info.container?.state === 'running'
  const [epoch, setEpoch] = useState(0)
  const [session, setSession] = useState<SessionState>({ kind: 'connecting' })

  function newSession() {
    setSession({ kind: 'connecting' })
    setEpoch((n) => n + 1)
  }

  const status = !info.container
    ? ''
    : !running
      ? 'container is not running'
      : session.kind === 'open'
        ? 'connected'
        : session.kind === 'connecting'
          ? 'connecting…'
          : `closed: ${session.reason}`

  return (
    <div className="space-y-4">
      <ContainerHeader
        stackId={id}
        cid={cid}
        title="Shell"
        info={info}
        actions={
          <button type="button" className={btnSecondary} disabled={!running} onClick={newSession}>
            {session.kind === 'closed' ? 'Reconnect' : 'New session'}
          </button>
        }
      />
      <section className={card}>
        <div className="mb-2 flex items-center justify-between gap-2 text-xs">
          <span className="text-zinc-500">
            Runs <code>bash</code> if available, otherwise <code>sh</code>. Leaving this page ends the session.
          </span>
          <span className={session.kind === 'open' && running ? 'text-green-600 dark:text-green-400' : 'text-zinc-500'}>
            {status}
          </span>
        </div>
        <div className="h-[70dvh] overflow-hidden rounded-md bg-zinc-950 p-2 ring-1 ring-zinc-800">
          {running && <TerminalSession key={epoch} stackId={id} cid={cid} onState={setSession} />}
        </div>
      </section>
    </div>
  )
}
