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
  overflow: hidden;
}
</style>
