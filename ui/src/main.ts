import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import './style.css'
import { i18n } from './i18n'
import EnginesView from './views/EnginesView.vue'
import ProfilesView from './views/ProfilesView.vue'
import ProxiesView from './views/ProxiesView.vue'
import MacrosView from './views/MacrosView.vue'
import MacroEditor from './views/MacroEditor.vue'
import RunsView from './views/RunsView.vue'
import SettingsView from './views/SettingsView.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/profiles' },
    { path: '/profiles', component: ProfilesView },
    { path: '/proxies', component: ProxiesView },
    { path: '/macros', component: MacrosView },
    { path: '/macros/:id', component: MacroEditor },
    { path: '/runs', component: RunsView },
    { path: '/engines', component: EnginesView },
    { path: '/settings', component: SettingsView },
  ],
})
createApp(App).use(router).use(i18n).mount('#app')
import './bridge/gate'
