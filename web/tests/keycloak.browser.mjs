// Real local Keycloak pages and Go cookie/SSE routes; no response substitution.
import assert from 'node:assert/strict'
import { createHash, randomUUID } from 'node:crypto'
import { readFile, lstat, mkdir, writeFile, open } from 'node:fs/promises'
import { chromium } from 'playwright'
import { execFileSync, spawn } from 'node:child_process'
import { fileURLToPath } from 'node:url'

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
const root = fileURLToPath(new URL('../../', import.meta.url))
let ownedStack
let stackLog
const context = await browser.newContext()
const page = await context.newPage()
const checks = []
let finished = false
let turn, resultHash
let phase = 'initial'
const digest = value => createHash('sha256').update(JSON.stringify(value)).digest('hex')
const authorityConfig = process.env.KEYCLOAK_BROWSER_AUTHORITY_ENV
const authorityFixture = `
import json,sys
from pathlib import Path
sys.path.insert(0,str(Path.cwd()/'scripts'))
from v2_services import load_values
import psycopg
try:
 config=load_values(Path(sys.argv[1]));action=sys.argv[2];snapshot=Path(sys.argv[3])
 with psycopg.connect(config['V2_MIGRATION_DSN']) as db:
  name,superuser,bypass=db.execute('SELECT current_database(),rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user').fetchone()
  assert name=='ecr_v2_dev_20261013_temporal' and not superuser and not bypass
  row=db.execute("SELECT u.id,u.role,u.shop_ids,u.permission_revision,u.revoked_at FROM app_user u JOIN tenant t ON t.id=u.tenant_id WHERE t.external_id='v2-demo-tenant' AND u.external_id='dev-user'").fetchone()
  assert row
  if action=='inspect':
   assert row[4] is None and not snapshot.exists()
   import os
   with os.fdopen(os.open(snapshot,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600),'w') as out:json.dump([str(row[0]),row[1],row[2],row[3]],out)
  elif action=='restore':
   original=json.loads(snapshot.read_text());assert str(row[0])==original[0]
   db.execute('UPDATE app_user SET role=%s,shop_ids=%s,revoked_at=NULL WHERE id=%s',(original[1],original[2],original[0]))
   db.execute('UPDATE app_user SET permission_revision=%s WHERE id=%s',(original[3],original[0]))
  elif action=='revoke':db.execute('UPDATE app_user SET revoked_at=now() WHERE id=%s',(row[0],))
  elif action=='shops':db.execute('UPDATE app_user SET shop_ids=%s WHERE id=%s',([],row[0]))
  elif action=='role':db.execute("UPDATE app_user SET role='viewer' WHERE id=%s",(row[0],))
  elif action=='revision':db.execute("UPDATE app_user SET permission_revision='browser-fixture-revision' WHERE id=%s",(row[0],))
  else:raise ValueError()
 print(json.dumps({'ok':True}))
except Exception:
 raise SystemExit('bounded browser authority fixture failed') from None
`
function authority(action) {
  execFileSync(root + '/python/.venv/bin/python', ['-c', authorityFixture, authorityConfig, action, output + '/authority-snapshot.json'], { cwd: root, stdio: ['ignore', 'pipe', 'pipe'] })
}
let authorityInspected = false
const accountFixture = `
import sys,secrets,json
from pathlib import Path
sys.path.insert(0,str(Path.cwd()/'eval'))
from run_keycloak_protocol import HTTP,protected_json
try:
 a=protected_json(Path(sys.argv[1]));assert a['realm']=='ecr-v2-test'
 h=HTTP('http://127.0.0.1:18095')
 status,result=h.call('POST','/realms/master/protocol/openid-connect/token',{'grant_type':'password','client_id':'admin-cli','username':a['admin_username'],'password':a['admin_password']},form=True)
 assert status==200
 token=result['access_token'];prefix='/admin/realms/ecr-v2-test/users'
 if sys.argv[2]=='create':
  status,original=h.call('GET',prefix+'/'+a['subject'],token=token);assert status==200
  username='browser-isolation-'+secrets.token_hex(8);password=secrets.token_hex(32)
  status,_=h.call('POST',prefix,{'username':username,'enabled':True,'firstName':'Synthetic','lastName':'Isolation','email':username+'@example.invalid','emailVerified':True,'requiredActions':[],'attributes':original['attributes'],'credentials':[{'type':'password','value':password,'temporary':False}]},token=token);assert status==201
  status,rows=h.call('GET',prefix+'?username='+username+'&exact=true',token=token);assert status==200 and len(rows)==1
  print(json.dumps({'id':rows[0]['id'],'username':username,'password':password}))
 else:
  status,user=h.call('GET',prefix+'/'+sys.argv[3],token=token);assert status==200 and user['username'].startswith('browser-isolation-')
  status,_=h.call('DELETE',prefix+'/'+sys.argv[3],token=token);assert status==204
except Exception:
 raise SystemExit('bounded account fixture failed') from None
`
async function test(name, run) {
  try { await run(); checks.push({ check_id: name, passed: true }); console.log(JSON.stringify({ check_id: name, passed: true })) }
  catch (error) { checks.push({ check_id: name, passed: false, phase, error_type: error.name, ...(typeof error.actual === 'number' ? { observed_status: error.actual } : {}) }); throw new Error('bounded browser check failed') }
}
async function login(base, path) {
  await page.goto(base + path)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  // Keycloak SSO after a Go restart can immediately complete the callback.
  for (let i = 0; i < 80; i++) {
    if (await page.locator('#username').isVisible()) {
      await page.locator('#username').fill(accounts.username)
      await page.locator('#password').fill(accounts.password)
      await page.locator('#kc-login').click()
      break
    }
    if (await page.locator('#question').isVisible() || await page.getByRole('button', { name: '退出登录', exact: true }).isVisible()) break
    await page.waitForTimeout(250)
  }
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
  if (process.env.KEYCLOAK_BROWSER_ACCOUNT_ISOLATION === '1') {
    await test('separate-signed-account-cannot-inherit-mapped-user-or-result', async () => {
      phase = 'separate_account'
      const account = JSON.parse(execFileSync(root + '/python/.venv/bin/python', ['-c', accountFixture, accountsPath, 'create'], { cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }))
      const other = await browser.newContext()
      try {
        const p = await other.newPage()
        await p.goto(customer + '/chat'); await p.getByRole('button', { name: '登录', exact: true }).click()
        await p.locator('#username').fill(account.username); await p.locator('#password').fill(account.password)
        const rejected = p.waitForResponse(r => new URL(r.url()).pathname === '/v1/auth/callback')
        await p.locator('#kc-login').click(); assert.equal((await rejected).status(), 401)
        assert.ok(!(await other.cookies()).some(c => c.name === 'ecr_v2_session'))
        assert.equal((await p.request.get(customer + '/v1/turns/' + turn.request_id)).status(), 401)
        assert.equal(digest(await (await page.request.get(customer + '/v1/turns/' + turn.request_id)).json()), resultHash)
      } finally {
        await other.close()
        execFileSync(root + '/python/.venv/bin/python', ['-c', accountFixture, accountsPath, 'delete', account.id], { cwd: root, stdio: ['ignore', 'pipe', 'pipe'] })
      }
    })
  }
  if (authorityConfig) {
    authority('inspect'); authorityInspected = true
    for (const change of ['revoke', 'shops', 'role', 'revision']) {
      await test('actual-browser-' + change + '-invalidates-old-session', async () => {
        phase = 'authority_' + change
        authority(change)
        try {
          assert.equal((await page.request.get(customer + '/v1/turns/' + turn.request_id)).status(), 403)
          assert.equal((await page.request.get(customer + '/v1/turns/' + turn.request_id + '/events')).status(), 401)
          await page.reload()
          await page.getByRole('button', { name: '登录', exact: true }).waitFor()
          assert.equal(await page.locator('.evidence-card:visible,.message:visible,.draft-panel:visible').count(), 0)
        } finally { authority('restore') }
        await login(customer, '/chat')
        await page.locator('#question').waitFor()
        assert.equal(digest(await (await page.request.get(customer + '/v1/turns/' + turn.request_id)).json()), resultHash)
      })
    }
  }
  if (process.env.KEYCLOAK_BROWSER_OUTAGE === '1') {
    await test('identity-outage-fails-closed-and-local-logout-survives', async () => {
      phase = 'identity_outage'
      const auth = await (await page.request.get(customer + '/v1/auth/session')).json()
      execFileSync('docker', ['pause', 'ecr-v2-keycloak-26-8-1'], { stdio: 'ignore' })
      try {
        assert.equal((await page.request.get(customer + '/v1/session')).status(), 503)
        assert.equal((await page.request.post(customer + '/v1/auth/logout', { headers: { Origin: customer, 'X-CSRF-Token': auth.csrf_token } })).status(), 503)
        assert.equal((await page.request.get(customer + '/v1/turns/' + turn.request_id)).status(), 401)
      } finally { execFileSync('docker', ['unpause', 'ecr-v2-keycloak-26-8-1'], { stdio: 'ignore' }) }
      assert.equal((await (await page.request.get(customer + '/v1/auth/session')).json()).authenticated, false)
      await login(customer, '/chat'); await page.locator('#question').waitFor()
    })
  }
  if (process.env.KEYCLOAK_BROWSER_RESTART === '1') {
    await test('gateway-restart-requires-login-and-preserves-business', async () => {
      phase = 'stop_owned_stack'
      const runtime = '/etc/ecommerce-rag/v2-browser-20261014.env'
      const stop = `from pathlib import Path\nimport os,signal\nmatches=[]\nfor p in Path('/proc').iterdir():\n if not p.name.isdigit():continue\n try:args=(p/'cmdline').read_bytes().split(b'\\0')\n except OSError:continue\n if b'/opt/ecommerce-rag-v2/scripts/v2_services.py' in args and b'${runtime}' in args:matches.append(int(p.name))\nassert len(matches)==1\nos.kill(matches[0],signal.SIGTERM)\n`
      execFileSync(root + '/python/.venv/bin/python', ['-c', stop], { stdio: ['ignore', 'pipe', 'pipe'] })
      for (let i = 0; i < 100; i++) {
        try { const response = await page.request.get(customer + '/v1/auth/session', { timeout: 500 }); if (response.status() >= 500) break }
        catch { break }
        await page.waitForTimeout(200)
      }
      stackLog = await open(output + '/replacement.log', 'wx', 0o600)
      ownedStack = spawn('bash', ['scripts/v2-local-services.sh', '--env', runtime], { cwd: root, stdio: ['ignore', stackLog.fd, stackLog.fd] })
      phase = 'wait_replacement_ready'
      let ready = false
      // The launcher builds Go before its own readiness timer starts. A cold
      // local build measured about a minute; allow that build plus readiness.
      for (let i = 0; i < 600; i++) {
        if (ownedStack.exitCode !== null) break
        try { const response = await page.request.get(customer + '/v1/auth/session', { timeout: 500 }); if (response.status() === 200) { ready = true; break } } catch {}
        await page.waitForTimeout(250)
      }
      assert.ok(ready)
      phase = 'reject_old_cookie'
      assert.equal((await page.request.get(customer + '/v1/turns/' + turn.request_id)).status(), 401)
      await login(customer, '/chat')
      phase = 'read_restored_result'
      await page.locator('#question').waitFor()
      assert.equal(digest(await (await page.request.get(customer + '/v1/turns/' + turn.request_id)).json()), resultHash)
    })
  }
  finished = true
} catch {
  console.log(JSON.stringify({ status: 'NOT_PASSED', failure_code: 'browser_identity_check_failed' }))
  process.exitCode = 1
} finally {
  if (authorityInspected) authority('restore')
  await browser.close()
  if (ownedStack) {
    // This runner owns only the replacement supervisor and its children.
    const stop = `from pathlib import Path\nimport os,signal\npid=${ownedStack.pid}\nfor p in Path('/proc').iterdir():\n if not p.name.isdigit():continue\n try:\n  args=(p/'cmdline').read_bytes().split(b'\\0');ppid=int((p/'stat').read_text().split()[3])\n except (OSError,ValueError):continue\n if ppid==pid and b'/opt/ecommerce-rag-v2/scripts/v2_services.py' in args:os.kill(int(p.name),signal.SIGTERM)\n`
    execFileSync(root + '/python/.venv/bin/python', ['-c', stop], { stdio: ['ignore', 'pipe', 'pipe'] })
    ownedStack.kill('SIGTERM')
    await stackLog?.close()
  }
  await writeFile(output + '/report.json', JSON.stringify({ report_version: 'keycloak-browser-v1', status: finished ? 'PASS' : 'NOT_PASSED', checks,
    provider_profile: 'v2_langchain_mock', turn_id: turn?.request_id ?? null, result_sha256: resultHash ?? null,
    reviewer_kind: 'agent', human_review_completed: false, real_service_acceptance: false,
    implementation_sha256: Object.fromEntries(await Promise.all(['go/internal/identity/browser.go', 'go/internal/gateway/identity.go', 'web/src/auth.ts', 'admin-web/src/auth.ts'].map(async p => [p, createHash('sha256').update(await readFile(root + p)).digest('hex')]))) }, null, 2), { mode: 0o600, flag: 'wx' })
  console.log(JSON.stringify({ report_directory: output, status: finished ? 'PASS' : 'NOT_PASSED' }))
}
