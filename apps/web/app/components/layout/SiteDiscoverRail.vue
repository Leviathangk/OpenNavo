<script setup lang="ts">
import { formatCountCompact } from '@opennavo/shared';
import { collectionIcons, OnCollectionCard, OnRailPanel, OnRailSection, OnRankRow } from '@opennavo/ui';
import { packagePath } from '~/utils/packageView';

// Discover rail (05 §11.2): weekly popular/featured collections, versus desktop local overview/popular; below main content under xl.
const { t, locale } = useI18n();
const localePath = useLocalePath();
const appLocale = computed(() => toAppLocale(locale.value));
const NuxtLink = resolveComponent('NuxtLink');

// Do not await shared Discover data; SSR still waits through useAsyncData's serverPrefetch.
const { data: home } = useHome();
const { data: weekly } = useWeeklyTop();
const collection = computed(() => home.value?.collections[0]);
</script>

<template>
  <OnRailPanel responsive>
    <OnRailSection v-if="weekly?.records.length" :title="t('home.weeklyTop')" :subtitle="t('home.weeklyTopHint')">
      <ol class="m-0 flex list-none flex-col gap-8px p-0">
        <li v-for="entry in weekly.records" :key="entry.package.token">
          <OnRankRow
            :rank="entry.rank"
            :pkg="entry.package"
            :value="formatCountCompact(entry.installs, { locale: appLocale })"
            :href="localePath(packagePath(entry.package.token))"
            :link-as="NuxtLink"
          />
        </li>
      </ol>
    </OnRailSection>

    <OnRailSection v-if="collection" :title="t('rail.featuredCollection')">
      <OnCollectionCard
        :title="collection.title"
        :subtitle="collection.subtitle"
        :count="collection.itemCount"
        :icons="collectionIcons(collection)"
        :href="localePath(`/collections/${collection.slug}`)"
        :link-as="NuxtLink"
      />
    </OnRailSection>
  </OnRailPanel>
</template>
