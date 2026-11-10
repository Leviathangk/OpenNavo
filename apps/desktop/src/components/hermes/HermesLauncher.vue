<script setup lang="ts">
import { computed } from 'vue';
import { RouterLink, useRoute } from 'vue-router';
import HermesMark from './HermesMark.vue';
import { useHermesContent } from '@/composables/useHermesContent';

const route = useRoute();
const content = useHermesContent();
const active = computed(() => route.meta.nav === 'assistant');
</script>

<template>
  <RouterLink
    to="/assistant"
    class="hermes-launcher"
    :class="{ 'hermes-launcher-active': active }"
    :aria-current="active ? 'page' : undefined"
    data-hermes-launcher
  >
    <HermesMark :size="22" />
    <span class="hermes-launcher-copy">
      <span class="hermes-launcher-name">{{ content.launcher.name }}</span>
      <span class="hermes-launcher-sub">{{ content.launcher.sub }}</span>
    </span>
    <kbd class="hermes-launcher-kbd">{{ content.launcher.shortcut }}</kbd>
  </RouterLink>
</template>

<style scoped>
.hermes-launcher {
  display: flex;
  align-items: center;
  gap: 9px;
  width: 100%;
  min-width: 0;
  height: 48px;
  margin: 0 0 10px;
  padding: 0 9px;
  box-sizing: border-box;
  border: 1px solid rgba(170, 170, 255, 0.16);
  border-radius: var(--on-radius-default, 10px);
  background: linear-gradient(
    102deg,
    rgba(123, 114, 238, 0.16),
    rgba(255, 115, 86, 0.06) 62%,
    rgba(255, 255, 255, 0.015)
  );
  color: var(--on-text-primary);
  text-decoration: none;
  outline: none;
  transition:
    border-color 140ms cubic-bezier(0.2, 0.8, 0.2, 1),
    background 140ms cubic-bezier(0.2, 0.8, 0.2, 1);
}
.hermes-launcher:hover {
  border-color: rgba(170, 170, 255, 0.32);
  background: linear-gradient(
    102deg,
    rgba(123, 114, 238, 0.22),
    rgba(255, 115, 86, 0.08) 62%,
    rgba(255, 255, 255, 0.02)
  );
}
.hermes-launcher:focus-visible {
  box-shadow: var(--on-shadow-focus-ring);
}
.hermes-launcher-active {
  border-color: rgba(170, 170, 255, 0.38);
  background: linear-gradient(
    102deg,
    rgba(123, 114, 238, 0.26),
    rgba(255, 115, 86, 0.09) 62%,
    rgba(255, 255, 255, 0.025)
  );
}
.hermes-launcher-copy {
  display: flex;
  flex: 1;
  min-width: 0;
  flex-direction: column;
  gap: 1px;
}
.hermes-launcher-name {
  overflow: hidden;
  color: var(--on-text-primary);
  font-size: 13px;
  font-weight: 600;
  letter-spacing: -0.01em;
  line-height: 1.35;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.hermes-launcher-sub {
  overflow: hidden;
  color: #b8b8b8;
  font-size: 11px;
  line-height: 1.35;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.hermes-launcher-kbd {
  flex: none;
  padding: 3px 5px;
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 4px;
  background: #34343a;
  color: #e4e4e4;
  font-family: inherit;
  font-size: 11px;
  line-height: 1;
}
</style>
