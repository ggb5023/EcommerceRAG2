// Actual isolated authority changes; no HTTP response substitution.
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { mkdir, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright'

const root = fileURLToPath(new URL('../../', import.meta.url))
const base = process.env.WORKSPACE_URL ?? 'http://127.0.0.1:5173'
const config = process.env.V2_AUTHORITY_FIXTURE_ENV
assert.ok(config, 'explicit protected isolated migration environment required')
assert.ok(['127.0.0.1', 'localhost'].includes(new URL(base).hostname))
const output = path.join(root, '.local/v2-authority/browser', randomUUID())
await mkdir(output, { recursive: true, mode: 0o700 })
// This fixture writes only app_user authority, never answers, events or chunks.
// Python reads the DSN from its protected file; secrets never enter argv/stdout.
const fixture = `
import json,sys
from pathlib import Path
sys.path.insert(0, str(Path.cwd() / 'scripts' / 'dev'))
from v2_services import load_values
import psycopg
try:
    values=load_values(Path(sys.argv[1]))
    with psycopg.connect(values['V2_MIGRATION_DSN']) as conn:
        db,superuser,bypass=conn.execute('SELECT current_database(),rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user').fetchone()
        assert db.startswith('ecr_v2_test_') and not superuser and not bypass
        uid,role,revoked=conn.execute("SELECT u.id,u.role,u.revoked_at FROM app_user u JOIN tenant t ON t.id=u.tenant_id WHERE t.external_id='v2-demo-tenant' AND u.external_id='dev-user'").fetchone()
        action=sys.argv[2]
        if action=='inspect':
            assert revoked is None
        elif action=='revoke':
            conn.execute('UPDATE app_user SET revoked_at=now() WHERE id=%s',(uid,))
        elif action=='revision':
            conn.execute("UPDATE app_user SET permission_revision=ecr_new_permission_revision(tenant_id,'browser_acceptance') WHERE id=%s",(uid,))
        elif action=='restore':
            conn.execute('UPDATE app_user SET revoked_at=NULL WHERE id=%s',(uid,))
        else:
            raise ValueError()
        print(json.dumps({'database':db,'role':role,'superuser':superuser,'bypass_rls':bypass}))
except Exception:
    raise SystemExit('FAIL isolated authority fixture') from None
`
function authority(action) {
  try {
    return JSON.parse(execFileSync(path.join(root, 'python/.venv/bin/python'), ['-c', fixture, config, action], { cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }))
  } catch {
    throw new Error('isolated authority fixture failed; private diagnostics suppressed')
  }
}
const environment = authority('inspect')
const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH, headless: true })
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
const checks = []
let finished = false
async function test(name, run) {
  try { await run(); checks.push({ name, status: 'PASS' }) }
  catch (error) { checks.push({ name, status: 'FAIL' }); throw error }
}
async function create() {
  await page.goto(base + '/chat')
  await page.locator('#question').waitFor()
  const created = page.waitForResponse(r => r.url().endsWith('/v1/conversations') && r.request().method() === 'POST')
  await page.getByRole('button', { name: '＋ 新建会话', exact: true }).click()
  assert.equal((await created).status(), 201)
  const sent = page.waitForResponse(r => /\/v1\/conversations\/[^/]+\/turns$/.test(new URL(r.url()).pathname) && r.request().method() === 'POST')
  await page.locator('#question').fill('SKU-CUP-480 保温杯容量？')
  await page.locator('#question').press('Enter')
  const response = await sent
  assert.equal(response.status(), 202)
  const turn = await response.json()
  await page.waitForFunction(id => document.querySelector('.result-binding')?.textContent.includes(id), turn.request_id)
  await page.getByRole('button', { name: '发送 ↗', exact: true }).waitFor()
  const result = await page.request.get(base + '/v1/turns/' + turn.request_id)
  assert.equal(result.status(), 200)
  assert.equal((await result.json()).status, 'DONE')
  assert.ok(await page.locator('.evidence-card').count() > 0)
  return turn
}
async function cleared() {
  await page.getByRole('button', { name: '查看本轮证据', exact: true }).last().click()
  await page.getByText('当前账号没有访问权限，请重新连接或联系管理员', { exact: true }).waitFor()
  assert.equal(await page.locator('.evidence-card:visible,.message:visible,.draft-panel:visible').count(), 0)
}
try {
  const session = await page.request.get(base + '/v1/session')
  assert.equal(session.status(), 200)
  const identity = await session.json()
  assert.equal(identity.profile, 'v2_langchain_mock')
  assert.equal(identity.tenantId, 'v2-demo-tenant')
  assert.equal(identity.userId, 'dev-user')
  await test('actual-revocation-clears-browser-and-denies-replay', async () => {
    const turn = await create()
    authority('revoke')
    try {
      await cleared()
      for (const url of ['/v1/session', '/v1/turns/' + turn.request_id, '/v1/turns/' + turn.request_id + '/events']) {
        const response = await page.request.get(base + url)
        assert.equal(response.status(), 403)
        assert.equal((await response.json()).code, 'identity_revoked')
      }
    } finally { authority('restore') }
  })
  await test('actual-permission-version-change-clears-old-evidence', async () => {
    const turn = await create()
    const before = await (await page.request.get(base + '/v1/session')).json()
    authority('revision')
    await cleared()
    const current = await (await page.request.get(base + '/v1/session')).json()
    assert.notEqual(current.permission_revision, before.permission_revision)
    for (const suffix of ['', '/events']) {
      const response = await page.request.get(base + '/v1/turns/' + turn.request_id + suffix)
      assert.equal(response.status(), 403)
      assert.equal((await response.json()).code, 'execution_permission_changed')
    }
  })
  await test('restored-identity-creates-current-authorized-turn', async () => { await create() })
  finished = true
} finally {
  authority('restore')
  await writeFile(path.join(output, 'report.json'), JSON.stringify({ status: finished ? 'PASS' : 'FAIL', checks, environment, profile: 'v2_langchain_mock', real_service_acceptance: false, authority_mutations: 'reversible revocation; monotonic permission revisions', business_result_writes: false }, null, 2), { mode: 0o600, flag: 'wx' })
  await browser.close()
}
console.log(`PASS actual V2 authority browser: ${checks.length} checks; report=${output}/report.json`)
