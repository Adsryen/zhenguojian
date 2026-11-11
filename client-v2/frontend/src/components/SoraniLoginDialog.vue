<script setup lang="ts">
/* SoraniLoginDialog —— 青空账号登录对话框（v1 SoraniAccountDialog 三页栈 → v-show 三态：
   状态说明 → 滑块 → 账密）。差异说明见返报：失败一律红字 + 自动重取验证码回滑块页，
   且保留已填账密（v1 仅在消息含「安全验证/验证码」时回滑块）。
   Esc / 遮罩点击 = 关闭；busy 期间所有动作按钮禁用。 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import SliderCaptcha from './SliderCaptcha.vue'
import { api } from '@/services/api'
import { t, tf } from '@/services/i18n'
import type { SoraniCaptcha, SoraniStatus, SoraniTrack } from '@/services/types'

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'status', s: SoraniStatus | null): void
  (e: 'logged-in', s: SoraniStatus): void
}>()

type Page = 'status' | 'captcha' | 'creds'
const page = ref<Page>('status')
const busy = ref(false)
const error = ref('')
const status = ref<SoraniStatus | null>(null)
const captcha = ref<SoraniCaptcha | null>(null)
const track = ref<SoraniTrack | null>(null)
const identifier = ref('')
const password = ref('')

const statusText = computed(() => {
  if (!status.value) return t('sorani.unknown')
  if (!status.value.loggedIn) return t('sorani.loggedOut')
  return status.value.name ? `${t('sorani.loggedIn')} · ${status.value.name}` : t('sorani.loggedIn')
})

function msg(e: unknown): string {
  return (e as any)?.message ?? String(e)
}

async function refreshStatus() {
  try {
    status.value = await api.soraniStatus()
    emit('status', status.value)
  } catch (e) {
    status.value = null // 读不到 → 「未知」（v1 对齐：不谎报未登录）
    error.value = tf('sorani.statusFail', { msg: msg(e) })
  }
}

/** 进滑块页并重取验证码（首次/刷新/失败重试共用路径） */
async function beginCaptcha() {
  error.value = ''
  page.value = 'captcha'
  captcha.value = null
  track.value = null
  busy.value = true
  try {
    captcha.value = await api.soraniCaptcha()
  } catch (e) {
    error.value = tf('sorani.captchaFail', { msg: msg(e) })
    page.value = 'status'
  } finally {
    busy.value = false
  }
}

function onSolved(tr: SoraniTrack) {
  track.value = tr
  error.value = ''
  page.value = 'creds'
}

async function submit() {
  if (busy.value) return
  error.value = ''
  if (!captcha.value || !track.value) {
    error.value = t('sorani.needTrack')
    return
  }
  if (!identifier.value.trim() || !password.value) {
    error.value = t('sorani.needCreds')
    return
  }
  busy.value = true
  try {
    const s = await api.soraniLogin(identifier.value.trim(), password.value, captcha.value.id, track.value)
    status.value = s
    emit('status', s)
    emit('logged-in', s)
    emit('close')
  } catch (e) {
    const failText = tf('sorani.loginFail', { msg: msg(e) })
    await beginCaptcha() // 失败一律重取验证码回滑块（账密输入保留：refs 不清）
    if (captcha.value) error.value = failText
  } finally {
    busy.value = false
  }
}

async function onLogout() {
  if (busy.value) return
  busy.value = true
  try {
    const s = await api.soraniLogout()
    status.value = s
    emit('status', s)
    error.value = ''
  } catch (e) {
    error.value = tf('sorani.logoutFail', { msg: msg(e) })
  } finally {
    busy.value = false
  }
}

function close() {
  emit('close')
}

function onKey(ev: KeyboardEvent) {
  if (ev.key === 'Escape') close()
}

onMounted(() => {
  window.addEventListener('keydown', onKey)
  refreshStatus()
})
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <div class="dlg-mask" data-testid="sorani-mask" @click.self="close">
    <div class="dlg" role="dialog" aria-modal="true" data-testid="sorani-dialog">
      <div class="dlg-head">
        <span class="dlg-title">{{ t('sorani.dialogTitle') }}</span>
        <button class="dlg-x" type="button" data-testid="sorani-close-btn" @click="close">✕</button>
      </div>

      <div v-if="error" class="dlg-error" data-testid="sorani-error">{{ error }}</div>

      <!-- 态① 状态说明（v1 主菜单页） -->
      <div v-show="page === 'status'" class="dlg-body" data-testid="sorani-page-status">
        <div class="dlg-kv">
          <span class="dlg-k">{{ t('sorani.rowName') }}</span>
          <span class="dlg-v" data-testid="sorani-dialog-status">{{ statusText }}</span>
        </div>
        <p class="dlg-hint">{{ t('sorani.intro') }}</p>
        <div class="dlg-actions">
          <button class="btn" type="button" data-testid="sorani-start-btn" :disabled="busy" @click="beginCaptcha">
            {{ t('sorani.login') }}
          </button>
          <button
            v-if="status && status.loggedIn"
            class="btn btn-ghost"
            type="button"
            data-testid="sorani-dialog-logout"
            :disabled="busy"
            @click="onLogout"
          >
            {{ busy ? t('sorani.busy') : t('sorani.logout') }}
          </button>
        </div>
      </div>

      <!-- 态② 滑块 -->
      <div v-show="page === 'captcha'" class="dlg-body" data-testid="sorani-page-captcha">
        <p class="dlg-hint">{{ t('sorani.captchaHint') }}</p>
        <SliderCaptcha :captcha="captcha" :disabled="busy" @solved="onSolved" @refresh="beginCaptcha" />
        <div class="dlg-actions">
          <button class="btn btn-ghost" type="button" data-testid="sorani-back-btn" @click="page = 'status'">
            {{ t('sorani.back') }}
          </button>
        </div>
      </div>

      <!-- 态③ 账密 -->
      <div v-show="page === 'creds'" class="dlg-body" data-testid="sorani-page-creds">
        <label class="fld">
          <span class="fld-k">{{ t('sorani.identifier') }}</span>
          <input v-model="identifier" class="fld-i" type="text" data-testid="sorani-identifier" :placeholder="t('sorani.identifierPh')" />
        </label>
        <label class="fld">
          <span class="fld-k">{{ t('sorani.password') }}</span>
          <input v-model="password" class="fld-i" type="password" data-testid="sorani-password" :placeholder="t('sorani.passwordPh')" autocomplete="off" />
        </label>
        <div class="dlg-actions">
          <button class="btn" type="button" data-testid="sorani-submit-btn" :disabled="busy" @click="submit">
            {{ busy ? t('sorani.busy') : t('sorani.submit') }}
          </button>
          <button class="btn btn-ghost" type="button" data-testid="sorani-reverify-btn" :disabled="busy" @click="beginCaptcha">
            {{ t('sorani.reverify') }}
          </button>
          <button class="btn btn-ghost" type="button" data-testid="sorani-creds-back" :disabled="busy" @click="page = 'captcha'">
            {{ t('sorani.back') }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.dlg-mask {
  position: fixed;
  inset: 0;
  z-index: 1000;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(8, 10, 16, 0.62);
}
.dlg {
  width: 360px;
  max-width: calc(100vw - 48px);
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 16px 18px 18px;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--surface);
  box-shadow: 0 18px 48px rgba(0, 0, 0, 0.45);
}
.dlg-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.dlg-title {
  font-size: 15px;
  font-weight: 600;
  color: var(--text);
}
.dlg-x {
  border: none;
  background: transparent;
  color: var(--muted);
  font-size: 14px;
  cursor: pointer;
  padding: 2px 6px;
  border-radius: 6px;
}
.dlg-x:hover {
  background: var(--surface-3);
  color: var(--text);
}
.dlg-error {
  color: var(--danger);
  font-size: 13px;
  line-height: 1.5;
  word-break: break-all;
}
.dlg-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.dlg-kv {
  display: flex;
  gap: 12px;
  font-size: 13px;
}
.dlg-k {
  color: var(--muted);
}
.dlg-v {
  color: var(--text);
}
.dlg-hint {
  margin: 0;
  font-size: 12.5px;
  line-height: 1.6;
  color: var(--muted);
}
.dlg-actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
.fld {
  display: flex;
  align-items: center;
  gap: 10px;
}
.fld-k {
  flex-shrink: 0;
  width: 44px;
  font-size: 13px;
  color: var(--muted);
}
.fld-i {
  flex: 1;
  min-width: 0;
  padding: 6px 10px;
  font-size: 13px;
  color: var(--text);
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: 6px;
}
.fld-i:focus {
  outline: none;
  border-color: var(--accent);
}
</style>
