// Actual Keycloak login, merchant upload, customer queue and staff response.
// Captures stay in the explicit protected output directory; no API substitution.
import assert from 'node:assert/strict'
import {chromium} from 'playwright'
import {readFile,writeFile,mkdir,chmod,lstat} from 'node:fs/promises'
import {createHash,randomUUID} from 'node:crypto'
const runtime=process.env.CUSTOMER_RUNTIME_DIR;assert.ok(runtime)
const base=process.env.CUSTOMER_BASE_URL??'https://merchant.example.test:18443'
const output=process.env.CUSTOMER_REPORT_DIR;assert.ok(output)
await mkdir(output,{recursive:true,mode:0o700});assert.equal((await lstat(output)).mode&0o777,0o700)
const accounts=JSON.parse(await readFile(runtime+'/bootstrap/accounts.json','utf8')).accounts
const browser=await chromium.launch({executablePath:process.env.CHROMIUM_PATH??'/root/.cache/ms-playwright/chromium-1187/chrome-linux/chrome',headless:true})
const checks=[];const errors=[]
const admin=await browser.newContext({ignoreHTTPSErrors:true});const staff=await browser.newContext({ignoreHTTPSErrors:true});const customer=await browser.newContext({ignoreHTTPSErrors:true})
for(const ctx of [admin,staff,customer])ctx.on('page',p=>p.on('pageerror',e=>errors.push(e.message)))
const adminPage=await admin.newPage();const staffPage=await staff.newPage();const customerPage=await customer.newPage()
async function login(page,user,path){
 await page.goto(base+path)
 const state=await page.evaluate(()=>fetch('/v1/auth/session').then(r=>r.json()))
 if(state.authenticated)return
 await page.goto(base+`/v1/auth/login?app=${path.startsWith('/admin')?'admin':'customer'}&return_to=${path}`)
 if(new URL(page.url()).pathname!==path){
  await page.locator('#username').fill(user.username);await page.locator('#password').fill(user.password);await page.locator('#kc-login').click()
 }
 await page.waitForURL(base+path,{timeout:20000})
}
async function api(page,path,body){return page.evaluate(async({path,body})=>{const s=await fetch('/v1/auth/session').then(r=>r.json());const r=await fetch(path,{credentials:'same-origin',...(body===undefined?{}:{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':s.csrf_token,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify(body)})});return {status:r.status,data:await r.json()}},{path,body})}
async function adminApi(path,body){await login(adminPage,accounts.find(a=>a.username==='admin'),'/admin/merchant/reviews');const response=await api(adminPage,path,body);assert.ok(response.status<400,`admin API ${response.status}`);return response.data}
try{
 await login(adminPage,accounts.find(a=>a.username==='admin'),'/admin/merchant/sources')
 // Import actual browser-produced package; this also covers CSRF on multipart.
 await adminPage.locator('input[accept=".docx,.md,.markdown,.csv"]').setInputFiles([
  '../data/synthetic/customer-service-v1/care.md','../data/synthetic/customer-service-v1/materials.csv',runtime+'/acceptance/returns.docx'])
 await adminPage.getByRole('button',{name:'上传并解析'}).click()
 await adminPage.getByText(/已提交 3 份资料/).waitFor({timeout:20000})
 checks.push('browser_md_csv_docx_upload')
 await login(adminPage,accounts.find(a=>a.username==='admin'),'/admin/merchant/sources')
 const faq=JSON.parse(await readFile('../eval/customer-service-cases.v1.json','utf8'))
 await adminPage.getByPlaceholder('returns-policy').fill('gift-card')
 await adminPage.locator('form').filter({has:adminPage.getByRole('heading',{name:'FAQ 表单'})}).locator('input').nth(1).fill('礼盒是否含贺卡？')
 await adminPage.locator('form').filter({has:adminPage.getByRole('heading',{name:'FAQ 表单'})}).locator('textarea').fill('岚叶礼盒包含一张空白贺卡，不提供代写服务。礼盒外包装采用纸盒，可按当地分类要求回收。')
 await adminPage.getByRole('button',{name:'保存 FAQ 新版本'}).click();await adminPage.getByText(/已提交 1 份资料/).waitFor({timeout:20000});checks.push('browser_faq_upload')
 let versions=[]
 for(let i=0;i<90;i++){const v=await adminApi('/admin/v1/merchant/reviews');versions=v.items.filter(v=>v.shop_id==='main-shop');if(versions.length===4&&versions.every(v=>['ready','active'].includes(v.status)))break;await new Promise(r=>setTimeout(r,1000))}
 assert.equal(versions.length,4)
 for(const v of versions){if(v.status==='active'){assert.equal(v.review_status,'approved');continue;}assert.equal(v.status,'ready');const detail=await adminApi('/admin/v1/merchant/reviews/'+v.version_id);assert.ok(detail.content.length);const session=await adminApi('/admin/v1/session');await adminApi('/admin/v1/merchant/versions/'+v.version_id+'/review',{confirm:true,decision:'approved',disclosure_class:'external_allowed',external_allowed:true,expected_hash:v.source_hash,expected_fencing_epoch:detail.fencing_epoch,permission_revision:session.permission_revision,reason:'Controlled synthetic customer milestone review approved.'});const current=await adminApi('/admin/v1/session');await adminApi('/admin/v1/merchant/versions/'+v.version_id+'/publish',{confirm:true,tenant_id:current.tenant_id,shop_id:v.shop_id,document_id:v.document_id,version_hash:v.source_hash,permission_revision:current.permission_revision,expected_fencing_epoch:detail.fencing_epoch,reason:'Controlled synthetic customer milestone publication.'})}
 checks.push('four_real_ingestion_review_publication_lifecycles')
 await customerPage.goto(base+'/shop/my-shop');await customerPage.locator('#question').waitFor()
 await customerPage.locator('#question').fill('岚叶杯的容量是多少？');await customerPage.getByRole('button',{name:'发送',exact:true}).click()
 await customerPage.locator('article.assistant .message-text').filter({hasText:'450'}).waitFor({timeout:75000})
 await customerPage.getByRole('button',{name:/查看来源/}).last().click();await customerPage.getByRole('complementary',{name:'回答来源'}).waitFor()
 const checkedAnswer=await customerPage.evaluate(async()=>{const c=await fetch('/v1/customer/conversations').then(r=>r.json());return fetch('/v1/customer/conversations/'+c.conversations[0].id).then(r=>r.json())})
 await writeFile(output+'/browser-answer-capture.json',JSON.stringify(checkedAnswer,null,2),{mode:0o600})
 checks.push('browser_real_ai_answer_and_authorized_sources')
 await customerPage.getByRole('button',{name:'联系人工客服'}).click();await customerPage.getByText(/正在等待人工客服领取/).waitFor()
 const cid=(await customerPage.evaluate(()=>fetch('/v1/customer/conversations').then(r=>r.json()))).conversations[0].id
 await login(staffPage,accounts.find(a=>a.username==='support'),'/support');await staffPage.getByRole('button',{name:new RegExp(cid.slice(-8))}).click();await staffPage.getByRole('button',{name:'领取会话'}).click();await staffPage.getByRole('button',{name:'发送人工回复'}).waitFor()
 await staffPage.locator('textarea').fill('您好，我是人工客服，已接到您的咨询。');await staffPage.getByRole('button',{name:'发送人工回复'}).click();await customerPage.getByText('您好，我是人工客服，已接到您的咨询。').waitFor({timeout:10000});checks.push('actual_customer_handoff_claim_human_reply')
 await customerPage.reload();await customerPage.getByText('您好，我是人工客服，已接到您的咨询。').waitFor();checks.push('customer_cookie_reload_recovery')
 await staffPage.reload();await staffPage.getByRole('button',{name:'发送人工回复'}).waitFor();checks.push('staff_claim_reload_recovery')
 await staffPage.getByRole('button',{name:'交回 AI'}).click();await customerPage.getByText('AI 知识客服',{exact:true}).waitFor();checks.push('explicit_return_to_ai')
 for(const [name,ctx] of [['staff',staff],['admin',admin],['customer',customer]]){await ctx.storageState({path:output+'/'+name+'-browser-state.json'});await chmod(output+'/'+name+'-browser-state.json',0o600)}
 assert.deepEqual(errors,[])
 await writeFile(output+'/browser-report.json',JSON.stringify({status:'PASS',checks,real_service_acceptance:false,browser_tls:'explicit_local_self_signed_test',generation_review_completed:false},null,2),{mode:0o600})
 console.log(JSON.stringify({status:'PASS',checks,real_service_acceptance:false}))
}catch(e){await writeFile(output+'/browser-report.json',JSON.stringify({status:'NOT_PASSED',checks,error_class:e.constructor.name,real_service_acceptance:false}),{mode:0o600});console.error('NOT_PASSED browser milestone; inspect private capture');await writeFile(output+'/failure-diagnostic.txt',String(e),{mode:0o600});await adminPage.screenshot({path:output+'/failure.png'});await chmod(output+'/failure.png',0o600);await writeFile(output+'/failure-context.txt',await adminPage.locator('body').innerText(),{mode:0o600});process.exitCode=1}
finally{await browser.close()}
