// Real Go/Keycloak/Compose controls. No SQL state writes or response substitution.
import assert from 'node:assert/strict'
import {chromium} from 'playwright'
import {readFile,writeFile,mkdir} from 'node:fs/promises'
import {randomUUID,createHash} from 'node:crypto'
import {execFileSync} from 'node:child_process'
const runtime=process.env.CUSTOMER_RUNTIME_DIR;const out=process.env.CUSTOMER_REPORT_DIR;assert.ok(runtime&&out)
const base=process.env.CUSTOMER_BASE_URL??'https://merchant.example.test:18443'
await mkdir(out,{mode:0o700});const accounts=JSON.parse(await readFile(runtime+'/bootstrap/accounts.json')).accounts
const browser=await chromium.launch({executablePath:process.env.CHROMIUM_PATH??'/root/.cache/ms-playwright/chromium-1187/chrome-linux/chrome',headless:true})
const contexts=await Promise.all([0,1,2,3].map(()=>browser.newContext({ignoreHTTPSErrors:true})))
const [customer,other,admin,support]=await Promise.all(contexts.map(c=>c.newPage()));const checks=[]
async function login(page,name){await page.goto(base+'/v1/auth/login?app=customer&return_to=/support');if(new URL(page.url()).pathname!='/support'){const a=accounts.find(a=>a.username===name);await page.locator('#username').fill(a.username);await page.locator('#password').fill(a.password);await page.locator('#kc-login').click()}await page.waitForURL(base+'/support')}
async function api(page,path,body,csrf,idem=randomUUID()) {return page.evaluate(async({path,body,csrf,idem})=>{const r=await fetch(path,{...(body===undefined?{}:{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf??'','Idempotency-Key':idem},body:JSON.stringify(body)})});return {status:r.status,data:await r.json()}},{path,body,csrf,idem})}
async function staff(page,path,body,idem){const auth=(await api(page,'/v1/auth/session')).data;return api(page,path,body,auth.csrf_token,idem)}
async function waitState(id,state){for(let i=0;i<160;i++){const r=await api(customer,'/v1/customer/conversations/'+id);if(r.data.conversation.state===state&&!r.data.conversation.active_execution_id)return r.data;await new Promise(r=>setTimeout(r,1000))}throw Error('state timeout')}
try{
 await customer.goto(base+'/');await other.goto(base+'/');await login(admin,'admin');await login(support,'support')
 const session=await api(customer,'/v1/customer/session',{shop_slug:'my-shop'});assert.equal(session.status,201);const csrf=session.data.csrf_token
 await api(other,'/v1/customer/session',{shop_slug:'my-shop'})
 const id=(await api(customer,'/v1/customer/conversations',{},csrf)).data.conversation_id;const path='/v1/customer/conversations/'+id;const sp='/v1/support/conversations/'+id
 assert.equal((await api(customer,path+'/messages',{text:'岚叶杯的容量是多少？'},'wrong')).status,403);checks.push('csrf_rejected')
 const key=randomUUID();const first=await api(customer,path+'/messages',{text:'岚叶杯的容量是多少？'},csrf,key);assert.equal(first.status,202)
 const repeated=await api(customer,path+'/messages',{text:'岚叶杯的容量是多少？'},csrf,key);assert.equal(repeated.data.message_id,first.data.message_id);assert.equal(repeated.data.replayed,true)
 assert.equal((await api(customer,path+'/messages',{text:'different'},csrf,key)).status,409);checks.push('message_idempotency_and_collision')
 // Explicit handoff cancels this actual execution; the old completion cannot publish.
 assert.equal((await api(customer,path+'/handoff',{},csrf)).status,200)
 let snapshot=await waitState(id,'WAITING_HUMAN');assert.equal(snapshot.messages.filter(m=>m.source==='assistant').length,0)
 assert.equal((await api(other,path)).status,404);checks.push('cross_visitor_denied')
 const waiting=await api(customer,path+'/messages',{text:'请人工核实后回复。'},csrf);assert.equal(waiting.data.execution_id,'');checks.push('waiting_messages_do_not_start_ai')
 const attempts=await Promise.all([admin,support].map(p=>staff(p,sp+'/claim',{epoch:snapshot.conversation.epoch})));assert.deepEqual(attempts.map(r=>r.status).sort(),[200,409]);const winner=attempts[0].status===200?admin:support;let epoch=attempts.find(r=>r.status===200).data.epoch;checks.push('two_real_staff_atomic_claim')
 const replyKey=randomUUID();const reply=await staff(winner,sp+'/messages',{epoch,text:'人工已收到，将核实资料后回复。'},replyKey);assert.equal(reply.status,201)
 const again=await staff(winner,sp+'/messages',{epoch,text:'人工已收到，将核实资料后回复。'},replyKey);assert.equal(again.data.message_id,reply.data.message_id)
 snapshot=(await api(customer,path)).data;assert.equal(snapshot.messages.filter(m=>m.source==='agent').length,1);checks.push('human_reply_once_and_received')
 console.log('checking real 120-second claim expiry')
 // The pages stay on the unselected queue: no selected owner heartbeat is sent.
 await new Promise(r=>setTimeout(r,125000));snapshot=await waitState(id,'WAITING_HUMAN')
 assert.equal((await staff(winner,sp+'/messages',{epoch,text:'expired claim'})).status,409);checks.push('real_lease_expiry_and_stale_epoch')
 const before=snapshot.messages.filter(m=>m.source!=='system').map(m=>m.id);const claim=await staff(winner,sp+'/claim',{epoch:snapshot.conversation.epoch});assert.equal(claim.status,200);epoch=claim.data.epoch
 assert.ok(process.env.CUSTOMER_ALLOW_OWNED_RESTART==='1','explicit owned-project restart test required')
 execFileSync('docker',['compose','--env-file',runtime+'/compose.env','-f','../docker/selfhost/compose.yml','restart','gateway'],{stdio:'pipe'})
 await new Promise(r=>setTimeout(r,5000));snapshot=await waitState(id,'WAITING_HUMAN');assert.deepEqual(snapshot.messages.filter(m=>m.source!=='system').map(m=>m.id),before)
 assert.equal(snapshot.messages.filter(m=>m.source==='agent').length,1);assert.equal(snapshot.messages.filter(m=>m.source==='assistant').length,0);assert.equal((await api(winner,'/v1/auth/session')).data.authenticated,false);checks.push('restart_cookie_messages_queue_and_staff_relogin')
 await login(winner,winner===admin?'admin':'support');const claimed=await staff(winner,sp+'/claim',{epoch:snapshot.conversation.epoch});assert.equal(claimed.status,200)
 assert.equal((await staff(winner,sp+'/close',{epoch:claimed.data.epoch})).status,200);assert.equal((await api(customer,path+'/messages',{text:'closed'},csrf)).status,409);checks.push('closed_conversation_rejects_writes')
 const capture={checks,snapshot};await writeFile(out+'/capture.json',JSON.stringify(capture,null,2),{mode:0o600})
 await writeFile(out+'/report.json',JSON.stringify({status:'PASS',checks,capture_sha256:createHash('sha256').update(JSON.stringify(capture,null,2)).digest('hex'),real_service_acceptance:false},null,2),{mode:0o600});console.log(JSON.stringify({status:'PASS',checks}))
}catch(e){await writeFile(out+'/report.json',JSON.stringify({status:'NOT_PASSED',checks,error_class:e.constructor.name,real_service_acceptance:false}),{mode:0o600});await writeFile(out+'/failure.txt',String(e),{mode:0o600});console.error('NOT_PASSED customer control; inspect protected capture');process.exitCode=1}
finally{await browser.close()}
