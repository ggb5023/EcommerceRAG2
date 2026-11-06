<script setup lang="ts">
import { ref } from 'vue'
import Button from './Button.vue'

withDefaults(defineProps<{
  open: boolean
  title: string
  facts?: { label: string; value: string }[]
  confirmLabel?: string
  busy?: boolean
  reasonRequired?: boolean
}>(), { facts: () => [], confirmLabel: '确认', busy: false, reasonRequired: true })

const emit = defineEmits<{ confirm: [reason: string]; cancel: [] }>()
const reason = ref('')
const checked = ref(false)

function close() {
  reason.value = ''
  checked.value = false
  emit('cancel')
}
function confirm() {
  if (checked.value && reason.value.trim().length >= 10) {
    emit('confirm', reason.value.trim())
    reason.value = ''
    checked.value = false
  }
}
</script>

<template>
  <div v-if="open" class="ds-confirm-backdrop" role="dialog" aria-modal="true" :aria-label="title">
    <section class="ds-confirm">
      <header><h2>{{ title }}</h2></header>
      <dl v-if="facts.length" class="ds-confirm-facts">
        <div v-for="fact in facts" :key="fact.label"><dt>{{ fact.label }}</dt><dd>{{ fact.value }}</dd></div>
      </dl>
      <label v-if="reasonRequired">理由（至少 10 个字符）
        <textarea v-model="reason" rows="3" maxlength="2000" />
      </label>
      <label class="ds-confirm-check">
        <input v-model="checked" type="checkbox" /> 我已核对上述信息并确认执行
      </label>
      <footer>
        <Button variant="secondary" :disabled="busy" @click="close">取消</Button>
        <Button variant="primary" :disabled="busy || !checked || (reasonRequired && reason.trim().length < 10)" @click="confirm">
          {{ busy ? '处理中…' : confirmLabel }}
        </Button>
      </footer>
    </section>
  </div>
</template>

<style scoped>
.ds-confirm-backdrop{position:fixed;inset:0;z-index:20;display:grid;place-items:center;padding:var(--space-4);background:var(--color-overlay)}
.ds-confirm{width:min(560px,100%);max-height:90dvh;overflow:auto;padding:var(--space-5);border:1px solid var(--color-border);border-radius:var(--radius-lg);background:var(--color-surface)}
.ds-confirm header{margin-bottom:var(--space-3)}
.ds-confirm h2{margin:0;font-size:var(--text-lg);color:var(--color-text)}
.ds-confirm-facts{display:grid;grid-template-columns:1fr 1fr;gap:1px;margin:0 0 var(--space-3);border:1px solid var(--color-border);background:var(--color-border)}
.ds-confirm-facts div{display:grid;gap:var(--space-1);padding:var(--space-2);background:var(--color-bg)}
.ds-confirm-facts dt{color:var(--color-text-muted);font-size:var(--text-xs)}
.ds-confirm-facts dd{margin:0;font-size:var(--text-xs);color:var(--color-text);overflow-wrap:anywhere}
.ds-confirm label{display:grid;gap:var(--space-2);margin-bottom:var(--space-3);color:var(--color-text-muted);font-size:var(--text-sm)}
.ds-confirm textarea{width:100%;box-sizing:border-box;padding:var(--space-2);border:1px solid var(--color-border);border-radius:var(--radius-md);background:var(--color-surface);color:var(--color-text);font:inherit}
.ds-confirm-check{display:flex !important;align-items:center;gap:var(--space-2)}
.ds-confirm footer{display:flex;justify-content:flex-end;gap:var(--space-2);margin-top:var(--space-4)}
@media (max-width:700px){.ds-confirm-facts{grid-template-columns:1fr}}
</style>
