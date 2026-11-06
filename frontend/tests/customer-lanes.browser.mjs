// Pre-sales/after-sales entry: the lane is chosen before the composer appears,
// after-sales requires an order number, and the active ticket is visible.
import assert from 'node:assert/strict'
import { chromium } from 'playwright'
import { mkdirSync, writeFileSync } from 'node:fs'

const base = (process.env.WORKSPACE_URL ?? 'http://127.0.0.1:5174').replace(/\/$/, '')
const out = new URL('../../.local/wp85/', import.meta.url).pathname
const conversation = {
  id: 'conv-lane',
  state: 'WAITING_HUMAN',
  epoch: 2,
  service_type: 'after_sales',
  order_id: 'ORD-2609-0005',
}
const snapshot = {
  conversation,
  ticket: { id: 'ticket-1', status: 'ASSIGNED' },
  messages: [{ id: 's1', source: 'system', text: '已进入人工客服等待队列。', redacted: false, citations: [] }],
}

function json(route, body, status = 200) {
  return route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

const browser = await chromium.launch({ headless: true })
const report = { status: 'PASS', checks: [], screenshot: '' }
try {
  mkdirSync(out, { recursive: true })
  const context = await browser.newContext({ viewport: { width: 390, height: 844 } })
  const page = await context.newPage()
  const errors = []
  page.on('pageerror', error => errors.push(String(error)))
  let created = null
  await page.route('**/v1/customer/**', async route => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path === '/v1/customer/shops/lane-shop') return json(route, { name: '售后测试店', welcome_message: '' })
    if (path === '/v1/customer/session') return json(route, { csrf_token: 'lane-csrf' })
    if (path === '/v1/customer/conversations' && request.method() === 'GET') return json(route, { conversations: [] })
    if (path === '/v1/customer/conversations' && request.method() === 'POST') {
      created = JSON.parse(request.postData() ?? '{}')
      return json(route, { conversation_id: conversation.id, service_type: created.service_type, order_id: created.order_id })
    }
    if (path === '/v1/customer/conversations/conv-lane') return json(route, snapshot)
    if (path === '/v1/customer/conversations/conv-lane/events') return route.fulfill({ status: 200, contentType: 'text/event-stream', body: '' })
    return json(route, {}, 404)
  })
  await page.goto(base + '/shop/lane-shop', { waitUntil: 'networkidle' })
  const picker = page.getByRole('radiogroup', { name: '服务类型' })
  await picker.waitFor()
  assert.equal(await page.locator('#question').count(), 0, 'composer appeared before a lane was chosen')
  report.checks.push('lane picker precedes the composer')

  await page.getByRole('radio', { name: /售后咨询/ }).click()
  const start = page.getByRole('button', { name: '开始咨询' })
  assert.equal(await start.isDisabled(), true, 'after-sales started without an order')
  await page.locator('.order-field input').fill('ord-2609-0005')
  await start.click()

  await page.getByText('售后 · ORD-2609-0005').waitFor()
  await page.getByText('人工客服已领取工单').waitFor()
  await page.getByText('售后 AI 只读查询订单、物流、库存与价格；退款、改地址、取消订单由人工客服处理。').waitFor()
  assert.deepEqual(created, { service_type: 'after_sales', order_id: 'ORD-2609-0005' })
  report.checks.push('after-sales binds the uppercase order and shows the ticket')
  assert.equal(errors.length, 0, errors.join('; '))
  const file = out + 'lanes.png'
  await page.screenshot({ path: file, fullPage: true })
  report.screenshot = file
} catch (error) {
  report.status = 'FAIL'
  report.error = String(error)
} finally {
  await browser.close()
  writeFileSync(out + 'manifest.json', JSON.stringify(report, null, 2))
}
if (report.status !== 'PASS') {
  console.error('FAIL customer lanes: ' + report.error)
  process.exit(1)
}
console.log('PASS customer lanes: ' + report.checks.join('; '))
