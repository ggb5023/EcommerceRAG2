import { describe, expect, it } from 'vitest'
import { groupedNavigation } from './navigation'

const pages = (flags: Parameters<typeof groupedNavigation>[0]) =>
  groupedNavigation(flags).flatMap((group) => group.items.map((item) => item.page))

describe('grouped admin navigation', () => {
  it('shows operations, knowledge and permission groups for a merchant admin', () => {
    const result = pages({ access: false, manage: true, review: false, observe: false })
    expect(result).toContain('overview')
    expect(result).toContain('sources')
    expect(result).toContain('answers')
    expect(result).toContain('accounts')
    expect(result).not.toContain('reviews')
    expect(result).not.toContain('audit')
  })

  it('gives the access role permission management but no knowledge review', () => {
    const result = pages({ access: true, manage: false, review: false, observe: false })
    expect(result).toContain('accounts')
    expect(result).toContain('audit')
    expect(result).not.toContain('reviews')
    expect(result).not.toContain('answers')
    expect(result).not.toContain('overview')
  })

  it('omits empty groups', () => {
    const groups = groupedNavigation({ access: false, manage: false, review: false, observe: false })
    const labels = groups.map((group) => group.label)
    expect(labels).toEqual(['权限'])
    expect(groups[0].items.map((item) => item.page)).toEqual(['requests'])
  })
})
