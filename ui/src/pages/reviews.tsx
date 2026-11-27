import { useLocation } from 'react-router'
import { reviewColumns } from '@/components/columns'
import { DataTable } from '@/components/data-table/data-table'
import { Pagination } from '@/components/data-table/pagination'
import { ClearFilters, FilterSelect, SearchInput } from '@/components/data-table/toolbar'
import { PageHeader } from '@/components/page-header'
import { EmptyState } from '@/components/state'
import { useApi } from '@/hooks/use-api'
import { useListState } from '@/hooks/use-list-state'
import type { Paged } from '@/lib/api'
import { carry } from '@/lib/scope'
import type { ReviewRow } from '@/lib/types'

/** The fields the server sorts this list by. */
const SORTS = ['at', 'repo', 'findings', 'errors', 'result']
const ASC = ['repo', 'result']

export default function Reviews() {
  const list = useListState('at', 'desc', ASC, SORTS)
  const { search } = useLocation()
  const complete = list.get('complete')
  const { data, error, loading, reload } = useApi<Paged<ReviewRow>>('/v1/reviews', { ...list.api, complete })
  const filtered = Boolean(list.q || complete)

  return (
    <>
      <PageHeader title="Reviews" description="Each run of nitpick that reported to this server, and whether it finished." />
      <DataTable
        columns={reviewColumns(carry(search))}
        rows={data?.items ?? []}
        rowKey={(r) => r.id}
        loading={loading}
        error={error}
        onRetry={reload}
        sort={list.sort}
        order={list.order}
        onSort={list.onSort}
        caption="Reviews"
        toolbar={
          <>
            <SearchInput value={list.q} onChange={list.onSearch} placeholder="Search reviews" label="Search reviews" />
            <FilterSelect
              label="Outcome"
              any="Any outcome"
              value={complete}
              onChange={(v) => list.set({ complete: v })}
              options={[
                { value: 'true', label: 'Finished' },
                { value: 'false', label: 'Did not finish' },
              ]}
              className="w-44"
            />
            <ClearFilters show={filtered} onClear={() => list.set({ q: '', complete: '' })} />
          </>
        }
        empty={<EmptyState title={filtered ? 'No review matches these filters' : 'No reviews in this period'}>{filtered ? 'Widen the period or reset the filters.' : 'A review appears once nitpick posts its report here.'}</EmptyState>}
        footer={data && <Pagination page={data.page} pageSize={data.page_size} pages={data.pages} total={data.total} onPage={list.onPage} onPageSize={list.onPageSize} />}
      />
    </>
  )
}
