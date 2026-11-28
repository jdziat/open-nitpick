import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SearchInput } from './toolbar'

beforeEach(() => vi.useFakeTimers({ shouldAdvanceTime: true }))
afterEach(() => vi.useRealTimers())

const input = () => screen.getByRole('searchbox', { name: 'Search things' })

describe('SearchInput', () => {
  it('waits for the reader to pause, and asks once for what they typed', async () => {
    const onChange = vi.fn()
    render(<SearchInput value="" onChange={onChange} placeholder="Search" label="Search things" />)
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
    await user.type(input(), 'auth')
    expect(onChange).not.toHaveBeenCalled()
    act(() => vi.advanceTimersByTime(350))
    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange).toHaveBeenCalledWith('auth')
  })

  it('sends what was typed without the spaces around it', async () => {
    const onChange = vi.fn()
    render(<SearchInput value="" onChange={onChange} placeholder="Search" label="Search things" />)
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
    await user.type(input(), '  auth  ')
    act(() => vi.advanceTimersByTime(350))
    expect(onChange).toHaveBeenCalledWith('auth')
  })

  it('asks at once, and with nothing, when the box is cleared', async () => {
    const onChange = vi.fn()
    render(<SearchInput value="auth" onChange={onChange} placeholder="Search" label="Search things" />)
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
    await user.click(screen.getByRole('button', { name: 'Clear search' }))
    expect(onChange).toHaveBeenCalledWith('')
    expect(input()).toHaveValue('')
  })

  it('follows the address when it changes under it, as the back button does', () => {
    const { rerender } = render(<SearchInput value="auth" onChange={vi.fn()} placeholder="Search" label="Search things" />)
    expect(input()).toHaveValue('auth')
    rerender(<SearchInput value="" onChange={vi.fn()} placeholder="Search" label="Search things" />)
    expect(input()).toHaveValue('')
  })

  it('does not ask again for a value it already has', () => {
    const onChange = vi.fn()
    render(<SearchInput value="auth" onChange={onChange} placeholder="Search" label="Search things" />)
    act(() => vi.advanceTimersByTime(1000))
    expect(onChange).not.toHaveBeenCalled()
  })
})
