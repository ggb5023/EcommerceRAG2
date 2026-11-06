<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref} from 'vue'
import {useRoute,useRouter} from 'vue-router'
import {browserAuth,checkBrowserAuth,login,logout} from './auth'
import {channel,ChannelError,watchChannel,laneLabel,ticketStatusText,type Snapshot,type QueueItem,type SuggestionReceipt,type SuggestionView,type TurnView} from './customer-api'
import SuggestionPanel from './SuggestionPanel.vue'
import {canSuggestFor} from './staff-suggestion'
const route=useRoute();const router=useRouter();const queue=ref<QueueItem[]>([]);const snapshot=ref<Snapshot|null>(null);const text=ref('');const error=ref('');const busy=ref(false);const owned=ref(false)
const suggestion=ref<SuggestionView|null>(null);const suggestionBusy=ref(false);const suggestionError=ref('')
const adoptBusy=ref(false);const adoptNotice=ref('')
const canSuggest=computed(()=>canSuggestFor(owned.value,snapshot.value?.conversation.state))
function resetSuggestion(){suggestion.value=null;suggestionError.value='';adoptNotice.value=''}
let stream:AbortController|undefined; let poll:ReturnType<typeof setInterval>;let heartbeat:ReturnType<typeof setInterval>
function failed(e:unknown){error.value=e instanceof Error?e.message:'服务暂不可用。';if(e instanceof ChannelError&&[401,403].includes(e.status)){queue.value=[];snapshot.value=null;owned.value=false;stream?.abort();if(e.status===401)browserAuth.authenticated=false}}
function suggestionFailed(e:unknown){suggestionError.value=e instanceof Error?e.message:'建议生成暂不可用。'}
async function refresh(){if(snapshot.value){snapshot.value=await channel<Snapshot>(`/v1/support/conversations/${snapshot.value.conversation.id}`);owned.value=!!snapshot.value.can_reply;if(!owned.value)resetSuggestion()}}
async function load(){await checkBrowserAuth();if(browserAuth.enabled&&!browserAuth.authenticated){queue.value=[];snapshot.value=null;owned.value=false;resetSuggestion();stream?.abort();return;}queue.value=(await channel<{conversations:QueueItem[]}>('/v1/support/queue')).conversations;await refresh()}
async function open(id:string){stream?.abort();owned.value=false;resetSuggestion();snapshot.value=await channel<Snapshot>(`/v1/support/conversations/${id}`);owned.value=!!snapshot.value.can_reply;await router.replace({ path: '/staff', query: { tab: 'queue', id } });stream=new AbortController();void watchChannel(`/v1/support/conversations/${id}/events`,stream.signal,refresh,failed)}
async function act(action:string){if(!snapshot.value||busy.value)return;busy.value=true;error.value='';try{await channel(`/v1/support/conversations/${snapshot.value.conversation.id}/${action}`,{epoch:snapshot.value.conversation.epoch,...(action==='messages'?{text:text.value}:{})},browserAuth.csrf);if(action==='claim')owned.value=true;if(['release','close','resolve','resume-ai','requeue'].includes(action)){owned.value=false;resetSuggestion();}if(action==='messages')text.value='';await load()}catch(e){if(action==='heartbeat')owned.value=false;failed(e);await refresh().catch(failed)}finally{busy.value=false}}
function turnToSuggestion(requestID:string,turn:TurnView):SuggestionView{const evidence=new Map((turn.evidence||[]).map(item=>[item.id,item]));return {request_id:requestID,answer:turn.customer_reply?.text_plain??turn.answer??'',can_copy:!!turn.customer_reply?.can_copy,blocked_reason:turn.customer_reply?.blocked_reason??'',citations:(turn.citations||[]).map(citation=>{const item=evidence.get(citation.evidence_id);return {evidence_id:citation.evidence_id,citation_index:citation.citation_index,title:item?.title??item?.documentId??'',version_id:item?.versionId??'',snippet:item?.snippet??''}})}}
async function pollSuggestion(receipt:SuggestionReceipt){const deadline=Date.now()+60000;while(Date.now()<deadline){const turn=await channel<TurnView>(`/v1/turns/${receipt.request_id}`);if(!['PENDING','EXECUTING','CANCEL_REQUESTED'].includes(turn.status)){suggestion.value=turnToSuggestion(receipt.request_id,turn);return}await new Promise(resolve=>setTimeout(resolve,300))}throw new Error('建议生成超时，请稍后重试。')}
async function generateSuggestion(value:string){if(!value.trim()||suggestionBusy.value)return;suggestionBusy.value=true;suggestionError.value='';try{const receipt=await channel<SuggestionReceipt>('/v1/support/suggestions',{text:value.trim()},browserAuth.csrf);await pollSuggestion(receipt)}catch(e){suggestionFailed(e)}finally{suggestionBusy.value=false}}
async function generateFromConversation(){if(!snapshot.value||suggestionBusy.value)return;suggestionBusy.value=true;suggestionError.value='';try{const receipt=await channel<SuggestionReceipt>(`/v1/support/conversations/${snapshot.value.conversation.id}/suggestions`,{},browserAuth.csrf);await pollSuggestion(receipt)}catch(e){suggestionFailed(e)}finally{suggestionBusy.value=false}}
async function adoptCurrentSuggestion(){if(!suggestion.value||adoptBusy.value)return;adoptBusy.value=true;adoptNotice.value='';suggestionError.value='';try{const receipt=await channel<{job_id:string}>(`/v1/support/suggestions/${suggestion.value.request_id}/adopt`,{},browserAuth.csrf);adoptNotice.value=`已提交知识草稿（任务 ${receipt.job_id}），审核发布后才会参与回答。`}catch(e){suggestionError.value=e instanceof Error?e.message:'采纳暂不可用。'}finally{adoptBusy.value=false}}
async function copySuggestion(){if(!suggestion.value?.can_copy||suggestionBusy.value)return;suggestionBusy.value=true;suggestionError.value='';try{const copied=await channel<{text:string}>(`/v1/support/suggestions/${suggestion.value.request_id}/copy`,{},browserAuth.csrf);if(navigator.clipboard)navigator.clipboard.writeText(copied.text).catch(suggestionFailed);suggestionError.value='已复制建议回复。'}catch(e){suggestionFailed(e)}finally{suggestionBusy.value=false}}
async function exit(){owned.value=false;stream?.abort();snapshot.value=null;queue.value=[];try{await logout()}catch(e){failed(e)}}
onMounted(async()=>{try{await load();if(route.query.id)await open(String(route.query.id))}catch(e){failed(e)}poll=setInterval(()=>void load().catch(failed),5000);heartbeat=setInterval(()=>{if(owned.value)void act('heartbeat')},30000)})
onUnmounted(()=>{stream?.abort();clearInterval(poll);clearInterval(heartbeat)})
</script>

<template>
  <main class="channel-shell zone-web">
    <header><h1>客服工作台</h1><nav><a href="/assistant">AI 查询台</a><a href="/assistant/catalog">商品目录</a><button v-if="browserAuth.authenticated" @click="exit">退出</button></nav></header>
    <p v-if="error" role="alert">{{error}}</p>
    <button v-if="browserAuth.enabled&&!browserAuth.authenticated" @click="login('customer')">员工登录</button>
    <div v-else class="support-columns" :class="{ 'has-suggestion': canSuggest }">
      <aside><h2>待接管 / 我的会话</h2><button @click="load().catch(failed)">刷新队列</button><p v-if="!queue.length">暂无待接管会话。</p><button v-for="c in queue" :key="c.id" class="queue-item" @click="open(c.id).catch(failed)">
          <strong>{{c.state==='WAITING_HUMAN'?'待领取':'已领取'}} · {{laneLabel(c.service_type,c.order_id)}} · {{c.id.slice(-8)}}</strong>
          <small v-if="c.ticket_status">{{ticketStatusText(c.ticket_status)}}</small>
        </button></aside>
      <section v-if="snapshot"><h2>会话 {{snapshot.conversation.id.slice(-8)}}</h2><p role="status">{{snapshot.conversation.state}} · 控制版本 {{snapshot.conversation.epoch}}</p><p class="conversation-lane" data-testid="conversation-lane">{{laneLabel(snapshot.conversation.service_type,snapshot.conversation.order_id)}}<span v-if="snapshot.ticket"> · {{ticketStatusText(snapshot.ticket.status)}}</span></p><button v-if="snapshot.conversation.state==='WAITING_HUMAN'" @click="act('claim')" :disabled="busy">领取会话</button>
        <article v-for="m in snapshot.messages" :key="m.id"><strong>{{({customer:'客户',assistant:'AI 客服',agent:'人工客服',system:'服务提示'})[m.source]}}</strong><p class="message-text">{{m.text}}</p></article>
        <form v-if="snapshot.conversation.state==='HUMAN'&&owned" @submit.prevent="act('messages')"><label>人工回复<textarea v-model="text" maxlength="4000" required/></label><button :disabled="busy||!text.trim()">发送人工回复</button><button type="button" @click="act('release')">释放到队列</button><button type="button" @click="act('resume-ai')">交回 AI</button><button type="button" @click="act('close')">结束会话</button><button v-if="snapshot.conversation.service_type==='after_sales'" type="button" data-testid="resolve-ticket" @click="act('resolve')">解决并关闭工单</button></form>
        <p v-else-if="snapshot.conversation.state==='HUMAN'">本页尚未取得有效领取，请重新领取或等待释放。</p>
      </section>
      <section v-if="canSuggest" class="suggestion-column"><SuggestionPanel :key="snapshot?.conversation.id" :suggestion="suggestion" :busy="suggestionBusy" :error="suggestionError" :adopt-busy="adoptBusy" :adopt-notice="adoptNotice" @generate="generateSuggestion" @copy="copySuggestion" @adopt="adoptCurrentSuggestion"/><button type="button" data-testid="suggestion-from-conversation" :disabled="suggestionBusy" @click="generateFromConversation">对最后一条顾客消息生成建议</button></section>
    </div>
  </main>
</template>
<style src="./channel.css"/>
