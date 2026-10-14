<script setup lang="ts">
import { recentReturnPath, withRecentReturn } from '@opennavo/shared';
import { computed } from 'vue';
import { RouterLink, RouterView, useRoute } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { OnBreadcrumb, OnPackageNotice, OnSkeleton, OnTabs } from '@opennavo/ui';
import RequestError from '@/components/common/RequestError.vue';
import DetailHeader from '@/components/package/DetailHeader.vue';
import DetailStats from '@/components/package/DetailStats.vue';
import type { Kind } from '@/ipc/bindings';
import { providePackage } from '@/composables/usePackageDetail';
import { useAppLocale } from '@/composables/useAppLocale';

// Detail shell (mockup 03, 06 §13): breadcrumbs, disabled/deprecated notices, header, stats, tabs; use local catalog header offline.
const props = defineProps<{ kind: Kind; token: string }>();

const { t } = useI18n();
const { pick } = useAppLocale();
const route = useRoute();
const context = providePackage(props);
const { detail, local, releaseStats, error } = context;

type Tab = 'overview' | 'versions' | 'dependencies' | 'details';
const tab = computed<Tab>(() => {
  const name = String(route.name ?? '');
  return name === 'package-versions'
    ? 'versions'
    : name === 'package-dependencies'
      ? 'dependencies'
      : name === 'package-details'
        ? 'details'
        : 'overview';
});

const base = computed(() => `/package/${props.kind}/${props.token}`);
const returnTo = computed(() => recentReturnPath(route.query.returnTo));
const tabs = computed(() => [
  { value: 'overview' as const, label: t('package.tabs.overview'), href: withRecentReturn(base.value, returnTo.value) },
  {
    value: 'versions' as const,
    label: t('package.tabs.versions'),
    count: detail.value?.releaseCount || undefined,
    href: withRecentReturn(`${base.value}/versions`, returnTo.value)
  },
  {
    value: 'dependencies' as const,
    label: t('package.tabs.dependencies'),
    href: withRecentReturn(`${base.value}/dependencies`, returnTo.value)
  },
  {
    value: 'details' as const,
    label: t('package.tabs.details'),
    href: withRecentReturn(`${base.value}/details`, returnTo.value)
  }
]);

const name = computed(
  () =>
    detail.value?.displayName ??
    (local.value ? pick(local.value.displayName, local.value.name, local.value.sourceLocale) : props.token)
);
const category = computed(() => detail.value?.primaryCategory);
const breadcrumb = computed(() =>
  returnTo.value
    ? [{ label: t('recent.title'), href: returnTo.value }, { label: name.value }]
    : [
        { label: t('nav.discover'), href: '/discover' },
        ...(category.value ? [{ label: category.value.name, href: `/categories/${category.value.slug}` }] : []),
        { label: name.value }
      ]
);

const notice = computed(() => {
  const value = detail.value;
  if (!value) return null;
  if (value.disable)
    return {
      tone: 'danger' as const,
      text: t('package.disabledNotice', { reason: value.disable.reason ?? t('package.noReason') }),
      replacement: value.disable.replacement
    };
  if (value.deprecation)
    return {
      tone: 'warning' as const,
      text: t('package.deprecatedNotice', {
        reason: value.deprecation.reason ?? t('package.noReason')
      }),
      replacement: value.deprecation.replacement
    };
  return null;
});

const offline = computed(() => Boolean(error.value) && !detail.value);
</script>

<template>
  <div class="flex flex-col gap-14px pt-6px">
    <OnBreadcrumb :items="breadcrumb" :link-as="RouterLink" />
    <template v-if="detail || local">
      <RequestError v-if="offline" :error="error" />
      <!-- Only apps have details pages; command-line alternatives show names only (ADR-018). -->
      <OnPackageNotice
        v-if="notice"
        :tone="notice.tone"
        :text="notice.text"
        :replacement-label="
          notice.replacement ? t('package.replacement', { token: notice.replacement.token }) : undefined
        "
        :replacement-href="
          notice.replacement?.kind === 'cask' ? `/package/cask/${notice.replacement.token}` : undefined
        "
        :link-as="RouterLink"
      />
      <DetailHeader :kind="kind" :token="token" :detail="detail" :local="local" />
      <DetailStats v-if="detail" :pkg="detail" :release-stats="releaseStats" />
      <OnTabs
        class="mt-2px"
        :model-value="tab"
        :items="tabs"
        :aria-label="t('package.tabs.label')"
        :link-as="RouterLink"
      />
      <RouterView />
    </template>
    <RequestError v-else-if="error" :error="error" />
    <div v-else class="flex flex-col gap-14px" aria-hidden="true">
      <OnSkeleton height="146px" radius="huge" />
      <OnSkeleton height="86px" radius="big" />
      <OnSkeleton height="320px" radius="big" />
    </div>
  </div>
</template>
