<script setup lang="ts">
import type { PackageSummary } from '@opennavo/api';
import { OnAppCard } from '@opennavo/ui';
import type { OnAppCardStat } from '@opennavo/ui';

// Web app card: shared OnAppCard with web Get menu (open client / copy command / add to list), linking to details.
const props = defineProps<{ pkg: PackageSummary; href?: string; stats?: readonly OnAppCardStat[] }>();

const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');
const href = computed(() => props.href ?? localePath(`/apps/${props.pkg.token}`));
</script>

<template>
  <OnAppCard :pkg="pkg" state="get" :stats="stats" :href="href" :link-as="NuxtLink">
    <template #action>
      <GetMenu :kind="pkg.kind" :token="pkg.token" :name="pkg.displayName" :link="pkg.link" />
    </template>
  </OnAppCard>
</template>
