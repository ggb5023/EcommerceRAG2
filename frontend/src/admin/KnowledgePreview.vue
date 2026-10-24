<script setup lang="ts">
import {computed,ref,watch} from 'vue'
import {sourceLocation,type PreviewChunk} from './knowledge-preview'
const props=defineProps<{chunks:PreviewChunk[]}>()
const kind=ref('');const page=ref(0);const size=12
const types=computed(()=>[...new Set(props.chunks.map(c=>c.content_type||'unknown'))])
const filtered=computed(()=>props.chunks.filter(c=>!kind.value||(c.content_type||'unknown')===kind.value))
const shown=computed(()=>filtered.value.slice(page.value*size,(page.value+1)*size))
watch(()=>[props.chunks,kind.value],()=>{page.value=0})
const hints:Record<string,string>={code_language:'已保留代码语言标记',repeated_table_header:'已处理重复表头',image_reference_only:'仅保留图片引用，未识别图片内容',unterminated_code_fence:'代码围栏未闭合，请检查结构',parser_warning:'解析结构提示，请检查原件'}
</script>
<template><section class="content-preview" aria-label="知识解析预览"><div class="preview-head"><strong>审核内容</strong><label>块类型 <select v-model="kind"><option value="">全部</option><option v-for="t in types" :key="t" :value="t">{{t}}</option></select></label></div>
<p>共 {{filtered.length}} 个片段，每段最多 {{size}} 个。位置对应原始行或文档块；字符范围是解析元素内偏移。</p>
<article v-for="chunk in shown" :key="chunk.chunk_id||chunk.chunk_index"><small>CHUNK {{chunk.chunk_index+1}} · {{chunk.chunk_id||'旧版本标识不可用'}}</small><dl><dt>标题路径</dt><dd>{{chunk.heading_path||'无标题路径'}}</dd><dt>块类型 / 拆分原因</dt><dd>{{chunk.content_type||'不可用'}} / {{chunk.split_reason||'不可用'}}</dd><dt>来源位置</dt><dd>{{sourceLocation(chunk)}}</dd><dt>解析元素内偏移</dt><dd>{{chunk.char_start??'不可用'}}–{{chunk.char_end??'不可用'}}（结束不含）</dd></dl>
<p v-for="(hint,i) in chunk.parse_hints??[]" :key="i" :class="`parse-${hint.kind}`">{{hint.kind==='structure'?'结构提示':'处理信息'}}：{{hints[hint.code]||'请检查资料结构'}}。发布仍按质量门控及审核决定。</p>
<p class="chunk-text">{{chunk.content}}</p><details v-if="chunk.table_body?.length"><summary>表格结构（{{chunk.table_body.length}} 行，展示前 20 行）</summary><p v-if="chunk.table_caption">{{chunk.table_caption}}</p><div class="preview-table"><table><tbody><tr v-for="(row,i) in chunk.table_body.slice(0,20)" :key="i"><td v-for="(cell,j) in row" :key="j">{{cell}}</td></tr></tbody></table></div></details></article>
<p v-if="!filtered.length" class="empty">无可展示的内容片段。</p><nav v-if="filtered.length>size" aria-label="预览分段"><button :disabled="page===0" @click="page--">上一段</button><span>第 {{page+1}} / {{Math.ceil(filtered.length/size)}} 段</span><button :disabled="(page+1)*size>=filtered.length" @click="page++">下一段</button></nav></section></template>
<style scoped>.chunk-text,td{white-space:pre-wrap;overflow-wrap:anywhere}.preview-table{overflow:auto;max-height:24rem}dl{display:grid;grid-template-columns:auto 1fr;gap:.3rem 1rem}dd{margin:0;overflow-wrap:anywhere}.parse-structure{color:#87520c}nav{display:flex;align-items:center;gap:1rem;flex-wrap:wrap}</style>
