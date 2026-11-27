const KEEP = ['days', 'repo']

/** The part of a query string that follows the reader from page to page: the period and repository. */
export function carry(search: string): string {
  const from = new URLSearchParams(search)
  const out = new URLSearchParams()
  for (const k of KEEP) if (from.get(k)) out.set(k, from.get(k)!)
  const s = out.toString()
  return s ? `?${s}` : ''
}
