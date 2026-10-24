// Browser-only fixture checks; real authorization/model evidence is separate.
import assert from 'node:assert/strict'
import {mkdir,writeFile} from 'node:fs/promises'
import {chromium} from 'playwright'
const base=process.env.KNOWLEDGE_ADMIN_URL??process.env.WORKSPACE_URL??'http://127.0.0.1:5173'
const out=process.env.KNOWLEDGE_BROWSER_REPORT_DIR
assert.ok(out,'fresh protected report directory required')
await mkdir(out,{mode:0o700})
const browser=await chromium.launch({executablePath:process.env.CHROMIUM_PATH??'/root/.cache/ms-playwright/chromium-1187/chrome-linux/chrome',headless:true})
const page=await browser.newPage();const checks=[];const errors=[]
page.on('pageerror',e=>errors.push(e.name))
let revoked=false
const session={tenant_id:'1',shop_id:'shop-demo',allowed_shop_ids:['shop-demo'],admin_roles:['merchant_admin','knowledge_reviewer'],permission_revision:'r1',admin_role_catalog:[],is_mock:true}
const shop={shop_id:'shop-demo',name:'Fixture shop',status:'active',customer_chat_enabled:true,customer_chat_url:'http://127.0.0.1:19573/shop/my-shop',customer_published_knowledge_available:false}
const row={version_id:'42',document_id:'guide',title:'Fixture guide',shop_id:'shop-demo',source_hash:'a'.repeat(64),fencing_epoch:1,status:'ready',review_status:'pending',disclosure_class:'unclassified',chunk_count:26}
const chunks=Array.from({length:26},(_,i)=>({chunk_index:i,chunk_id:`db-chunk-${i+1}`,content:`Preview ${i} <script>window.previewLeak=true</script>`,heading_path:'Guide / Care',content_type:i===0?'table':'text',split_reason:'element_boundary',char_start:0,char_end:15,source_position:i===25?null:{line_start:i+1,line_end:i+1},parse_hints:[{kind:'info',code:'code_language'}],...(i===0?{table_body:[['Column','Value'],['SKU','A1']]}:{})}))
await page.route('**/v1/auth/session',r=>r.fulfill({json:{enabled:false}}))
await page.route('**/admin/v1/**',r=>{
 const path=new URL(r.request().url()).pathname
 if(revoked)return r.fulfill({status:403,json:{error:{code:'admin_scope_denied'}}})
 if(path==='/admin/v1/session')return r.fulfill({json:session})
 if(path==='/admin/v1/merchant/shops')return r.fulfill({json:{items:[shop]}})
 if(path==='/admin/v1/merchant/reviews/42')return r.fulfill({json:{...row,content:chunks}})
 return r.fulfill({json:{items:[row]}})
})
try{
 await page.goto(base+'/admin/merchant/reviews');await page.locator('.version-row').click()
 const preview=page.getByRole('region',{name:'知识解析预览'})
 await preview.waitFor();assert.equal(await preview.locator('article').count(),12)
 await preview.getByRole('button',{name:'下一段'}).click();assert.equal(await preview.locator('article').count(),12)
 await preview.getByRole('button',{name:'下一段'}).click();assert.equal(await preview.locator('article').count(),2)
 await preview.getByText(/来源位置不可用/).waitFor();checks.push('pagination_and_legacy_location')
 await preview.getByRole('combobox').selectOption('table');assert.equal(await preview.locator('article').count(),1)
 await preview.getByText(/表格结构/).click();await preview.getByRole('cell',{name:'SKU',exact:true}).waitFor()
 assert.equal(await page.evaluate(()=>window.previewLeak),undefined);checks.push('block_filter_table_and_text_escaping')
 const link=page.getByRole('link',{name:'以客户身份试问'});assert.equal(await link.getAttribute('href'),shop.customer_chat_url)
 await page.getByText(/尚无客户可见的已发布知识/).waitFor();await page.getByText(/正常咨询记录、模型费用及人工排队/).waitFor();checks.push('configured_customer_origin_and_normal_business_notice')
 revoked=true
 await page.getByRole('button',{name:'刷新数据',exact:true}).click()
 await preview.waitFor({state:'detached'});assert.equal(await page.getByRole('link',{name:'以客户身份试问'}).count(),0);checks.push('authorization_failure_clears_preview_and_trial_links')
 assert.deepEqual(errors,[])
 await writeFile(out+'/report.json',JSON.stringify({status:'PASS',evidence_kind:'browser_http_fixture',checks,real_service_acceptance:false},null,2),{mode:0o600})
 console.log('PASS knowledge preview browser fixture')
}catch(e){await writeFile(out+'/failure.txt',String(e)+'\n'+await page.locator('body').innerText(),{mode:0o600});console.error('NOT_PASSED preview browser fixture; inspect protected evidence');process.exitCode=1}
finally{await browser.close()}
