import { ChevronLeftIcon, ChevronRightIcon } from 'lucide-react'
import { useMemo, useState } from 'react'
import { Link } from 'react-router'
import { DataTable, type Column } from '@/components/data-table/data-table'
import { ClearFilters } from '@/components/data-table/toolbar'
import { When } from '@/components/columns'
import { PageHeader } from '@/components/page-header'
import { EmptyState } from '@/components/state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useApi } from '@/hooks/use-api'
import { useSearchState } from '@/hooks/use-search-state'
import { formatNumber } from '@/lib/format'
import type { RecordSummary } from '@/lib/types'

type Listing = { records: RecordSummary[]; next_cursor: string }
const PAGE = 25

const keys: Record<string, (r: RecordSummary) => string | number> = {
  created: (r) => r.created,
  model: (r) => r.model,
  tool: (r) => r.tool,
  status: (r) => Number(r.complete),
  answers: (r) => r.answers,
  source: (r) => r.source,
}

// The notary lists records newest first by cursor, so a page of them is all it can
// order. Pressing a header orders the page that is on screen, and says so.
export default function Records() {
  const { get, set } = useSearchState()
  const cursor = get('cursor')
  const model = get('model')
  const version = get('version')
  const [sort, setSort] = useState('created')
  const [order, setOrder] = useState<'asc' | 'desc'>('desc')
  const [trail, setTrail] = useState<string[]>([])
  const { data, error, loading, reload } = useApi<Listing>('/v1/records/view', { limit: PAGE, cursor, model, version })
  const filtered = Boolean(model || version)

  const rows = useMemo(() => {
    const key = keys[sort]
    const list = [...(data?.records ?? [])]
    if (key) list.sort((a, b) => (key(a) < key(b) ? -1 : key(a) > key(b) ? 1 : 0) * (order === 'asc' ? 1 : -1))
    return list
  }, [data, sort, order])

  const columns: Column<RecordSummary>[] = [
    {
      id: 'created',
      header: 'Created',
      sort: 'created',
      required: true,
      cell: (r) => (
        <Link to={`/notary/records/${r.hash}`} className="hover:underline">
          <When at={r.created} />
        </Link>
      ),
    },
    { id: 'model', header: 'Model', sort: 'model', cell: (r) => <span className="font-mono text-[0.8rem]">{r.model}</span> },
    { id: 'tool', header: 'Version', sort: 'tool', className: 'hidden md:table-cell', cell: (r) => <code className="text-xs">{r.tool}</code> },
    {
      id: 'status',
      header: 'Status',
      sort: 'status',
      cell: (r) => (r.complete ? <Badge variant="secondary" className="font-normal">Replays</Badge> : <Badge variant="outline" className="font-normal">Resumes</Badge>),
    },
    { id: 'answers', header: 'Answers', sort: 'answers', align: 'right', cell: (r) => `${formatNumber(r.answers)} of ${formatNumber(r.batches)}` },
    { id: 'source', header: 'Written by', sort: 'source', className: 'hidden lg:table-cell', cell: (r) => r.source },
    {
      id: 'record',
      header: 'Record',
      className: 'hidden sm:table-cell',
      cell: (r) => (
        <Link to={`/notary/records/${r.hash}`} className="hover:underline">
          <code className="text-xs">{r.hash.slice(0, 12)}</code>
        </Link>
      ),
    },
  ]

  const older = () => {
    if (!data?.next_cursor) return
    setTrail((t) => [...t, cursor])
    set({ cursor: data.next_cursor }, { keepPage: true })
  }
  const newer = () => {
    const back = trail[trail.length - 1] ?? ''
    setTrail((t) => t.slice(0, -1))
    set({ cursor: back }, { keepPage: true })
  }
  const reset = () => {
    setTrail([])
    set({ model: '', version: '', cursor: '' })
  }

  return (
    <>
      <PageHeader title="Records" description="What the notary holds: the model answers a review replays or resumes from. Records are listed newest first; a header orders the page on screen." />
      <DataTable
        columns={columns}
        rows={rows}
        rowKey={(r) => r.hash}
        loading={loading}
        error={error}
        onRetry={reload}
        sort={sort}
        order={order}
        onSort={(k) => (k === sort ? setOrder((o) => (o === 'asc' ? 'desc' : 'asc')) : (setSort(k), setOrder(k === 'model' || k === 'tool' || k === 'source' ? 'asc' : 'desc')))}
        caption="Notary records"
        toolbar={
          <>
            <Input aria-label="Model" placeholder="Model, exactly" defaultValue={model} onBlur={(e) => e.target.value !== model && (setTrail([]), set({ model: e.target.value.trim(), cursor: '' }))} onKeyDown={(e) => e.key === 'Enter' && e.currentTarget.blur()} className="h-8 w-48" />
            <Input aria-label="Version" placeholder="Version, exactly" defaultValue={version} onBlur={(e) => e.target.value !== version && (setTrail([]), set({ version: e.target.value.trim(), cursor: '' }))} onKeyDown={(e) => e.key === 'Enter' && e.currentTarget.blur()} className="h-8 w-40" />
            <ClearFilters show={filtered} onClear={reset} />
          </>
        }
        empty={<EmptyState title={filtered ? 'No record matches this filter' : 'The notary holds no records'}>{filtered ? 'Reset the filter to see every record.' : 'A review run with -notary leaves one.'}</EmptyState>}
        footer={
          <div className="flex items-center justify-between gap-3 border-t px-4 py-3">
            <p className="text-muted-foreground text-sm">{data ? `${rows.length} record${rows.length === 1 ? '' : 's'} on this page` : ''}</p>
            <div className="flex gap-1">
              <Button variant="outline" size="sm" onClick={newer} disabled={trail.length === 0 && !cursor}>
                <ChevronLeftIcon /> Newer
              </Button>
              <Button variant="outline" size="sm" onClick={older} disabled={!data?.next_cursor}>
                Older <ChevronRightIcon />
              </Button>
            </div>
          </div>
        }
      />
    </>
  )
}
