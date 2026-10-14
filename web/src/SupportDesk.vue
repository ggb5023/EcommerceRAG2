<script setup lang="ts">
import {onMounted,onUnmounted,ref} from 'vue'
import {useRoute,useRouter} from 'vue-router'
import {browserAuth,checkBrowserAuth,login,logout} from './auth'
import {channel,ChannelError,watchChannel,type Snapshot,type Conversation} from './customer-api'
const route=useRoute();const router=useRouter();const queue=ref<Conversation[]>([]);const snapshot=ref<Snapshot|null>(null);const text=ref('');const error=ref('');const busy=ref(false);const owned=ref(false)
let stream:AbortController|undefined; let poll:ReturnType<typeof setInterval>;let heartbeat:ReturnType<typeof setInterval>
function failed(e:unknown){error.value=e instanceof Error?e.message:'服务暂不可用。';if(e instanceof ChannelError&&[401,403].includes(e.status)){queue.value=[];snapshot.value=null;owned.value=false;stream?.abort();if(e.status===401)browserAuth.authenticated=false}}
async function refresh(){if(snapshot.value){snapshot.value=await channel<Snapshot>(`/v1/support/conversations/${snapshot.value.conversation.id}`);owned.value=!!snapshot.value.can_reply}}
async function load(){await checkBrowserAuth();if(browserAuth.enabled&&!browserAuth.authenticated){queue.value=[];snapshot.value=null;owned.value=false;stream?.abort();return;}queue.value=(await channel<{conversations:Conversation[]}>('/v1/support/queue')).conversations;await refresh()}
async function open(id:string){stream?.abort();owned.value=false;snapshot.value=await channel<Snapshot>(`/v1/support/conversations/${id}`);owned.value=!!snapshot.value.can_reply;await router.replace(`/support/${id}`);stream=new AbortController();void watchChannel(`/v1/support/conversations/${id}/events`,stream.signal,refresh,failed)}
async function act(action:string){if(!snapshot.value||busy.value)return;busy.value=true;error.value='';try{await channel(`/v1/support/conversations/${snapshot.value.conversation.id}/${action}`,{epoch:snapshot.value.conversation.epoch,...(action==='messages'?{text:text.value}:{})},browserAuth.csrf);if(action==='claim')owned.value=true;if(['release','close','resume-ai','requeue'].includes(action))owned.value=false;if(action==='messages')text.value='';await load()}catch(e){if(action==='heartbeat')owned.value=false;failed(e);await refresh().catch(failed)}finally{busy.value=false}}
async function exit(){owned.value=false;stream?.abort();snapshot.value=null;queue.value=[];try{await logout()}catch(e){failed(e)}}
onMounted(async()=>{try{await load();if(route.params.id)await open(String(route.params.id))}catch(e){failed(e)}poll=setInterval(()=>void load().catch(failed),5000);heartbeat=setInterval(()=>{if(owned.value)void act('heartbeat')},30000)})
onUnmounted(()=>{stream?.abort();clearInterval(poll);clearInterval(heartbeat)})
</script>
<template><main class="channel-shell"><header><h1>客服工作台</h1><nav><RouterLink to="/chat">员工 AI 助手</RouterLink><a href="/admin/overview">知识管理</a><button v-if="browserAuth.authenticated" @click="exit">退出</button></nav></header><p v-if="error" role="alert">{{error}}</p>
<button v-if="browserAuth.enabled&&!browserAuth.authenticated" @click="login('customer')">员工登录</button>
<div v-else class="support-columns"><aside><h2>待接管 / 我的会话</h2><button @click="load().catch(failed)">刷新队列</button><p v-if="!queue.length">暂无待接管会话。</p><button v-for="c in queue" :key="c.id" @click="open(c.id).catch(failed)">{{c.state==='WAITING_HUMAN'?'待领取':'已领取'}} · {{c.id.slice(-8)}}</button></aside>
<section v-if="snapshot"><h2>会话 {{snapshot.conversation.id.slice(-8)}}</h2><p role="status">{{snapshot.conversation.state}} · 控制版本 {{snapshot.conversation.epoch}}</p><button v-if="snapshot.conversation.state==='WAITING_HUMAN'" @click="act('claim')" :disabled="busy">领取会话</button>
<article v-for="m in snapshot.messages" :key="m.id"><strong>{{({customer:'客户',assistant:'AI 客服',agent:'人工客服',system:'服务提示'})[m.source]}}</strong><p class="message-text">{{m.text}}</p></article>
<form v-if="snapshot.conversation.state==='HUMAN'&&owned" @submit.prevent="act('messages')"><label>人工回复<textarea v-model="text" maxlength="4000" required/></label><button :disabled="busy||!text.trim()">发送人工回复</button><button type="button" @click="act('release')">释放到队列</button><button type="button" @click="act('resume-ai')">交回 AI</button><button type="button" @click="act('close')">结束会话</button></form><p v-else-if="snapshot.conversation.state==='HUMAN'">本页尚未取得有效领取，请重新领取或等待释放。</p>
</section></div></main></template><style src="./channel.css"/>
