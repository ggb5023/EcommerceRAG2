// Real browser submit against the dev gateway through a temporary Vite server.
// The test proves the P1-5 form path: a successful validity write, a stale
// fencing conflict, and restoration of the version's original date window.
import assert from 'node:assert/strict'
import {execFileSync, spawn} from 'node:child_process'
import {openSync} from 'node:fs'
import {mkdir, writeFile} from 'node:fs/promises'
import path from 'node:path'
import {fileURLToPath} from 'node:url'
import {chromium} from 'playwright'

const root = path.resolve(fileURLToPath(new URL('../../', import.meta.url)))
const frontend = path.join(root, 'frontend')
const gateway = 'http://127.0.0.1:8080'
const workspace = (process.env.WORKSPACE_URL ?? 'http://127.0.0.1:5174').replace(/\/$/, '')
const output = path.join(root, '.local/redesign')
const screenshot = path.join(output, 'validity-submit.png')
const conflictScreenshot = path.join(output, 'validity-conflict.png')
const reportPath = path.join(output, 'validity-submit-report.json')
const viteLog = path.join(output, 'validity-vite.log')
await mkdir(output, {recursive: true, mode: 0o700})
assert.ok(['127.0.0.1', 'localhost'].includes(new URL(workspace).hostname), 'loopback Vite URL required')

const checks = []
const errors = []
let vite
let browser
let page
let mutated = false
let original
let restored
let versionId
let submittedDates
let successBody
let conflictStatus
const optional = value => value || null

function step(name, run) {
  return run().then(() => {
    checks.push({name, status: 'PASS'})
    console.log('PASS', name)
  }).catch(async error => {
    checks.push({name, status: 'FAIL', error: String(error)})
    await page?.screenshot({path: path.join(output, 'validity-failure.png'), fullPage: true}).catch(() => {})
    throw error
  })
}

function isoDate(offsetDays) {
  const date = new Date()
  date.setUTCDate(date.getUTCDate() + offsetDays)
  return date.toISOString().slice(0, 10)
}

async function waitForVite() {
  for (let attempt = 0; attempt < 60; attempt += 1) {
    try {
      const response = await fetch(workspace + '/')
      if (response.ok) return
    } catch { /* not listening yet */ }
    await new Promise(resolve => setTimeout(resolve, 500))
  }
  throw new Error(`temporary Vite did not start; see ${viteLog}`)
}

async function stopVite() {
  if (!vite) return true
  try { process.kill(-vite.pid, 'SIGTERM') } catch { /* already exited */ }
  for (let attempt = 0; attempt < 20; attempt += 1) {
    try {
      await fetch(workspace + '/', {signal: AbortSignal.timeout(500)})
    } catch {
      vite = null
      return true
    }
    await new Promise(resolve => setTimeout(resolve, 250))
  }
  return false
}

async function api(pathname, init = {}) {
  const response = await fetch(gateway + pathname, {...init, headers: {'Content-Type': 'application/json', ...(init.headers ?? {})}})
  const body = await response.json().catch(() => ({}))
  return {status: response.status, body}
}

async function detail() {
  const result = await api(`/admin/v1/merchant/reviews/${versionId}`)
  assert.equal(result.status, 200, `review detail ${result.status}`)
  return result.body
}

async function waitForDetail(predicate, label) {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    const current = await detail()
    if (predicate(current)) return current
    await new Promise(resolve => setTimeout(resolve, 250))
  }
  throw new Error(`timed out waiting for ${label}`)
}

function curlValidity(expectedFence, from, to, reason, key) {
  const payload = JSON.stringify({
    effective_from: from || '', effective_to: to || '', reason, confirm: true,
    expected_fencing_epoch: expectedFence, permission_revision: original.session.permission_revision,
  })
  const output = execFileSync('curl', [
    '-sS', '-w', '\n%{http_code}', '-X', 'POST',
    `${gateway}/admin/v1/merchant/versions/${versionId}/validity`,
    '-H', 'Content-Type: application/json', '-H', `Idempotency-Key: ${key}`,
    '--data-binary', payload,
  ], {encoding: 'utf8'})
  const split = output.lastIndexOf('\n')
  return {status: Number(output.slice(split + 1)), body: JSON.parse(output.slice(0, split))}
}

async function restoreOriginal() {
  if (!mutated || !original) return null
  const current = await detail()
  const same = optional(current.effective_from) === original.effective_from
    && optional(current.effective_to) === original.effective_to
  if (same) {
    restored = current
    return current
  }
  const result = curlValidity(current.fencing_epoch, original.effective_from ?? '', original.effective_to ?? '',
    'WP42 恢复版本原始有效期', `wp42-validity-restore-${Date.now()}`)
  assert.equal(result.status, 200, `restore status ${result.status}: ${JSON.stringify(result.body)}`)
  restored = await waitForDetail(item =>
    optional(item.effective_from) === original.effective_from
    && optional(item.effective_to) === original.effective_to, 'restored validity dates')
  return restored
}

async function writeReport(status, extra = {}) {
  const report = {
    status,
    checks,
    version_id: versionId,
    screenshot,
    conflict_screenshot: conflictScreenshot,
    original_dates: original ? {effective_from: original.effective_from, effective_to: original.effective_to} : null,
    submitted_dates: submittedDates ?? null,
    restored_dates: restored ? {effective_from: restored.effective_from, effective_to: restored.effective_to} : null,
    restored_match: original && restored
      ? optional(restored.effective_from) === original.effective_from
        && optional(restored.effective_to) === original.effective_to
      : false,
    success_status: successBody?.status ?? null,
    conflict_http_status: conflictStatus ?? null,
    page_errors: errors,
    vite_stopped: await stopVite(),
    ...extra,
  }
  await writeFile(reportPath, JSON.stringify(report, null, 2), {mode: 0o600})
  console.log(JSON.stringify(report))
  return report
}

try {
  await step('start-temporary-vite', async () => {
    try {
      await fetch(workspace + '/', {signal: AbortSignal.timeout(500)})
      throw new Error(`port 5174 is already serving; stop it first (workspace=${workspace})`)
    } catch (error) {
      if (error instanceof Error && error.message.includes('already serving')) throw error
    }
    const log = openSync(viteLog, 'a')
    vite = spawn('npm', ['run', 'dev', '--', '--port', '5174', '--strictPort', '--force'], {
      cwd: frontend,
      env: {...process.env, VITE_API_PROXY_TARGET: gateway},
      detached: true,
      stdio: ['ignore', log, log],
    })
    await waitForVite()
  })

  await step('dev-staff-session', async () => {
    const session = await api('/admin/v1/session')
    assert.equal(session.status, 200)
    assert.equal(session.body.user_id, 'dev-user')
    assert.equal(session.body.tenant_id, 'v2-demo-tenant')
    assert.ok(session.body.admin_roles.includes('knowledge_reviewer'), 'knowledge_reviewer role required')
    original = {session: session.body}
  })

  await step('select-editable-version', async () => {
    const list = await api('/admin/v1/merchant/reviews')
    assert.equal(list.status, 200)
    const candidate = list.body.items?.find(item => item.status === 'ready' && item.fencing_epoch >= 0)
    assert.ok(candidate, 'no ready version available')
    versionId = candidate.version_id
    const before = await detail()
    original.effective_from = optional(before.effective_from)
    original.effective_to = optional(before.effective_to)
    original.fencing_epoch = before.fencing_epoch
    original.source_hash = candidate.source_hash
    console.log(JSON.stringify({selected_version: versionId, original_dates: {
      effective_from: original.effective_from, effective_to: original.effective_to,
    }, fencing_epoch: original.fencing_epoch}))
  })

  browser = await chromium.launch({
    executablePath: process.env.CHROMIUM_PATH ?? '/root/.cache/ms-playwright/chromium-1187/chrome-linux/chrome',
    headless: true,
  })
  page = await browser.newPage({viewport: {width: 1440, height: 1000}})
  page.on('pageerror', error => errors.push(String(error)))
  page.on('dialog', dialog => dialog.accept())

  await step('open-review-detail-and-form', async () => {
    await page.goto(workspace + '/admin/merchant/reviews', {waitUntil: 'networkidle'})
    await page.getByRole('heading', {name: '版本审核', level: 1}).waitFor()
    const history = page.locator('details').filter({hasText: '历史版本'}).first()
    await history.locator('summary').click()
    await history.getByRole('button', {name: '加载历史版本'}).click()
    await history.locator('tbody tr').first().waitFor({timeout: 20000})
    const row = page.locator('.version-row').filter({hasText: original.source_hash}).first()
    assert.ok(await row.count(), `version ${versionId} row not found by source hash`)
    const detailResponse = page.waitForResponse(response =>
      response.request().method() === 'GET' && response.url().endsWith(`/admin/v1/merchant/reviews/${versionId}`))
    await row.click()
    await detailResponse
    await page.locator('[data-testid="validity-form"]').waitFor()
  })

  await step('browser-submit-success', async () => {
    submittedDates = {effective_from: isoDate(30), effective_to: isoDate(180)}
    await page.locator('[data-testid="validity-from"]').fill(submittedDates.effective_from)
    await page.locator('[data-testid="validity-to"]').fill(submittedDates.effective_to)
    await page.locator('[data-testid="validity-form"] textarea').fill('WP42 浏览器真实提交有效期验证')
    const responsePromise = page.waitForResponse(response =>
      response.request().method() === 'POST' && response.url().endsWith(`/admin/v1/merchant/versions/${versionId}/validity`))
    await page.locator('[data-testid="validity-submit"]').click()
    const response = await responsePromise
    assert.equal(response.status(), 200, `success status ${response.status()}`)
    mutated = true
    successBody = await response.json()
    assert.equal(successBody.command_status, 'applied')
    const updated = await waitForDetail(item =>
      item.fencing_epoch > original.fencing_epoch
      && optional(item.effective_from) === submittedDates.effective_from
      && optional(item.effective_to) === submittedDates.effective_to, 'browser-submitted dates')
    assert.equal(updated.effective_from, submittedDates.effective_from)
    assert.equal(updated.effective_to, submittedDates.effective_to)
    // loadReviewDetail remounts the form, so success is asserted on the
    // refreshed detail grid rather than on the transient form notice.
    await page.locator('.detail-grid').filter({hasText: `${submittedDates.effective_from} 至 ${submittedDates.effective_to}`}).waitFor({timeout: 20000})
    await page.locator('[data-testid="validity-form"]').waitFor({timeout: 20000})
    await page.screenshot({path: screenshot, fullPage: true})
  })

  await step('curl-advances-fencing', async () => {
    const current = await detail()
    const drift = {effective_from: isoDate(60), effective_to: isoDate(240)}
    const result = curlValidity(current.fencing_epoch, drift.effective_from, drift.effective_to,
      'WP42 浏览器冲突前推进 fencing epoch', `wp42-validity-advance-${Date.now()}`)
    assert.equal(result.status, 200, `advance status ${result.status}: ${JSON.stringify(result.body)}`)
    assert.equal(result.body.command_status, 'applied')
    const advanced = await waitForDetail(item => item.fencing_epoch > current.fencing_epoch, 'advanced fencing epoch')
    assert.equal(advanced.effective_from, drift.effective_from)
    assert.equal(advanced.effective_to, drift.effective_to)
  })

  await step('browser-stale-detail-gets-409', async () => {
    // The form was remounted by the success refresh, so fill its local reason
    // again while its detail prop still holds the pre-curl fencing epoch.
    await page.locator('[data-testid="validity-form"] textarea').fill('WP42 浏览器旧 fencing epoch 冲突验证')
    const responsePromise = page.waitForResponse(response =>
      response.request().method() === 'POST' && response.url().endsWith(`/admin/v1/merchant/versions/${versionId}/validity`))
    await page.locator('[data-testid="validity-submit"]').click()
    const response = await responsePromise
    conflictStatus = response.status()
    assert.equal(conflictStatus, 409, `stale submit status ${conflictStatus}`)
    const error = page.locator('[data-testid="validity-error"]')
    await error.waitFor({timeout: 10000})
    const text = await error.innerText()
    assert.ok(text.includes('fencing epoch 已变化'), `error text: ${text}`)
    await page.screenshot({path: conflictScreenshot, fullPage: true})
  })

  await step('restore-original-validity', async () => {
    restored = await restoreOriginal()
    assert.ok(restored, 'restore did not run')
    assert.equal(optional(restored.effective_from), original.effective_from)
    assert.equal(optional(restored.effective_to), original.effective_to)
  })

  await step('reload-shows-restored-dates', async () => {
    await page.reload({waitUntil: 'networkidle'})
    await page.getByRole('heading', {name: '版本审核', level: 1}).waitFor()
    const row = page.locator('.version-row').filter({hasText: original.source_hash}).first()
    assert.ok(await row.count(), `version ${versionId} row not found after reload`)
    await row.click()
    await page.locator('[data-testid="validity-form"]').waitFor()
    const current = await detail()
    assert.equal(optional(current.effective_from), original.effective_from)
    assert.equal(optional(current.effective_to), original.effective_to)
  })

  assert.deepEqual(errors, [], `page errors: ${errors.join('; ')}`)
  const report = await writeReport('PASS')
  assert.equal(report.vite_stopped, true, 'temporary Vite must stop before the test exits')
} catch (error) {
  let cleanupError
  if (mutated && original && versionId) {
    try {
      restored = await restoreOriginal()
      assert.equal(optional(restored.effective_from), original.effective_from)
      assert.equal(optional(restored.effective_to), original.effective_to)
    } catch (caught) {
      cleanupError = String(caught)
    }
  }
  await writeReport('NOT_PASSED', {error: String(error), cleanup_error: cleanupError}).catch(() => {})
  console.error(JSON.stringify({status: 'NOT_PASSED', error: String(error), cleanup_error: cleanupError, checks}))
  process.exitCode = 1
} finally {
  await browser?.close().catch(() => {})
  await stopVite()
}
