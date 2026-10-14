<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref} from 'vue'
import {useRoute} from 'vue-router'
import {channel,ChannelError,watchChannel,type Snapshot,type Citation} from './customer-api'
const route=useRoute(); const slug=String(route.params.slug)
const shop=ref({name:'客户咨询',welcome_message:''}); const csrf=ref(''); const snapshot=ref<Snapshot|null>(null)
const text=ref(''); const error=ref(''); const busy=ref(false); const sources=ref<Citation[]>([])
const stateText=computed(()=>({AI:snapshot.value?.conversation.active_execution_id?'正在查找店铺知识…':'AI 知识客服',WAITING_HUMAN:'正在等待人工客服领取；暂无法提供预计响应时间。',HUMAN:'人工客服正在为你服务',CLOSED:'本次会话已结束'}[snapshot.value?.conversation.state??'AI']))
let controller=new AbortController(); let poll:ReturnType<typeof setInterval>
function failed(e:unknown) {error.value=e instanceof Error?e.message:'服务暂不可用。'; if(e instanceof ChannelError && [401,403,404].includes(e.status)){snapshot.value=null;sources.value=[];controller.abort()} }
async function refresh(){if(!snapshot.value)return; snapshot.value=await channel<Snapshot>(`/v1/customer/conversations/${snapshot.value.conversation.id}`); if(sources.value.length)sources.value=[]}
async function enter(fresh=false){controller.abort();controller=new AbortController();busy.value=true;error.value='';try{
 shop.value=await channel(`/v1/customer/shops/${encodeURIComponent(slug)}`)
 const session=await channel<{csrf_token:string}>('/v1/customer/session',{shop_slug:slug});csrf.value=session.csrf_token
 const list=await channel<{conversations:{id:string;state:string}[]}>('/v1/customer/conversations')
 let id=fresh?undefined:list.conversations.find(c=>c.state!=='CLOSED')?.id
 if(!id)id=(await channel<{conversation_id:string}>('/v1/customer/conversations',{},csrf.value)).conversation_id
 snapshot.value=await channel<Snapshot>(`/v1/customer/conversations/${id}`)
 void watchChannel(`/v1/customer/conversations/${id}/events`,controller.signal,refresh,failed)
 }catch(e){failed(e)}finally{busy.value=false}}
async function send(){if(!text.value.trim()||!snapshot.value)return;busy.value=true;error.value='';try{await channel(`/v1/customer/conversations/${snapshot.value.conversation.id}/messages`,{text:text.value},csrf.value);text.value='';await refresh()}catch(e){failed(e)}finally{busy.value=false}}
async function handoff(){if(!snapshot.value)return;busy.value=true;try{await channel(`/v1/customer/conversations/${snapshot.value.conversation.id}/handoff`,{},csrf.value);await refresh()}catch(e){failed(e)}finally{busy.value=false}}
async function showSources(id:string){if(!snapshot.value)return;try{sources.value=(await channel<{citations:Citation[]}>(`/v1/customer/conversations/${snapshot.value.conversation.id}/citations/${id}`)).citations}catch(e){failed(e)}}
onMounted(()=>{void enter();poll=setInterval(()=>void refresh().catch(failed),5000)})
onUnmounted(()=>{controller.abort();clearInterval(poll)})
</script>
<template><main class="channel-shell"><header><h1>{{shop.name}}</h1><p>{{shop.welcome_message}}</p><strong role="status">{{stateText}}</strong></header>
<p v-if="error" role="alert">{{error}}</p><button v-if="!snapshot" @click="enter()" :disabled="busy">重新进入</button>
<section aria-label="咨询记录"><article v-for="m in snapshot?.messages" :key="m.id" :class="m.source"><strong>{{({customer:'你',assistant:'AI 客服',agent:'人工客服',system:'服务提示'})[m.source]}}</strong><p class="message-text">{{m.text}}</p><button v-if="m.citations.length" @click="showSources(m.id)">查看来源（{{m.citations.length}}）</button></article></section>
<aside v-if="sources.length" aria-label="回答来源"><button @click="sources=[]">收起来源</button><article v-for="s in sources" :key="s.evidence_id"><h3>[{{s.citation_index}}] {{s.title}}</h3><small>资料版本 {{s.version_id}}</small><p class="message-text">{{s.snippet}}</p></article></aside>
<form v-if="snapshot?.conversation.state!=='CLOSED' && snapshot" @submit.prevent="send"><label for="question">你的问题</label><textarea id="question" v-model="text" maxlength="2000" required :disabled="busy||!!snapshot.conversation.active_execution_id"/><button :disabled="busy||!text.trim()||!!snapshot.conversation.active_execution_id">发送</button><button type="button" :disabled="busy||snapshot.conversation.state!=='AI'" @click="handoff">联系人工客服</button></form>
<button v-if="snapshot?.conversation.state==='CLOSED'" @click="enter(true)">开始新咨询</button><small>匿名历史保存在当前浏览器会话中；丢失 Cookie 后无法找回。当前价格、库存、订单和配送承诺请联系人工。</small></main></template>
<style src="./channel.css"/>
