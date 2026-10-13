<script setup lang="ts">
import type { PackageDetail } from '@opennavo/api';
import { OnAppIcon, OnChip } from '@opennavo/ui';
import { packagePath } from '~/utils/packageView';

// WP9 confirmed alternatives (WP12 data): editor order, why to pick each one and what to weigh.
// The automatic "similar apps" list stays in the side column with its own title.
const props = defineProps<{ pkg: PackageDetail }>();

const { t } = useI18n();
const localePath = useLocalePath();
const items = computed(() => props.pkg.alternatives ?? []);
</script>

<template>
  <section
    v-if="items.length"
    class="rounded-big border border-solid border-line-subtle bg-surface-card px-18px py-16px"
    aria-labelledby="package-alternatives-title"
    data-testid="package-alternatives"
  >
    <h2 id="package-alternatives-title" class="m-0 text-13.5px font-600 text-ink-primary">
      {{ t('package.alternatives.title') }}
    </h2>
    <p class="m-0 mt-4px text-12px text-ink-tertiary">{{ t('package.alternatives.hint') }}</p>
    <ul class="m-0 mt-8px list-none p-0">
      <li
        v-for="item in items"
        :key="item.package.token"
        class="relative flex gap-12px border-t border-t-solid border-line-subtle py-10px first:border-t-0"
      >
        <OnAppIcon
          :kind="item.package.kind"
          :token="item.package.token"
          :name="item.package.displayName"
          :src="item.package.iconUrl"
          :accent="item.package.accentColor"
          :size="40"
        />
        <div class="min-w-0 flex-1">
          <NuxtLink
            :to="localePath(packagePath(item.package.token))"
            class="text-13px font-600 text-ink-primary no-underline hover:underline"
            >{{ item.package.displayName }}</NuxtLink
          >
          <p v-if="item.package.summary" class="m-0 text-12px text-ink-tertiary">{{ item.package.summary }}</p>
          <div class="mt-6px flex flex-wrap gap-6px">
            <OnChip v-for="reason in item.reasons" :key="reason" tone="accent">{{
              t(`package.alternatives.reasons.${reason}`)
            }}</OnChip>
            <OnChip v-for="limit in item.limits" :key="limit" tone="neutral" outline>{{
              t(`package.alternatives.limits.${limit}`)
            }}</OnChip>
          </div>
          <p v-if="item.bestFor" class="m-0 mt-4px text-12px text-ink-secondary">
            {{ t('package.alternatives.bestFor', { audience: t(`package.alternatives.audience.${item.bestFor}`) }) }}
          </p>
        </div>
      </li>
    </ul>
  </section>
</template>
