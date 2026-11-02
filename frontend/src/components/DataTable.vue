<script setup lang="ts">
import Input from './Input.vue'
withDefaults(defineProps<{ title: string; modelValue?: string; placeholder?: string }>(), {
  modelValue: '', placeholder: '搜索…',
})
defineEmits<{ 'update:modelValue': [value: string] }>()
</script>

<template>
  <section class="ds-data-table" data-testid="data-table">
    <header class="ds-data-table-head">
      <div><h2>{{ title }}</h2><slot name="subtitle" /></div>
      <div class="ds-data-table-tools">
        <Input :model-value="modelValue" :placeholder="placeholder"
          @update:model-value="$emit('update:modelValue', $event)" />
        <slot name="actions" />
      </div>
    </header>
    <slot />
  </section>
</template>

<style scoped>
.ds-data-table{display:grid;gap:var(--space-3);min-width:0}
.ds-data-table-head{display:flex;align-items:flex-end;justify-content:space-between;gap:var(--space-3);flex-wrap:wrap}
.ds-data-table-head h2{margin:0 0 var(--space-1);font-size:var(--text-md)}
.ds-data-table-tools{display:flex;align-items:center;gap:var(--space-2);flex-wrap:wrap}
.ds-data-table-tools :deep(input){min-width:180px}
</style>
