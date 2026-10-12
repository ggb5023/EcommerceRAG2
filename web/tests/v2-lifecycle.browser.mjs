// Actual UI mutations against an isolated V2 mock stack; no response fixtures.
import assert from 'node:assert/strict'
import { createHash, randomUUID } from 'node:crypto'
import { mkdir, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright'

const workspace = process.env.WORKSPACE_URL ?? 'http://127.0.0.1:5173'
const administration = process.env.ADMIN_WEB_BASE_URL ?? 'http://127.0.0.1:5174'
for (const url of [workspace, administration]) {
  assert.ok(['127.0.0.1', 'localhost'].includes(new URL(url).hostname), 'isolated loopback URL required')
}
const run = randomUUID().replaceAll('-', '').slice(0, 12)
const documentId = `browser-policy-${run}`
const output = path.resolve(fileURLToPath(new URL(`../../.local/v2-lifecycle/browser/${run}/`, import.meta.url)))
await mkdir(output, { recursive: true, mode: 0o700 })
const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH, headless: true })
const admin = await browser.newPage({ viewport: { width: 1440, height: 900 } })
const customer = await browser.newPage({ viewport: { width: 1440, height: 900 } })
const checks = []
const inputs = []
let mutations = 0
let finished = false

async function step(name, fn) {
  try {
    await fn()
    checks.push({ name, status: 'PASS' })
    console.log(`PASS ${name}`)
  } catch (error) {
    checks.push({ name, status: 'FAIL' })
    await admin.screenshot({ path: path.join(output, `${name}-admin.png`) }).catch(() => {})
    await customer.screenshot({ path: path.join(output, `${name}-customer.png`) }).catch(() => {})
    throw error
  }
}

async function upload(days) {
  const content = Buffer.from(`# ${documentId}\n\n编号 ${documentId} 的标准配送时效为 ${days} 个工作日。\n`)
  const hash = createHash('sha256').update(content).digest('hex')
  const manifest = `schema_version: 1\nsource:\n  id: ${documentId}\n  name: Browser controlled policy\npipeline_version: v2-browser-lifecycle\ndocuments:\n  - document_id: ${documentId}\n    title: ${documentId}\n    path: policy.md\n    format: markdown\n    sha256: ${hash}\n    disclosure_class: external_allowed\n`
  await admin.goto(administration + '/admin/merchant/sources')
  await admin.locator('.import-form select').selectOption('shop-demo')
  await admin.locator('.import-form input[type=file]').nth(0).setInputFiles({ name: 'manifest.yaml', mimeType: 'application/yaml', buffer: Buffer.from(manifest) })
  await admin.locator('.import-form input[type=file]').nth(1).setInputFiles({ name: 'policy.md', mimeType: 'text/markdown', buffer: content })
  await admin.locator('.import-form textarea').fill(manifest)
  const submitted = admin.waitForResponse(r => r.url().endsWith('/admin/v1/merchant/sources/import') && r.request().method() === 'POST')
  await admin.getByRole('button', { name: '校验并排队', exact: true }).click()
  const response = await submitted
  assert.equal(response.status(), 202)
  mutations++
  const job = await response.json()
  for (let attempt = 0; attempt < 100; attempt++) {
    const status = await admin.request.get(administration + '/admin/v1/merchant/ingestion/' + job.job_id)
    assert.equal(status.status(), 200)
    const current = await status.json()
    if (current.status === 'awaiting_review') {
      const item = current.items.find(item => item.document_id === documentId)
      assert.ok(item?.version_id, 'worker did not create a reviewable version')
      inputs.push({ document_id: documentId, source_sha256: hash, job_id: job.job_id, version_id: item.version_id, format: 'markdown' })
      return { hash, version: item.version_id }
    }
    assert.ok(['pending', 'running'].includes(current.status), 'ingestion did not reach review')
    await new Promise(resolve => setTimeout(resolve, 200))
  }
  throw new Error('worker did not finish within the bounded acceptance window')
}

async function operate(input, operation) {
  await admin.goto(administration + '/admin/merchant/reviews')
  const row = admin.locator('.version-row').filter({ hasText: input.hash })
  await row.waitFor()
  const loaded = admin.waitForResponse(r => new URL(r.url()).pathname.endsWith('/admin/v1/merchant/reviews/' + input.version))
  await row.click()
  assert.equal((await loaded).status(), 200)
  const labels = { review: '记录审核结论', publish: '发布此版本', rollback: '回退到此版本', revoke: '撤销版本' }
  await admin.getByRole('button', { name: labels[operation], exact: true }).click()
  const dialog = admin.getByRole('dialog', { name: operation === 'review' ? '确认资料审核' : '确认版本操作', exact: true })
  await dialog.waitFor()
  assert.ok((await dialog.innerText()).includes(input.hash), 'confirmation lost source/version binding')
  const confirm = dialog.getByRole('button', { name: '确认并记录审计', exact: true })
  assert.equal(await confirm.isDisabled(), true)
  await dialog.locator('textarea').fill(`Controlled isolated V2 browser ${operation} acceptance.`)
  await dialog.getByRole('checkbox').check()
  const completed = admin.waitForResponse(r => new URL(r.url()).pathname.endsWith(`/admin/v1/merchant/versions/${input.version}/${operation}`) && r.request().method() === 'POST')
  await confirm.click()
  const response = await completed
  assert.equal(response.status(), 200, `actual ${operation} command failed`)
  mutations++
  await dialog.waitFor({ state: 'hidden' })
}

async function query(expectedVersion) {
  await customer.goto(workspace + '/chat')
  await customer.locator('#question').waitFor()
  const created = customer.waitForResponse(r => r.url().endsWith('/v1/conversations') && r.request().method() === 'POST')
  await customer.getByRole('button', { name: '＋ 新建会话', exact: true }).click()
  assert.equal((await created).status(), 201)
  const sent = customer.waitForResponse(r => /\/v1\/conversations\/[^/]+\/turns$/.test(new URL(r.url()).pathname) && r.request().method() === 'POST')
  await customer.locator('#question').fill(`编号 ${documentId} 的配送时效？`)
  await customer.locator('#question').press('Enter')
  const response = await sent
  assert.equal(response.status(), 202)
  const turn = await response.json()
  // The persisted result binding can render with the initial EXECUTING
  // response before the worker has written the terminal row. Wait for the
  // composer to become available, then read the authoritative GET result.
  await customer.getByRole('button', { name: '发送 ↗', exact: true }).waitFor({ state: 'visible', timeout: 15000 })
  await customer.waitForFunction(id => document.querySelector('.result-binding')?.textContent.includes(id), turn.request_id)
  let result
  for (let attempt = 0; attempt < 75; attempt++) {
    const persisted = await customer.request.get(workspace + '/v1/turns/' + turn.request_id)
    assert.equal(persisted.status(), 200)
    result = await persisted.json()
    if (!['PENDING', 'EXECUTING', 'CANCEL_REQUESTED'].includes(result.status)) break
    await new Promise(resolve => setTimeout(resolve, 100))
  }
  assert.equal(result.status, 'DONE')
  assert.equal(result.customer_reply.can_copy, false)
  const bound = result.evidence.filter(item => item.documentId === documentId)
  if (expectedVersion) {
    assert.ok(bound.length > 0, 'actual published policy was not recalled')
    assert.ok(bound.every(item => item.versionId === expectedVersion), 'evidence contains an inactive policy version')
    assert.ok((await customer.locator('.evidence-card footer').allTextContents()).some(text => text.includes(documentId)), 'browser lost the actual policy citation')
  } else {
    assert.equal(bound.length, 0, 'pending/revoked policy remained retrievable')
  }
}

try {
  const response = await customer.request.get(workspace + '/v1/session')
  assert.equal(response.status(), 200)
  assert.equal((await response.json()).profile, 'v2_langchain_mock', 'browser mutations require the explicit offline development profile')
  let first, second
  await step('upload-first-version', async () => { first = await upload('2-5') })
  await step('pending-version-is-not-retrievable', () => query(null))
  await step('review-first-version', () => operate(first, 'review'))
  await step('publish-first-version', () => operate(first, 'publish'))
  await step('customer-cites-first-version', () => query(first.version))
  await step('upload-second-version', async () => { second = await upload('1-3') })
  await step('review-second-version', () => operate(second, 'review'))
  await step('publish-second-version', () => operate(second, 'publish'))
  await step('customer-cites-only-second-version', () => query(second.version))
  await step('rollback-to-first-version', () => operate(first, 'rollback'))
  await step('customer-cites-rolled-back-version', () => query(first.version))
  await step('revoke-first-version', () => operate(first, 'revoke'))
  await step('revoked-policy-is-not-retrievable', () => query(null))
  finished = true
} finally {
  await writeFile(path.join(output, 'report.json'), JSON.stringify({ status: finished ? 'PASS' : 'FAIL', profile: 'v2_langchain_mock', real_service_acceptance: false, inputs, mutations_submitted: mutations, checks }, null, 2), { mode: 0o600 })
  await browser.close()
}
console.log(`PASS V2 browser ingestion/publication/rollback/revocation: ${checks.length} checks; report=${output}/report.json`)
