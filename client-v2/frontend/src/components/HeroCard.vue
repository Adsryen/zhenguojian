<script setup lang="ts">
/* HeroCard —— 找剧首屏顶部横条大卡（v1 poster_wall._WallHero 的 Web 版）
   对齐 v1 语义（zhenguojian/app/zhenguojian/poster_wall.py:307-427 + 40-48 常量）：
   - 高度固定 260（v1 HERO_H=260），宽随内容区；矮窗由宿主（DiscoverView）隐藏让位给墙
   - 常态不描边（v1 原话：带边框的卡片像「中间突出的块」），hover/focus 才给一圈 accent
   - 点击 = v1 hero_clicked → emit('select', drama)，父级转 detailRoute
   版式按任务口径把 v1 的「右封面 + 左文字」镜像成「左封面 160×224 + 右标题/简介/标签」
   （封面仍保持 v1 HERO_COVER 150×210 的 5:7 比例，只是槽位左右对调、放大一档）。
   v1 hero 不画角标（徽标是卡片墙的事），这里同样不加 VIP/集数徽标，避免与徽标语义重复。 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { Drama } from '@/services/types'
import { resolveCover } from '@/services/covers'
import { t } from '@/services/i18n'

const props = defineProps<{ drama: Drama }>()
const emit = defineEmits<{ (e: 'select', drama: Drama): void }>()

/* 组件内中文文案已收敛至 i18n.ts hero.*（原 L 表）；t 随 locale 响应 */

const COVER_W = 160
const COVER_H = 224

const title = computed(() => (props.drama.title ?? '').trim() || t('hero.fallbackTitle'))
const desc = computed(() =>
  String(props.drama.description ?? '')
    .replace(/\s*\n\s*/g, ' ')
    .trim()
)
/* 标签槽：category 打头，再补 tags（v1 hero 无标签行，这里按任务要求补，最多 4 枚防挤） */
const tagList = computed(() => {
  const out: string[] = []
  const cat = (props.drama.category ?? '').trim()
  if (cat) out.push(cat)
  for (const t of props.drama.tags ?? []) {
    const s = String(t ?? '').trim()
    if (s && !out.includes(s)) out.push(s)
  }
  return out.slice(0, 4)
})
/* 副信息行：热度 / 播放量（缺字段就不显示，v1 _sub_text 同口径） */
const statText = computed(() => {
  const parts: string[] = []
  const heat = String(props.drama.heat ?? '').trim()
  const views = String(props.drama.views ?? '').trim()
  if (heat) parts.push(`${t('hero.heat')} ${heat}`)
  if (views) parts.push(`${t('hero.views')} ${views}`)
  return parts.join(' · ')
})
const fallbackChar = computed(() => title.value[0] || t('hero.fallbackChar'))

/* 封面：与 PosterGrid 同一套 resolveCover（LRU + in-flight 去重，重复 id 安全） */
const coverSrc = ref('')
const broken = ref(false)
let alive = true
async function resolveNow() {
  const d = props.drama
  if (!d?.cover) return
  const src = await resolveCover(d)
  if (alive && src) coverSrc.value = src
}
watch(
  () => props.drama.id,
  () => {
    coverSrc.value = ''
    broken.value = false
    void resolveNow()
  }
)
onMounted(() => {
  alive = true
  void resolveNow()
})
onBeforeUnmount(() => {
  alive = false
})
</script>

<template>
  <div
    class="hero"
    role="button"
    tabindex="0"
    :aria-label="title"
    data-testid="discover-hero"
    @click="emit('select', drama)"
    @keyup.enter="emit('select', drama)"
  >
    <div class="hero-cover" :style="{ width: COVER_W + 'px', height: COVER_H + 'px' }">
      <img
        v-if="coverSrc && !broken"
        :src="coverSrc"
        :alt="title"
        decoding="async"
        @error="broken = true"
      />
      <div v-else class="hero-fallback">{{ fallbackChar }}</div>
    </div>

    <div class="hero-info">
      <div class="hero-title" :title="title">{{ title }}</div>
      <p v-if="desc" class="hero-desc">{{ desc }}</p>
      <div v-if="tagList.length" class="hero-tags">
        <span v-for="tg in tagList" :key="tg" class="hero-tag">{{ tg }}</span>
      </div>
      <div v-if="statText" class="hero-stat">{{ statText }}</div>
    </div>
  </div>
</template>

<style scoped>
.hero {
  display: flex;
  align-items: center;
  gap: 16px;
  box-sizing: border-box;
  height: 260px;
  padding: 16px;
  flex-shrink: 0;
  cursor: pointer;
  outline: none;
  border-radius: var(--guo-radius-lg);
  background: var(--guo-card-bg);
  /* 常态透明描边占位：hover 换 accent 时不产生 1px 抖动 */
  border: 1px solid transparent;
  box-shadow: 0 1px 2px var(--guo-shadow);
  transition: border-color 0.15s, box-shadow 0.15s;
}
.hero:hover,
.hero:focus-visible {
  border-color: var(--accent);
  box-shadow: 0 6px var(--guo-shadow-blur) var(--guo-shadow);
}

.hero-cover {
  position: relative;
  flex-shrink: 0;
  border-radius: var(--guo-radius);
  overflow: hidden;
  background: var(--cover-bg);
}
.hero-cover img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}
.hero-fallback {
  width: 100%;
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 48px;
  font-weight: 700;
  color: var(--muted-2);
  background: linear-gradient(160deg, var(--surface-3), var(--surface));
}

.hero-info {
  min-width: 0;
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 8px;
}
.hero-title {
  font-size: 19px;
  font-weight: 700;
  line-height: 26px;
  color: var(--text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.hero-desc {
  margin: 0;
  font-size: 12.5px;
  line-height: 19px;
  color: var(--muted);
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  overflow: hidden;
}
.hero-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.hero-tag {
  font-size: 11px;
  line-height: 1;
  padding: 5px 8px;
  border-radius: 999px;
  background: var(--guo-chip-bg);
  color: var(--muted);
}
.hero-stat {
  font-size: 11.5px;
  color: var(--muted-2);
}

/* 窄窗：封面槽退让（大卡不横向溢出，文字优先） */
@media (max-width: 560px) {
  .hero {
    gap: 10px;
    padding: 12px;
  }
  .hero-cover {
    width: 112px !important;
    height: 157px !important;
  }
}
</style>
