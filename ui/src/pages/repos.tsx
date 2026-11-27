import { Link } from 'react-router'
import { RepoName, When } from '@/components/columns'
import { DataTable, type Column } from '@/components/data-table/data-table'
import { Pagination } from '@/components/data-table/pagination'
import { SearchInput } from '@/components/data-table/toolbar'
import { PageHeader } from '@/components/page-header'
import { EmptyState } from '@/components/state'
import { useApi } from '@/hooks/use-api'
import { useListState } from '@/hooks/use-list-state'
import type { Paged } from '@/lib/api'
import { formatNumber } from '@/lib/format'
import type { RepoRow } from '@/lib/types'

const ASC = ['repo']

export default function Repositories() {
  const list = useListState('findings', 'desc', ASC)
  // This page lists every repository, so a repository filter carried from another page is not sent.
  const api = { ...list.api, repo: undefined }
  const { data, error, loading, reload } = useApi<Paged<RepoRow>>('/v1/insights/repos', api)
  const days = list.get('days')

  const columns: Column<RepoRow>[] = [
    {
      id: 'repo',
      header: 'Repository',
      sort: 'repo',
      required: true,
      cell: (r) => (
        <Link to={{ pathname: '/insights', search: `?days=${days}&repo=${encodeURIComponent(r.repo)}` }} className="hover:underline">
          <RepoName repo={r.repo} />
        </Link>
      ),
    },
    { id: 'reviews', header: 'Reviews', sort: 'reviews', align: 'right', cell: (r) => formatNumber(r.reviews) },
    { id: 'findings', header: 'Findings', sort: 'findings', align: 'right', cell: (r) => formatNumber(r.findings) },
    { id: 'errors', header: 'Error or critical', sort: 'errors', align: 'right', cell: (r) => formatNumber(r.errors) },
    { id: 'last', header: 'Last review', sort: 'last', cell: (r) => <When at={r.last} /> },
  ]

  return (
    <>
      <PageHeader title="Repositories" description="Every repository that reported in the period, with what its reviews found." />
      <DataTable
        columns={columns}
        rows={data?.items ?? []}
        rowKey={(r) => r.repo}
        loading={loading}
        error={error}
        onRetry={reload}
        sort={list.sort}
        order={list.order}
        onSort={list.onSort}
        caption="Repositories"
        toolbar={<SearchInput value={list.q} onChange={list.onSearch} placeholder="Search repositories" label="Search repositories" />}
        empty={<EmptyState title={list.q ? 'No repository matches' : 'No reviews in this period'} />}
        footer={data && <Pagination page={data.page} pageSize={data.page_size} pages={data.pages} total={data.total} onPage={list.onPage} onPageSize={list.onPageSize} />}
      />
    </>
  )
}
