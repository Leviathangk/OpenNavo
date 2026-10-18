<script setup lang="ts">
import { OnIcon } from '@opennavo/ui';

// The six security checks (store/security plan §6), shared by the home page's security section and /security.
const { t } = useI18n();

const POINTS = [
  { key: 'scan', icon: 'scan-search' },
  { key: 'install', icon: 'package' },
  { key: 'downloads', icon: 'download' },
  { key: 'startup', icon: 'power' },
  { key: 'signatures', icon: 'key-round' },
  { key: 'posture', icon: 'lock' }
] as const;
</script>

<template>
  <ul class="m-0 grid list-none gap-12px p-0 md:grid-cols-2 lg:grid-cols-3">
    <li
      v-for="(point, index) in POINTS"
      :key="point.key"
      class="flex flex-col gap-10px rounded-big border border-solid border-line-subtle bg-surface-card px-20px pb-20px pt-18px"
      data-reveal
      :style="{ '--landing-delay': `${(index % 3) * 90}ms` }"
    >
      <span
        class="grid h-36px w-36px place-items-center rounded-default bg-status-success-subtle text-status-success"
        aria-hidden="true"
      >
        <OnIcon :name="point.icon" :size="18" />
      </span>
      <h3 class="m-0 text-15px font-600 text-ink-primary">{{ t(`landing.security.points.${point.key}.title`) }}</h3>
      <p class="m-0 text-13.5px leading-[1.65] text-ink-secondary">
        {{ t(`landing.security.points.${point.key}.body`) }}
      </p>
    </li>
  </ul>
</template>
