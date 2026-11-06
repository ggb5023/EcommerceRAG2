<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useWorkspace } from './useWorkspace'
import ConversationSidebar from './components/ConversationSidebar.vue'
import MessageTimeline from './components/MessageTimeline.vue'
import Composer from './components/Composer.vue'
import EvidencePanel from './components/EvidencePanel.vue'
import { browserAuth, checkBrowserAuth, login, logout } from './auth'
const { session, conversations, activeConversation, selectedTurn, selectedRequestId, evidence, citations, draft, input, turnStatus, clarification, connectionState, loading, unavailable, error, identityErrorStatus, busy, loadWorkspace, newConversation, selectConversation, selectRequest, send, stop, clearTurnState } = useWorkspace()
const route = useRoute(); const router = useRouter()
const width = ref(window.innerWidth)
const drawer = ref<'conversations' | 'evidence' | null>(null)
let returnFocus: HTMLElement | null = null
const mobile = computed(() => width.value < 720)
const narrow = computed(() => width.value < 1024)
const connectionLabel = computed(() => ({ idle: '未连接', connecting: '连接中', connected: '已连接', reconnecting: '重连中', disconnected: '已断开' }[connectionState.value]))
async function closeDrawer() { drawer.value = null; await nextTick(); returnFocus?.focus(); returnFocus = null }
async function openDrawer(value: 'conversations' | 'evidence') { returnFocus = document.activeElement as HTMLElement; drawer.value = value; await nextTick(); document.querySelector<HTMLElement>('.drawer-open button')?.focus() }
function resize() { width.value = window.innerWidth; if (drawer.value && (!narrow.value || drawer.value === 'conversations' && !mobile.value)) void closeDrawer() }
function keys(event: KeyboardEvent) {
  if (!drawer.value) return
  if (event.key === 'Escape') { event.preventDefault(); void closeDrawer() }
  if (event.key === 'Tab') {
    const items = Array.from(document.querySelectorAll<HTMLElement>('.drawer-open button:not(:disabled),.drawer-open [tabindex="0"]')).filter(e => e.getClientRects().length)
    const first = items[0]; const last = items.at(-1)
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
  }
}
async function reloadWorkspace() {
  loading.value = true
  try {
    await checkBrowserAuth()
    if (browserAuth.enabled && !browserAuth.authenticated) { clearTurnState(); session.value = null; conversations.value = []; unavailable.value = true; identityErrorStatus.value = 401; error.value = '请登录后访问工作台。'; return }
    if (browserAuth.mustChangePassword) { await router.replace('/password'); return }
    await loadWorkspace(typeof route.query.conversation === 'string' ? route.query.conversation : undefined)
    if (activeConversation.value && route.query.conversation !== activeConversation.value.id) await router.replace({ path: '/staff', query: { tab: 'assist', conversation: activeConversation.value.id } })
  } catch (caught) { clearTurnState(); session.value = null; conversations.value = []; unavailable.value = true; identityErrorStatus.value = (caught as { status?: number }).status ?? 503; error.value = caught instanceof Error ? caught.message : '身份服务不可用。' }
  finally { loading.value = false }
}
async function signout() {
  clearTurnState(); session.value = null; conversations.value = []; activeConversation.value = null; input.value = ''
  unavailable.value = true; identityErrorStatus.value = 401; error.value = '已退出，请重新登录。'
  try { await logout() } catch (caught) { error.value = caught instanceof Error ? caught.message : '注销未确认。' }
}
async function createConversation() { await closeDrawer(); await newConversation(); if (activeConversation.value) await router.push({ path: '/staff', query: { tab: 'assist', conversation: activeConversation.value.id } }) }
async function openConversation(item: Parameters<typeof selectConversation>[0]) { await closeDrawer(); await selectConversation(item); if (activeConversation.value) await router.push({ path: '/staff', query: { tab: 'assist', conversation: activeConversation.value.id } }) }
watch(() => route.query.conversation, async id => { if (activeConversation.value && id === activeConversation.value.id) return; await reloadWorkspace() }, { immediate: true })
onMounted(() => { window.addEventListener('resize', resize); document.addEventListener('keydown', keys) })
onUnmounted(() => { clearTurnState(); window.removeEventListener('resize', resize); document.removeEventListener('keydown', keys) })
</script>
<template>
<div class="shell zone-web"><header class="topbar" :inert="drawer !== null"><div class="brand"><span class="brand-mark">ER</span><div><strong>客服工作台</strong><small>可追溯的回答与证据</small></div></div><div class="top-meta" v-if="session"><span class="status-dot" :class="{ live: connectionState === 'connected' }"></span>{{ connectionLabel }} · {{ session.shopName }}<span class="user">{{ session.displayName }}</span><button v-if="browserAuth.enabled" class="reference-btn" @click="signout">退出登录</button></div></header>
<div v-if="loading" class="workspace-state panel" role="status"><strong>正在连接工作台…</strong><span>正在读取会话和授权范围</span></div><div v-else-if="unavailable" class="workspace-state panel" role="alert"><strong>{{ identityErrorStatus === 401 ? '请登录客服工作台' : identityErrorStatus === 403 ? '当前账号无访问权限' : '工作台暂不可用' }}</strong><span>{{ error }}</span><button v-if="browserAuth.enabled && identityErrorStatus === 401" class="send-btn" type="button" @click="login('customer')">登录</button><button v-else class="send-btn" type="button" @click="reloadWorkspace">重试</button></div>
<div v-else class="workspace"><div id="conversations-pane" v-show="!mobile || drawer === 'conversations'" class="pane" :class="{ 'drawer-open': mobile && drawer === 'conversations' }" :role="mobile ? 'dialog' : undefined" :aria-modal="mobile ? true : undefined" aria-label="会话列表" :inert="drawer === 'evidence'"><button v-if="mobile" class="drawer-close" @click="closeDrawer">关闭会话</button><ConversationSidebar :items="conversations" :selected-id="activeConversation?.id" :shop-name="session?.shopName" :role="session?.role" @new="createConversation" @select="openConversation" /></div>
<main class="chat panel" :inert="drawer !== null"><nav v-if="narrow" class="toolbar" aria-label="工作台面板"><button v-if="mobile" class="toolbar-btn" aria-controls="conversations-pane" :aria-expanded="drawer === 'conversations'" @click="openDrawer('conversations')">会话</button><button class="toolbar-btn" aria-controls="evidence-pane" :aria-expanded="drawer === 'evidence'" @click="openDrawer('evidence')">证据与引用 ({{ evidence.length }})</button></nav><div class="chat-heading"><div><p class="kicker">ASSISTED REPLY</p><h1>{{ activeConversation?.title || '新会话' }}</h1></div><span class="env-badge">{{ session?.profile === 'v2_langchain_bailian' ? '百炼' : 'V2 MOCK' }}</span></div><p class="turn-state" role="status">{{ turnStatus || '等待问题' }} · {{ connectionLabel }}</p><div v-if="clarification" class="clarification-card" role="status"><strong>需要补充信息</strong><span>{{ clarification }}</span></div><MessageTimeline :messages="activeConversation?.messages ?? []" :busy="busy" :turn-status="turnStatus" :draft="draft" :selected-request-id="selectedRequestId" @select="selectRequest" /><div v-if="error" class="error-banner" role="alert">{{ error }} <button type="button" class="reference-btn" @click="reloadWorkspace">重新读取服务端状态</button></div><Composer v-model="input" :busy="busy" @send="send" @stop="stop" /></main>
<div id="evidence-pane" v-show="!narrow || drawer === 'evidence'" class="pane" :class="{ 'drawer-open': narrow && drawer === 'evidence' }" :role="narrow ? 'dialog' : undefined" :aria-modal="narrow ? true : undefined" aria-label="证据与引用" :inert="drawer === 'conversations'"><button v-if="narrow" class="drawer-close" @click="closeDrawer">关闭证据</button><EvidencePanel :evidence="evidence" :draft="draft" :citations="citations" :turn="selectedTurn" /></div></div><button v-if="drawer" class="backdrop" tabindex="-1" aria-label="关闭面板" @click="closeDrawer"></button></div>
</template>
