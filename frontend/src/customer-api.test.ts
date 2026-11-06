import { describe, expect, it } from 'vitest'
import { laneLabel, messageCopyState, orderIdPattern, ticketStatusText } from './customer-api'

describe('customer service lanes', () => {
  it('labels the active lane and its bound order', () => {
    expect(laneLabel(undefined)).toBe('售前咨询')
    expect(laneLabel('pre_sales')).toBe('售前咨询')
    expect(laneLabel('after_sales')).toBe('售后咨询')
    expect(laneLabel('after_sales', 'ORD-2609-0005')).toBe('售后 · ORD-2609-0005')
  })

  it('uses the same order format as the server', () => {
    expect(orderIdPattern.test('ORD-2609-0005')).toBe(true)
    expect(orderIdPattern.test('ord-2609-0005')).toBe(false)
    expect(orderIdPattern.test('ORD')).toBe(false)
    expect(orderIdPattern.test('ORD-2609-0005;DROP')).toBe(false)
  })

  it('renders active ticket states only', () => {
    expect(ticketStatusText('OPEN')).toBe('工单已提交，等待人工客服领取')
    expect(ticketStatusText('ASSIGNED')).toBe('人工客服已领取工单')
    expect(ticketStatusText('RESOLVED')).toBe('')
    expect(ticketStatusText(undefined)).toBe('')
  })
})

describe('customer message copy state', () => {
  it('uses the server copyable flag and reason', () => {
    expect(messageCopyState({ source: 'assistant', can_copy: true, blocked_reason: '' })).toEqual({ canCopy: true, reason: '' })
    expect(messageCopyState({ source: 'assistant', can_copy: false, blocked_reason: 'no_citable_evidence' }))
      .toEqual({ canCopy: false, reason: '没有可引用的资料依据' })
    expect(messageCopyState({ source: 'assistant', can_copy: false, blocked_reason: 'needs_clarification' }))
      .toEqual({ canCopy: false, reason: '需要追问' })
  })

  it('does not infer copyability when the server field is missing', () => {
    expect(messageCopyState({ source: 'assistant' })).toEqual({ canCopy: false, reason: '当前回答不可复制' })
    expect(messageCopyState({ source: 'agent', can_copy: true })).toEqual({ canCopy: false, reason: '仅 AI 回答可复制' })
  })
})
