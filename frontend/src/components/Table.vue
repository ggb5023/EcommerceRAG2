<script setup lang="ts">
defineOptions({ inheritAttrs: false })
</script>

<template>
  <div v-bind="$attrs" class="table-wrap ds-table"><table><slot /></table></div>
</template>

<style scoped>
.ds-table table{width:100%;border-collapse:collapse}
</style>
