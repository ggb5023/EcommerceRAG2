<script setup lang="ts">
import {ref} from 'vue'
import type {SuggestionView} from './customer-api'
import CitationCard from './components/CitationCard.vue'
defineProps<{suggestion:SuggestionView|null; busy:boolean; error:string}>()
defineEmits<{generate:[text:string]; copy:[]}>()
const text=ref('')
const blockedText:Record<string,string>={mock:'本地测试结果不可复制',no_citable_evidence:'没有可引用的资料依据',incomplete:'回答尚未完成',not_copyable:'当前建议不可复制'}
</script>

<template>
  <section class="suggestion-panel" data-testid="suggestion-panel">
    <h3>建议回复</h3>
    <form @submit.prevent="$emit('generate',text.trim())">
      <label>粘贴顾客问题<textarea v-model="text" maxlength="32768" placeholder="把顾客问题粘贴到这里" :disabled="busy"/></label>
      <button type="submit" data-testid="suggestion-generate" :disabled="busy||!text.trim()">生成建议</button>
    </form>
    <p v-if="error" role="alert">{{error}}</p>
    <article v-if="suggestion" data-testid="suggestion-result">
      <p class="message-text">{{suggestion.answer}}</p>
      <ul v-if="suggestion.citations.length" class="citation-list">
        <li v-for="citation in suggestion.citations" :key="citation.evidence_id" data-testid="suggestion-citation">
          <CitationCard :citation="citation"/>
        </li>
      </ul>
      <button type="button" data-testid="suggestion-copy" :disabled="busy||!suggestion.can_copy" @click="$emit('copy')">复制建议</button>
      <p v-if="!suggestion.can_copy" class="copy-blocked">{{blockedText[suggestion.blocked_reason]||'当前建议不可复制'}}</p>
    </article>
  </section>
</template>

<style scoped>
.suggestion-panel{margin-top:1rem;padding:1rem;border:1px solid #c9d8d2;border-radius:8px;background:#f7fbf9}
.suggestion-panel textarea{min-height:72px;margin-bottom:.5rem}
.suggestion-panel button[disabled]{opacity:.5;cursor:default}
.citation-list{display:grid;gap:.45rem;padding-left:0;list-style:none}
.citation-list li{margin:0}
.copy-blocked{color:#6b5a1f}
</style>
