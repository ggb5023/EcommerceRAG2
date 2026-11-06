import { describe, expect, it } from 'vitest'
import { shouldLandInAdmin } from './auth'

describe('admin landing', () => {
  it('sends admin-role accounts to the console', () => {
    expect(shouldLandInAdmin(['access_admin'], false)).toBe(true)
    expect(shouldLandInAdmin(['merchant_admin', 'knowledge_reviewer'], false)).toBe(true)
  })
  it('keeps staff and explicit stays in the chat workbench', () => {
    expect(shouldLandInAdmin([], false)).toBe(false)
    expect(shouldLandInAdmin(undefined, false)).toBe(false)
    expect(shouldLandInAdmin(['access_admin'], true)).toBe(false)
  })
})
