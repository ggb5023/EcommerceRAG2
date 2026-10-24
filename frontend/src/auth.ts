import { reactive } from 'vue'

export const browserAuth = reactive({ enabled: false, authenticated: false, csrf: '' })

export function csrfHeaders(): Record<string, string> {
  return browserAuth.enabled && browserAuth.csrf ? { 'X-CSRF-Token': browserAuth.csrf } : {}
}

export async function checkBrowserAuth(): Promise<void> {
  const response = await fetch('/v1/auth/session', { credentials: 'same-origin' })
  if (!response.ok) throw Object.assign(new Error(response.status === 403 ? '当前账号的权限已变化或被撤销。' : '身份服务暂不可用，请稍后重试。'), { status: response.status })
  const state = await response.json() as { enabled: boolean; authenticated: boolean; csrf_token?: string }
  browserAuth.enabled = state.enabled === true
  browserAuth.authenticated = state.authenticated === true
  browserAuth.csrf = state.csrf_token ?? ''
}

export function login(app: 'customer' | 'admin'): void {
  window.location.assign(`/v1/auth/login?${new URLSearchParams({ app, return_to: window.location.pathname })}`)
}

export async function logout(): Promise<void> {
  try {
    const response = await fetch('/v1/auth/logout', { method: 'POST', credentials: 'same-origin', headers: csrfHeaders() })
    if (!response.ok) throw Object.assign(new Error('本地会话已退出，身份服务注销未确认。'), { status: response.status })
  } finally {
    browserAuth.authenticated = false
    browserAuth.csrf = ''
  }
}
