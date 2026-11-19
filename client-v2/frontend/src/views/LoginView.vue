<script setup lang="ts">
// v3 容器版登录页：账号密码走 /login（Go 侧常量时间比对），成功后整页刷新
// 让 WS 桥与 router 守卫在已鉴权状态下重启。桌面/mock 形态不会进到这里
// （router 守卫只在 webBridgeEnabled() 时启用鉴权）。
import { ref } from 'vue'
import { login } from '@/services/wsBridge'

const user = ref('')
const pass = ref('')
const busy = ref(false)
const error = ref('')

async function submit() {
  if (busy.value) return
  busy.value = true
  error.value = ''
  const r = await login(user.value.trim(), pass.value)
  busy.value = false
  if (r.ok) {
    window.location.hash = '#/discover'
    window.location.reload()
    return
  }
  error.value = r.error || '登录失败'
}
</script>

<template>
  <div class="login-wrap">
    <form class="login-card" @submit.prevent="submit">
      <h1 class="login-title">真果鉴</h1>
      <label class="login-field">
        <span class="login-label">账号</span>
        <input v-model="user" class="login-input" autocomplete="username" placeholder="账号" />
      </label>
      <label class="login-field">
        <span class="login-label">密码</span>
        <input v-model="pass" type="password" class="login-input" autocomplete="current-password" placeholder="密码" />
      </label>
      <p v-if="error" class="login-error">{{ error }}</p>
      <button class="login-btn" type="submit" :disabled="busy || !user || !pass">
        {{ busy ? '登录中…' : '登录' }}
      </button>
    </form>
  </div>
</template>

<style scoped>
.login-wrap {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
}
.login-card {
  width: 100%;
  max-width: 340px;
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 28px 26px;
  border-radius: 14px;
  background: var(--surface, #17181c);
  border: 1px solid var(--line, #2a2c33);
  box-shadow: 0 12px 40px rgba(0, 0, 0, 0.35);
}
.login-title {
  margin: 0;
  font-size: 22px;
  text-align: center;
  color: var(--text, #eceef2);
}
.login-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.login-label {
  font-size: 13px;
  color: var(--muted, #9aa0ab);
}
.login-input {
  font-size: 15px;
  padding: 10px 12px;
  border-radius: 9px;
  border: 1px solid var(--line, #2a2c33);
  background: var(--surface-2, #101115);
  color: var(--text, #eceef2);
  outline: none;
}
.login-input:focus {
  border-color: var(--accent, #f5c518);
}
.login-error {
  margin: 0;
  font-size: 13px;
  color: #ff6b6b;
}
.login-btn {
  font-size: 15px;
  padding: 11px;
  border: none;
  border-radius: 9px;
  background: var(--accent, #f5c518);
  color: #14151a;
  font-weight: 600;
  cursor: pointer;
}
.login-btn:disabled {
  opacity: 0.55;
  cursor: default;
}
</style>
