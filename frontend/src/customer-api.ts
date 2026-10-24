export type Citation = { evidence_id: string; citation_index: number; title: string; version_id: string; snippet: string }
export type Message = { id: string; source: 'customer'|'assistant'|'agent'|'system'; text: string; redacted: boolean; citations: Citation[] }
export type Conversation = { id: string; state: 'AI'|'WAITING_HUMAN'|'HUMAN'|'CLOSED'; epoch: number; active_execution_id?: string; owner_user_id?: number }
export type Snapshot = { conversation: Conversation; messages: Message[]; can_reply?: boolean }
export class ChannelError extends Error { constructor(public status: number) { super(({401:'会话已过期，请重新进入。',403:'权限已变化，受限内容已清除。',409:'会话状态已变化，请刷新后重试。',429:'发送过于频繁，请稍后重试。',503:'服务暂不可用，请稍后重试。'} as Record<number,string>)[status] ?? '请求失败，请重试。') } }
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
