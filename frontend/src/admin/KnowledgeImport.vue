<script setup lang="ts">
import {ref} from 'vue'
import {request} from './api'
import {knowledgePackage} from './knowledge-package'
const props=defineProps<{shops:string[]}>();const emit=defineEmits<{imported:[]}>()
const shop=ref(props.shops[0]??'');const source=ref('merchant-knowledge');const files=ref<File[]>([]);const faqKey=ref('');const question=ref('');const answer=ref('');const busy=ref(false);const notice=ref('')
async function submit(faq=false){busy.value=true;notice.value='';try{
 let selected=files.value
 if(faq){if(!/^[a-z0-9-]{1,64}$/.test(faqKey.value)||!question.value.trim()||!answer.value.trim())throw new Error('请填写稳定 FAQ 标识、问题和答案。');selected=[new File([`# ${question.value.trim()}\n\n${answer.value.trim()}\n`],`faq-${faqKey.value}.md`,{type:'text/markdown'})]}
 const result=await request<{item_count:number;status:string}>('/admin/v1/merchant/sources/import',{method:'POST',body:await knowledgePackage(source.value,selected,shop.value)})
 notice.value=`已提交 ${result.item_count} 份资料，请到摄取任务查看解析进度，再审核并发布。`;emit('imported')
 }catch(e){notice.value=e instanceof Error?e.message:'提交失败，请重试。'}finally{busy.value=false}}
function template(){const link=document.createElement('a');const url=URL.createObjectURL(new Blob(['question,answer\n如何保养商品？,请按商品说明书清洁并干燥保存。\n'],{type:'text/csv;charset=utf-8'}));link.href=url;link.download='faq-template.csv';link.click();URL.revokeObjectURL(url)}
</script>
<template><section class="import-form"><h2>上传店铺知识 / 编辑 FAQ</h2><p>上传后依次解析、预览、审核并发布；修改同名文件或相同 FAQ 标识会生成新版本。</p><div class="form-grid"><label>店铺<select v-model="shop"><option v-for="s in shops" :key="s">{{s}}</option></select></label><label>来源标识<input v-model="source" maxlength="64"></label></div>
<form @submit.prevent="submit()"><label>DOCX、Markdown 或 CSV<input type="file" multiple accept=".docx,.md,.markdown,.csv" @change="files=Array.from(($event.target as HTMLInputElement).files??[])"></label><button class="primary" :disabled="busy||!shop||!files.length">上传并解析</button><button type="button" @click="template">下载 FAQ CSV 模板</button></form>
<form @submit.prevent="submit(true)"><h3>FAQ 表单</h3><label>固定 FAQ 标识<input v-model="faqKey" placeholder="returns-policy" required></label><label>问题<input v-model="question" maxlength="500" required></label><label>答案<textarea v-model="answer" rows="5" maxlength="16000" required/></label><button class="primary" :disabled="busy||!shop">保存 FAQ 新版本</button></form><p role="status">{{notice}}</p></section></template>
