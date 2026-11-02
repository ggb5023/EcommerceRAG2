<script setup lang="ts">
import {computed,nextTick,onMounted,onUnmounted,ref,watch} from 'vue'
import {useRoute} from 'vue-router'
import CitationCard from './components/CitationCard.vue'
import {channel,ChannelError,watchChannel,type Snapshot,type Citation,type Message} from './customer-api'
const route=useRoute(); const slug=String(route.params.slug)
const shop=ref({name:'客户咨询',welcome_message:''}); const csrf=ref(''); const snapshot=ref<Snapshot|null>(null)
const text=ref(''); const error=ref(''); const busy=ref(false); const sources=ref<Citation[]>([])
const stateText=computed(()=>({AI:snapshot.value?.conversation.active_execution_id?'正在查找店铺知识…':'AI 知识客服',WAITING_HUMAN:'正在等待人工客服领取；暂无法提供预计响应时间。',HUMAN:'人工客服正在为你服务',CLOSED:'本次会话已结束'}[snapshot.value?.conversation.state??'AI']))
const chatEl=ref<HTMLElement|null>(null); const messagesEl=ref<HTMLElement|null>(null)
const drawerOpen=ref(false); const activeMessage=ref<Message|null>(null); const sourcesLoading=ref(false)
const stickToBottom=ref(true); const showJump=ref(false); const copiedId=ref(''); const copyNotice=ref('')
let viewEpoch=0
let controller=new AbortController(); let poll:ReturnType<typeof setInterval>; let viewport:VisualViewport|null=null; let pressTimer:ReturnType<typeof setTimeout>|undefined
const blockedText:Record<string,string>={mock:'本地测试结果不可复制',no_citable_evidence:'没有可引用的资料依据',incomplete:'回答尚未完成',not_copyable:'当前回答不可复制'}
function copyState(message:Message){const explicit=message as Message&{can_copy?:boolean;blocked_reason?:string};if(typeof explicit.can_copy==='boolean')return {canCopy:explicit.can_copy,reason:explicit.can_copy?'':(blockedText[explicit.blocked_reason??'']??'当前回答不可复制')};if(message.source!=='assistant')return {canCopy:false,reason:'仅 AI 回答可复制'};if(!message.citations.length)return {canCopy:false,reason:'没有可引用的资料依据'};return {canCopy:true,reason:''}}
function failed(e:unknown) {error.value=e instanceof Error?e.message:'服务暂不可用。'; if(e instanceof ChannelError && [401,403,404].includes(e.status)){snapshot.value=null;sources.value=[];controller.abort();closeSources()}}
function syncViewport(){if(!chatEl.value)return;const height=Math.round(viewport?.height??window.innerHeight);const top=Math.round(viewport?.offsetTop??0);chatEl.value.style.setProperty('--chat-height',`${height}px`);chatEl.value.style.setProperty('--chat-top',`${top}px`)}
function onMessagesScroll(){const el=messagesEl.value;if(!el)return;stickToBottom.value=el.scrollHeight-el.scrollTop-el.clientHeight<80;showJump.value=!stickToBottom.value}
function scrollLatest(behavior:ScrollBehavior='smooth'){const el=messagesEl.value;if(!el)return;el.scrollTo({top:el.scrollHeight,behavior});stickToBottom.value=true;showJump.value=false}
function closeSources(){drawerOpen.value=false;activeMessage.value=null;sources.value=[];sourcesLoading.value=false}
async function loadSources(message:Message){if(!snapshot.value)return;sourcesLoading.value=true;try{sources.value=(await channel<{citations:Citation[]}>(`/v1/customer/conversations/${snapshot.value.conversation.id}/citations/${message.id}`)).citations}finally{sourcesLoading.value=false}}
async function openSources(message:Message){if(!snapshot.value)return;activeMessage.value=message;drawerOpen.value=true;copyNotice.value='';sources.value=[];try{await loadSources(message)}catch(e){failed(e);closeSources()}}
async function refresh(){if(!snapshot.value)return;const epoch=viewEpoch;const id=snapshot.value.conversation.id;const result=await channel<Snapshot>(`/v1/customer/conversations/${id}`);if(epoch!==viewEpoch||snapshot.value?.conversation.id!==id)return;snapshot.value=result;if(drawerOpen.value&&activeMessage.value){const updated=result.messages?.find(message=>message.id===activeMessage.value?.id);if(!updated)closeSources();else{activeMessage.value=updated;if(updated.citations.length)await loadSources(updated).catch(failed);else sources.value=[]}}else if(sources.value.length)sources.value=[]}
async function enter(fresh=false){const epoch=++viewEpoch;controller.abort();controller=new AbortController();closeSources();snapshot.value=null;sources.value=[];text.value='';busy.value=true;error.value='';stickToBottom.value=true;showJump.value=false;try{
 shop.value=await channel(`/v1/customer/shops/${encodeURIComponent(slug)}`)
 const session=await channel<{csrf_token:string}>('/v1/customer/session',{shop_slug:slug});csrf.value=session.csrf_token
 const list=await channel<{conversations:{id:string;state:string}[]}>('/v1/customer/conversations')
 let id=fresh?undefined:list.conversations.find(c=>c.state!=='CLOSED')?.id
 if(!id)id=(await channel<{conversation_id:string}>('/v1/customer/conversations',{},csrf.value)).conversation_id
 snapshot.value=await channel<Snapshot>(`/v1/customer/conversations/${id}`)
 void watchChannel(`/v1/customer/conversations/${id}/events`,controller.signal,refresh,e=>{if(epoch===viewEpoch)failed(e)})
 }catch(e){failed(e)}finally{busy.value=false;nextTick(()=>scrollLatest('auto'))}}
async function send(){if(!text.value.trim()||!snapshot.value)return;busy.value=true;error.value='';stickToBottom.value=true;try{await channel(`/v1/customer/conversations/${snapshot.value.conversation.id}/messages`,{text:text.value},csrf.value);text.value='';await refresh();nextTick(()=>scrollLatest('auto'))}catch(e){failed(e)}finally{busy.value=false}}
async function handoff(){if(!snapshot.value)return;busy.value=true;try{await channel(`/v1/customer/conversations/${snapshot.value.conversation.id}/handoff`,{},csrf.value);await refresh()}catch(e){failed(e)}finally{busy.value=false}}
function beginPress(message:Message){cancelPress();if(message.source!=='assistant'||!copyState(message).canCopy)return;pressTimer=setTimeout(()=>void copyMessage(message),600)}
function cancelPress(){if(pressTimer){clearTimeout(pressTimer);pressTimer=undefined}}
async function copyMessage(message:Message){if(!copyState(message).canCopy)return;copiedId.value=message.id;copyNotice.value='';try{if(!navigator.clipboard)throw new Error('clipboard unavailable');await navigator.clipboard.writeText(message.text);copyNotice.value='已复制回答。'}catch{copyNotice.value='复制失败，请手动选择文本。'}}
watch(()=>snapshot.value?.messages.map(message=>`${message.id}:${message.text}`).join('|'),()=>{if(stickToBottom.value)nextTick(()=>scrollLatest('auto'))})
onMounted(()=>{viewport=window.visualViewport;viewport?.addEventListener('resize',syncViewport);viewport?.addEventListener('scroll',syncViewport);window.addEventListener('resize',syncViewport);nextTick(syncViewport);void enter();poll=setInterval(()=>void refresh().catch(failed),5000)})
onUnmounted(()=>{cancelPress();controller.abort();clearInterval(poll);viewport?.removeEventListener('resize',syncViewport);viewport?.removeEventListener('scroll',syncViewport);window.removeEventListener('resize',syncViewport)})
</script>

<template>
<main ref="chatEl" class="customer-chat zone-web">
  <header class="chat-header">
    <div class="chat-topline">
      <div class="chat-identity"><h1>{{shop.name}}</h1><strong role="status">{{stateText}}</strong></div>
      <button v-if="snapshot" type="button" class="quiet-action" :disabled="busy" @click="enter(true)">开始新咨询</button>
    </div>
    <details v-if="shop.welcome_message" class="shop-note"><summary>店铺说明</summary><p>{{shop.welcome_message}}</p></details>
    <small v-if="snapshot" class="new-note">新咨询保留旧会话及消息，不会结束原有人工排队或接管。</small>
  </header>

  <section ref="messagesEl" class="messages" aria-label="咨询记录" @scroll="onMessagesScroll">
    <p v-if="error" role="alert" class="chat-error">{{error}}</p>
    <div v-if="!snapshot" class="enter-state"><button type="button" :disabled="busy" @click="enter()">{{busy?'正在连接…':'重新进入'}}</button></div>
    <article v-for="m in snapshot?.messages" :key="m.id" :class="m.source" @pointerdown="beginPress(m)" @pointerup="cancelPress" @pointercancel="cancelPress" @pointermove="cancelPress">
      <strong>{{({customer:'你',assistant:'AI 客服',agent:'人工客服',system:'服务提示'})[m.source]}}</strong>
      <p class="message-text">{{m.text}}</p>
      <div v-if="m.source==='assistant'" class="answer-actions">
        <div v-if="m.citations.length" class="citation-badges">
          <button v-for="citation in m.citations" :key="citation.evidence_id" type="button" class="citation-badge" :aria-label="`查看来源 ${citation.citation_index}`" @pointerdown.stop @click="openSources(m)">[{{citation.citation_index}}]</button>
        </div>
        <button type="button" class="copy-action" @pointerdown.stop :disabled="!copyState(m).canCopy" @click="copyMessage(m)">复制回答</button>
        <small v-if="!copyState(m).canCopy" class="copy-reason">{{copyState(m).reason}}</small>
        <small v-else-if="copiedId===m.id" role="status" class="copy-notice">{{copyNotice}}</small>
      </div>
    </article>
    <button v-if="showJump" type="button" class="jump-action" @click="scrollLatest()">回到最新</button>
  </section>

  <form v-if="snapshot?.conversation.state!=='CLOSED' && snapshot" class="composer" @submit.prevent="send">
    <label class="sr-only" for="question">你的问题</label>
    <div class="composer-row">
      <textarea id="question" v-model="text" maxlength="2000" rows="2" required :disabled="busy||!!snapshot.conversation.active_execution_id"/>
      <button type="submit" class="send-action" :disabled="busy||!text.trim()||!!snapshot.conversation.active_execution_id">发送</button>
    </div>
    <button type="button" class="handoff-action" :disabled="busy||snapshot.conversation.state!=='AI'" @click="handoff">联系人工客服</button>
  </form>
  <div v-if="snapshot" class="chat-notes"><small>匿名历史保存在当前浏览器会话中；丢失 Cookie 后无法找回。</small><small>当前价格、库存、订单和配送承诺请联系人工。</small></div>

  <div v-if="drawerOpen" class="source-backdrop" @click.self="closeSources">
    <aside class="source-drawer" role="complementary" aria-label="回答来源">
      <header><h2>回答来源</h2><button type="button" class="quiet-action" @click="closeSources">收起来源</button></header>
      <p v-if="sourcesLoading" role="status">正在读取来源…</p>
      <div v-else class="source-list"><CitationCard v-for="source in sources" :key="source.evidence_id" :citation="source"/><p v-if="!sources.length">没有可显示的资料依据。</p></div>
    </aside>
  </div>
</main>
</template>

<style scoped>
.customer-chat{position:fixed;top:var(--chat-top,0);left:0;right:0;margin:0 auto;width:100%;max-width:900px;height:var(--chat-height,100dvh);display:flex;flex-direction:column;overflow:hidden;background:var(--color-bg);color:var(--color-text);font:16px/1.6 var(--font-sans)}
.chat-header{flex:none;padding:calc(12px + env(safe-area-inset-top)) 16px 10px;border-bottom:1px solid var(--color-border);background:var(--color-surface)}
.chat-topline{display:flex;align-items:center;justify-content:space-between;gap:12px}
.chat-identity{min-width:0}.chat-identity h1{margin:0;font-size:18px;line-height:1.3;overflow-wrap:anywhere}.chat-identity strong{display:block;margin-top:2px;color:var(--color-text-muted);font-size:12px;font-weight:500}
.shop-note{margin-top:6px;color:var(--color-text-muted);font-size:12px}.shop-note summary{display:flex;align-items:center;min-height:44px;cursor:pointer}.shop-note p{margin:4px 0 0;font-size:12px;line-height:1.5}
.new-note{display:block;margin-top:2px;color:var(--color-text-muted);font-size:11px;line-height:1.45}
.messages{flex:1;min-height:0;overflow-y:auto;display:flex;flex-direction:column;gap:12px;padding:16px;overscroll-behavior:contain;scroll-padding-bottom:12px}
.messages article{max-width:85%;padding:12px;border:1px solid var(--color-border);border-radius:16px;background:var(--color-surface);overflow-wrap:anywhere}
.messages article strong{display:block;margin-bottom:4px;color:var(--color-text-muted);font-size:11px}
.messages article.customer{align-self:flex-end;border-color:var(--color-primary);background:var(--color-primary);color:var(--color-on-primary)}
.messages article.customer strong{color:var(--color-on-primary);opacity:.8}
.messages article.assistant{align-self:flex-start}
.messages article.agent{align-self:flex-start;border-left:4px solid var(--color-success)}
.messages article.system{align-self:center;max-width:100%;background:var(--color-bg);font-size:13px}
.message-text{margin:0;white-space:pre-wrap;overflow-wrap:anywhere}
.answer-actions{display:flex;align-items:center;gap:8px;flex-wrap:wrap;margin-top:10px}
.citation-badges{display:flex;gap:8px;flex-wrap:wrap}
.citation-badge{min-width:32px;min-height:32px;padding:2px 7px;border:1px solid var(--color-border);border-radius:999px;background:var(--color-bg);color:var(--color-primary);font-size:13px;font-weight:650}
.copy-action,.quiet-action,.handoff-action,.jump-action,.send-action,.enter-state button{min-height:44px;padding:8px 12px;border:1px solid var(--color-border);border-radius:var(--radius-sm);background:var(--color-surface);color:var(--color-text);font:inherit;font-size:13px;font-weight:650}
.copy-action:disabled,.handoff-action:disabled,.send-action:disabled,.quiet-action:disabled,.enter-state button:disabled{opacity:.5;cursor:not-allowed}
.copy-reason,.copy-notice{color:var(--color-text-muted);font-size:12px}
.chat-error{padding:10px 12px;border:1px solid var(--color-border);border-radius:var(--radius-sm);background:var(--color-surface);color:var(--color-danger);font-size:13px}
.enter-state{display:flex;justify-content:center;padding:24px 0}
.jump-action{align-self:center;flex:none;background:var(--color-surface);box-shadow:var(--shadow-md)}
.composer{flex:none;display:grid;gap:8px;padding:10px 16px;border-top:1px solid var(--color-border);background:var(--color-surface)}
.composer-row{display:flex;align-items:flex-end;gap:8px}
.composer textarea{flex:1;min-width:0;min-height:52px;max-height:132px;resize:none;padding:10px;border:1px solid var(--color-border);border-radius:var(--radius-md);background:var(--color-surface);color:var(--color-text);font-size:16px;line-height:1.4}
.send-action{flex:none;min-width:64px;border-color:var(--color-primary);background:var(--color-primary);color:var(--color-on-primary)}
.handoff-action{justify-self:start;border-color:var(--color-border);background:var(--color-bg);color:var(--color-text-muted)}
.chat-notes{flex:none;display:grid;gap:2px;padding:0 16px calc(8px + env(safe-area-inset-bottom));background:var(--color-surface);color:var(--color-text-muted);font-size:11px;line-height:1.4}
.sr-only{position:absolute;width:1px;height:1px;padding:0;margin:-1px;overflow:hidden;clip:rect(0,0,0,0);white-space:nowrap;border:0}
.source-backdrop{position:fixed;inset:0;z-index:50;display:flex;align-items:flex-end;background:var(--color-overlay)}
.source-drawer{width:100%;max-height:85dvh;overflow:auto;padding:16px 16px calc(16px + env(safe-area-inset-bottom));border-radius:20px 20px 0 0;background:var(--color-surface);box-shadow:var(--shadow-md)}
.source-drawer header{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:12px}.source-drawer h2{margin:0;font-size:16px}
.source-list{display:grid;gap:10px}.source-list>p{color:var(--color-text-muted);font-size:13px}
@media(min-width:901px){.customer-chat{border-left:1px solid var(--color-border);border-right:1px solid var(--color-border)}.source-drawer{max-width:900px;margin:0 auto}}
</style>
