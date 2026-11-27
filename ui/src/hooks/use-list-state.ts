import { useCallback, useMemo } from 'react'
import { useSearchState } from './use-search-state'
import { DEFAULT_DAYS } from '@/components/layout/insights-controls'

type Order = 'asc' | 'desc'

/**
 * The state of a table of the insights data, kept in the address: the period and
 * repository every page shares, and this table's page, size, sort and search. Anything
 * else a page filters by goes through get and set.
 */
export function useListState(defaultSort: string, defaultOrder: Order, ascFirst: string[] = []) {
  const defaults = useMemo(() => ({ days: DEFAULT_DAYS, sort: defaultSort, order: defaultOrder, page_size: '20' }), [defaultSort, defaultOrder])
  const { get, set, params } = useSearchState(defaults)

  const sort = get('sort')
  const order = get('order') as Order
  const page = Math.max(1, Number(get('page')) || 1)
  const pageSize = Number(get('page_size')) || 20

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
