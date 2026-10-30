// Local-account login flow against a running local-profile stack.
// Requires LOGIN_USER with must_change_password=true (operator resets it first).
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { mkdir, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright'

const base = (process.env.WORKSPACE_URL ?? 'http://127.0.0.1:5173').replace(/\/$/, '')
const user = process.env.LOGIN_USER ?? 'owner'
const initial = process.env.LOGIN_PASSWORD
const next = process.env.LOGIN_NEW_PASSWORD ?? 'browser-final-pass-123456'
assert.ok(initial, 'LOGIN_PASSWORD is required')
assert.ok(['127.0.0.1', 'localhost'].includes(new URL(base).hostname), 'isolated loopback URL required')

const run = randomUUID()
const output = path.resolve(fileURLToPath(new URL(`../../.local/local-login/browser/${run}/`, import.meta.url)))
await mkdir(output, { recursive: true, mode: 0o700 })
const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH ?? '/root/.cache/ms-playwright/chromium-1187/chrome-linux/chrome', headless: true })
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
const errors = []
page.on('pageerror', error => errors.push(String(error)))
const checks = []
async function step(name, run) {
  try { await run(); checks.push({ name, status: 'PASS' }); console.log('PASS', name) }
  catch (error) { checks.push({ name, status: 'FAIL' }); throw error }
}
async function submitLogin(password) {
  await page.locator('input[autocomplete="username"]').fill(user)
  await page.locator('input[type="password"]').fill(password)
  await page.getByRole('button', { name: /登录/ }).click()
}

let finished = false
try {
  await step('anonymous-workspace-prompts-login', async () => {
    await page.goto(base + '/chat')
    // App.vue renders the anonymous state on /chat with an entry point to /login;
    // the form itself only exists on /login.
    await page.getByText('请登录客服工作台', { exact: true }).waitFor({ timeout: 15000 })
    await page.getByRole('button', { name: '登录', exact: true }).click()
    await page.waitForURL(/\/login/)
    assert.equal(new URL(page.url()).pathname, '/login')
    await page.locator('input[autocomplete="username"]').waitFor({ timeout: 15000 })
  })
  await step('wrong-password-shows-uniform-error', async () => {
    await submitLogin('definitely-wrong-password')
    await page.getByRole('alert').filter({ hasText: '账号或密码不正确' }).waitFor()
  })
  await step('forced-change-lands-on-password-page', async () => {
    await submitLogin(initial)
    await page.waitForURL(/\/password/)
    await page.getByRole('heading', { name: '修改密码' }).waitFor()
  })
  await step('password-change-returns-to-login', async () => {
    await page.locator('input[autocomplete="current-password"]').fill(initial)
    await page.locator('input[autocomplete="new-password"]').fill(next)
    await page.getByRole('button', { name: '修改密码' }).click()
    await page.waitForURL(/\/login/)
  })
  await step('new-password-enters-workspace', async () => {
    await submitLogin(next)
    await page.waitForURL(/\/chat/)
    await page.getByText('客服工作台', { exact: true }).waitFor({ timeout: 15000 })
  })
  await step('logout-clears-session', async () => {
    await page.getByRole('button', { name: '退出登录' }).click()
    // After logout the anonymous workspace state must be back.
    await page.getByText('请登录客服工作台', { exact: true }).waitFor({ timeout: 15000 })
  })
  await step('repeated-failures-lock-the-account', async () => {
    await page.goto(base + '/login')
    for (let attempt = 0; attempt < 5; attempt++) await submitLogin('wrong-' + attempt)
    await page.getByRole('alert').filter({ hasText: '锁定' }).waitFor()
  })
  assert.deepEqual(errors, [], 'page errors: ' + errors.join('; '))
  finished = true
} finally {
  await writeFile(path.join(output, 'report.json'), JSON.stringify({
    status: finished ? 'PASS' : 'FAIL',
    profile: 'local',
    real_service_acceptance: false,
    environment: { workspace: base },
    page_errors: errors,
    checks,
  }, null, 2), { mode: 0o600, flag: 'wx' })
  await browser.close()
}
console.log(`PASS local login browser: ${checks.length} checks; report=${output}/report.json`)
