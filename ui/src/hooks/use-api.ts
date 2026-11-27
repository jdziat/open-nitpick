import { useEffect, useRef, useState } from 'react'
import { getJSON, type Params } from '@/lib/api'

type State<T> = { data?: T; error?: unknown; loading: boolean }

/**
 * Fetches JSON for a path and parameters, and again when they change. The request
 * for parameters that are no longer current is cancelled, and the last answer stays
 * on screen while the next one loads so a table does not flash empty between pages.
 */
export function useApi<T>(path: string | null, params: Params = {}): State<T> & { reload: () => void } {
  const [state, setState] = useState<State<T>>({ loading: path !== null })
  const [tick, setTick] = useState(0)
  const key = JSON.stringify([path, params, tick])
  const latest = useRef(key)

  useEffect(() => {
    if (path === null) return
    latest.current = key
    const ctl = new AbortController()
    // Starting a request is the external synchronization this effect exists for.
    // oxlint-disable-next-line react/set-state-in-effect
    setState((s) => ({ ...s, loading: true, error: undefined }))
    getJSON<T>(path, params, ctl.signal)
      .then((data) => latest.current === key && setState({ data, loading: false }))
      .catch((error) => {
        if (ctl.signal.aborted) return
        setState((s) => ({ data: s.data, error, loading: false }))
      })
    return () => ctl.abort()
    // The key holds everything the request depends on.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])

  return { ...state, reload: () => setTick((t) => t + 1) }
}
