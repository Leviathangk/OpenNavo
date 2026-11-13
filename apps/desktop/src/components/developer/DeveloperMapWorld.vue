<script setup lang="ts">
import { computed, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { WORLD_HEIGHT, WORLD_PATHS, WORLD_WIDTH } from '@opennavo/ui';
import { developerPlaceName, type DeveloperMapPlace } from '@/composables/useDeveloperMap';

const props = withDefaults(
  defineProps<{
    places: readonly DeveloperMapPlace[];
    selected?: string | null;
    metric?: 'developers' | 'installs';
    region?: string;
  }>(),
  { selected: null, metric: 'developers', region: 'all' }
);

const emit = defineEmits<{ select: [key: string] }>();
const { t, locale } = useI18n();
const mapEl = ref<HTMLElement>();
const hovered = ref<{ place: DeveloperMapPlace; x: number; y: number } | null>(null);

const number = computed(() => new Intl.NumberFormat(locale.value));
const compact = computed(() => new Intl.NumberFormat(locale.value, { notation: 'compact', maximumFractionDigits: 1 }));

const visiblePlaces = computed(() =>
  props.region === 'all' ? props.places : props.places.filter(place => place.region === props.region)
);

function metricValue(place: DeveloperMapPlace): number {
  return props.metric === 'developers' ? place.developers : place.installs30d;
}

function radius(place: DeveloperMapPlace): number {
  if (props.metric === 'developers') return 3 + Math.sqrt(place.developers) * 0.65;
  return 3 + Math.sqrt(place.installs30d / 1000) * 0.32;
}

function project(lng: number, lat: number): [number, number] {
  return [((lng + 180) / 360) * WORLD_WIDTH, ((84 - lat) / (84 - -56)) * WORLD_HEIGHT];
}

const topLabels = computed(() => [...visiblePlaces.value].sort((a, b) => metricValue(b) - metricValue(a)).slice(0, 6));

function labelPosition(place: DeveloperMapPlace) {
  const [x, y] = project(place.lng, place.lat);
  const end = x > 820;
  return { x: x + (end ? -12 : 12), y: y + 4, anchor: end ? 'end' : 'start' };
}

function show(place: DeveloperMapPlace, event: MouseEvent) {
  const rect = mapEl.value?.getBoundingClientRect();
  if (!rect) return;
  hovered.value = { place, x: event.clientX - rect.left, y: event.clientY - rect.top };
}

function move(event: MouseEvent) {
  if (!hovered.value) return;
  const rect = mapEl.value?.getBoundingClientRect();
  if (!rect) return;
  hovered.value = { ...hovered.value, x: event.clientX - rect.left, y: event.clientY - rect.top };
}

const tipStyle = computed(() => {
  const width = mapEl.value?.clientWidth ?? 0;
  return {
    left: `${Math.max(8, Math.min(width - 244, (hovered.value?.x ?? 0) + 14))}px`,
    top: `${Math.max(8, (hovered.value?.y ?? 0) - 18)}px`
  };
});

function choose(place: DeveloperMapPlace) {
  emit('select', place.key);
}

function onKey(place: DeveloperMapPlace, event: KeyboardEvent) {
  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault();
    choose(place);
  }
}
</script>

<template>
  <div ref="mapEl" class="dm-map">
    <svg
      class="dm-world"
      :viewBox="`0 0 ${WORLD_WIDTH} ${WORLD_HEIGHT}`"
      role="img"
      :aria-label="t('developerMap.mapAria')"
    >
      <defs>
        <radialGradient id="dmGlow">
          <stop offset="0" stop-color="var(--dm-dot)" stop-opacity="0.42" />
          <stop offset="1" stop-color="var(--dm-dot)" stop-opacity="0" />
        </radialGradient>
      </defs>
      <g aria-hidden="true">
        <path v-for="(path, index) in WORLD_PATHS" :key="index" class="dm-land" :d="path" />
      </g>
      <g class="dm-cities">
        <g
          v-for="place in visiblePlaces"
          :key="place.key"
          class="dm-city"
          :class="{ 'is-selected': props.selected === place.key, 'is-top': place.key === 'bay-area' }"
          :transform="`translate(${project(place.lng, place.lat)[0].toFixed(1)} ${project(place.lng, place.lat)[1].toFixed(1)})`"
          tabindex="0"
          role="button"
          :aria-label="`${developerPlaceName(place, locale)} · ${number.format(place.developers)} ${t('developerMap.developers')} · ${number.format(place.apps)} ${t('developerMap.apps')}`"
          :aria-pressed="props.selected === place.key"
          @click="choose(place)"
          @keydown="onKey(place, $event)"
          @mouseenter="show(place, $event)"
          @mousemove="move"
          @mouseleave="hovered = null"
          @focus="hovered = null"
        >
          <g class="dm-city-scale">
            <circle class="dm-halo" :r="radius(place) * 3.1" />
            <circle class="dm-pulse" :r="radius(place) + 3" />
            <circle class="dm-dot" :r="radius(place)" />
            <circle class="dm-hit" :r="Math.max(radius(place) + 9, 16)" />
          </g>
        </g>
      </g>
      <g class="dm-labels" aria-hidden="true">
        <text
          v-for="place in topLabels"
          :key="`label-${place.key}`"
          class="dm-label"
          :x="labelPosition(place).x.toFixed(1)"
          :y="labelPosition(place).y.toFixed(1)"
          :text-anchor="labelPosition(place).anchor"
        >
          {{ developerPlaceName(place, locale) }}
          <tspan class="count" dx="5">{{ number.format(metricValue(place)) }}</tspan>
        </text>
      </g>
    </svg>

    <div v-if="hovered" class="dm-tip is-on" :style="tipStyle">
      <div class="dm-tip-head">
        <b>{{ developerPlaceName(hovered.place, locale) }}</b>
        <span>{{ hovered.place.countryCode }}</span>
      </div>
      <div class="dm-tip-stats">
        <div>
          <b>{{ number.format(hovered.place.developers) }}</b
          ><span>{{ t('developerMap.developers') }}</span>
        </div>
        <div>
          <b>{{ number.format(hovered.place.apps) }}</b
          ><span>{{ t('developerMap.apps') }}</span>
        </div>
        <div>
          <b>{{ compact.format(hovered.place.installs30d) }}</b
          ><span>{{ t('developerMap.installs30d') }}</span>
        </div>
      </div>
      <div v-if="hovered.place.topDevelopers.length" class="dm-tip-devs">
        <span v-for="developer in hovered.place.topDevelopers" :key="developer.name" class="dm-tip-dev">
          {{ developer.name }}
        </span>
      </div>
      <div class="dm-tip-cta">{{ t('developerMap.clickCity') }} →</div>
    </div>

    <div v-if="!visiblePlaces.length" class="dm-empty">{{ t('developerMap.empty') }}</div>
  </div>
</template>

<style scoped>
.dm-map {
  --dm-ocean-1: #161618;
  --dm-ocean-2: #0d0d0f;
  --dm-land: rgba(255, 255, 255, 0.035);
  --dm-land-hover: rgba(255, 255, 255, 0.085);
  --dm-stroke: rgba(255, 255, 255, 0.065);
  --dm-stroke-hover: rgba(255, 255, 255, 0.24);
  --dm-dot: #ff7356;
  --dm-dot-ring: #0c0c0e;
  --dm-label: #a8a8a8;
  --dm-label-strong: #ededed;
  position: relative;
  min-height: 250px;
  max-height: 620px;
  aspect-ratio: 1000 / 420;
  overflow: hidden;
  background: radial-gradient(ellipse 70% 75% at 52% 42%, var(--dm-ocean-1), var(--dm-ocean-2) 72%);
}

:global([data-theme='light']) .dm-map {
  --dm-ocean-1: #f7f7f7;
  --dm-ocean-2: #ededed;
  --dm-land: rgba(0, 0, 0, 0.055);
  --dm-land-hover: rgba(0, 0, 0, 0.11);
  --dm-stroke: rgba(0, 0, 0, 0.085);
  --dm-stroke-hover: rgba(0, 0, 0, 0.26);
  --dm-dot: #ee5533;
  --dm-dot-ring: #f8f8f8;
  --dm-label: #575757;
  --dm-label-strong: #1a1a1a;
}

.dm-map::after {
  position: absolute;
  inset: 0;
  content: '';
  pointer-events: none;
  background: radial-gradient(ellipse 75% 80% at 50% 44%, transparent 52%, rgba(0, 0, 0, 0.34) 100%);
}

:global([data-theme='light']) .dm-map::after {
  background: radial-gradient(ellipse 75% 80% at 50% 44%, transparent 58%, rgba(255, 255, 255, 0.5) 100%);
}

.dm-world {
  display: block;
  width: 100%;
  height: 100%;
  overflow: visible;
}

.dm-land {
  fill: var(--dm-land);
  stroke: var(--dm-stroke);
  stroke-width: 0.65;
  stroke-linejoin: round;
  vector-effect: non-scaling-stroke;
  transition:
    fill 180ms ease,
    stroke 180ms ease;
}

.dm-land:hover {
  fill: var(--dm-land-hover);
  stroke: var(--dm-stroke-hover);
}

.dm-city {
  cursor: pointer;
  outline: none;
}

.dm-city-scale {
  transition: transform 180ms cubic-bezier(0.2, 0.8, 0.2, 1);
}

.dm-city:hover .dm-city-scale,
.dm-city:focus-visible .dm-city-scale {
  transform: scale(1.14);
}

.dm-city .dm-halo {
  fill: url(#dmGlow);
  opacity: 0.95;
}

.dm-city .dm-dot {
  fill: var(--dm-dot);
  stroke: var(--dm-dot-ring);
  stroke-width: 1.2;
  vector-effect: non-scaling-stroke;
}

.dm-city .dm-hit {
  fill: transparent;
  stroke: none;
}

.dm-city.is-selected .dm-dot {
  stroke: var(--dm-label-strong);
  stroke-width: 2;
}

.dm-city.is-selected .dm-halo {
  opacity: 1;
}

.dm-city:focus-visible .dm-dot {
  stroke: var(--dm-label-strong);
  stroke-width: 2;
}

.dm-pulse {
  fill: none;
  stroke: var(--dm-dot);
  stroke-width: 1.2;
  opacity: 0;
  transform-box: fill-box;
  transform-origin: center;
  pointer-events: none;
}

.dm-city.is-top .dm-pulse {
  animation: dmPulse 3.8s cubic-bezier(0.2, 0.8, 0.2, 1) infinite;
}

@keyframes dmPulse {
  0% {
    transform: scale(1);
    opacity: 0.45;
  }
  60% {
    transform: scale(2.7);
    opacity: 0;
  }
  100% {
    transform: scale(2.7);
    opacity: 0;
  }
}

.dm-labels {
  pointer-events: none;
}

.dm-label {
  fill: var(--dm-label);
  font-family: var(--on-font-family-sans);
  font-size: 10.5px;
  font-weight: 500;
  letter-spacing: 0.01em;
  paint-order: stroke;
  stroke: var(--dm-ocean-2);
  stroke-width: 2.4px;
  stroke-linejoin: round;
}

.dm-label .count {
  fill: var(--dm-label-strong);
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.dm-tip {
  position: absolute;
  z-index: 5;
  width: 232px;
  padding: 12px;
  border: 1px solid var(--on-border-default);
  border-radius: 10px;
  background: var(--on-surface-popover);
  box-shadow: var(--on-shadow-popover);
  pointer-events: none;
}

.dm-tip-head {
  display: flex;
  align-items: baseline;
  gap: 6px;
}

.dm-tip-head b {
  font-size: 13px;
  font-weight: 600;
}

.dm-tip-head span {
  font-size: 11px;
  color: var(--on-text-tertiary);
}

.dm-tip-stats {
  display: flex;
  gap: 16px;
  margin-top: 10px;
  padding-top: 10px;
  border-top: 1px solid var(--on-border-subtle);
}

.dm-tip-stats div {
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.dm-tip-stats b {
  font-size: 14px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.dm-tip-stats span {
  font-size: 10.5px;
  color: var(--on-text-tertiary);
}

.dm-tip-devs {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-top: 10px;
}

.dm-tip-dev {
  max-width: 100%;
  overflow: hidden;
  border-radius: 4px;
  background: var(--on-surface-inset);
  padding: 2px 6px;
  color: var(--on-text-secondary);
  font-size: 10.5px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dm-tip-cta {
  margin-top: 9px;
  color: var(--on-text-accent);
  font-size: 11px;
  font-weight: 500;
}

.dm-empty {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  color: var(--on-text-tertiary);
  font-size: 13px;
}
</style>
