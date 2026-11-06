import { describe, expect, it } from 'vitest'
import { canSuggestFor } from './staff-suggestion'

describe('staff suggestion visibility', () => {
  it('shows suggestions only for an owned human ticket', () => {
    expect(canSuggestFor(true, 'HUMAN')).toBe(true)
    expect(canSuggestFor(false, 'HUMAN')).toBe(false)
    expect(canSuggestFor(true, 'WAITING_HUMAN')).toBe(false)
    expect(canSuggestFor(true, 'AI')).toBe(false)
    expect(canSuggestFor(true, undefined)).toBe(false)
  })
})
