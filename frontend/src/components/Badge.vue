<script setup lang="ts">
defineOptions({ inheritAttrs: false })
withDefaults(defineProps<{ tone?: 'success' | 'warning' | 'danger' | 'muted' }>(), { tone: 'muted' })
</script>

<template>
  <span v-bind="$attrs" :class="['ds-badge', `ds-badge-${tone}`]"><slot /></span>
</template>

<style scoped>
.ds-badge{display:inline-flex;align-items:center;font-family:var(--font-sans);font-size:var(--text-xs);font-weight:650;line-height:1.4}
.ds-badge-success{color:var(--color-success)}
.ds-badge-warning{color:var(--color-warning)}
.ds-badge-danger{color:var(--color-danger)}
.ds-badge-muted{color:var(--color-text-muted)}
</style>
