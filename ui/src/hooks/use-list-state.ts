import { useCallback, useMemo } from 'react'
import { useSearchState } from './use-search-state'
import { DEFAULT_DAYS } from '@/components/layout/insights-controls'

type Order = 'asc' | 'desc'

/** The most rows a request may ask for, which the server enforces. */
export const MAX_PAGE_SIZE = 100

/**
 * The state of a table of the insights data, kept in the address: the period and
 * repository every page shares, and this table's page, size, sort and search. Anything
 * else a page filters by goes through get and set.
 *
 * A link can be edited or can outlive a release, so a sort the table does not have,
 * an order that is neither direction and a page size outside what the server
 * accepts fall back to the defaults here, and the table shows rows and not the
 * server's complaint.
 */
export function useListState(defaultSort: string, defaultOrder: Order, ascFirst: string[] = [], allowedSorts?: string[]) {
  const defaults = useMemo(() => ({ days: DEFAULT_DAYS, sort: defaultSort, order: defaultOrder, page_size: '20' }), [defaultSort, defaultOrder])
  const { get, set, params } = useSearchState(defaults)

  const rawSort = get('sort')
  const sort = allowedSorts && !allowedSorts.includes(rawSort) ? defaultSort : rawSort
  const rawOrder = get('order')
  const order: Order = rawOrder === 'asc' || rawOrder === 'desc' ? rawOrder : defaultOrder
  const page = Math.max(1, Math.floor(Number(get('page'))) || 1)
  const size = Math.floor(Number(get('page_size')))
  const pageSize = size >= 1 && size <= MAX_PAGE_SIZE ? size : 20

  // Pressing the sorted column reverses it. Pressing another starts it the way a
  // reader expects: text from A, numbers and dates from the largest.
  const onSort = useCallback(
    (key: string) => {
      if (key === sort) set({ order: order === 'asc' ? 'desc' : 'asc' })
      else set({ sort: key, order: ascFirst.includes(key) ? 'asc' : 'desc' })
    },
    [sort, order, set, ascFirst],
  )

  return {
    get,
    set,
    params,
    page,
    pageSize,
    sort,
    order,
    q: get('q'),
    onSort,
    onPage: (p: number) => set({ page: String(p) }, { keepPage: true }),
    onPageSize: (n: number) => set({ page_size: String(n) }),
    onSearch: useCallback((q: string) => set({ q }), [set]),
    /** The parameters every list request carries. */
    api: {
      days: get('days'),
      repo: get('repo'),
      page,
      page_size: pageSize,
      sort,
      order,
      q: get('q'),
    },
  }
}
