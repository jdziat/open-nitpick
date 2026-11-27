import { ArrowDownIcon, ArrowUpDownIcon, ArrowUpIcon } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { ErrorState } from '@/components/state'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { cn } from '@/lib/utils'
import { ColumnsMenu } from './columns-menu'

export type Column<T> = {
  id: string
  header: string
  cell: (row: T) => ReactNode
  /** The server's sort key. A column with none cannot be sorted. */
  sort?: string
  align?: 'right'
  className?: string
  /** The column is left out until the reader turns it on. */
  hidden?: boolean
  /** The column cannot be turned off. */
  required?: boolean
}

type Props<T> = {
  columns: Column<T>[]
  rows: T[]
  rowKey: (row: T) => string
  loading: boolean
  error?: unknown
  onRetry?: () => void
  sort?: string
  order?: 'asc' | 'desc'
  onSort?: (key: string) => void
  /** What to show when there is nothing to list. */
  empty: ReactNode
  /** How many rows to draw as placeholders while the first answer is awaited. */
  placeholderRows?: number
  caption: string
  /** Toolbar content, left of the columns menu. */
  toolbar?: ReactNode
  /** Whether to offer the columns menu. A small panel on a dashboard does not. */
  columnsMenu?: boolean
  /** Draw no frame of its own, for a table that sits inside a card. */
  bare?: boolean
  footer?: ReactNode
}

/**
 * A table whose rows, order and page come from the server. It sorts nothing and
 * pages nothing itself: it shows what it is given, says when it is waiting, and asks
 * for a new order when a header is pressed.
 */
export function DataTable<T>({ columns, rows, rowKey, loading, error, onRetry, sort, order, onSort, empty, placeholderRows = 8, caption, toolbar, columnsMenu = true, bare = false, footer }: Props<T>) {
  const [shown, setShown] = useState<Record<string, boolean>>(() => Object.fromEntries(columns.map((c) => [c.id, !c.hidden])))
  const visible = columns.filter((c) => c.required || shown[c.id] !== false)
  const choices = columns.filter((c) => !c.required).map((c) => ({ id: c.id, header: c.header, visible: shown[c.id] !== false }))
  const first = loading && rows.length === 0

  return (
    <div className="space-y-3">
      {(toolbar || columnsMenu) && (
        <div className="flex flex-wrap items-center gap-2">
          <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2">{toolbar}</div>
          {columnsMenu && <ColumnsMenu columns={choices} onToggle={(id, v) => setShown((s) => ({ ...s, [id]: v }))} />}
        </div>
      )}
      <div className={cn(!bare && "bg-card overflow-hidden rounded-xl border")}>
        <div className={cn('overflow-x-auto transition-opacity', loading && rows.length > 0 && 'opacity-60')} aria-busy={loading}>
          <Table className="[&_td:first-child]:pl-4 [&_td:last-child]:pr-4 [&_th:first-child]:pl-4 [&_th:last-child]:pr-4">
            <caption className="sr-only">{caption}</caption>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                {visible.map((c) => {
                  const sorted = sort !== undefined && c.sort === sort
                  return (
                    <TableHead key={c.id} className={cn('text-muted-foreground h-10 text-xs font-medium', c.align === 'right' && 'text-right', c.className)} aria-sort={sorted ? (order === 'asc' ? 'ascending' : 'descending') : c.sort ? 'none' : undefined}>
                      {c.sort && onSort ? (
                        <button
                          type="button"
                          onClick={() => onSort(c.sort!)}
                          className={cn('hover:text-foreground focus-visible:ring-ring/50 -mx-1.5 inline-flex items-center gap-1 rounded-md px-1.5 py-1 outline-none focus-visible:ring-3', c.align === 'right' && 'flex-row-reverse', sorted && 'text-foreground')}
                        >
                          {c.header}
                          {sorted ? order === 'asc' ? <ArrowUpIcon className="size-3.5" aria-hidden /> : <ArrowDownIcon className="size-3.5" aria-hidden /> : <ArrowUpDownIcon className="size-3.5 opacity-40" aria-hidden />}
                          <span className="sr-only">{sorted ? `, sorted ${order === 'asc' ? 'ascending' : 'descending'}. Activate to reverse.` : ', activate to sort'}</span>
                        </button>
                      ) : (
                        c.header
                      )}
                    </TableHead>
                  )
                })}
              </TableRow>
            </TableHeader>
            <TableBody>
              {first &&
                Array.from({ length: placeholderRows }, (_, i) => (
                  <TableRow key={i} className="hover:bg-transparent">
                    {visible.map((c) => (
                      <TableCell key={c.id}>
                        <Skeleton className="h-4 w-full max-w-40" />
                      </TableCell>
                    ))}
                  </TableRow>
                ))}
              {!first &&
                rows.map((row) => (
                  <TableRow key={rowKey(row)}>
                    {visible.map((c) => (
                      <TableCell key={c.id} className={cn(c.align === 'right' && 'text-right tabular-nums', c.className)}>
                        {c.cell(row)}
                      </TableCell>
                    ))}
                  </TableRow>
                ))}
            </TableBody>
          </Table>
        </div>
        {error !== undefined && rows.length === 0 && <ErrorState error={error} onRetry={onRetry} />}
        {error === undefined && !loading && rows.length === 0 && empty}
        {error !== undefined && rows.length > 0 && (
          <p role="alert" className="text-sev-critical border-t px-4 py-2 text-sm">
            The newest answer could not be loaded, so this is the last one that was.{' '}
            <button className="underline" onClick={onRetry}>
              Try again
            </button>
          </p>
        )}
        {footer}
      </div>
    </div>
  )
}
