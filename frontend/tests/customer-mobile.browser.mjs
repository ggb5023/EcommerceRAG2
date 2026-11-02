import assert from 'node:assert/strict'
import { chromium, devices } from 'playwright'
import { mkdirSync, writeFileSync } from 'node:fs'

const base = (process.env.WORKSPACE_URL ?? 'http://127.0.0.1:5174').replace(/\/$/, '')
const out = new URL('../../.local/wp20/', import.meta.url).pathname
const devicesToCheck = [
  ['iphone-14', { ...devices['iPhone 14'], viewport: { width: 390, height: 844 } }],
  ['pixel-5', { ...devices['Pixel 5'], viewport: { width: 393, height: 851 } }],
]
const citation = { evidence_id: 'e1', citation_index: 1, title: '商品参数', version_id: 'v7', snippet: '容量：480 毫升。' }
const snapshot = {
  conversation: { id: 'conv-mobile', state: 'AI' },
  messages: [
    { id: 'm1', source: 'customer', text: '杯子的容量是多少？', redacted: false, citations: [] },
    { id: 'm2', source: 'assistant', text: '这款杯子的容量是 480 毫升。', redacted: false, citations: [citation] },
    { id: 'm3', source: 'assistant', text: '请补充具体型号。', redacted: false, citations: [] },
  ],
}

function json(route, body, status = 200) {
  return route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

async function mockCustomerApi(page) {
  await page.route('**/v1/customer/**', async route => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path === '/v1/customer/shops/mobile-shop') return json(route, { name: '移动测试店', welcome_message: '你好，欢迎咨询。' })
    if (path === '/v1/customer/session') return json(route, { csrf_token: 'test-csrf' })
    if (path === '/v1/customer/conversations' && request.method() === 'GET') return json(route, { conversations: [{ id: 'conv-mobile', state: 'AI' }] })
    if (path === '/v1/customer/conversations' && request.method() === 'POST') return json(route, { conversation_id: 'conv-mobile' })
    if (path === '/v1/customer/conversations/conv-mobile') return json(route, snapshot)
    if (path === '/v1/customer/conversations/conv-mobile/citations/m2') return json(route, { citations: [citation] })
    if (path === '/v1/customer/conversations/conv-mobile/events') return route.fulfill({ status: 200, contentType: 'text/event-stream', body: '' })
    return json(route, {}, 404)
  })
}

const browser = await chromium.launch({ headless: true })
const report = { status: 'PASS', devices: [], checks: [], screenshots: [] }
try {
  mkdirSync(out, { recursive: true })
  for (const [name, descriptor] of devicesToCheck) {
    const width = descriptor.viewport.width
    const context = await browser.newContext(descriptor)
    const page = await context.newPage()
    const errors = []
    page.on('pageerror', error => errors.push(String(error)))
    await mockCustomerApi(page)
    await page.goto(base + '/shop/mobile-shop', { waitUntil: 'networkidle' })
    await page.locator('#question').waitFor()

    const initial = await page.evaluate(() => ({
      width: innerWidth,
      scrollWidth: document.documentElement.scrollWidth,
      viewportMeta: document.querySelector('meta[name=viewport]')?.getAttribute('content') ?? '',
      input: document.querySelector('#question').getBoundingClientRect().toJSON(),
    }))
    assert.equal(initial.scrollWidth, width, `${name}: horizontal overflow at ${width}px`)
    assert.match(initial.viewportMeta, /viewport-fit=cover/, `${name}: viewport-fit missing`)
    assert.ok(initial.input.bottom <= descriptor.viewport.height + 1, `${name}: input below initial viewport`)
    report.checks.push(`${name}: initial input visible and no horizontal overflow`)

    const copyButtons = page.getByRole('button', { name: '复制回答' })
    assert.equal(await copyButtons.count(), 2, `${name}: expected two assistant copy controls`)
    assert.equal(await copyButtons.nth(0).isEnabled(), true, `${name}: cited answer must be copyable`)
    assert.equal(await copyButtons.nth(1).isDisabled(), true, `${name}: uncited answer must be blocked`)
    assert.ok(await page.getByText('没有可引用的资料依据').count(), `${name}: blocked reason missing`)
    report.checks.push(`${name}: copy enabled with citation and disabled with reason without citation`)

    await page.getByRole('button', { name: '查看来源 1' }).click()
    const drawer = page.getByRole('complementary', { name: '回答来源' })
    await drawer.waitFor()
    await drawer.getByTestId('citation-card').waitFor()
    assert.equal(await drawer.getByTestId('citation-card').count(), 1, `${name}: citation card missing from drawer`)
    await page.screenshot({ path: `${out}${name}-drawer.png`, fullPage: false })
    report.screenshots.push(`${name}-drawer.png`)
    await drawer.getByRole('button', { name: '收起来源' }).click()
    await drawer.waitFor({ state: 'detached' })
    report.checks.push(`${name}: citation drawer opens and closes`)
    await page.screenshot({ path: `${out}${name}-open.png`, fullPage: false })
    report.screenshots.push(`${name}-open.png`)

    await page.locator('#question').focus()
    const keyboardHeight = 500
    await page.setViewportSize({ width, height: keyboardHeight })
    await page.evaluate(() => window.visualViewport?.dispatchEvent(new Event('resize')))
    await page.waitForFunction(height => Math.round(parseFloat(getComputedStyle(document.querySelector('.customer-chat')).height)) === height, keyboardHeight)
    const lifted = await page.evaluate(() => ({
      viewport: innerHeight,
      scrollWidth: document.documentElement.scrollWidth,
      viewportMeta: document.querySelector('meta[name=viewport]')?.getAttribute('content') ?? '',
      input: document.querySelector('#question').getBoundingClientRect().toJSON(),
      composer: document.querySelector('.composer').getBoundingClientRect().toJSON(),
    }))
    assert.equal(lifted.scrollWidth, width, `${name}: horizontal overflow after keyboard resize`)
    assert.ok(lifted.input.bottom <= keyboardHeight + 1, `${name}: input hidden after keyboard resize`)
    assert.ok(lifted.composer.bottom <= keyboardHeight + 1, `${name}: composer hidden after keyboard resize`)
    report.checks.push(`${name}: simulated keyboard resize keeps composer and input visible`)
    await page.screenshot({ path: `${out}${name}-keyboard.png`, fullPage: false })
    report.screenshots.push(`${name}-keyboard.png`)

    assert.deepEqual(errors, [], `${name}: page errors`)
    report.devices.push({ name, viewport: descriptor.viewport, device_scale_factor: descriptor.deviceScaleFactor, checks: 'PASS' })
    await context.close()
  }
  writeFileSync(`${out}/customer-mobile.json`, JSON.stringify(report, null, 2))
  console.log(JSON.stringify(report))
} catch (error) {
  report.status = 'NOT_PASSED'
  report.error = String(error)
  mkdirSync(out, { recursive: true })
  writeFileSync(`${out}/customer-mobile.json`, JSON.stringify(report, null, 2))
  console.error(JSON.stringify(report))
  process.exitCode = 1
} finally {
  await browser.close()
}
