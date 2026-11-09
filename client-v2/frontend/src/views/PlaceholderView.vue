<script setup lang="ts">
import { useRoute } from 'vue-router'
const route = useRoute()
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">{{ route.meta.title }}</h1>
  </div>
  <div class="state-block ph-body">
    <svg viewBox="0 0 24 24" width="44" height="44" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" class="ph-ico">
      <rect x="3" y="5" width="18" height="16" rx="2" />
      <path d="M8 3v4M16 3v4M3 11h18M9 15h6" />
    </svg>
    <div class="ph-title">{{ route.meta.title }}</div>
    <div>该页面骨架已就位，真实数据将在 <b>{{ route.meta.phase || 'V2-x' }}</b> 接入。</div>
    <div class="text-muted">（导航/令牌/绑定适配层均已就绪，届时只需新增视图组件）</div>
  </div>
</template>

<style scoped>
.ph-body {
  flex: 1;
}
.ph-ico {
  color: var(--muted-2);
}
.ph-title {
  font-size: 16px;
  font-weight: 700;
  color: var(--text);
}
</style>
