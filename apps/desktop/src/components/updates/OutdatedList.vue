<script setup lang="ts">
import { useAppLocale as useFormattingLocale } from '@/composables/useAppLocale';
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatBytes, formatDate, formatVersionChange } from '@opennavo/shared';
import { OnAppRow, OnButton, OnChip, OnMenu } from '@opennavo/ui';
import { RouterLink } from 'vue-router';
import PackageGetButton from '@/components/package/PackageGetButton.vue';
import type { OutdatedItem } from '@/ipc/bindings';
import { packageKey } from '@/ipc/client';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageIdentity } from '@/composables/usePackageIdentity';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useUpdatesStore } from '@/stores';

const { appLocale: formattingLocale } = useFormattingLocale();

// Available updates (mockup 05): version change, download size, release date; second line is frontend-looked-up release summary (06 §6.8).
// Release notes opens version history; More can ignore this version.
const props = defineProps<{
  items: readonly OutdatedItem[];
  summaries: ReadonlyMap<string, { summary: string | null; publishedAt: string | null }>;
}>();

const { t } = useI18n();
const { appLocale } = useAppLocale();
const { displayName, icon } = usePackageIdentity();
const { errorText } = usePackageState();
const updates = useUpdatesStore();
const toasts = useToasts();

function meta(item: OutdatedItem): string {
  const info = props.summaries.get(packageKey(item.kind, item.token));
  return [
    formatVersionChange(item.installedVersion, item.currentVersion),
    item.downloadSize ? formatBytes(item.downloadSize, { locale: formattingLocale.value }) : null,
    info?.publishedAt ? formatDate(info.publishedAt, { locale: appLocale.value }) : null
  ]
    .filter(Boolean)
    .join(' · ');
}

async function ignore(item: OutdatedItem) {
  try {
    await updates.ignore({ kind: item.kind, token: item.token }, item.currentVersion);
    toasts.push({
      tone: 'info',
      title: t('updates.ignored', {
        name: displayName(item.kind, item.token),
        version: item.currentVersion
      })
    });
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

const rows = computed(() =>
  props.items.map(item => ({
    item,
    summary: props.summaries.get(packageKey(item.kind, item.token))?.summary ?? null
  }))
);
</script>

<template>
  <ul class="m-0 list-none rounded-big border border-solid border-line-subtle bg-surface-card p-0">
    <li
      v-for="{ item, summary } in rows"
      :key="packageKey(item.kind, item.token)"
      class="border-t border-t-solid border-line-subtle px-16px first:border-t-0"
    >
      <OnAppRow
        v-bind="icon(item.kind, item.token)"
        :meta="meta(item)"
        :description="summary"
        :href="`/package/${item.kind}/${item.token}`"
        :link-as="RouterLink"
      >
        <template #badges>
          <OnChip v-if="item.autoUpdates" tone="info">{{ t('package.autoUpdates') }}</OnChip>
          <OnChip v-if="item.pinned">{{ t('package.pinned') }}</OnChip>
        </template>
        <template #actions>
          <OnButton
            variant="ghost"
            size="sm"
            :href="`/package/${item.kind}/${item.token}/versions`"
            :link-as="RouterLink"
          >
            {{ t('updates.changelog') }}
          </OnButton>
          <PackageGetButton v-if="!item.pinned" :kind="item.kind" :token="item.token" />
          <span v-else class="text-12px text-ink-tertiary">{{ t('updates.pinned') }}</span>
          <OnMenu
            :items="[{ key: 'ignore', label: t('updates.ignore'), icon: 'x' }]"
            :label="t('common.more')"
            variant="ghost"
            size="sm"
            @select="ignore(item)"
          />
        </template>
      </OnAppRow>
    </li>
  </ul>
</template>
