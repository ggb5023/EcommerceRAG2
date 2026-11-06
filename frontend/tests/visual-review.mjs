// Screenshot + layout review across admin, staff, assistant and customer zones.
// Usage: WORKSPACE_URL=http://127.0.0.1:5173 \
//   VISUAL_ADMIN_USER=access VISUAL_ADMIN_PASSWORD=... \
//   VISUAL_STAFF_USER=staff VISUAL_STAFF_PASSWORD=... npm run test:visual
import assert from 'node:assert/strict'
import { chromium } from 'playwright'
import { mkdirSync, writeFileSync } from 'node:fs'

const base = (process.env.WORKSPACE_URL ?? 'http://127.0.0.1:5173').replace(/\/$/, '')
const out = process.env.VISUAL_OUT ?? '../.local/visual-review'
const adminUser = process.env.VISUAL_ADMIN_USER
const adminPassword = process.env.VISUAL_ADMIN_PASSWORD
const staffUser = process.env.VISUAL_STAFF_USER
const staffPassword = process.env.VISUAL_STAFF_PASSWORD
const viewports = [{ width: 1440, height: 900 }, { width: 390, height: 844 }]
const themes = ['light', 'dark']
const routes = [
  ['admin', '/admin/overview', '运行状态', 'admin-overview'],
  ['admin', '/admin/access/accounts', '账号与权限', 'admin-accounts'],
  ['admin', '/admin/access/requests', '权限申请', 'admin-requests'],
  ['admin', '/admin/access/audit', '授权审计', 'admin-audit'],
  ['admin', '/admin/merchant/shops', '店铺范围', 'admin-shops'],
  ['admin', '/admin/merchant/reviews', '版本审核', 'admin-reviews'],
  ['admin', '/admin/merchant/answers', '回答与引用', 'admin-answers'],
  ['staff', '/staff', '客服工作台', 'staff-queue'],
  ['staff', '/assistant', null, 'assistant-qa'],
  ['staff', '/assistant/catalog', '商品目录', 'assistant-catalog'],
  ['staff', '/shop/shop-demo', null, 'customer-chat'],
  ['staff', '/login', null, 'login'],
]

async function login(page, user, password) {
  if (!user || !password) return
  await page.goto(`${base}/v1/auth/login?app=admin`, { waitUntil: 'networkidle' })
  if (!page.url().includes('/identity/')) return
  await page.fill('#username', user)
  await page.fill('#password', password)
  await page.click('input[type=submit], button[type=submit]')
  await page.waitForURL(/\/(admin|staff|assistant)/, { timeout: 30000 })
}

const browser = await chromium.launch({ headless: true })
const contexts = { admin: await browser.newContext(), staff: await browser.newContext() }
const failures = []
try {
  mkdirSync(out, { recursive: true })
  const pages = { admin: await contexts.admin.newPage(), staff: await contexts.staff.newPage() }
  for (const [name, page] of Object.entries(pages)) {
    page.on('pageerror', error => failures.push(`${name}:pageerror:${error.message}`))
    await page.setViewportSize(viewports[0])
    await login(page, name === 'admin' ? adminUser : staffUser, name === 'admin' ? adminPassword : staffPassword)
  }
  for (const theme of themes) {
    for (const [role, path, heading, slug] of routes) {
      const page = pages[role]
      await page.addInitScript(value => localStorage.setItem('ecr-theme', value), theme)
      await page.setViewportSize(viewports[0])
      await page.goto(base + path, { waitUntil: 'networkidle' })
      await page.waitForTimeout(400)
      const layout = await page.evaluate(() => ({ viewport: innerWidth, document: document.documentElement.scrollWidth }))
      if (layout.document > layout.viewport + 1) failures.push(`${slug}: horizontal overflow ${layout.document}>${layout.viewport}`)
      if (heading) {
        const text = await page.locator('h1').first().innerText().catch(() => '')
        if (!text.includes(heading)) failures.push(`${slug}: heading ${JSON.stringify(text)} lacks ${heading}`)
      }
      if (slug === 'staff-queue' && await page.locator('a[href="/admin/overview"]').count()) {
        failures.push('staff: knowledge-management link still present')
      }
      if (slug === 'assistant-qa') {
        const body = await page.locator('body').innerText()
        if (!body.includes('AI 查询台')) failures.push('assistant: brand missing')
      }
      if (slug === 'assistant-catalog') {
        const body = await page.locator('body').innerText()
        if (!body.includes('库存、订单与配送请咨询人工')) failures.push('catalog: non-realtime notice missing')
      }
      await page.screenshot({ path: `${out}/${slug}-${theme}-1440.png`, fullPage: true })
      await page.setViewportSize({ width: 390, height: 844 })
      await page.goto(base + path, { waitUntil: 'networkidle' })
      await page.waitForTimeout(300)
      const mobile = await page.evaluate(() => ({ viewport: innerWidth, document: document.documentElement.scrollWidth }))
      if (mobile.document > mobile.viewport + 1) failures.push(`${slug}: mobile overflow ${mobile.document}>${mobile.viewport}`)
      await page.screenshot({ path: `${out}/${slug}-${theme}-390.png`, fullPage: true })
    }
  }
  const report = { status: failures.length ? 'FAIL' : 'PASS', routes: routes.length, themes, viewports, failures }
  writeFileSync(`${out}/manifest.json`, JSON.stringify(report, null, 2))
  console.log(JSON.stringify(report))
  assert.equal(failures.length, 0, failures.join('; '))
} finally {
  await browser.close()
}
