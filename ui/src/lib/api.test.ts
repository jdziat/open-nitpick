import { afterEach, describe as suite, expect, it, vi } from 'vitest'
import { ApiError, describe, getJSON, query } from './api'

afterEach(() => vi.unstubAllGlobals())

suite('query', () => {
  it('keeps what is set and leaves out what is empty, so a cleared filter is not sent', () => {
    expect(query({ days: 30, repo: '', q: undefined, sort: 'at', page: 0, nothing: null })).toBe('?days=30&sort=at&page=0')
    expect(query({})).toBe('')
  })

  it('escapes what the reader typed', () => {
    expect(query({ q: 'a&b=c d' })).toBe('?q=a%26b%3Dc+d')
  })
})

suite('getJSON', () => {
  const reply = (status: number, body: string) => vi.fn().mockResolvedValue(new Response(body, { status }))

  it('sends the session cookie and asks for JSON', async () => {
    const f = reply(200, '{"ok":true}')
    vi.stubGlobal('fetch', f)
    expect(await getJSON('/v1/x', { days: 7 })).toEqual({ ok: true })
    expect(f).toHaveBeenCalledWith('/v1/x?days=7', expect.objectContaining({ credentials: 'same-origin', headers: { Accept: 'application/json' } }))
  })

  it('sends a person whose session ended to sign in again, and back to where they were', async () => {
    const assign = vi.fn()
    vi.stubGlobal('location', { assign, pathname: '/ui/insights/findings', search: '?days=7' })
    vi.stubGlobal('fetch', reply(401, 'sign in'))
    await expect(getJSON('/v1/x')).rejects.toBeInstanceOf(ApiError)
    expect(assign).toHaveBeenCalledWith('/auth/login?next=%2Fui%2Finsights%2Ffindings%3Fdays%3D7')
  })

  it('carries the status and the server text of a refusal', async () => {
    vi.stubGlobal('fetch', reply(400, 'days must be a whole number from 1 to 365\n'))
    await expect(getJSON('/v1/x')).rejects.toMatchObject({ status: 400, message: 'days must be a whole number from 1 to 365' })
  })

  it('does not turn a failed answer into data', async () => {
    vi.stubGlobal('fetch', reply(500, 'boom'))
    await expect(getJSON('/v1/x')).rejects.toMatchObject({ status: 500 })
  })
})

suite('describe', () => {
  it('says what a person can act on and keeps the server fault to itself', () => {
    expect(describe(new ApiError(403, 'only the authoritative role'))).toMatch(/role/)
    expect(describe(new ApiError(500, 'open /var/secret/dir: boom'))).not.toMatch(/secret/)
    expect(describe(new ApiError(400, 'page_size must be 1 to 100'))).toBe('page_size must be 1 to 100')
    expect(describe(new TypeError('network'))).toMatch(/could not be loaded/)
  })
})
