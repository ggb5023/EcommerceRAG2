// Isolated accept DB + mock provider is intentional: this test verifies the
// import wizard and batch/database state; real provider capability is proven
// by the lifecycle run.
import assert from 'node:assert/strict'
import {chromium} from 'playwright'
import {mkdir,writeFile,readdir,readFile} from 'node:fs/promises'
import {execFileSync} from 'node:child_process'
import path from 'node:path'
import {fileURLToPath} from 'node:url'
import {randomUUID} from 'node:crypto'
const workspace=(process.env.WORKSPACE_URL??'http://127.0.0.1:5174').replace(/\/$/,'')
assert.ok(['127.0.0.1','localhost'].includes(new URL(workspace).hostname))
const root=path.resolve(fileURLToPath(new URL('../../',import.meta.url)))
const fixture=path.join(root,'tests/fixtures/import-batch-20')
const output=path.resolve(fileURLToPath(new URL(`../../.local/import-wizard/browser/${randomUUID()}/`,import.meta.url)))
await mkdir(output,{recursive:true,mode:0o700})
const env=Object.fromEntries((await readFile(process.env.ECR_BROWSER_ENV??'/etc/ecommerce-rag/import-integration.env','utf8')).split('\n').filter(l=>l.includes('=')).map(l=>{const i=l.indexOf('=');return[l.slice(0,i),l.slice(i+1)]}))
const manifestText=await readFile(path.join(fixture,'manifest.yaml'),'utf8')
const importedDocuments=[...manifestText.matchAll(/- document_id: (\S+)/g)].map(m=>m[1])
const seedHash=(await import('node:crypto')).createHash('sha256').update(await readFile(path.join(fixture,'doc-03.md'))).digest('hex')
execFileSync('psql',[env.V2_MIGRATION_DSN,'-v','ON_ERROR_STOP=1','-c',`
DO $$ DECLARE tid bigint; sid bigint; did bigint;
BEGIN
 SELECT id INTO tid FROM tenant WHERE external_id='v2-demo-tenant';
 SELECT id INTO sid FROM source WHERE tenant_id=tid AND shop_id='shop-demo' LIMIT 1;
 IF sid IS NULL THEN INSERT INTO source(tenant_id,shop_id,type) VALUES(tid,'shop-demo','admin_manifest') RETURNING id INTO sid; END IF;
 DELETE FROM document_version WHERE tenant_id=tid AND document_id IN (SELECT id FROM document WHERE tenant_id=tid AND logical_key='browser-seed-duplicate');
 DELETE FROM document WHERE tenant_id=tid AND logical_key='browser-seed-duplicate';
 INSERT INTO document(tenant_id,source_id,logical_key,title) VALUES(tid,sid,'browser-seed-duplicate','Seed duplicate') RETURNING id INTO did;
 INSERT INTO document_version(tenant_id,document_id,source_hash,parser_version,chunk_rule_version,embedding_model,pipeline_version,object_key,status,disclosure_class,review_status)
 VALUES(tid,did,'${seedHash}','seed','seed','seed','seed','seed','ready','internal_only','pending');
END $$;`])
const browser=await chromium.launch({executablePath:process.env.CHROMIUM_PATH??'/root/.cache/ms-playwright/chromium-1187/chrome-linux/chrome',headless:true})
const context=await browser.newContext({viewport:{width:1440,height:900}})
const page=await context.newPage();const listPage=await context.newPage()
const checks=[];const errors=[];page.on('pageerror',e=>errors.push(String(e)));page.on('dialog',d=>d.accept())
async function step(name,fn){try{await fn();checks.push({name,status:'PASS'});console.log('PASS',name)}catch(e){checks.push({name,status:'FAIL'});await page.screenshot({path:path.join(output,`${name}.png`)}).catch(()=>{});throw e}}
async function files(names){return Promise.all(names.map(async name=>({name,mimeType:'text/markdown',buffer:await readFile(path.join(fixture,name))})))}
let wizard
try{
 await step('enable-model-switch',async()=>{
  await page.goto(workspace+'/admin/merchant/shops',{waitUntil:'networkidle'})
  await page.getByRole('heading',{name:'店铺范围',level:1}).waitFor()
  const settings=page.locator('section.import-form').filter({hasText:'模型辅助开关'})
  const box=settings.locator('input[type=checkbox]').first()
  if(!(await box.isChecked()))await box.check()
  await settings.locator('textarea').fill('Browser enable model assistance for isolated batch flow.')
  await settings.getByRole('button',{name:'保存开关'}).click()
  await page.getByText('设置已保存。').waitFor()
 })
 await step('upload-preview-four-issues',async()=>{
  await page.goto(workspace+'/admin/merchant/sources',{waitUntil:'networkidle'})
  wizard=page.locator('section.import-form').filter({hasText:'导入预检（三步）'})
  await wizard.locator('input[accept=".yaml,.yml"]').setInputFiles(path.join(fixture,'manifest.yaml'))
  const names=(await readdir(fixture)).filter(n=>n.endsWith('.md')).sort()
  await wizard.locator('input[accept=".docx,.markdown,.md,.csv"]').setInputFiles(await files(names))
  await wizard.getByRole('button',{name:'开始预检'}).click()
  await page.getByText(/预检完成：/).waitFor({timeout:30000})
  const details=wizard.locator('details');const codes=[]
  for(let i=0;i<await details.count();i++)codes.push((await details.nth(i).locator('summary').innerText()).split('（')[0])
  for(const code of ['missing_source','expired_content','duplicate_import','missing_disclosure_basis'])assert.ok(codes.includes(code),`${code}: ${codes}`)
  await details.first().locator('summary').click();assert.ok((await details.first().innerText()).length>0,'snippet non-empty')
 })
 await step('confirm-and-command-done',async()=>{
  await wizard.getByRole('button',{name:'确认导入'}).click()
  await wizard.getByText('已完成',{exact:false}).waitFor({timeout:90000})
 })
 await step('reviews-show-twenty-pending',async()=>{
  await listPage.goto(workspace+'/admin/merchant/reviews',{waitUntil:'networkidle'})
  await listPage.getByRole('heading',{name:'版本审核',level:1}).waitFor()
  const rows=listPage.locator('.version-row').filter({hasText:/Document \d+/})
  for(let i=0;i<40&&await rows.count()<20;i++)await listPage.waitForTimeout(500)
  // The queue may already hold historical versions, so the assertion is about the
  // twenty documents this case imported, not about a fixed row count.
  assert.ok(await rows.count()>=20,`queue shows ${await rows.count()} rows, want at least 20`)
  assert.ok((await rows.first().innerText()).includes('待处理'))
  const pending=new Set()
  const response=await listPage.request.get(workspace+'/admin/v1/merchant/reviews')
  assert.equal(response.status(),200)
  for(const item of (await response.json()).items??[])if(item.review_status==='pending')pending.add(item.document_id)
  for(const document of importedDocuments)assert.ok(pending.has(document),`${document} is not pending in the review queue`)
 })
 await step('revoke-whole-batch',async()=>{
  await wizard.getByRole('button',{name:'撤回整包'}).click()
  await page.getByText('整包已撤回。').waitFor()
  assert.ok(await wizard.evaluate(el=>el.classList.contains('revoked')),'batch row must be greyed')
  assert.ok((await wizard.innerText()).includes('撤回时间'),'revoked time missing')
  await listPage.reload({waitUntil:'networkidle'})
  await listPage.getByRole('heading',{name:'版本审核',level:1}).waitFor()
  const pending=listPage.locator('.version-row').filter({hasText:/Document \d+/}).filter({hasText:'待审核'})
  assert.equal(await pending.count(),0)
 })
 await page.screenshot({path:path.join(output,'wizard.png')})
 await listPage.screenshot({path:path.join(output,'reviews.png')})
 const report={status:'PASS',checks,check_count:checks.length,reviewer_kind:'agent',human_review_completed:false,real_service_acceptance:false,screenshots:output,page_errors:errors}
 await writeFile(path.join(output,'report.json'),JSON.stringify(report,null,2),{mode:0o600,flag:'wx'});console.log(JSON.stringify(report))
}catch(e){const report={status:'NOT_PASSED',checks,error:String(e),screenshots:output};await writeFile(path.join(output,'report.json'),JSON.stringify(report,null,2),{mode:0o600,flag:'wx'});console.error(JSON.stringify(report));process.exitCode=1}finally{await browser.close()}
