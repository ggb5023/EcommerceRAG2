<script setup lang="ts">
defineOptions({ inheritAttrs: false })
</script>

<template>
  <article v-bind="$attrs" class="ds-card"><slot /></article>
</template>

<style scoped>
.ds-card{border:1px solid var(--color-border);border-radius:var(--radius-md);background:var(--color-surface);box-shadow:var(--shadow-sm);padding:var(--space-4)}
</style>
