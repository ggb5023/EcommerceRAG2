import type { RouteRecordRaw } from 'vue-router'
import App from './App.vue'
import CustomerChat from './CustomerChat.vue'
import SupportDesk from './SupportDesk.vue'
import AdminApp from './admin/AdminApp.vue'

// One router for the three browser zones. The gateway stays the only origin the
// browser talks to; nothing here grants or checks permissions.
export const routes: RouteRecordRaw[] = [
  { path: '/shop/:slug', component: CustomerChat },
  { path: '/support', component: SupportDesk },
  { path: '/support/:id', component: SupportDesk },
  { path: '/admin', redirect: '/admin/overview' },
  { path: '/admin/:pathMatch(.*)*', component: AdminApp },
  { path: '/', redirect: '/chat' },
  { path: '/chat', name: 'chat', component: App },
  { path: '/chat/:conversation_id', name: 'conversation', component: App, props: true },
  { path: '/:pathMatch(.*)*', redirect: '/chat' },
]
