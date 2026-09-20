import { createApp } from 'vue'
import { createPinia } from 'pinia'

import App from './App.vue'
import router from './router'
import { appSession } from './features/auth/browser'
import './styles.css'

// 页面重载后用刷新 Cookie 恢复登录；没有可恢复的会话属于正常状态。
void appSession.restore()

createApp(App).use(createPinia()).use(router).mount('#app')
