// Resume a prior opaque guest cookie after isolated database/file restoration.
import assert from 'node:assert/strict'
import {chromium} from 'playwright'
import {readFile,writeFile,mkdir} from 'node:fs/promises'
import {createHash} from 'node:crypto'
const runtime=process.env.CUSTOMER_RUNTIME_DIR;const prior=process.env.CUSTOMER_PRIOR_BROWSER_DIR;const out=process.env.CUSTOMER_REPORT_DIR;assert.ok(runtime&&prior&&out)
const base=process.env.CUSTOMER_BASE_URL??'https://merchant.example.test:18443';await mkdir(out,{mode:0o700})
const browser=await chromium.launch({executablePath:process.env.CHROMIUM_PATH??'/root/.cache/ms-playwright/chromium-1187/chrome-linux/chrome',headless:true})
const expected=JSON.parse(await readFile(prior+'/browser-answer-capture.json'));const checks=[]
try{
 const customer=await browser.newContext({ignoreHTTPSErrors:true,storageState:prior+'/customer-browser-state.json'});const page=await customer.newPage();await page.goto(base+'/shop/my-shop');await page.locator('#question').waitFor()
 const snapshot=await page.evaluate(id=>fetch('/v1/customer/conversations/'+id).then(r=>r.json()),expected.conversation.id)
 for(const old of expected.messages){const actual=snapshot.messages.find(m=>m.id===old.id);assert.ok(actual);assert.equal(actual.text,old.text);assert.deepEqual(actual.citations,old.citations)}
 assert.equal(snapshot.messages.filter(m=>m.source==='agent').length,1);assert.equal(snapshot.conversation.active_execution_id,undefined);checks.push('prior_guest_cookie_and_messages_restore_without_ai_reexecution')
 const account=JSON.parse(await readFile(runtime+'/bootstrap/accounts.json')).accounts.find(a=>a.username==='support');const staff=await (await browser.newContext({ignoreHTTPSErrors:true})).newPage();await staff.goto(base+'/v1/auth/login?app=customer&return_to=/support');await staff.locator('#username').fill(account.username);await staff.locator('#password').fill(account.password);await staff.locator('#kc-login').click();await staff.waitForURL(base+'/support');const auth=await staff.evaluate(()=>fetch('/v1/auth/session').then(r=>r.json()));assert.equal(auth.authenticated,true);checks.push('restored_keycloak_account_and_go_staff_login')
 const raw=JSON.stringify(snapshot,null,2);await writeFile(out+'/capture.json',raw,{mode:0o600});await writeFile(out+'/report.json',JSON.stringify({status:'PASS',checks,capture_sha256:createHash('sha256').update(raw).digest('hex'),real_service_acceptance:false},null,2),{mode:0o600});console.log(JSON.stringify({status:'PASS',checks}))
}catch(e){await writeFile(out+'/failure.txt',String(e),{mode:0o600});await writeFile(out+'/report.json',JSON.stringify({status:'NOT_PASSED',checks,error_class:e.constructor.name,real_service_acceptance:false}),{mode:0o600});console.error('NOT_PASSED restored browser flow');process.exitCode=1}finally{await browser.close()}
