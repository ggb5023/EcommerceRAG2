<script setup lang="ts">
import {onUnmounted,ref} from 'vue'
import {get,post,request} from './api'
import {knowledgePackage} from './knowledge-package'
import {canRevokeImport,commandStateLabel} from './import-wizard-state'
const props=defineProps<{shops:string[]}>();const emit=defineEmits<{imported:[]}>()
const shop=ref(props.shops[0]??'');const businessDate=ref(new Date().toISOString().slice(0,10));const files=ref<File[]>([]);const manifestFile=ref<File|null>(null);const batchId=ref('');const preview=ref<any>(null);const notice=ref('');const busy=ref(false);const jobId=ref('');const jobState=ref('');const jobError=ref('');const revokedAt=ref<string|null>(null);let timer:ReturnType<typeof setTimeout>|undefined
function stopPolling(){if(timer)clearTimeout(timer);timer=undefined}
async function pollJob(){if(!jobId.value)return;try{const job=await get<any>(`/admin/v1/merchant/commands/${jobId.value}`);jobState.value=job.state??job.command_status??'';jobError.value=job.error_code??'';if(jobState.value==='done'||jobState.value==='failed'||jobState.value==='cancelled'){stopPolling();return}}catch{jobError.value='job_lookup_failed'}timer=setTimeout(pollJob,1500)}
async function upload(){busy.value=true;notice.value='';try{
 if(!shop.value||!files.value.length)throw new Error('请选择店铺和至少一个文件。')
 let form:FormData
 if(manifestFile.value){form=new FormData();form.set('shop_id',shop.value);form.set('business_date',businessDate.value);form.set('manifest',await manifestFile.value.text());for(const f of files.value){form.append('paths',f.name);form.append('files',f,f.name)}}
 else{form=await knowledgePackage('merchant-precheck',files.value,shop.value);form.set('business_date',businessDate.value)}
 const result=await request<{batch_id:string;item_count:number;blocks:number;warnings:number}>('/admin/v1/merchant/imports',{method:'POST',body:form})
 batchId.value=result.batch_id;revokedAt.value=null;preview.value=await get(`/admin/v1/merchant/imports/${result.batch_id}`)
 notice.value=`预检完成：${result.item_count} 份，block ${result.blocks}，warn ${result.warnings}。本地规则预检仅供参考，发布前请确认。`
 }catch(e){notice.value=e instanceof Error?e.message:'预检失败，请重试。'}finally{busy.value=false}}
async function confirm(){busy.value=true;notice.value='';try{
 const result=await post<any>(`/admin/v1/merchant/imports/${batchId.value}/confirm`,{confirm:true,reason:'商家已核对预检结果并确认导入'})
 jobId.value=String(result.command_id??result.job_id??'');jobState.value='pending';preview.value=await get(`/admin/v1/merchant/imports/${batchId.value}`);notice.value='已确认导入，正在跟踪摄取任务。';emit('imported');void pollJob()
 }catch(e){notice.value=e instanceof Error?e.message:'确认失败，请重试。'}finally{busy.value=false}}
async function revoke(){if(!window.confirm('撤回整包后，未发布版本会软删除，且不可恢复。确认继续？'))return;busy.value=true;try{await post(`/admin/v1/merchant/imports/${batchId.value}/revoke`,{confirm:true,reason:'商家确认撤回整包未发布版本'});revokedAt.value=new Date().toISOString();preview.value=await get(`/admin/v1/merchant/imports/${batchId.value}`);notice.value='整包已撤回。';stopPolling()}catch(e){notice.value=e instanceof Error?e.message:'撤回失败。'}finally{busy.value=false}}
function reset(){batchId.value='';preview.value=null;notice.value='';jobId.value='';jobState.value='';revokedAt.value=null;stopPolling()}
onUnmounted(stopPolling)
</script>
<template><section class="import-form" :class="{revoked:!!revokedAt}"><h2>导入预检（三步）</h2>
<p v-if="!batchId">第一步：上传资料包。R1–R4 本地规则预检只给建议，不会发布或写入审核结论；模型辅助默认关闭。</p>
<p v-else>第二步：核对片段与问题；确认后才会创建待审核版本。</p>
<div class="form-grid"><label>店铺<select v-model="shop"><option v-for="s in shops" :key="s">{{s}}</option></select></label><label>业务日期<input v-model="businessDate" type="date"></label></div>
<form v-if="!batchId" @submit.prevent="upload"><label>manifest.yaml（可选；提供时按原 manifest 预检）<input type="file" accept=".yaml,.yml" @change="manifestFile=($event.target as HTMLInputElement).files?.[0]??null"></label><label>资料文件<input type="file" multiple accept=".docx,.markdown,.md,.csv" @change="files=Array.from(($event.target as HTMLInputElement).files??[])"></label><button class="primary" :disabled="busy||!shop||!files.length">开始预检</button></form>
<div v-else><p v-for="(item,index) in preview?.items??[]" :key="item.item_id" class="precheck-item"><strong>{{Number(index)+1}}. {{item.precheck_detail?.title}}</strong><details v-for="issue in item.precheck_result" :key="issue.issue_code"><summary>{{issue.issue_code}}（{{issue.severity}}）</summary><span>{{issue.snippet}} — {{issue.suggested_action}}</span></details></p>
<p>批次状态：<strong>{{revokedAt?'已撤回':preview?.status}}</strong><span v-if="revokedAt"> · 撤回时间：{{revokedAt}}</span></p>
<p v-if="jobId">摄取任务：{{commandStateLabel(jobState,jobError)}}<span v-if="jobError">（{{jobError}}）</span></p>
<p><button class="primary" :disabled="busy||!!revokedAt" @click="confirm">确认导入</button> <button type="button" :disabled="busy||!canRevokeImport(preview?.status,revokedAt)" @click="revoke">撤回整包</button> <button type="button" @click="reset">关闭</button></p><small>本地规则预检仅供参考，发布前请确认。</small></div>
<p role="status">{{notice}}</p></section></template>
