import { act, renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import { MemoryRouter, useLocation } from 'react-router'
import { describe, expect, it } from 'vitest'
import { useListState } from './use-list-state'

function setup(url = '/insights/findings') {
  const wrapper = ({ children }: { children: ReactNode }) => <MemoryRouter initialEntries={[url]}>{children}</MemoryRouter>
  return renderHook(
    () => {
      const list = useListState('at', 'desc', ['path'], ['at', 'path', 'repo'])
      return { list, search: useLocation().search }
    },
    { wrapper },
  )
}

describe('useListState', () => {
  it('starts from the defaults and puts none of them in the address', () => {
    const { result } = setup()
    expect(result.current.list).toMatchObject({ sort: 'at', order: 'desc', page: 1, pageSize: 20 })
    expect(result.current.list.api).toMatchObject({ days: '30', page: 1, page_size: 20, sort: 'at', order: 'desc' })
    expect(result.current.search).toBe('')
  })

  it('reads the state from the address, so a reload and a shared link show the same page', () => {
    const { result } = setup('/x?days=90&repo=acme%2Fapi&page=3&page_size=50&sort=repo&order=asc&q=auth')
    expect(result.current.list.api).toEqual({ days: '90', repo: 'acme/api', page: 3, page_size: 50, sort: 'repo', order: 'asc', q: 'auth' })
  })

  it('goes back to the first page when anything but the page changes', () => {
    const { result } = setup('/x?page=4')
    act(() => result.current.list.onSearch('auth'))
    expect(new URLSearchParams(result.current.search).get('page')).toBeNull()
    expect(new URLSearchParams(result.current.search).get('q')).toBe('auth')
  })

  it('reads a page that is not a positive number as the first page', () => {
    for (const bad of ['0', '-3', 'abc', '']) {
      const { result } = setup(`/x?page=${bad}`)
      expect(result.current.list.page, `page=${bad}`).toBe(1)
      expect(result.current.list.api.page, `page=${bad}`).toBe(1)
    }
  })

  it('falls back to the defaults for a sort, an order or a page size the server would refuse', () => {
    const { result } = setup('/x?sort=files&order=sideways&page_size=500')
    expect(result.current.list).toMatchObject({ sort: 'at', order: 'desc', pageSize: 20 })
    expect(result.current.list.api).toMatchObject({ sort: 'at', order: 'desc', page_size: 20 })
    expect(setup('/x?page_size=0').result.current.list.pageSize).toBe(20)
    expect(setup('/x?page_size=100&sort=repo&order=asc').result.current.list).toMatchObject({ pageSize: 100, sort: 'repo', order: 'asc' })
  })

  it('keeps the page it was told to go to', () => {
    const { result } = setup()
    act(() => result.current.list.onPage(3))
    expect(new URLSearchParams(result.current.search).get('page')).toBe('3')
  })

  it('reverses the sorted column, and starts a new one the way a reader expects', () => {
    const { result } = setup()
    act(() => result.current.list.onSort('at'))
    expect(new URLSearchParams(result.current.search).get('order')).toBe('asc')
    act(() => result.current.list.onSort('path'))
    // path is text, so it starts from A; the default order is not written down.
    const p = new URLSearchParams(result.current.search)
    expect(p.get('sort')).toBe('path')
    expect(p.get('order')).toBe('asc')
    act(() => result.current.list.onSort('findings'))
    expect(new URLSearchParams(result.current.search).get('order')).toBeNull() // desc is the default
  })

  it('drops a filter that is cleared from the address', () => {
    const { result } = setup('/x?class=bug&days=7')
    act(() => result.current.list.set({ class: '' }))
    expect(result.current.search).toBe('?days=7')
  })
})
