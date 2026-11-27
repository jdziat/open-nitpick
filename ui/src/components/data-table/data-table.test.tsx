import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api'
import { DataTable, type Column } from './data-table'

type Row = { id: string; name: string; n: number }
const rows: Row[] = [
  { id: 'a', name: 'alpha', n: 3 },
  { id: 'b', name: 'beta', n: 9 },
]
const columns: Column<Row>[] = [
  { id: 'name', header: 'Name', sort: 'name', required: true, cell: (r) => r.name },
  { id: 'n', header: 'Count', sort: 'n', align: 'right', cell: (r) => String(r.n) },
  { id: 'plain', header: 'Plain', cell: () => 'x' },
  { id: 'extra', header: 'Extra', hidden: true, cell: () => 'secret-extra' },
]

function show(props: Partial<React.ComponentProps<typeof DataTable<Row>>> = {}) {
  return render(<DataTable columns={columns} rows={rows} rowKey={(r) => r.id} loading={false} empty={<p>Nothing here</p>} caption="Things" {...props} />)
}

describe('DataTable', () => {
  it('shows the rows it is given and does not sort or page them itself', () => {
    show({ rows: [rows[1], rows[0]] })
    const body = screen.getAllByRole('row').slice(1)
    expect(within(body[0]).getByText('beta')).toBeInTheDocument()
    expect(within(body[1]).getByText('alpha')).toBeInTheDocument()
  })

  it('asks for a sort when a sortable header is pressed, and only a sortable one is a button', async () => {
    const onSort = vi.fn()
    show({ onSort, sort: 'name', order: 'asc' })
    await userEvent.click(screen.getByRole('button', { name: /Count/ }))
    expect(onSort).toHaveBeenCalledWith('n')
    expect(screen.queryByRole('button', { name: /Plain/ })).toBeNull()
  })

  it('says which column is sorted, to a screen reader as well as to the eye', () => {
    show({ onSort: vi.fn(), sort: 'n', order: 'desc' })
    expect(screen.getByRole('columnheader', { name: /Count/ })).toHaveAttribute('aria-sort', 'descending')
    expect(screen.getByRole('columnheader', { name: /Name/ })).toHaveAttribute('aria-sort', 'none')
    expect(screen.getByRole('columnheader', { name: /Plain/ })).not.toHaveAttribute('aria-sort')
  })

  it('draws placeholders while the first answer is awaited, and says it is busy', () => {
    const { container } = show({ rows: [], loading: true, placeholderRows: 3 })
    expect(container.querySelectorAll('[data-slot="skeleton"]').length).toBeGreaterThanOrEqual(3)
    expect(screen.queryByText('Nothing here')).toBeNull()
    expect(container.querySelector('[aria-busy="true"]')).not.toBeNull()
  })

  it('keeps the last rows on screen while the next page loads, so the table does not flash empty', () => {
    show({ loading: true })
    expect(screen.getByText('alpha')).toBeInTheDocument()
  })

  it('says it is empty only when it is not loading and nothing failed', () => {
    show({ rows: [] })
    expect(screen.getByText('Nothing here')).toBeInTheDocument()
  })

  it('shows an error, and not the empty message, when the request failed', async () => {
    const onRetry = vi.fn()
    show({ rows: [], error: new ApiError(500, 'open /var/secret: boom'), onRetry })
    expect(screen.getByRole('alert')).toHaveTextContent(/could not be loaded/i)
    expect(screen.queryByText('Nothing here')).toBeNull()
    expect(screen.queryByText(/secret/)).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: /try again/i }))
    expect(onRetry).toHaveBeenCalled()
  })

  it('says the rows are stale when a later request failed', () => {
    show({ error: new ApiError(500, 'x') })
    expect(screen.getByText('alpha')).toBeInTheDocument()
    expect(screen.getByRole('alert')).toHaveTextContent(/last one that was/i)
  })

  it('leaves out a column that starts hidden, and offers it in the columns menu', async () => {
    show()
    expect(screen.queryByText('secret-extra')).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: /columns/i }))
    await userEvent.click(await screen.findByRole('menuitemcheckbox', { name: 'Extra' }))
    expect(screen.getAllByText('secret-extra').length).toBe(2)
  })

  it('does not let a required column be turned off', async () => {
    show()
    await userEvent.click(screen.getByRole('button', { name: /columns/i }))
    expect(await screen.findByRole('menuitemcheckbox', { name: 'Count' })).toBeInTheDocument()
    expect(screen.queryByRole('menuitemcheckbox', { name: 'Name' })).toBeNull()
  })
})
