import { hotspotColumns, sparkScale } from '@/components/columns'
import { DataTable } from '@/components/data-table/data-table'
import { Pagination } from '@/components/data-table/pagination'
import { ClearFilters, FilterSelect, SearchInput } from '@/components/data-table/toolbar'
import { PageHeader } from '@/components/page-header'
import { EmptyState } from '@/components/state'
import { useApi } from '@/hooks/use-api'
import { useListState } from '@/hooks/use-list-state'
import type { Paged } from '@/lib/api'
import { SEVERITIES } from '@/lib/severity'
import type { Hotspot } from '@/lib/types'

/** The fields the server sorts this list by. */
const SORTS = ['score', 'findings', 'path', 'repo', 'delta', 'worst', 'last']
const ASC = ['path']
const trends = [
  { value: 'up', label: 'Getting worse' },
  { value: 'flat', label: 'Steady' },
  { value: 'down', label: 'Getting better' },
]

export default function Hotspots() {
  const list = useListState('score', 'desc', ASC, SORTS)
  const trend = list.get('trend')
  const floor = list.get('min_severity')
  const { data, error, loading, reload } = useApi<Paged<Hotspot>>('/v1/insights/hotspots', { ...list.api, trend, min_severity: floor })
  const rows = data?.items ?? []
  const filtered = Boolean(list.q || trend || floor)

  return (
    <>
      <PageHeader
        title="Hotspots"
        description="Files ranked by the findings reviews reported in the period. Severe and recent findings count most, and a finding left unfixed through several pushes counts each time."
      />
      <DataTable
        columns={hotspotColumns(sparkScale(rows), (h) => `?path=${encodeURIComponent(h.path)}&repo=${encodeURIComponent(h.repo)}&days=${list.get('days')}`)}
        rows={rows}
        rowKey={(h) => `${h.repo}\u0000${h.path}`}
        loading={loading}
        error={error}
        onRetry={reload}
        sort={list.sort}
        order={list.order}
        onSort={list.onSort}
        caption="Files ranked by findings"
        toolbar={
          <>
            <SearchInput value={list.q} onChange={list.onSearch} placeholder="Search files" label="Search files and repositories" />
            <FilterSelect label="Worst severity" any="Any severity" value={floor} onChange={(v) => list.set({ min_severity: v })} options={[...SEVERITIES].reverse().map((s) => ({ value: s, label: `${s[0].toUpperCase()}${s.slice(1)} or worse` }))} className="w-44" />
            <FilterSelect label="Trend" any="Any trend" value={trend} onChange={(v) => list.set({ trend: v })} options={trends} className="w-40" />
            <ClearFilters show={filtered} onClear={() => list.set({ q: '', trend: '', min_severity: '' })} />
          </>
        }
        empty={<EmptyState title={filtered ? 'No file matches these filters' : 'No findings in this period'}>{filtered ? 'Widen the period or reset the filters.' : 'Reviews ran and reported nothing, or none ran.'}</EmptyState>}
        footer={data && <Pagination page={data.page} pageSize={data.page_size} pages={data.pages} total={data.total} onPage={list.onPage} onPageSize={list.onPageSize} />}
      />
    </>
  )
}
