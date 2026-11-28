import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { useApi } from './use-api'

afterEach(() => vi.unstubAllGlobals())

const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200 })

describe('useApi', () => {
  it('loads, then holds the answer', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(json({ n: 1 })))
    const { result } = renderHook(() => useApi<{ n: number }>('/v1/x', { days: 7 }))
    expect(result.current.loading).toBe(true)
    await waitFor(() => expect(result.current.data).toEqual({ n: 1 }))
    expect(result.current.loading).toBe(false)
  })

  it('asks again when the parameters change, and keeps the last answer while it waits', async () => {
    let release: (r: Response) => void = () => {}
    const f = vi.fn().mockResolvedValueOnce(json({ page: 1 })).mockImplementationOnce(() => new Promise<Response>((r) => (release = r)))
    vi.stubGlobal('fetch', f)
    const { result, rerender } = renderHook(({ page }) => useApi<{ page: number }>('/v1/x', { page }), { initialProps: { page: 1 } })
    await waitFor(() => expect(result.current.data).toEqual({ page: 1 }))
    rerender({ page: 2 })
    await waitFor(() => expect(result.current.loading).toBe(true))
    expect(result.current.data).toEqual({ page: 1 })
    act(() => release(json({ page: 2 })))
    await waitFor(() => expect(result.current.data).toEqual({ page: 2 }))
    expect(f.mock.calls[1][0]).toBe('/v1/x?page=2')
  })

  it('cancels the request for parameters that are no longer current', async () => {
    const signals: AbortSignal[] = []
    vi.stubGlobal('fetch', vi.fn((_: string, init: RequestInit) => {
      signals.push(init.signal as AbortSignal)
      return new Promise<Response>(() => {})
    }))
    const { rerender } = renderHook(({ q }) => useApi('/v1/x', { q }), { initialProps: { q: 'a' } })
    rerender({ q: 'ab' })
    await waitFor(() => expect(signals.length).toBe(2))
    expect(signals[0].aborted).toBe(true)
    expect(signals[1].aborted).toBe(false)
  })

  it('reports an error without losing the data it had, and loads again on request', async () => {
    const f = vi.fn().mockResolvedValueOnce(json({ ok: 1 })).mockResolvedValueOnce(new Response('boom', { status: 500 })).mockResolvedValueOnce(json({ ok: 2 }))
    vi.stubGlobal('fetch', f)
    const { result } = renderHook(() => useApi<{ ok: number }>('/v1/x'))
    await waitFor(() => expect(result.current.data).toEqual({ ok: 1 }))
    act(() => result.current.reload())
    await waitFor(() => expect(result.current.error).toBeDefined())
    expect(result.current.data).toEqual({ ok: 1 })
    act(() => result.current.reload())
    await waitFor(() => expect(result.current.data).toEqual({ ok: 2 }))
    expect(result.current.error).toBeUndefined()
  })

  it('does nothing with no path', () => {
    const f = vi.fn()
    vi.stubGlobal('fetch', f)
    renderHook(() => useApi(null))
    expect(f).not.toHaveBeenCalled()
  })
})
