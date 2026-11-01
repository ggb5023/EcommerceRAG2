<script setup lang="ts">
defineOptions({ inheritAttrs: false })
</script>

<template>
  <section v-bind="$attrs" class="ds-empty">
    <strong><slot name="title">暂无数据</slot></strong>
    <span><slot /></span>
  </section>
</template>

<style scoped>
.ds-empty{display:flex;flex-direction:column;align-items:center;justify-content:center;gap:var(--space-2);padding:var(--space-6);border:1px solid var(--color-border);background:var(--color-surface);color:var(--color-text-muted);text-align:center;font-family:var(--font-sans)}
.ds-empty strong{color:var(--color-text);font-size:var(--text-sm)}
.ds-empty span{font-size:var(--text-xs)}
</style>
