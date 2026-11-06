import type { RouteRecordRaw } from 'vue-router'
import CustomerChat from './CustomerChat.vue'
import App from './App.vue'
import StaffWorkspace from './StaffWorkspace.vue'
import AssistantCatalog from './AssistantCatalog.vue'
import AdminApp from './admin/AdminApp.vue'
import LoginPage from './LoginPage.vue'
import PasswordPage from './PasswordPage.vue'

// One router for the three browser zones. The gateway stays the only origin the
// browser talks to; nothing here grants or checks permissions.
export const routes: RouteRecordRaw[] = [
  { path: '/shop/:slug', component: CustomerChat },
  { path: '/staff', component: StaffWorkspace },
  // One staff workspace: the queue and the AI-assisted reply live in tabs.
  { path: '/support', redirect: { path: '/staff', query: { tab: 'queue' } } },
  { path: '/support/:id', redirect: to => ({ path: '/staff', query: { tab: 'queue', id: String(to.params.id) } }) },
  { path: '/login', component: LoginPage },
  { path: '/password', component: PasswordPage },
  { path: '/admin', redirect: '/admin/overview' },
  { path: '/admin/:pathMatch(.*)*', component: AdminApp },
  { path: '/', redirect: '/staff' },
  { path: '/assistant', component: App },
  { path: '/assistant/catalog', component: AssistantCatalog },
  { path: '/chat', redirect: { path: '/assistant', query: {} } },
  { path: '/chat/:conversation_id', redirect: to => ({ path: '/assistant', query: { conversation: String(to.params.conversation_id) } }) },
  { path: '/:pathMatch(.*)*', redirect: '/staff' },
]
