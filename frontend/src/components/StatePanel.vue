<script setup lang="ts">
withDefaults(defineProps<{ tone?: 'loading' | 'error' | 'forbidden' | 'empty'; title: string; detail?: string }>(), {
  tone: 'loading', detail: '',
})
</script>

<template>
  <section class="ds-state-panel" :class="`ds-state-panel-${tone}`" role="status">
    <strong>{{ title }}</strong>
    <span v-if="detail">{{ detail }}</span>
    <div v-if="$slots.default" class="ds-state-panel-actions"><slot /></div>
  </section>
</template>

<style scoped>
.ds-state-panel{max-width:620px;margin:var(--space-7) auto;padding:var(--space-7);border:1px solid var(--color-border);border-radius:var(--radius-lg);background:var(--color-surface);display:flex;flex-direction:column;align-items:center;gap:var(--space-2);text-align:center}
.ds-state-panel strong{font-size:var(--text-lg);color:var(--color-text)}
.ds-state-panel span{color:var(--color-text-muted);font-size:var(--text-sm)}
.ds-state-panel-error strong{color:var(--color-danger)}
.ds-state-panel-forbidden strong{color:var(--color-warning)}
.ds-state-panel-actions{display:flex;gap:var(--space-2);margin-top:var(--space-2)}
</style>
