<script setup lang="ts">
import type { PackageSummary } from '@opennavo/api';
import { OnAppCard } from '@opennavo/ui';
import type { OnAppCardStat } from '@opennavo/ui';
import { packagePath } from '~/utils/packageView';

// Web app card: shared OnAppCard with web Get menu (open client / copy command / add to list), linking to details.
const props = defineProps<{
  pkg: PackageSummary;
  href?: string;
  stats?: readonly OnAppCardStat[];
  /** Router history state for the click, e.g. where Recently updated should send the visitor back to. */
  linkState?: Record<string, unknown>;
}>();

const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');
const href = computed(() => props.href ?? localePath(packagePath(props.pkg.token)));
</script>

<template>
  <OnAppCard :pkg="pkg" state="get" :stats="stats" :href="href" :link-state="linkState" :link-as="NuxtLink">
    <template #action>
      <GetMenu :kind="pkg.kind" :token="pkg.token" :name="pkg.displayName" :link="pkg.link" />
    </template>
  </OnAppCard>
</template>
