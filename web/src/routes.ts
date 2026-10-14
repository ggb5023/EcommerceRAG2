import type { RouteRecordRaw } from 'vue-router'
import App from './App.vue'
import CustomerChat from './CustomerChat.vue'
import SupportDesk from './SupportDesk.vue'

export const routes: RouteRecordRaw[] = [
  { path: '/shop/:slug', component: CustomerChat },
  { path: '/support', component: SupportDesk },
  { path: '/support/:id', component: SupportDesk },
  { path: '/', redirect: '/chat' },
  { path: '/chat', name: 'chat', component: App },
  { path: '/chat/:conversation_id', name: 'conversation', component: App, props: true },
  { path: '/:pathMatch(.*)*', redirect: '/chat' },
]
