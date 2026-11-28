import { session } from './session'

/** An answer that was not a success, with what the server said. */
export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

export type Paged<T> = { items: T[]; total: number; page: number; page_size: number; pages: number }

export type Params = Record<string, string | number | undefined | null>

/** Builds a query string from the parameters that are set. */
export function query(params: Params): string {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== '') q.set(k, String(v))
  }
  const s = q.toString()
  return s ? `?${s}` : ''
}

/** GETs JSON with the session cookie. A 401 means the session ended: go and sign in again. */
export async function getJSON<T>(path: string, params: Params = {}, signal?: AbortSignal): Promise<T> {
  const res = await fetch(path + query(params), { credentials: 'same-origin', headers: { Accept: 'application/json' }, signal })
  if (res.status === 401) {
    window.location.assign(`/auth/login?next=${encodeURIComponent(window.location.pathname + window.location.search)}`)
    throw new ApiError(401, 'Your session ended.')
  }
  if (!res.ok) {
    const text = (await res.text()).trim()
    throw new ApiError(res.status, text || res.statusText)
  }
  return (await res.json()) as T
}

// What the app shows for an error. The server's text is for the person only when it
// is about what they asked for; a store fault is a sentence that says nothing.
export function describe(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.status === 403) return 'Your account does not hold the role this page needs.'
    if (err.status >= 500) return 'The server could not answer. Try again in a moment.'
    return err.message
  }
  return 'The page could not be loaded.'
}

export const csrf = () => session.csrf
