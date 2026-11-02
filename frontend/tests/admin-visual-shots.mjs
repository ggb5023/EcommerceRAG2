import assert from 'node:assert/strict'
import { chromium } from 'playwright'
import { mkdirSync, writeFileSync } from 'node:fs'

const base = (process.env.WORKSPACE_URL ?? 'http://127.0.0.1:5174').replace(/\/$/, '')
const out = process.env.REDESIGN_OUT ?? '../.local/redesign'
const routes = [
  ['/admin/overview', '运行状态'], ['/admin/merchant/outcomes', '结果度量'],
  ['/admin/merchant/gaps', '内容缺口'], ['/admin/access/accounts', '账号与权限'],
  ['/admin/access/requests', '权限申请'], ['/admin/access/audit', '授权审计'],
  ['/admin/merchant/shops', '店铺范围'], ['/admin/merchant/sources', '资料来源'],
  ['/admin/merchant/ingestion', '摄取任务'], ['/admin/merchant/reviews', '版本审核'],
]
const viewports = [{width:1440,height:900},{width:1280,height:800},{width:1024,height:768},{width:390,height:844}]
const browser = await chromium.launch({headless:true})
const page = await browser.newPage({viewport:viewports[0]})
const results = []
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
  for (const theme of ['light','dark']) {
    const context = await browser.newContext({viewport:{width:1440,height:900}})
    await context.addInitScript((value) => localStorage.setItem('ecr-theme', value), theme)
    const shot = await context.newPage()
    for (const [name,path] of [['outcomes','/admin/merchant/outcomes'],['gaps','/admin/merchant/gaps'],['login','/login']]) {
      await shot.goto(base+path,{waitUntil:'networkidle'})
      await shot.waitForTimeout(1200)
      await shot.screenshot({path:`${out}/${theme}-${name}.png`,fullPage:true})
    }
    await context.close()
  }
  const report = {status:'PASS',route_count:routes.length,viewports,results,theme:{before,after},
    screenshots:['light-outcomes.png','light-gaps.png','light-login.png','dark-outcomes.png','dark-gaps.png','dark-login.png']}
  writeFileSync(`${out}/browser-matrix.json`, JSON.stringify(report,null,2))
  console.log(JSON.stringify({status:'PASS',route_count:routes.length,viewports,theme:report.theme}))
} finally { await browser.close() }
