<script setup lang="ts">
defineOptions({ inheritAttrs: false })
defineProps<{ modelValue?: string | number }>()
defineEmits<{ 'update:modelValue': [value: string] }>()
</script>

<template>
  <!-- The input is the root: data-testid, type, required and validation
       attributes are forwarded unchanged. -->
  <input v-bind="$attrs" :value="modelValue" @input="$emit('update:modelValue', ($event.target as HTMLInputElement).value)">
</template>

<style scoped>
input{min-height:35px;border:1px solid var(--color-border);border-radius:var(--radius-sm);padding:7px 9px;background:var(--color-surface);color:var(--color-text);font-family:var(--font-sans);font-size:var(--text-sm)}
</style>
