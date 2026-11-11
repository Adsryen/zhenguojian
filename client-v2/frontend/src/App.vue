<script setup lang="ts">
import NavRail from '@/components/NavRail.vue'
import { useAppStore } from '@/stores/app'
import { setupContextMenu } from '@/services/contextMenu' // V2-6 右键菜单：全局一处挂载

const store = useAppStore()
setupContextMenu()
</script>

<template>
  <div class="shell" :class="{ 'nav-collapsed': store.navCollapsed }">
    <NavRail />
    <main class="shell-main">
      <router-view />
    </main>
  </div>
</template>

<style scoped>
.shell {
  display: flex;
  height: 100%;
  overflow: hidden;
}
.shell-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  /* 页面自带滚动容器时这里永不出条；忘写的页面从裁切变可滚，兜底 */
  overflow-y: auto;
}
</style>
