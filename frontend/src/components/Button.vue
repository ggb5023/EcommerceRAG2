<script setup lang="ts">
defineOptions({ inheritAttrs: false })
withDefaults(defineProps<{
  variant?: 'primary' | 'secondary' | 'danger' | 'text'
  type?: 'button' | 'submit' | 'reset'
  disabled?: boolean
}>(), { variant: 'secondary', type: 'button', disabled: false })
</script>

<template>
  <!-- $attrs binds data-testid and a caller-supplied class on the single root. -->
  <button v-bind="$attrs" :type="type" :disabled="disabled" :class="['ds-button', `ds-button-${variant}`]"><slot /></button>
</template>

<style scoped>
.ds-button{display:inline-flex;align-items:center;justify-content:center;gap:var(--space-2);min-height:33px;padding:7px 12px;border:0;border-radius:var(--radius-sm);font-family:var(--font-sans);font-size:var(--text-xs);font-weight:650;cursor:pointer}
.ds-button:disabled{opacity:.5;cursor:not-allowed}
.ds-button-primary{background:var(--color-primary);color:var(--color-on-primary)}
.ds-button-secondary{border:1px solid var(--color-border);background:var(--color-surface);color:var(--color-text)}
.ds-button-danger{border:1px solid var(--color-danger);background:var(--color-surface);color:var(--color-danger)}
.ds-button-text{min-height:auto;padding:0;background:transparent;color:var(--color-primary)}
</style>
