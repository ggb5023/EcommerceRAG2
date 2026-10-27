// Isolated accept DB + mock provider; this verifies L2 UI and stale handling.
import assert from 'node:assert/strict'
import {chromium} from 'playwright'
import {createHash,randomUUID} from 'node:crypto'
import {execFileSync} from 'node:child_process'
import {mkdir,writeFile,readFile} from 'node:fs/promises'
import path from 'node:path'
import {fileURLToPath} from 'node:url'
const workspace=(process.env.WORKSPACE_URL??'http://127.0.0.1:5174').replace(/\/$/,'')
const env=Object.fromEntries((await readFile('/etc/ecommerce-rag/import-integration.env','utf8')).split('\n').filter(l=>l.includes('=')).map(l=>{const i=l.indexOf('=');return[l.slice(0,i),l.slice(i+1)]}))
const output=path.resolve(fileURLToPath(new URL(`../../.local/review-suggestion/browser/${randomUUID()}/`,import.meta.url)));await mkdir(output,{recursive:true,mode:0o700})
const h=(s)=>createHash('sha256').update(s).digest('hex')
const sql=[`DELETE FROM review_suggestion WHERE version_id IN (SELECT v.id FROM document_version v JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id WHERE d.logical_key LIKE 'l2-seed-%')`,`DELETE FROM chunk WHERE doc_id IN (SELECT id FROM document WHERE logical_key LIKE 'l2-seed-%')`,`DELETE FROM document_version WHERE document_id IN (SELECT id FROM document WHERE logical_key LIKE 'l2-seed-%')`,`DELETE FROM document WHERE logical_key LIKE 'l2-seed-%'`]
for(let i=1;i<=4;i++){
 const auth=i<4?`jsonb_build_object('authorization',jsonb_build_object('basis','license','reference','ref-${i}','checked_at','2026-10-01'))`:`'{}'::jsonb`
 sql.push(`WITH s AS (SELECT id FROM source WHERE tenant_id=(SELECT id FROM tenant WHERE external_id='v2-demo-tenant') AND shop_id='shop-demo' AND type='admin_manifest' LIMIT 1), d AS (INSERT INTO document(tenant_id,source_id,logical_key,title,meta_json) SELECT t.id,s.id,'l2-seed-${i}','Suggestion ${i}',${auth} FROM tenant t,s WHERE t.external_id='v2-demo-tenant' RETURNING id,tenant_id), v AS (INSERT INTO document_version(tenant_id,document_id,source_hash,parser_version,chunk_rule_version,embedding_model,pipeline_version,object_key,status,disclosure_class,review_status) SELECT d.tenant_id,d.id,'${h('v'+i)}','seed','seed','seed','seed','seed','ready','internal_only','pending' FROM d RETURNING id,tenant_id) INSERT INTO chunk(version_id,doc_id,tenant_id,chunk_index,section_seq,section_chunk_index,content,embed_text,token_count,content_type,split_reason,embedding_model,embedding_dim,tokenizer_id,chunk_hash) SELECT v.id,d.id,d.tenant_id,0,1,0,'seed body ${i}','seed body ${i}',2,'paragraph','single','seed',1024,'seed','${h('c'+i)}' FROM v,d`)
}
execFileSync('psql',[env.V2_MIGRATION_DSN,'-v','ON_ERROR_STOP=1','-c',sql.join(';')])
const browser=await chromium.launch({executablePath:process.env.CHROMIUM_PATH??'/root/.cache/ms-playwright/chromium-1187/chrome-linux/chrome',headless:true})
const context=await browser.newContext({viewport:{width:1440,height:900}});const page=await context.newPage();const checks=[];const errors=[];page.on('pageerror',e=>errors.push(String(e)));page.on('dialog',d=>d.accept())
async function step(name,fn){try{await fn();checks.push({name,status:'PASS'});console.log('PASS',name)}catch(e){checks.push({name,status:'FAIL'});await page.screenshot({path:path.join(output,`${name}.png`)}).catch(()=>{});throw e}}
try{
 await step('seed-suggestions',async()=>{
  await page.goto(workspace+'/admin/merchant/reviews',{waitUntil:'networkidle'});await page.getByRole('heading',{name:'版本审核',level:1}).waitFor()
  for(const i of [1,2,3,4]){const row=page.locator('.version-row').filter({hasText:`Suggestion ${i}`}).first();await row.getByRole('button',{name:'生成建议'}).click();await page.waitForTimeout(800)}
  await page.reload({waitUntil:'networkidle'});await page.getByText(/AI 建议/).first().waitFor()
 })
 await step('filter-needs-revision',async()=>{
  await page.locator('select').filter({has:page.locator('option[value="needs_revision"]')}).selectOption('needs_revision');await page.waitForTimeout(800)
  assert.ok(await page.locator('.version-row').filter({hasText:'Suggestion 4'}).count()>=1)
  await page.locator('select').filter({has:page.locator('option[value="needs_revision"]')}).selectOption('');await page.waitForTimeout(800)
 })
 await step('batch-confirm-with-stale',async()=>{
  const staleId=execFileSync('psql',[env.V2_MIGRATION_DSN,'-Atc',`SELECT v.id FROM document_version v JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id WHERE d.logical_key='l2-seed-2' ORDER BY v.id DESC LIMIT 1`]).toString().trim()
  execFileSync('psql',[env.V2_MIGRATION_DSN,'-c',`UPDATE document_version SET source_hash=repeat('f',64) WHERE id=${staleId}`])
  for(const i of [1,2,3]){const row=page.locator('.version-row').filter({hasText:`Suggestion ${i}`}).first();await row.locator('input[type=checkbox]').check()}
  await page.getByRole('button',{name:/批量确认（3）/}).click();const dialog=page.getByRole('dialog',{name:'批量确认审核'});await dialog.waitFor();await dialog.locator('textarea').fill('Browser batch confirmation with independent merchant reason.')
  await dialog.getByRole('button',{name:'确认审核'}).click();await page.getByText(/成功 2/).waitFor({timeout:30000});assert.ok(await page.getByText(/stale 1/).count())
 })
 await step('stale-regenerate-and-publish-remains-manual',async()=>{
  await page.getByRole('button',{name:'重新生成建议'}).click();await page.waitForTimeout(800)
  const row=page.locator('.version-row').filter({hasText:'Suggestion 1'}).first();await row.click();await page.getByRole('button',{name:'发布此版本'}).waitFor({timeout:30000})
 })
 const report={status:'PASS',checks,check_count:checks.length,page_errors:errors,screenshots:output,reviewer_kind:'agent',human_review_completed:false}
 await page.screenshot({path:path.join(output,'reviews.png')});await writeFile(path.join(output,'report.json'),JSON.stringify(report,null,2),{mode:0o600,flag:'wx'});console.log(JSON.stringify(report))
}catch(e){const report={status:'NOT_PASSED',checks,error:String(e),screenshots:output};await writeFile(path.join(output,'report.json'),JSON.stringify(report,null,2),{mode:0o600,flag:'wx'});console.error(JSON.stringify(report));process.exitCode=1}finally{await browser.close()}
