<script setup lang="ts">
defineProps<{ width: number }>()
</script>

<template>
  <div class="sk" :style="{ width: width + 'px' }">
    <div class="sk-cover" />
    <div class="sk-line w70" />
    <div class="sk-line w40" />
  </div>
</template>

<style scoped>
.sk-cover {
  width: 100%;
  aspect-ratio: 3 / 4;
  border-radius: var(--guo-radius);
  background: var(--surface-3);
  animation: shimmer 1.2s ease-in-out infinite;
}
.sk-line {
  height: 12px;
  border-radius: 4px;
  margin-top: 8px;
  background: var(--surface-3);
  animation: shimmer 1.2s ease-in-out infinite;
}
.w70 {
  width: 70%;
}
.w40 {
  width: 40%;
}
</style>
