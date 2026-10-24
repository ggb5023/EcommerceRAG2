import { afterEach, describe, expect, it, vi } from 'vitest'
import { browserAuth, checkBrowserAuth, csrfHeaders, logout } from './auth'

afterEach(() => {
  browserAuth.enabled = false; browserAuth.authenticated = false; browserAuth.csrf = ''
  vi.unstubAllGlobals()
})

describe('browser identity boundary', () => {
  it('keeps only the CSRF value in application memory', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ enabled: true, authenticated: true, csrf_token: 'synthetic-csrf' }))))
    await checkBrowserAuth()
    expect(csrfHeaders()).toEqual({ 'X-CSRF-Token': 'synthetic-csrf' })
    expect(Object.keys(browserAuth).sort()).toEqual(['authenticated', 'csrf', 'enabled'])
  })
  it('clears local state even when upstream logout fails', async () => {
    browserAuth.enabled = true; browserAuth.authenticated = true; browserAuth.csrf = 'synthetic-csrf'
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 503 }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(logout()).rejects.toMatchObject({ status: 503 })
    expect(browserAuth.authenticated).toBe(false)
    expect(browserAuth.csrf).toBe('')
    expect(fetchMock).toHaveBeenCalledWith('/v1/auth/logout', expect.objectContaining({ method: 'POST', headers: { 'X-CSRF-Token': 'synthetic-csrf' } }))
  })
  it('does not treat identity outages as a disabled development profile', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{}', { status: 503 })))
    await expect(checkBrowserAuth()).rejects.toMatchObject({ status: 503 })
  })
})
