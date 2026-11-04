import { afterEach, describe, expect, it, vi } from 'vitest'
import { downloadGaps, triggerDownload } from './api'

afterEach(() => vi.unstubAllGlobals())

describe('gap download', () => {
  it('downloads the JSONL body and filename', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{"query":"退款"}\n', {
      status: 200, headers: { 'Content-Disposition': 'attachment; filename="gaps-test.jsonl"' },
    })))
    const result = await downloadGaps('30d')
    expect(result.filename).toBe('gaps-test.jsonl')
    expect(await result.blob.text()).toContain('退款')
  })

  it('triggers Blob URL download and revokes it', () => {
    const click = vi.fn()
    const create = vi.fn(() => 'blob:test')
    const revoke = vi.fn()
    vi.stubGlobal('URL', { createObjectURL: create, revokeObjectURL: revoke })
    vi.stubGlobal('document', { createElement: vi.fn(() => ({ click, href: '', download: '' })) })
    triggerDownload(new Blob(['x']), 'gaps.jsonl')
    expect(create).toHaveBeenCalled()
    expect(click).toHaveBeenCalled()
    expect(revoke).toHaveBeenCalledWith('blob:test')
  })
})
