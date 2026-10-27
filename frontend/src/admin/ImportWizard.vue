<script setup lang="ts">
import {ref} from 'vue'
import {get,post,request} from './api'
import {knowledgePackage} from './knowledge-package'
const props=defineProps<{shops:string[]}>();const emit=defineEmits<{imported:[]}>()
const shop=ref(props.shops[0]??'');const businessDate=ref(new Date().toISOString().slice(0,10));const files=ref<File[]>([]);const batchId=ref('');const preview=ref<any>(null);const notice=ref('');const busy=ref(false)
async function upload(){busy.value=true;notice.value='';try{
 if(!shop.value||!files.value.length)throw new Error('请选择店铺和至少一个文件。')
 const form=await knowledgePackage('merchant-precheck',files.value,shop.value);form.set('business_date',businessDate.value)
 const result=await request<{batch_id:string;item_count:number;blocks:number;warnings:number}>('/admin/v1/merchant/imports',{method:'POST',body:form})
 batchId.value=result.batch_id;preview.value=await get(`/admin/v1/merchant/imports/${result.batch_id}`)
 notice.value=`预检完成：${result.item_count} 份，block ${result.blocks}，warn ${result.warnings}。本地规则预检仅供参考，发布前请确认。`
 }catch(e){notice.value=e instanceof Error?e.message:'预检失败，请重试。'}finally{busy.value=false}}
async function confirm(){busy.value=true;try{
 await post(`/admin/v1/merchant/imports/${batchId.value}/confirm`,{confirm:true,reason:'商家已核对 agent 预检结果并确认导入'})
 notice.value='已确认导入，版本进入待审核队列。';emit('imported');preview.value=null;batchId.value=''
 }catch(e){notice.value=e instanceof Error?e.message:'确认失败，请重试。'}finally{busy.value=false}}
function reset(){batchId.value='';preview.value=null;notice.value=''}
</script>
<template><section class="import-form"><h2>导入预检（三步）</h2>
<p v-if="!batchId">第一步：上传资料包。R1–R4 本地规则预检只给建议，不会发布或写入审核结论；模型辅助默认关闭。</p>
<p v-else>第二步：核对片段与问题；确认后才会创建待审核版本。</p>
<div class="form-grid"><label>店铺<select v-model="shop"><option v-for="s in shops" :key="s">{{s}}</option></select></label><label>业务日期<input v-model="businessDate" type="date"></label></div>
<form v-if="!batchId" @submit.prevent="upload"><label>DOCX、Markdown 或 CSV<input type="file" multiple accept=".docx,.markdown,.md,.csv" @change="files=Array.from(($event.target as HTMLInputElement).files??[])"></label><button class="primary" :disabled="busy||!shop||!files.length">开始预检</button></form>
<div v-else><p v-for="(item,index) in preview?.items??[]" :key="item.item_id" class="precheck-item"><strong>{{Number(index)+1}}. {{item.precheck_detail?.title}}</strong><span v-for="issue in item.precheck_result" :key="issue.issue_code"> · {{issue.issue_code}}（{{issue.severity}}）：{{issue.snippet}} — {{issue.suggested_action}}</span></p>
<p><button class="primary" :disabled="busy" @click="confirm">确认导入</button> <button type="button" @click="reset">取消</button></p><small>本地规则预检仅供参考，发布前请确认。</small></div>
<p role="status">{{notice}}</p></section></template>
