<script setup lang="ts">
defineProps<{ eyebrow?: string; title: string; subtitle?: string }>()
</script>

<template>
  <header class="ds-page-header">
    <div>
      <p v-if="eyebrow" class="ds-page-header-eyebrow">{{ eyebrow }}</p>
      <h1>{{ title }}</h1>
      <p v-if="subtitle" class="ds-page-header-subtitle">{{ subtitle }}</p>
    </div>
    <div class="ds-page-header-actions"><slot name="actions" /></div>
  </header>
</template>

<style scoped>
.ds-page-header{display:flex;align-items:flex-start;justify-content:space-between;gap:var(--space-4);margin-bottom:var(--space-5);padding-bottom:var(--space-4);border-bottom:1px solid var(--color-border)}
.ds-page-header h1{margin:0;font-size:var(--text-xl);color:var(--color-text)}
.ds-page-header-eyebrow{margin:0 0 var(--space-1);color:var(--color-text-muted);font-size:var(--text-xs);font-weight:700;letter-spacing:.08em;text-transform:uppercase}
.ds-page-header-subtitle{margin:var(--space-2) 0 0;color:var(--color-text-muted);font-size:var(--text-sm)}
.ds-page-header-actions{display:flex;align-items:center;gap:var(--space-2);flex-shrink:0}
</style>
