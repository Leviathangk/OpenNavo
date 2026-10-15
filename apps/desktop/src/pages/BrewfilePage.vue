<script setup lang="ts">
import { computed, ref } from 'vue';
import { RouterLink, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { installCommand, toBrewfile } from '@opennavo/shared';
import { OnAppRow, OnButton, OnConfirm, OnEmpty, OnPageHeader } from '@opennavo/ui';
import PackageGetButton from '@/components/package/PackageGetButton.vue';
import RequestError from '@/components/common/RequestError.vue';
import { useBrewfileStore } from '@/stores/brewfile';
import { useEnvStore, useLibraryStore, useTasksStore } from '@/stores';
import type { LocalItem } from '@/ipc/client';
import { commands, unwrap } from '@/ipc/client';
import { useCatalogLoader } from '@/composables/useCatalogLoader';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { pickSavePath } from '@/utils/files';

const { t } = useI18n();
const { pick, appLocale } = useAppLocale();
const router = useRouter();
const brewfile = useBrewfileStore();
const library = useLibraryStore();
const tasks = useTasksStore();
const env = useEnvStore();
const toasts = useToasts();
const { errorText } = usePackageState();
const { data, loading, error } = useCatalogLoader(
  async () => {
    const queue = [...brewfile.items];
    // Bound local IPC fan-out even for a full 200-app list.
    const rows = new Map<string, LocalItem | null>();
    await Promise.all(
      Array.from({ length: Math.min(6, queue.length) }, async () => {
        for (let item = queue.shift(); item; item = queue.shift()) {
          rows.set(item.token, await unwrap(commands.catalogGet(item.kind, item.token)));
        }
      })
    );
    return rows;
  },
  [() => brewfile.items.map(item => item.token).join(','), appLocale],
  rows => [...rows.values()].filter(item => item !== null),
  // Adding, removing and reordering keep the names and icons of listed apps until the reread lands.
  []
);
function nameOf(token: string) {
  const item = data.value?.get(token);
  return item ? pick(item.displayName, item.name, item.sourceLocale) : token;
}
const pending = computed(() =>
  brewfile.items.filter(item => {
    const local = data.value?.get(item.token);
    return local && !local.disabled && !library.find(item.kind, item.token) && !tasks.forPackage(item.kind, item.token);
  })
);
const text = computed(() => toBrewfile(brewfile.items, { title: t('brewfile.fileTitle') }));
const pendingCommands = computed(() => pending.value.map(item => installCommand(item.kind, item.token)).join('\n'));
const confirmOpen = ref(false);
const queueing = ref(false);
const saving = ref(false);
const dragging = ref<number | null>(null);
function drop(index: number) {
  if (dragging.value !== null) brewfile.move(dragging.value, index);
  dragging.value = null;
}
async function copy() {
  try {
    await navigator.clipboard.writeText(text.value);
    toasts.push({ tone: 'success', title: t('common.copied') });
  } catch (cause) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(cause) });
  }
}
async function download() {
  saving.value = true;
  try {
    const path = await pickSavePath('Brewfile');
    if (!path) return;
    await unwrap(
      commands.brewfileExport(
        path,
        brewfile.items.map(({ kind, token }) => ({ kind, token }))
      )
    );
    toasts.push({ tone: 'success', title: t('brewfile.exported') });
  } catch (cause) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(cause) });
  } finally {
    saving.value = false;
  }
}
function requestInstall() {
  if (env.info && !env.hasBrew) {
    void router.push('/welcome');
    return;
  }
  confirmOpen.value = true;
}
async function installAll() {
  if (queueing.value || !pending.value.length) return;
  const targets = pending.value.map(({ kind, token }) => ({ kind, token }));
  queueing.value = true;
  confirmOpen.value = false;
  try {
    const queued = await tasks.enqueueMany('install', targets, 'bundle');
    if (queued.length)
      toasts.push({
        tone: 'success',
        title: t('collections.queued', { count: queued.length }, { plural: queued.length })
      });
  } catch (cause) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(cause) });
  } finally {
    queueing.value = false;
  }
}
</script>

<template>
  <div class="flex flex-col gap-20px pt-6px">
    <OnPageHeader :title="t('brewfile.title')" :subtitle="t('brewfile.description')">
      <p class="m-0 mt-8px text-13px text-ink-tertiary">
        {{ t('brewfile.count', { count: brewfile.items.length }, { plural: brewfile.items.length }) }}
      </p>
    </OnPageHeader>
    <p v-if="brewfile.storageError" role="alert" class="m-0 text-13px text-ink-secondary">
      {{ t('brewfile.saveFailed') }}
    </p>
    <OnEmpty
      v-if="!brewfile.items.length"
      icon="lucide:file-text"
      :title="t('brewfile.emptyTitle')"
      :description="t('brewfile.emptyDescription')"
    >
      <template #actions
        ><OnButton variant="primary" href="/discover" :link-as="RouterLink">{{
          t('brewfile.browse')
        }}</OnButton></template
      >
    </OnEmpty>
    <template v-else>
      <RequestError v-if="error" :error="error" />
      <div class="flex flex-wrap items-center justify-between gap-12px">
        <p class="m-0 text-13px text-ink-secondary">{{ t('brewfile.installHint') }}</p>
        <OnButton
          variant="primary"
          icon="download"
          :disabled="loading || !pending.length || queueing"
          :loading="queueing"
          @click="requestInstall"
          >{{ t('brewfile.install', { count: pending.length }, { plural: pending.length }) }}</OnButton
        >
      </div>
      <ol class="m-0 list-none rounded-big border border-solid border-line-subtle bg-surface-card p-0">
        <li
          v-for="(item, index) in brewfile.items"
          :key="item.token"
          class="px-16px"
          :class="dragging === index ? 'opacity-50' : ''"
          draggable="true"
          @dragstart="dragging = index"
          @dragend="dragging = null"
          @dragover.prevent
          @drop.prevent="drop(index)"
        >
          <OnAppRow
            :kind="item.kind"
            :token="item.token"
            :name="nameOf(item.token)"
            :src="data?.get(item.token)?.iconUrl"
            :accent="data?.get(item.token)?.accentColor"
            :meta="item.token"
            :href="`/package/${item.kind}/${item.token}`"
            :link-as="RouterLink"
          >
            <template #actions>
              <PackageGetButton
                :kind="item.kind"
                :token="item.token"
                :disabled="!data?.get(item.token) || Boolean(data?.get(item.token)?.disabled)"
              />
              <OnButton
                variant="ghost"
                size="sm"
                icon="chevron-up"
                icon-only
                :aria-label="t('brewfile.moveUp', { name: item.token })"
                :disabled="index === 0"
                @click="brewfile.move(index, index - 1)"
              />
              <OnButton
                variant="ghost"
                size="sm"
                icon="chevron-down"
                icon-only
                :aria-label="t('brewfile.moveDown', { name: item.token })"
                :disabled="index === brewfile.items.length - 1"
                @click="brewfile.move(index, index + 1)"
              />
              <OnButton
                variant="ghost"
                size="sm"
                icon="x"
                icon-only
                :aria-label="t('brewfile.remove', { name: item.token })"
                @click="brewfile.remove(item.kind, item.token)"
              />
            </template>
          </OnAppRow>
        </li>
      </ol>
      <section class="flex flex-col gap-10px">
        <div class="flex flex-wrap items-center justify-between gap-8px">
          <h2 class="m-0 text-headline text-ink-primary">Brewfile</h2>
          <div class="flex gap-8px">
            <OnButton icon="copy" @click="copy">{{ t('brewfile.copy') }}</OnButton>
            <OnButton icon="download" :loading="saving" @click="download">{{ t('brewfile.download') }}</OnButton>
          </div>
        </div>
        <pre
          class="m-0 overflow-x-auto rounded-default border border-solid border-line-subtle bg-surface-inset px-14px py-12px font-mono text-12px leading-[1.7] text-ink-secondary"
          >{{ text }}</pre
        >
      </section>
    </template>
    <OnConfirm
      v-model:open="confirmOpen"
      :title="t('brewfile.installTitle')"
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
  </div>
</template>
