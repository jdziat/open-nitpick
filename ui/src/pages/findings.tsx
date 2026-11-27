import { ExternalLinkIcon, XIcon } from 'lucide-react'
import { useState } from 'react'
import { Link, useLocation } from 'react-router'
import { RepoName, When } from '@/components/columns'
import { DataTable, type Column } from '@/components/data-table/data-table'
import { Pagination } from '@/components/data-table/pagination'
import { ClearFilters, FilterSelect, SearchInput } from '@/components/data-table/toolbar'
import { PageHeader } from '@/components/page-header'
import { SeverityBadge } from '@/components/severity-badge'
import { EmptyState } from '@/components/state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { useApi } from '@/hooks/use-api'
import { useListState } from '@/hooks/use-list-state'
import type { Paged } from '@/lib/api'
import { formatDateTime, shortCommit } from '@/lib/format'
import { carry } from '@/lib/scope'
import { SEVERITIES } from '@/lib/severity'
import type { Facets, FindingRow } from '@/lib/types'

const ASC = ['path', 'repo', 'class', 'title']

const where = (f: FindingRow) => `${f.path}${f.line ? `:${f.line}` : ''}`

export default function Findings() {
  const list = useListState('at', 'desc', ASC)
  const { search } = useLocation()
  const [open, setOpen] = useState<FindingRow | null>(null)
  const path = list.get('path')
  const cls = list.get('class')
  const floor = list.get('min_severity')
  const { data, error, loading, reload } = useApi<Paged<FindingRow>>('/v1/insights/findings', { ...list.api, path, class: cls, min_severity: floor })
  const facets = useApi<Facets>('/v1/insights/facets', { days: list.get('days') })
  const filtered = Boolean(list.q || path || cls || floor)

  const columns: Column<FindingRow>[] = [
    { id: 'severity', header: 'Severity', sort: 'severity', required: true, cell: (f) => <SeverityBadge severity={f.severity} /> },
    {
      id: 'finding',
      header: 'Finding',
      sort: 'title',
      required: true,
      className: 'min-w-64 max-w-xl whitespace-normal',
      cell: (f) => (
        <div className="min-w-0 space-y-0.5">
          <button type="button" onClick={() => setOpen(f)} className="focus-visible:ring-ring/50 rounded text-left font-medium underline-offset-4 outline-none hover:underline focus-visible:ring-3">
            {f.title}
          </button>
          <div className="text-muted-foreground flex flex-wrap items-center gap-x-2 text-xs">
            <code className="break-all">{where(f)}</code>
            {f.class && <Badge variant="outline" className="h-4 px-1.5 text-[0.7rem] font-normal">{f.class}</Badge>}
          </div>
        </div>
      ),
    },
    { id: 'repo', header: 'Repository', sort: 'repo', className: 'hidden md:table-cell', cell: (f) => <RepoName repo={f.repo} /> },
    {
      id: 'review',
      header: 'Review',
      className: 'hidden lg:table-cell',
      cell: (f) => (
        <Link to={{ pathname: `/insights/reviews/${f.review_id}`, search: carry(search) }} className="hover:underline">
          <code className="text-xs">{shortCommit(f.commit) || 'open'}</code>
        </Link>
      ),
    },
    { id: 'at', header: 'Reported', sort: 'at', cell: (f) => <When at={f.at} /> },
  ]

  return (
    <>
      <PageHeader title="Findings" description="Every finding reviews reported in the period. Select one to read why it was raised." />
      <DataTable
        columns={columns}
        rows={data?.items ?? []}
        rowKey={(f) => `${f.review_id}\u0000${f.path}\u0000${f.line}\u0000${f.title}`}
        loading={loading}
        error={error}
        onRetry={reload}
        sort={list.sort}
        order={list.order}
        onSort={list.onSort}
        caption="Findings"
        toolbar={
          <>
            <SearchInput value={list.q} onChange={list.onSearch} placeholder="Search title, file or reason" label="Search findings" />
            <FilterSelect label="Severity" any="Any severity" value={floor} onChange={(v) => list.set({ min_severity: v })} options={[...SEVERITIES].reverse().map((s) => ({ value: s, label: `${s[0].toUpperCase()}${s.slice(1)} or worse` }))} className="w-44" />
            <FilterSelect label="Class" any="Any class" value={cls} onChange={(v) => list.set({ class: v })} options={(facets.data?.classes ?? []).map((c) => ({ value: c, label: c }))} className="w-36" />
            {path && (
              <Button variant="secondary" size="sm" onClick={() => list.set({ path: '' })} aria-label={`Remove the file filter ${path}`}>
                <code className="max-w-48 truncate text-xs">{path}</code> <XIcon />
              </Button>
            )}
            <ClearFilters show={filtered} onClear={() => list.set({ q: '', path: '', class: '', min_severity: '' })} />
          </>
        }
        empty={
          <EmptyState title={filtered ? 'No finding matches these filters' : 'No findings in this period'}>
            {filtered ? 'The period had reviews, so this is a result and not a gap. Widen the period or reset the filters.' : 'Reviews ran and reported nothing, or none ran.'}
          </EmptyState>
        }
        footer={data && <Pagination page={data.page} pageSize={data.page_size} pages={data.pages} total={data.total} onPage={list.onPage} onPageSize={list.onPageSize} />}
      />

      <Sheet open={open !== null} onOpenChange={(o) => !o && setOpen(null)}>
        <SheetContent className="w-full overflow-y-auto sm:max-w-lg">
          {open && (
            <>
              <SheetHeader>
                <div>
                  <SeverityBadge severity={open.severity} />
                </div>
                <SheetTitle className="text-lg leading-snug">{open.title}</SheetTitle>
                <SheetDescription className="font-mono text-xs break-all">{where(open)}</SheetDescription>
              </SheetHeader>
              <div className="space-y-5 px-4 pb-6 text-sm">
                {open.rationale ? <p className="leading-relaxed whitespace-pre-wrap">{open.rationale}</p> : <p className="text-muted-foreground">The reviewer gave no reason.</p>}
                <dl className="grid grid-cols-[6rem_1fr] gap-x-4 gap-y-2">
                  <dt className="text-muted-foreground">Repository</dt>
                  <dd>
                    <RepoName repo={open.repo} />
                  </dd>
                  {open.class && (
                    <>
                      <dt className="text-muted-foreground">Class</dt>
                      <dd>{open.class}{open.category ? ` · ${open.category}` : ''}</dd>
                    </>
                  )}
                  {open.commit && (
                    <>
                      <dt className="text-muted-foreground">Commit</dt>
                      <dd>
                        <code className="text-xs">{open.commit}</code>
                      </dd>
                    </>
                  )}
                  <dt className="text-muted-foreground">Reported</dt>
                  <dd>{formatDateTime(open.at)}</dd>
                </dl>
                <Button variant="outline" size="sm" render={<Link to={{ pathname: `/insights/reviews/${open.review_id}`, search: carry(search) }} />}>
                  Open the review <ExternalLinkIcon />
                </Button>
              </div>
            </>
          )}
        </SheetContent>
      </Sheet>
    </>
  )
}
