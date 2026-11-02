<script setup lang="ts">
import Button from './Button.vue'
defineOptions({ inheritAttrs: false })
withDefaults(defineProps<{ open: boolean; title: string }>(), { open: false })
const emit = defineEmits<{ close: [] }>()
</script>

<template>
  <div v-if="open" class="ds-drawer-backdrop" data-testid="drawer" @click.self="emit('close')">
    <aside v-bind="$attrs" class="ds-drawer" role="dialog" aria-modal="true" :aria-label="title">
      <header><h2>{{ title }}</h2><Button variant="text" aria-label="关闭" @click="emit('close')">×</Button></header>
      <div class="ds-drawer-body"><slot /></div>
      <footer v-if="$slots.footer"><slot name="footer" /></footer>
    </aside>
  </div>
</template>

<style scoped>
.ds-drawer-backdrop{position:fixed;inset:0;z-index:30;display:flex;justify-content:flex-end;background:var(--color-overlay)}
.ds-drawer{width:min(440px,100%);height:100%;display:flex;flex-direction:column;background:var(--color-surface);border-left:1px solid var(--color-border);box-shadow:var(--shadow-md);padding:var(--space-5);overflow:auto}
.ds-drawer header{display:flex;align-items:center;justify-content:space-between;gap:var(--space-3);margin-bottom:var(--space-4)}
.ds-drawer header h2{margin:0;font-size:var(--text-lg)}
.ds-drawer-body{display:grid;gap:var(--space-3)}
.ds-drawer footer{display:flex;justify-content:flex-end;gap:var(--space-2);margin-top:var(--space-5)}
</style>
