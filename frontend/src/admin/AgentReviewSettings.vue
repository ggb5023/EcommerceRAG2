<script setup lang="ts">
import {onMounted,ref} from 'vue'
import {get,put} from './api'
const enabled=ref(false);const policy=ref('v1');const actor=ref<string|null>(null);const updated=ref<string|null>(null);const reason=ref('');const busy=ref(false);const notice=ref('')
async function load(){try{const value=await get<any>('/admin/v1/merchant/agent-review');enabled.value=!!value.enabled;policy.value=value.policy_version||'v1';actor.value=value.actor??null;updated.value=value.updated_at??null}catch(e){notice.value=e instanceof Error?e.message:'读取失败。'}}
async function save(){busy.value=true;notice.value='';try{await put('/admin/v1/merchant/agent-review',{enabled:enabled.value,policy_version:policy.value,confirm:true,reason:reason.value.trim()});notice.value='设置已保存。';reason.value='';await load()}catch(e){notice.value=e instanceof Error?e.message:'保存失败。'}finally{busy.value=false}}
onMounted(load)
</script>
<template><section class="import-form"><h2>模型辅助开关</h2><p>允许把待审内容发送给模型（默认关闭；只发送片段与规则，不发送顾客对话正文）。R1–R4 本地预检不受此开关影响。</p>
<p>当前：<strong>{{enabled?'已开启':'已关闭'}}</strong><span v-if="actor"> · 最后修改：{{actor}} {{updated??''}}</span></p>
<label><input type="checkbox" v-model="enabled"> 允许发送待审片段给模型</label><label>策略版本<input v-model="policy" maxlength="64"></label><label>修改原因<textarea v-model="reason" rows="2" minlength="10" maxlength="2000"/></label><button type="button" class="primary" :disabled="busy||reason.trim().length<10" @click="save">保存开关</button><p role="status">{{notice}}</p></section></template>
