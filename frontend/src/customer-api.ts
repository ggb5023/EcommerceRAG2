export type Citation = { evidence_id: string; citation_index: number; title: string; version_id: string; snippet: string }
export type Message = { id: string; source: 'customer'|'assistant'|'agent'|'system'; text: string; redacted: boolean; citations: Citation[]; can_copy?: boolean; blocked_reason?: string }
export type ServiceType = 'pre_sales'|'after_sales'
export type TicketStatus = 'OPEN'|'ASSIGNED'|'RESOLVED'|'CLOSED'
export type Ticket = { id: string; status: TicketStatus }
export type Conversation = { id: string; state: 'AI'|'WAITING_HUMAN'|'HUMAN'|'CLOSED'; epoch: number; service_type?: ServiceType; order_id?: string; active_execution_id?: string; owner_user_id?: number }
export type Snapshot = { conversation: Conversation; messages: Message[]; can_reply?: boolean; ticket?: Ticket }
export type ConversationSummary = { id: string; state: string; epoch: number; service_type?: ServiceType; order_id?: string; ticket_status?: TicketStatus }
export const orderIdPattern = /^[A-Z0-9][A-Z0-9_-]{3,39}$/
export function laneLabel(serviceType?: ServiceType, orderId?: string) {
  if (serviceType === 'after_sales') return orderId ? `售后 · ${orderId}` : '售后咨询'
  return '售前咨询'
}
export function ticketStatusText(status?: TicketStatus) {
  if (status === 'OPEN') return '工单已提交，等待人工客服领取'
  if (status === 'ASSIGNED') return '人工客服已领取工单'
  return ''
}
export const copyBlockedText: Record<string, string> = {
  mock: '本地测试结果不可复制',
  no_citable_evidence: '没有可引用的资料依据',
  incomplete: '回答尚未完成',
  needs_clarification: '需要追问',
  not_answer: '当前回答不是可复制的正式回答',
  not_copyable: '当前回答不可复制',
}
export function messageCopyState(message: Pick<Message, 'source' | 'can_copy' | 'blocked_reason'>) {
  if (message.source !== 'assistant') return { canCopy: false, reason: '仅 AI 回答可复制' }
  const canCopy = message.can_copy === true
  return { canCopy, reason: canCopy ? '' : (copyBlockedText[message.blocked_reason ?? 'not_copyable'] ?? '当前回答不可复制') }
}
export class ChannelError extends Error { constructor(public status: number) { super(({400:'请检查服务类型或订单号。',401:'会话已过期，请重新进入。',403:'权限已变化，受限内容已清除。',409:'会话状态已变化，请刷新后重试。',429:'发送过于频繁，请稍后重试。',503:'服务暂不可用，请稍后重试。'} as Record<number,string>)[status] ?? '请求失败，请重试。') } }
export async function channel<T>(path: string, body?: unknown, csrf = '', key = crypto.randomUUID()): Promise<T> {
  const response = await fetch(path, { credentials: 'same-origin', ...(body === undefined ? {} : {method:'POST', headers:{'Content-Type':'application/json','X-CSRF-Token':csrf,'Idempotency-Key':key},body:JSON.stringify(body)}) })
  if (!response.ok) throw new ChannelError(response.status)
  return response.json() as Promise<T>
}
// Durable SSE is a wake-up signal. Always read an authorized snapshot, including
// after reconnect; this also removes withdrawn knowledge from displayed history.
export async function watchChannel(path: string, signal: AbortSignal, changed: () => Promise<void>, failed: (e: unknown) => void) {
  let last = 0
  while (!signal.aborted) {
    try {
      const response = await fetch(path, {credentials:'same-origin',signal,headers:last ? {'Last-Event-ID':String(last)} : {}})
      if (!response.ok) throw new ChannelError(response.status)
      const reader = response.body!.getReader(); const decoder = new TextDecoder(); let pending = ''
      while (!signal.aborted) {
        const {value,done} = await reader.read(); if (done) break
        pending += decoder.decode(value,{stream:true})
        let end: number
        while ((end=pending.indexOf('\n\n'))>=0) {
          const frame=pending.slice(0,end); pending=pending.slice(end+2)
          const id=Number(frame.match(/^id: (\d+)$/m)?.[1] ?? 0)
          if (id>last) { await changed(); last=id }
        }
      }
    } catch(e) { if (signal.aborted) return; failed(e); if (e instanceof ChannelError && [401,403,404].includes(e.status)) return }
    await new Promise<void>(resolve=>{ const timer=setTimeout(resolve,1000); signal.addEventListener('abort',()=>{clearTimeout(timer);resolve()},{once:true}) })
  }
}

export type SuggestionCitation = {evidence_id:string; citation_index:number; title:string; version_id:string; snippet:string}
export type SuggestionView = {request_id:string; answer:string; can_copy:boolean; blocked_reason:string; citations:SuggestionCitation[]}
export type SuggestionReceipt = {request_id:string; events_url:string; status:string}
export type TurnEvidence = {id:string; documentId?:string; versionId?:string; snippet?:string; title?:string}
export type TurnCitation = {evidence_id:string; citation_index:number}
export type TurnView = {status:string; answer?:string; evidence?:TurnEvidence[]; citations?:TurnCitation[];
  customer_reply?:{text_plain?:string; can_copy?:boolean; blocked_reason?:string}}
