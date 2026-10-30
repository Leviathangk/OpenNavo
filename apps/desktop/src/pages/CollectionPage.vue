<script setup lang="ts">
import { computed, ref } from 'vue';
import { RouterLink } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { unwrap } from '@opennavo/api';
import { catalogInstallCommand } from '@/utils/brew';
import { OnButton, OnCollectionView, OnConfirm } from '@opennavo/ui';
import type { OnCollectionEntry } from '@opennavo/ui';
import { OnMarkdown } from '@opennavo/ui/markdown';
import RequestError from '@/components/common/RequestError.vue';
import PackageGetButton from '@/components/package/PackageGetButton.vue';
import { api } from '@/api';
import { useAppLocale } from '@/composables/useAppLocale';
import { useRemoteLoader } from '@/composables/useRemoteLoader';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useLibraryStore, useTasksStore } from '@/stores';
import { useNamesStore } from '@/stores/names';

// Collection details (API): body, items, recommendations; the app UI previews the batch and sends trigger=bundle
// unchanged through IPC, preserving the queue's unattended-run and pause policies. Installed, disabled and link
// items stay out of the batch. Rust handles deep-link collection installs from the opaque pending id.
// Shared with the web view (ADR-017).
const props = defineProps<{ slug: string }>();

const { t } = useI18n();
const { appLocale } = useAppLocale();
const library = useLibraryStore();
const tasks = useTasksStore();
const names = useNamesStore();
const toasts = useToasts();
const { errorText } = usePackageState();

const { data, error } = useRemoteLoader(
  'collection',
  () =>
    unwrap(
      api.GET('/collections/{slug}', {
        params: { path: { slug: props.slug } }
      })
    ),
  [() => props.slug, appLocale]
);

// Install the whole collection: link apps are downloaded from their sites, so they stay out of the batch.
const pending = computed(() =>
  (data.value?.items ?? []).filter(
    item =>
      item.package.kind !== 'link' && !item.package.disabled && !library.find(item.package.kind, item.package.token)
  )
);
const pendingCommands = computed(() =>
  pending.value
    .flatMap(item =>
      item.package.kind === 'link'
        ? []
        : [
            catalogInstallCommand(
              item.package.kind,
              item.package.token,
              names.lookup(item.package.kind, item.package.token)
            )
          ]
    )
    .join('\n')
);
const entries = computed<OnCollectionEntry[]>(() =>
  (data.value?.items ?? []).map(item => ({
    key: `${item.package.kind}/${item.package.token}`,
    kind: item.package.kind,
    token: item.package.token,
    name: item.package.displayName,
    src: item.package.iconUrl,
    accent: item.package.accentColor,
    description: item.note ?? item.package.summary,
    href: `/package/${item.package.kind}/${item.package.token}`
  }))
);
const disabledOf = (token: string) => data.value?.items.find(item => item.package.token === token)?.package.disabled;
const confirmOpen = ref(false);
const queueing = ref(false);

async function installAll() {
  queueing.value = true;
  try {
    const targets = pending.value.map(item => ({
      kind: item.package.kind,
      token: item.package.token
    }));
    confirmOpen.value = false;
    const queued = await tasks.enqueueMany('install', targets, 'bundle');
    if (!queued.length) return;
    toasts.push({
      tone: 'success',
      title: t('collections.queued', { count: queued.length }, { plural: queued.length })
    });
    confirmOpen.value = false;
  } catch (cause) {
    toasts.push({
      tone: 'danger',
      title: t('errors.actionFailed'),
      description: errorText(cause)
    });
  } finally {
    queueing.value = false;
  }
}
</script>

<template>
  <OnCollectionView
    :breadcrumb="[{ label: t('collections.title'), href: '/collections' }, { label: data?.title ?? slug }]"
    :title="data?.title ?? null"
    :subtitle="data?.subtitle"
    :entries="entries"
    :loading="!data && !error"
    :link-as="RouterLink"
  >
    <RequestError v-if="error && !data" :error="error" class="mt-16px" />
    <template #actions>
      <OnButton variant="primary" icon="lucide:download" :disabled="!pending.length" @click="confirmOpen = true">
        {{ t('collections.install') }}
      </OnButton>
    </template>
    <template v-if="data?.body" #body>
      <OnMarkdown size="md" :heading-level="2" :source="data.body" />
    </template>
    <template #action="{ entry }">
      <PackageGetButton :kind="entry.kind" :token="entry.token" :disabled="disabledOf(entry.token)" />
    </template>
  </OnCollectionView>
  <OnConfirm
    v-if="data"
    v-model:open="confirmOpen"
    :title="t('collections.installTitle', { title: data.title })"
    :description="t('collections.installDescription', { count: pending.length }, { plural: pending.length })"
    :confirm-label="t('collections.installConfirm')"
    :loading="queueing"
    @confirm="installAll"
  >
    <pre
      class="m-0 mt-12px max-h-200px overflow-y-auto rounded-default bg-surface-inset px-14px py-12px font-mono text-12px leading-[1.7] text-ink-secondary"
      >{{ pendingCommands }}</pre
    >
  </OnConfirm>
</template>
