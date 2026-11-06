<script setup lang="ts">
import { computed } from 'vue'
import { useAdminContext } from './admin-context'
import OverviewPage from './pages/OverviewPage.vue'
import OutcomesPage from './pages/OutcomesPage.vue'
import ReportsPage from './pages/ReportsPage.vue'
import GapsPage from './pages/GapsPage.vue'
import InspectionsPage from './pages/InspectionsPage.vue'
import AccountsPage from './pages/AccountsPage.vue'
import RequestsPage from './pages/RequestsPage.vue'
import AuditPage from './pages/AuditPage.vue'
import ShopsPage from './pages/ShopsPage.vue'
import SourcesPage from './pages/SourcesPage.vue'
import IngestionPage from './pages/IngestionPage.vue'
import ReviewsPage from './pages/ReviewsPage.vue'
import NotFoundPage from './pages/NotFoundPage.vue'
const { page } = useAdminContext()
const pages: Record<string, unknown> = { overview: OverviewPage, outcomes: OutcomesPage, reports: ReportsPage, gaps: GapsPage, inspections: InspectionsPage, accounts: AccountsPage,
  requests: RequestsPage, audit: AuditPage, shops: ShopsPage, sources: SourcesPage, ingestion: IngestionPage, reviews: ReviewsPage,
  'not-found': NotFoundPage }
const current = computed(() => pages[page.value] ?? NotFoundPage)
</script>

<template><component :is="current"/></template>
