import './styles/base.css'
import './styles/tokens.css'
import './styles/workbench.css'
import './admin/admin.css'
import { createApp, h } from 'vue'
import { createRouter, createWebHistory, RouterView } from 'vue-router'
import { routes } from './routes'
import { applyThemeChoice, loadThemeChoice } from './admin/theme'

applyThemeChoice(loadThemeChoice())
const router = createRouter({ history: createWebHistory(), routes })

createApp({ render: () => h(RouterView) }).use(router).mount('#app')
