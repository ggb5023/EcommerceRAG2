<script setup lang="ts">
import type { Shop } from './admin-context'
defineProps<{shop:Shop}>()
</script>
<template><div class="customer-trial"><a v-if="shop.customer_chat_url" :href="shop.customer_chat_url" target="_blank" rel="noopener noreferrer">以客户身份试问</a><p v-else>请先在店铺设置启用客户入口，并配置客户页面地址。</p><p v-if="shop.customer_chat_url&&!shop.customer_published_knowledge_available">尚无客户可见的已发布知识，请先审核发布并确认披露及访问权限；当前咨询仍按正常规则转人工。</p><small>试问使用独立访客身份，仅查询已发布且允许对外使用的知识。会产生正常咨询记录、模型费用及人工排队；可在客户页点击“开始新咨询”。</small></div></template>
<style scoped>
.customer-trial{display:grid;gap:var(--space-2);width:100%;color:var(--color-text-muted);font-size:var(--text-xs)}
.customer-trial a{color:var(--color-primary);font-weight:650}
.customer-trial p{margin:0;line-height:1.5}
.customer-trial small{color:var(--color-text-muted);font-size:var(--text-xs);line-height:1.5}
</style>
