<script setup lang="ts">
import { ref, watch } from 'vue'
import Button from '../components/Button.vue'
import Input from '../components/Input.vue'
import { submitValidity, validityError, type ValidityDetail, type ValiditySession } from './validity'

const props = defineProps<{ detail: ValidityDetail; session: ValiditySession; disabled?: boolean }>()
const emit = defineEmits<{ saved: [] }>()
const from = ref('')
const to = ref('')
const reason = ref('')
const busy = ref(false)
const error = ref('')
const notice = ref('')

watch(() => props.detail.version_id, () => {
  from.value = props.detail.effective_from ?? ''
  to.value = props.detail.effective_to ?? ''
  reason.value = ''
  error.value = ''
  notice.value = ''
}, { immediate: true })

async function save() {
  if (reason.value.trim().length < 10) { error.value = '理由至少 10 字。'; return }
  busy.value = true
  error.value = ''
  notice.value = ''
  try {
    await submitValidity(props.detail, props.session, from.value, to.value, reason.value)
    notice.value = '有效期已保存。'
    emit('saved')
  } catch (caught) {
    error.value = validityError(caught)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <form class="validity-form" data-testid="validity-form" @submit.prevent="save">
    <h3>设置有效期</h3>
    <label>生效日期<Input v-model="from" data-testid="validity-from" type="date" /></label>
    <label>失效日期<Input v-model="to" data-testid="validity-to" type="date" /></label>
    <label>变更理由<textarea v-model="reason" rows="2" maxlength="2000" /></label>
    <p v-if="error" class="alert" role="alert" data-testid="validity-error">{{ error }}</p>
    <p v-if="notice" class="notice" role="status">{{ notice }}</p>
    <Button data-testid="validity-submit" variant="primary" :disabled="disabled || busy" @click="save">{{ busy ? '保存中…' : '保存有效期' }}</Button>
  </form>
</template>
