import { useCallback, useEffect, useRef, useState } from 'react'
import { errorMessage } from '../api/client'

export interface PollingState<T> {
  data: T | undefined
  error: string | null
  loading: boolean
  reload: () => Promise<void>
}

interface Snapshot<T> {
  key: string
  data: T | undefined
  error: string | null
  done: boolean
}

/**
 * Fetches `fn` immediately and then every `intervalMs` (skipping ticks while a
 * request for the same key is in flight). `intervalMs` may change between
 * renders. Changing `key` (e.g. a route param) discards previous data and
 * reloads; responses for a stale key are ignored.
 */
export function usePolling<T>(fn: () => Promise<T>, intervalMs: number, key: string = ''): PollingState<T> {
  const [snap, setSnap] = useState<Snapshot<T>>({ key, data: undefined, error: null, done: false })
  const fnRef = useRef(fn)
  const keyRef = useRef(key)
  const inFlightKey = useRef<string | null>(null)

  useEffect(() => {
    fnRef.current = fn
    keyRef.current = key
  })

  const reload = useCallback(async () => {
    const k = keyRef.current
    if (inFlightKey.current === k) return
    inFlightKey.current = k
    try {
      const data = await fnRef.current()
      if (keyRef.current === k) setSnap({ key: k, data, error: null, done: true })
    } catch (err) {
      if (keyRef.current === k) {
        setSnap((prev) => ({
          key: k,
          data: prev.key === k ? prev.data : undefined,
          error: errorMessage(err),
          done: true,
        }))
      }
    } finally {
      if (inFlightKey.current === k) inFlightKey.current = null
    }
  }, [])

  useEffect(() => {
    void reload()
    const t = window.setInterval(() => void reload(), intervalMs)
    return () => window.clearInterval(t)
  }, [key, intervalMs, reload])

  const current = snap.key === key
  return {
    data: current ? snap.data : undefined,
    error: current ? snap.error : null,
    loading: !current || !snap.done,
    reload,
  }
}
