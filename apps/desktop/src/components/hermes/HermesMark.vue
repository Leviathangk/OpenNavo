<script setup lang="ts">
import { useId } from 'vue';

withDefaults(defineProps<{ size?: number }>(), { size: 24 });
const gradient = `hermes-${useId()}`;
</script>

<template>
  <svg
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    xmlns="http://www.w3.org/2000/svg"
    class="shrink-0"
    aria-hidden="true"
  >
    <defs>
      <linearGradient :id="gradient" x1="5" y1="20" x2="20" y2="4" gradientUnits="userSpaceOnUse">
        <stop stop-color="#FF8F6B" />
        <stop offset="0.55" stop-color="#C9C9FF" />
        <stop offset="1" stop-color="#8E8EF7" />
      </linearGradient>
    </defs>
    <path
      d="M12 3.15c.38 4.45 1.4 5.47 5.85 5.85-4.45.38-5.47 1.4-5.85 5.85-.38-4.45-1.4-5.47-5.85-5.85 4.45-.38 5.47-1.4 5.85-5.85Z"
      :fill="`url(#${gradient})`"
    />
    <path
      d="M17.15 14.9c.25 2.55-1.15 4.4-3.7 4.95"
      fill="none"
      stroke="#8E8EF7"
      stroke-width="1.35"
      stroke-linecap="round"
      opacity="0.75"
    />
    <circle cx="18.55" cy="14.15" r="1.15" fill="#FFB08F" />
  </svg>
</template>
