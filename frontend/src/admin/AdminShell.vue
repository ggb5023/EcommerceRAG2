<script setup lang="ts">
import { useAdminContext } from './admin-context'
const { navigationGroups, page, title, data, selectedShop, session, browserAuth, roleLabel, go, signout, refresh, themeChoice, themeLabel, toggleTheme } = useAdminContext()
</script>

<template>
  <div class="admin-shell">
    <aside class="rail">
      <a class="admin-brand" href="/admin/overview" @click.prevent="go('overview')"><span class="admin-brand-mark">ER</span><span><strong>知识管理</strong><small>运营中控台</small></span></a>
      <template v-for="group in navigationGroups" :key="group.label">
        <div class="rail-label">{{ group.label }}</div>
        <nav :aria-label="`管理：${group.label}`">
          <button v-for="item in group.items" :key="item.page" type="button" class="nav-item" :class="{ active: page === item.page }" @click="go(item.page)">
            <span class="nav-mark" aria-hidden="true">{{ item.icon }}</span>
            {{ item.label }}
          </button>
        </nav>
      </template>
      <div class="rail-label">系统</div>
      <nav aria-label="系统入口">
        <a class="nav-item" href="/assistant"><span class="nav-mark" aria-hidden="true">✦</span>AI 查询台</a>
        <a class="nav-item" href="/staff?stay=1"><span class="nav-mark" aria-hidden="true">☰</span>员工工作台</a>
      </nav>
      <div class="rail-bottom" v-if="session">
        <span class="env-mark">{{ session.is_mock ? 'SYNTHETIC' : 'CONNECTED' }}</span>
        <strong>{{ session.tenant_id }}</strong>
        <small>{{ session.runtime_role }} · {{ session.shop_id }}</small>
      </div>
    </aside>

    <main class="main-area">
      <header class="page-head">
        <div><p class="eyebrow">ECOMMERCE RAG / ADMIN</p><h1>{{ title }}</h1></div>
        <div class="head-actions">
          <label v-if="page === 'inspections' || data.some((item) => item.shop_id)" class="shop-picker">店铺
            <select v-model="selectedShop" aria-label="筛选店铺"><option value="">全部授权店铺</option><option v-for="shop in session?.allowed_shop_ids ?? []" :key="shop" :value="shop">{{ shop }}</option></select>
          </label>
          <button class="secondary" type="button" data-testid="theme-toggle" :title="`主题：${themeLabel(themeChoice)}`" @click="toggleTheme">{{ themeChoice === 'dark' ? '☾' : themeChoice === 'light' ? '☀' : '◐' }} {{ themeLabel(themeChoice) }}</button>
          <span v-if="session" class="identity">{{ session.user_id }} <small>{{ session.admin_roles?.map(roleLabel).join(' · ') || '无管理授权' }}</small></span>
          <button v-if="session && browserAuth.enabled" class="secondary" type="button" @click="signout">退出登录</button>
          <button class="icon-action" type="button" title="刷新" aria-label="刷新数据" @click="refresh">↻</button>
        </div>
      </header>
    <slot/>
  </main>
  </div>
</template>
