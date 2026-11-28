import { useCallback } from 'react'
import { useSearchParams } from 'react-router'

/**
 * The page's state lives in the address: filters, search, sort and page survive a
 * reload and can be sent to a colleague. A change to anything but the page resets the
 * page to the first, since the page the reader was on may not exist any more.
 */
export function useSearchState(defaults: Record<string, string> = {}) {
  const [params, setParams] = useSearchParams()

  const get = useCallback((key: string) => params.get(key) ?? defaults[key] ?? '', [params, defaults])

  const set = useCallback(
    (changes: Record<string, string | undefined>, opts: { keepPage?: boolean } = {}) => {
      setParams(
        (prev) => {
          const next = new URLSearchParams(prev)
          for (const [k, v] of Object.entries(changes)) {
            if (v === undefined || v === '' || v === defaults[k]) next.delete(k)
            else next.set(k, v)
          }
          if (!opts.keepPage && !('page' in changes)) next.delete('page')
          return next
        },
        { replace: false },
      )
    },
    [setParams, defaults],
  )

  return { params, get, set }
}
