import { ChevronLeftIcon, ChevronRightIcon, ChevronsLeftIcon, ChevronsRightIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { formatNumber } from '@/lib/format'

export const PAGE_SIZES = ['10', '20', '50', '100']

type Props = { page: number; pageSize: number; pages: number; total: number; onPage: (p: number) => void; onPageSize: (n: number) => void }

/** Page controls under a table. "Showing 21 to 40 of 135" says where the reader is in the whole. */
export function Pagination({ page, pageSize, pages, total, onPage, onPageSize }: Props) {
  const from = total === 0 ? 0 : (page - 1) * pageSize + 1
  const to = Math.min(page * pageSize, total)
  const last = Math.max(pages, 1)
  return (
    <div className="flex flex-wrap items-center justify-between gap-3 border-t px-4 py-3">
      <p className="text-muted-foreground text-sm" aria-live="polite">
        {total === 0 ? 'No rows' : `Showing ${formatNumber(from)} to ${formatNumber(to)} of ${formatNumber(total)}`}
      </p>
      <div className="flex flex-wrap items-center gap-x-6 gap-y-2">
        <div className="flex items-center gap-2">
          <span className="text-muted-foreground text-sm" id="rows-per-page">
            Rows per page
          </span>
          <Select value={String(pageSize)} onValueChange={(v) => v && onPageSize(Number(v))}>
            <SelectTrigger size="sm" className="w-[4.5rem]" aria-labelledby="rows-per-page">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {PAGE_SIZES.map((s) => (
                <SelectItem key={s} value={s}>
                  {s}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <nav className="flex items-center gap-1" aria-label="Pages">
          <span className="text-muted-foreground mr-2 text-sm tabular-nums">
            Page {formatNumber(Math.min(page, last))} of {formatNumber(last)}
          </span>
          <Button variant="outline" size="icon-sm" onClick={() => onPage(1)} disabled={page <= 1} aria-label="First page">
            <ChevronsLeftIcon />
          </Button>
          <Button variant="outline" size="icon-sm" onClick={() => onPage(page - 1)} disabled={page <= 1} aria-label="Previous page">
            <ChevronLeftIcon />
          </Button>
          <Button variant="outline" size="icon-sm" onClick={() => onPage(page + 1)} disabled={page >= pages} aria-label="Next page">
            <ChevronRightIcon />
          </Button>
          <Button variant="outline" size="icon-sm" onClick={() => onPage(pages)} disabled={page >= pages} aria-label="Last page">
            <ChevronsRightIcon />
          </Button>
        </nav>
      </div>
    </div>
  )
}
