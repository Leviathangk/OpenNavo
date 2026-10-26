<script setup lang="ts">
import { formatDate } from '~/utils/packageDate';
import type { PublicComponents } from '@opennavo/api';
import { formatVersion } from '@opennavo/shared';
import { OnAppIcon, OnAppRow } from '@opennavo/ui';
import { packagePath } from '~/utils/packageView';

type RemovedPackage = PublicComponents['schemas']['RemovedPackage'];

// An app Homebrew removed (WP2): the page answers 410 but still says what it was, its last version,
// the replacement Homebrew named and similar apps that can be installed. No install actions.
const props = defineProps<{ info: RemovedPackage }>();

const { t, locale } = useI18n();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');
const appLocale = computed(() => toAppLocale(locale.value));

usePageSeo({
  title: () => t('package.removed.metaTitle', { name: props.info.displayName }),
  description: () => props.info.summary ?? undefined,
  noindex: true
});
</script>

<template>
  <section class="flex flex-col gap-24px pt-8px" data-testid="package-removed">
    <div class="flex items-start gap-16px">
      <OnAppIcon kind="cask" :token="info.token" :name="info.displayName" :src="info.iconUrl" :size="64" />
      <div class="flex min-w-0 flex-col gap-8px">
        <h1 class="m-0 text-title1 text-ink-primary">{{ t('package.removed.title', { name: info.displayName }) }}</h1>
        <p v-if="info.summary" class="m-0 text-body text-ink-secondary">{{ info.summary }}</p>
        <p class="m-0 text-14px leading-[1.7] text-ink-secondary">
          {{
            t('package.removed.body', {
              version: formatVersion(info.lastVersion),
              date: formatDate(info.removedAt, { locale: appLocale })
            })
          }}
        </p>
      </div>
    </div>
    <section v-if="info.replacement" class="flex flex-col gap-4px">
      <h2 class="m-0 text-15px font-600 text-ink-primary">{{ t('package.removed.replacement') }}</h2>
      <OnAppRow
        kind="cask"
        :token="info.replacement.token"
        :name="info.replacement.displayName"
        :src="info.replacement.iconUrl"
        :accent="info.replacement.accentColor"
        :description="info.replacement.summary"
        :href="localePath(packagePath(info.replacement.token))"
        :link-as="NuxtLink"
      />
    </section>
    <section v-if="info.alternatives.length" class="flex flex-col gap-4px">
      <h2 class="m-0 text-15px font-600 text-ink-primary">{{ t('package.removed.alternatives') }}</h2>
      <OnAppRow
        v-for="item in info.alternatives"
        :key="item.token"
        kind="cask"
        :token="item.token"
        :name="item.displayName"
        :src="item.iconUrl"
        :accent="item.accentColor"
        :description="item.summary"
        :href="localePath(packagePath(item.token))"
        :link-as="NuxtLink"
      />
    </section>
    <section v-if="info.categories.length" class="flex flex-col gap-8px">
      <h2 class="m-0 text-15px font-600 text-ink-primary">{{ t('package.removed.categories') }}</h2>
      <div class="flex flex-wrap gap-8px">
        <NuxtLink
          v-for="category in info.categories"
          :key="category.slug"
          :to="localePath(`/categories/${category.slug}`)"
          class="rounded-default border border-solid border-line-default px-12px py-6px text-13px text-ink-secondary no-underline hover:text-ink-primary focus-visible:shadow-focus-ring"
        >
          {{ category.name }}
        </NuxtLink>
      </div>
    </section>
  </section>
</template>
