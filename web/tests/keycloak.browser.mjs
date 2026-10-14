// Real local Keycloak pages and Go cookie/SSE routes; no response substitution.
import assert from 'node:assert/strict'
import { createHash, randomUUID } from 'node:crypto'
import { readFile, lstat, mkdir, writeFile } from 'node:fs/promises'
import { chromium } from 'playwright'

const accountsPath = process.env.KEYCLOAK_ACCOUNTS_FILE
assert.ok(accountsPath, 'protected synthetic accounts required')
const info = await lstat(accountsPath)
assert.equal(info.mode & 0o777, 0o600)
assert.ok(info.isFile() && !info.isSymbolicLink())
const accounts = JSON.parse(await readFile(accountsPath, 'utf8'))
const customer = 'http://127.0.0.1:5173'
const admin = 'http://127.0.0.1:5174'
const output = process.env.KEYCLOAK_BROWSER_REPORT_DIR ?? `/var/lib/ecommerce-rag/real-docs/reports/keycloak-browser-${randomUUID()}`
assert.ok(output.startsWith('/var/lib/ecommerce-rag/'))
await mkdir(output, { recursive: true, mode: 0o700 })
const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH ?? '/root/.cache/ms-playwright/chromium-1187/chrome-linux/chrome', headless: true })
const context = await browser.newContext()
const page = await context.newPage()
const checks = []
let finished = false
let turn, resultHash
const digest = value => createHash('sha256').update(JSON.stringify(value)).digest('hex')
async function test(name, run) {
  try { await run(); checks.push({ check_id: name, passed: true }); console.log(JSON.stringify({ check_id: name, passed: true })) }
  catch { checks.push({ check_id: name, passed: false }); throw new Error('bounded browser check failed') }
}
async function login(base, path) {
  await page.goto(base + path)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.waitForURL(url => url.origin === 'http://127.0.0.1:18095')
  await page.locator('#username').fill(accounts.username)
  await page.locator('#password').fill(accounts.password)
  await page.locator('#kc-login').click()
  await page.waitForURL(url => url.origin === base, { timeout: 20000 })
}
try {
  await test('customer-authorize-code-login-and-cookie', async () => {
    await login(customer, '/chat')
    await page.locator('#question').waitFor({ timeout: 20000 })
    const response = await page.request.get(customer + '/v1/session')
    assert.equal(response.status(), 200)
    assert.equal((await response.json()).userId, accounts.username === 'dev-user' ? accounts.username : 'dev-user')
    const cookies = await context.cookies()
    const session = cookies.find(c => c.name === 'ecr_v2_session')
    assert.ok(session && session.httpOnly && session.sameSite === 'Lax')
    assert.ok(!cookies.some(c => /access_token|refresh_token|id_token/.test(c.name)))
    assert.deepEqual(await page.evaluate(() => [localStorage.length, sessionStorage.length]), [0, 0])
  })
  await test('cookie-write-csrf-and-origin-rejection', async () => {
    assert.equal((await page.request.post(customer + '/v1/conversations', { data: { title: '' } })).status(), 403)
    const auth = await (await page.request.get(customer + '/v1/auth/session')).json()
    assert.equal((await page.request.post(customer + '/v1/conversations', { headers: { Origin: 'https://other.example', 'X-CSRF-Token': auth.csrf_token }, data: { title: '' } })).status(), 403)
    assert.equal((await page.request.get(customer + '/v1/session', { headers: { Authorization: 'Bearer synthetic' } })).status(), 401)
  })
  await test('actual-turn-persistence-sse-and-refresh-page', async () => {
    const sent = page.waitForResponse(r => /\/v1\/conversations\/[^/]+\/turns$/.test(new URL(r.url()).pathname) && r.request().method() === 'POST')
    await page.locator('#question').fill('保温杯容量和材质？')
    await page.locator('#question').press('Enter')
    const response = await sent
    assert.equal(response.status(), 202); turn = await response.json()
    for (let i = 0; i < 80; i++) {
      const result = await (await page.request.get(customer + '/v1/turns/' + turn.request_id)).json()
      if (['DONE', 'ASKING', 'FAILED', 'CANCELLED'].includes(result.status)) {
        assert.ok(['DONE', 'ASKING'].includes(result.status)); resultHash = digest(result); break
      }
      await page.waitForTimeout(250)
    }
    assert.ok(resultHash)
    const events = await page.request.get(customer + '/v1/turns/' + turn.request_id + '/events')
    assert.equal(events.status(), 200)
    const text = await events.text()
    const ids = [...text.matchAll(/^id: (\d+)$/gm)].map(m => Number(m[1]))
    assert.ok(ids.length > 0 && ids.every((id, index) => id === index + 1))
    await page.reload(); await page.locator('#question').waitFor()
    assert.equal(digest(await (await page.request.get(customer + '/v1/turns/' + turn.request_id)).json()), resultHash)
  })
  await test('admin-shares-go-session-and-loads-authorized-page', async () => {
    await page.goto(admin + '/admin/overview')
    await page.getByRole('button', { name: '退出登录', exact: true }).waitFor()
    assert.equal((await page.request.get(admin + '/admin/v1/session')).status(), 200)
    const shop = await page.request.get(admin + '/admin/v1/merchant/shops')
    assert.equal(shop.status(), 200)
  })
  if (process.env.KEYCLOAK_BROWSER_REFRESH === '1') {
    await test('actual-expired-access-token-refresh', async () => {
      console.log(JSON.stringify({ check_id: 'actual-expired-access-token-refresh', state: 'waiting_for_real_clock' }))
      await page.waitForTimeout(130000)
      assert.equal((await page.request.get(customer + '/v1/session')).status(), 200)
      assert.equal(digest(await (await page.request.get(customer + '/v1/turns/' + turn.request_id)).json()), resultHash)
    })
  }
  await test('admin-logout-clears-customer-cookie-and-old-results', async () => {
    await page.getByRole('button', { name: '退出登录', exact: true }).click()
    await page.getByRole('button', { name: '登录', exact: true }).waitFor()
    assert.equal((await page.request.get(customer + '/v1/turns/' + turn.request_id)).status(), 401)
    const auth = await (await page.request.get(customer + '/v1/auth/session')).json()
    assert.equal(auth.authenticated, false)
  })
  await test('admin-login-and-customer-business-recovery', async () => {
    await login(admin, '/admin/overview')
    await page.getByRole('button', { name: '退出登录', exact: true }).waitFor()
    await page.goto(customer + '/chat')
    await page.locator('#question').waitFor()
    assert.equal(digest(await (await page.request.get(customer + '/v1/turns/' + turn.request_id)).json()), resultHash)
  })
  finished = true
} catch {
  console.log(JSON.stringify({ status: 'NOT_PASSED', failure_code: 'browser_identity_check_failed' }))
  process.exitCode = 1
} finally {
  await browser.close()
  await writeFile(output + '/report.json', JSON.stringify({ report_version: 'keycloak-browser-v1', status: finished ? 'PASS' : 'NOT_PASSED', checks,
    provider_profile: 'v2_langchain_mock', turn_id: turn?.request_id ?? null, result_sha256: resultHash ?? null,
    reviewer_kind: 'agent', human_review_completed: false, real_service_acceptance: false }, null, 2), { mode: 0o600 })
  console.log(JSON.stringify({ report_directory: output, status: finished ? 'PASS' : 'NOT_PASSED' }))
}
