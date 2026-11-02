import assert from 'node:assert/strict'
import { chromium } from 'playwright'
import { mkdirSync, writeFileSync } from 'node:fs'

const base = (process.env.WORKSPACE_URL ?? 'http://127.0.0.1:5174').replace(/\/$/, '')
const out = process.env.REDESIGN_OUT ?? '../.local/redesign'
const routes = [
  ['/admin/overview', '运行状态', 'overview'],
  ['/admin/merchant/outcomes', '结果度量', 'outcomes'],
  ['/admin/merchant/gaps', '内容缺口', 'gaps'],
  ['/admin/access/accounts', '账号与权限', 'accounts'],
  ['/admin/access/requests', '权限申请', 'requests'],
  ['/admin/access/audit', '授权审计', 'audit'],
  ['/admin/merchant/shops', '店铺范围', 'shops'],
  ['/admin/merchant/sources', '资料来源', 'sources'],
  ['/admin/merchant/ingestion', '摄取任务', 'ingestion'],
  ['/admin/merchant/reviews', '版本审核', 'reviews'],
]
const viewports = [{width:1440,height:900},{width:1280,height:800},{width:1024,height:768},{width:390,height:844}]
const themes = ['light', 'dark']
const browser = await chromium.launch({headless:true})
const page = await browser.newPage({viewport:viewports[0]})
const results = []
const screenshots = []
try {
  mkdirSync(out, {recursive:true})
  for (const viewport of viewports) {
    await page.setViewportSize(viewport)
    for (const [path,title] of routes) {
      await page.goto(base+path,{waitUntil:'networkidle'})
      await page.getByRole('heading',{name:title,level:1}).waitFor()
      const layout = await page.evaluate(() => ({viewport:innerWidth,
        document:document.documentElement.scrollWidth,
        main:Math.round(document.querySelector('.main-area')?.getBoundingClientRect().width ?? 0)}))
      assert.equal(layout.document, viewport.width, `${path} overflow ${viewport.width}`)
      assert.ok(layout.main > 0, `${path} no main ${viewport.width}`)
      results.push({path,width:viewport.width,...layout})
    }
  }
  await page.setViewportSize(viewports[0])
  await page.goto(base+'/admin/overview',{waitUntil:'networkidle'})
  const toggle = page.getByTestId('theme-toggle')
  await toggle.waitFor()
  const before = await page.evaluate(() => document.documentElement.className)
  await toggle.click()
  const after = await page.evaluate(() => document.documentElement.className)
  assert.notEqual(before, after, 'theme toggle did not change the root class')
  for (const theme of themes) {
    const context = await browser.newContext({viewport:{width:1440,height:900}})
    await context.addInitScript((value) => localStorage.setItem('ecr-theme', value), theme)
    const shot = await context.newPage()
    for (const [path,title,slug] of routes) {
      await shot.goto(base+path,{waitUntil:'networkidle'})
      await shot.getByRole('heading',{name:title,level:1}).waitFor()
      await shot.waitForTimeout(500)
      const name = `${slug}-${theme}.png`
      await shot.screenshot({path:`${out}/${name}`,fullPage:true})
      screenshots.push(name)
    }
    await shot.goto(base+'/login',{waitUntil:'networkidle'})
    await shot.waitForTimeout(500)
    const loginName = `login-${theme}.png`
    await shot.screenshot({path:`${out}/${loginName}`,fullPage:true})
    screenshots.push(loginName)
    await context.close()
  }
  const report = {status:'PASS',route_count:routes.length,viewports,themes,results,theme:{before,after},screenshots}
  writeFileSync(`${out}/browser-matrix.json`, JSON.stringify(report,null,2))
  console.log(JSON.stringify({status:'PASS',route_count:routes.length,viewports,themes,theme:report.theme,screenshot_count:screenshots.length}))
} finally { await browser.close() }
