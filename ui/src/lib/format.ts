const dateTime = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })
const day = new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric' })
const number = new Intl.NumberFormat()

export const formatDateTime = (iso: string) => dateTime.format(new Date(iso))
export const formatDay = (iso: string) => day.format(new Date(iso))
export const formatNumber = (n: number) => number.format(n)

/** "3 hours ago" style, in the reader's language. Falls back to the date past a month. */
export function formatRelative(iso: string, now = Date.now()): string {
  const seconds = Math.round((new Date(iso).getTime() - now) / 1000)
  const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' })
  const steps: [number, Intl.RelativeTimeFormatUnit][] = [[60, 'second'], [3600, 'minute'], [86400, 'hour'], [2592000, 'day']]
  let div = 1
  for (const [limit, unit] of steps) {
    if (Math.abs(seconds) < limit) return rtf.format(Math.round(seconds / div), unit)
    div = limit
  }
  return formatDateTime(iso)
}

export const shortCommit = (c?: string) => (c ? c.slice(0, 8) : '')
