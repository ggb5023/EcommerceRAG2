<script setup lang="ts">
export type CitationCardView = {
  evidence_id: string
  citation_index: number
  title: string
  version_id: string
  snippet: string
}
defineProps<{ citation: CitationCardView }>()
</script>

<template>
  <article class="citation-card" data-testid="citation-card">
    <header>
      <span class="citation-index">[{{ citation.citation_index }}]</span>
      <strong>{{ citation.title || '引用资料' }}</strong>
    </header>
    <small>资料版本 {{ citation.version_id }} · 引用 {{ citation.citation_index }}</small>
    <p>{{ citation.snippet }}</p>
  </article>
</template>

<style scoped>
.citation-card{display:grid;gap:var(--space-2);padding:var(--space-3);border:1px solid var(--color-border);border-radius:var(--radius-md);background:var(--color-surface)}
.citation-card header{display:flex;align-items:baseline;gap:var(--space-2)}
.citation-card .citation-index{flex:none;color:var(--color-primary);font-weight:700}
.citation-card strong{font-size:var(--text-sm);overflow-wrap:anywhere}
.citation-card small{color:var(--color-text-muted);font-size:var(--text-xs)}
.citation-card p{margin:0;color:var(--color-text);font-size:var(--text-sm);line-height:1.6;white-space:pre-wrap;overflow-wrap:anywhere}
</style>
