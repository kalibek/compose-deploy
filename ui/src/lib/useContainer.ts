import { useCallback } from 'react'
import { api } from '../api/client'
import type { Container } from '../api/types'
import { usePolling } from './usePolling'

export interface ContainerInfo {
  container: Container | undefined
  loaded: boolean
  error: string | null
}

/** Polls the stack's containers and returns the one with id `cid`. */
export function useContainer(stackId: string, cid: string): ContainerInfo {
  const list = useCallback(() => api.listContainers(stackId), [stackId])
  const q = usePolling(list, 5000, stackId)
  const container = q.data?.find((c) => c.id === cid)
  return { container, loaded: q.data !== undefined, error: q.error }
}
