import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { Pagination } from './pagination'

const props = { page: 2, pageSize: 20, pages: 7, total: 135, onPage: vi.fn(), onPageSize: vi.fn() }

describe('Pagination', () => {
  it('says where the reader is in the whole', () => {
    render(<Pagination {...props} />)
    expect(screen.getByText('Showing 21 to 40 of 135')).toBeInTheDocument()
    expect(screen.getByText('Page 2 of 7')).toBeInTheDocument()
  })

  it('says so for the short last page and for no rows', () => {
    const { rerender } = render(<Pagination {...props} page={7} />)
    expect(screen.getByText('Showing 121 to 135 of 135')).toBeInTheDocument()
    rerender(<Pagination {...props} page={1} pages={0} total={0} />)
    expect(screen.getByText('No rows')).toBeInTheDocument()
    expect(screen.getByText('Page 1 of 1')).toBeInTheDocument()
  })

  it('goes to the page each button names', async () => {
    const onPage = vi.fn()
    render(<Pagination {...props} onPage={onPage} />)
    await userEvent.click(screen.getByRole('button', { name: 'Next page' }))
    await userEvent.click(screen.getByRole('button', { name: 'Previous page' }))
    await userEvent.click(screen.getByRole('button', { name: 'First page' }))
    await userEvent.click(screen.getByRole('button', { name: 'Last page' }))
    expect(onPage.mock.calls.map((c) => c[0])).toEqual([3, 1, 1, 7])
  })

  it('cannot go before the first page or after the last', () => {
    const { rerender } = render(<Pagination {...props} page={1} />)
    expect(screen.getByRole('button', { name: 'First page' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Previous page' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Next page' })).toBeEnabled()
    rerender(<Pagination {...props} page={7} />)
    expect(screen.getByRole('button', { name: 'Next page' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Last page' })).toBeDisabled()
  })
})
