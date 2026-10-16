// Own a complete offline V2 stack, interrupt a real turn, and verify browser
// recovery. The configured ports must be unused. No business tables are written.
import assert from 'node:assert/strict'
import { spawn, execFileSync } from 'node:child_process'
import { createHash, randomUUID } from 'node:crypto'
import { mkdir, open, readFile, writeFile } from 'node:fs/promises'
import net from 'node:net'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright'

const root = fileURLToPath(new URL('../../', import.meta.url))
const python = path.join(root, 'python/.venv/bin/python')
const config = process.env.V2_RESTART_RUNTIME_ENV
const base = process.env.WORKSPACE_URL ?? 'http://127.0.0.1:5173'
assert.ok(config && path.isAbsolute(config), 'explicit protected offline V2 runtime required')
assert.ok(['127.0.0.1', 'localhost'].includes(new URL(base).hostname))
const preflight = `
import json,sys
from pathlib import Path
sys.path.insert(0, str(Path.cwd() / 'scripts' / 'dev'))
from v2_services import configure,load_values
import psycopg
try:
    values=configure(load_values(Path(sys.argv[1])))
    assert values['RAG_PROFILE']=='v2_langchain_mock'
    addresses=[values[k] for k in ('HTTP_ADDR','V2_GRPC_ADDR','V2_INGEST_GRPC_ADDR')]
    assert all(a.startswith('127.0.0.1:') for a in addresses)
    with psycopg.connect(values['V2_GATEWAY_DSN']) as conn:
        db,role,superuser,bypass=conn.execute('SELECT current_database(),current_user,rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user').fetchone()
        assert db.startswith('ecr_v2_test_') and role=='rag_v2_gateway' and not superuser and not bypass
    print(json.dumps({'database':db,'role':role,'superuser':superuser,'bypass_rls':bypass,'addresses':addresses}))
except Exception:
    raise SystemExit('FAIL isolated restart configuration') from None
`
const environment = JSON.parse(execFileSync(python, ['-c', preflight, config], { cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }))
const output = path.join(root, '.local/v2-recovery/browser', randomUUID())
await mkdir(output, { recursive: true, mode: 0o700 })
const checks = []
let stack
let owned = {}
let iteration = 0
let finished = false
const pause = ms => new Promise(resolve => setTimeout(resolve, ms))
async function portOpen(address) {
  return new Promise(resolve => {
    const socket = net.connect({ host: '127.0.0.1', port: Number(address.split(':')[1]) })
    socket.setTimeout(250)
    const done = value => { socket.destroy(); resolve(value) }
    socket.once('connect', () => done(true)); socket.once('error', () => done(false)); socket.once('timeout', () => done(false))
  })
}
async function waitFor(test, label, seconds = 15) {
  const deadline = Date.now() + seconds * 1000
  while (Date.now() < deadline) { if (await test()) return; await pause(100) }
  throw new Error(label)
}
async function childProcesses() {
  const children = (await readFile(`/proc/${stack.pid}/task/${stack.pid}/children`, 'utf8')).trim().split(/\s+/).filter(Boolean)
  const result = {}
  for (const id of children) {
    const args = (await readFile(`/proc/${id}/cmdline`, 'utf8')).split('\0')
    for (const [name, marker] of Object.entries({ gateway: path.join(root, '.local/v2-gateway'), ai: 'app.rpc.server', ingest: 'app.rpc.ingest_server', worker: 'app.ingest.worker_main' })) {
      if (args.includes(marker)) result[name] = Number(id)
    }
  }
  assert.deepEqual(Object.keys(result).sort(), ['ai', 'gateway', 'ingest', 'worker'])
  return result
}
async function startStack() {
  for (const address of environment.addresses) assert.equal(await portOpen(address), false, 'configured port is already in use')
  iteration++
  const logPath = path.join(output, `stack-${iteration}.log`)
  const log = await open(logPath, 'wx', 0o600)
  stack = spawn(python, [path.join(root, 'scripts/dev/v2_services.py'), '--env', config], { cwd: root, stdio: ['ignore', log.fd, log.fd] })
  await log.close()
  await waitFor(async () => {
    assert.equal(stack.exitCode, null, 'owned stack exited before readiness')
    return (await readFile(logPath, 'utf8')).includes('READY V2 gateway,ai,ingest,worker')
  }, 'four-process readiness timeout', 40)
  owned = await childProcesses()
}
async function stackStopped() {
  await waitFor(() => stack.exitCode !== null || stack.signalCode !== null, 'owned supervisor did not stop')
  for (const address of environment.addresses) assert.equal(await portOpen(address), false, 'owned service port remains open')
  for (const pid of Object.values(owned)) {
    try { await readFile(`/proc/${pid}/cmdline`); assert.fail('owned child remains alive') }
    catch (error) { assert.equal(error.code, 'ENOENT') }
  }
}
async function stopStack() {
  if (!stack || stack.exitCode !== null || stack.signalCode !== null) return
  try { process.kill(owned.ai, 'SIGCONT') } catch {}
  stack.kill('SIGTERM')
  await stackStopped()
}
async function step(name, run) {
  try { await run(); checks.push({ name, status: 'PASS' }) }
  catch (error) { checks.push({ name, status: 'FAIL' }); throw error }
}
const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH, headless: true })
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
async function createPage(query, complete = true) {
  await page.goto(base + '/chat'); await page.locator('#question').waitFor()
  const response = page.waitForResponse(r => r.url().endsWith('/v1/conversations') && r.request().method() === 'POST')
  await page.getByRole('button', { name: '＋ 新建会话', exact: true }).click()
  assert.equal((await response).status(), 201)
  return send(query, complete)
}
async function send(query, complete = true) {
  const sent = page.waitForResponse(r => /\/v1\/conversations\/[^/]+\/turns$/.test(new URL(r.url()).pathname) && r.request().method() === 'POST')
  await page.locator('#question').fill(query); await page.locator('#question').press('Enter')
  const response = await sent; assert.equal(response.status(), 202)
  const turn = await response.json()
  if (complete) await restored(turn)
  return turn
}
async function restored(turn) {
  await page.getByRole('button', { name: '发送 ↗', exact: true }).waitFor({ timeout: 15000 })
  await page.waitForFunction(id => document.querySelector('.result-binding')?.textContent.includes(id), turn.request_id)
}
async function result(turn) {
  const response = await page.request.get(base + '/v1/turns/' + turn.request_id)
  assert.equal(response.status(), 200)
  return response.json()
}
let done, asking, interrupted, doneHash, doneURL, askingURL, interruptedURL
try {
  await startStack()
  await step('persist-done-and-asking-before-restart', async () => {
    done = await createPage('SKU-CUP-480 保温杯容量？'); doneURL = page.url()
    const value = await result(done); assert.equal(value.status, 'DONE'); assert.ok(value.evidence.length)
    doneHash = createHash('sha256').update(JSON.stringify(value)).digest('hex')
    asking = await createPage('那个怎么样？'); askingURL = page.url()
    assert.equal((await result(asking)).status, 'ASKING')
  })
  await step('crash-real-active-execution-and-stop-all-four-processes', async () => {
    await page.goto(base + '/chat'); await page.locator('#question').waitFor()
    const created = page.waitForResponse(r => r.url().endsWith('/v1/conversations') && r.request().method() === 'POST')
    await page.getByRole('button', { name: '＋ 新建会话', exact: true }).click(); assert.equal((await created).status(), 201)
    process.kill(owned.ai, 'SIGSTOP')
    interrupted = await send('SKU-CUP-480 保温杯容量？', false); interruptedURL = page.url()
    // Freeze within this process, without an LLM/tool scheduling gap.
    process.kill(owned.gateway, 'SIGSTOP')
    process.kill(owned.gateway, 'SIGKILL')
    process.kill(owned.ai, 'SIGCONT')
    await stackStopped()
    assert.notEqual(stack.exitCode, 0)
  })
  await startStack()
  await step('browser-recovers-interrupted-terminal-and-contiguous-replay', async () => {
    await page.goto(interruptedURL); await page.locator('#question').waitFor(); await restored(interrupted)
    const value = await result(interrupted)
    assert.equal(value.status, 'FAILED'); assert.equal(value.answer, ''); assert.equal(value.customer_reply.can_copy, false)
    const stream = await page.request.get(base + value.events_url)
    assert.equal(stream.status(), 200)
    const frames = (await stream.text()).split('\n\n').filter(frame => frame.includes('data: '))
    const events = frames.map(frame => JSON.parse(frame.split('data: ')[1]))
    assert.deepEqual(events.map(event => event.seq), events.map((_, i) => i + 1))
    assert.equal(events.filter(event => event.type === 'completed').length, 1)
    assert.equal(events.at(-1).error_code, 'gateway_restarted')
    const replay = await page.request.get(base + value.events_url, { headers: { 'Last-Event-ID': String(events.at(-2).seq) } })
    assert.equal((await replay.text()).split('data: ').length - 1, 1)
    const next = await send('SKU-CUP-480 保温杯容量？')
    assert.equal((await result(next)).status, 'DONE')
  })
  await step('done-browser-result-survives-four-process-restart', async () => {
    await page.goto(doneURL); await page.locator('#question').waitFor(); await restored(done)
    assert.equal(createHash('sha256').update(JSON.stringify(await result(done))).digest('hex'), doneHash)
    assert.ok(await page.locator('.evidence-card').count())
  })
  await step('asking-browser-checkpoint-survives-and-resumes', async () => {
    await page.goto(askingURL); await page.locator('#question').waitFor(); await restored(asking)
    assert.equal((await result(asking)).status, 'ASKING')
    await page.getByText('需要补充信息', { exact: true }).waitFor()
    const child = await send('SKU-CUP-480 保温杯容量？')
    const value = await result(child); assert.equal(value.status, 'DONE'); assert.equal(value.parent_turn_id, asking.turn_id)
  })
  await step('graceful-shutdown-closes-owned-processes-and-ports', async () => { await stopStack() })
  finished = true
} finally {
  try { await stopStack() }
  finally {
    await writeFile(path.join(output, 'report.json'), JSON.stringify({ status: finished ? 'PASS' : 'FAIL', checks, environment,
      profile: 'v2_langchain_mock', real_service_acceptance: false, business_result_writes: false,
      git_head: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim(),
      requests: { done: done?.request_id, asking: asking?.request_id, interrupted: interrupted?.request_id },
      faults: ['SIGSTOP AI', 'SIGKILL owned Gateway', 'supervisor cleanup', 'four-process restart'] }, null, 2), { mode: 0o600, flag: 'wx' })
    await browser.close()
  }
}
console.log(`PASS actual V2 restart browser: ${checks.length} checks; report=${output}/report.json`)
