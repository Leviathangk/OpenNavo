<script setup lang="ts">
import { unwrap } from '@opennavo/api';
import type { PackageDetail } from '@opennavo/api';
import { toBrewfile } from '@opennavo/shared';
import { OnAppRow, OnButton, OnEmpty, OnInstallCommand, useUiMessages } from '@opennavo/ui';
import { packagePath } from '~/utils/packageView';
import { withVariantNames } from '~/utils/seoTitles';

// Brewfile generator (05 §7.2, 08 §10.14): browser-only via routeRules ssr:false; list from localStorage.
const { t } = useI18n();
const api = useApi();
const localePath = useLocalePath();
const messages = useUiMessages();
const toasts = useToasts();
const brewfile = useBrewfileList();
const NuxtLink = resolveComponent('NuxtLink');

// Fetch names/icons from details on demand; max 200 items and six concurrent requests.
const details = ref<Record<string, PackageDetail | null>>({});
const keyOf = (item: { kind: string; token: string }) => `${item.kind}/${item.token}`;

async function loadMissing() {
  const missing = brewfile.items.value.filter(item => !(keyOf(item) in details.value));
  const queue = [...missing];
  const worker = async () => {
    for (let item = queue.shift(); item; item = queue.shift()) {
      const key = keyOf(item);
      details.value[key] = null;
      try {
        details.value[key] = withVariantNames(
          await unwrap(
            api.GET('/packages/{kind}/{token}', { params: { path: { kind: item.kind, token: item.token } } })
          )
        );
      } catch {
        // Removed packages retain token display and remain included in generated Brewfiles.
      }
    }
  };
  await Promise.all(Array.from({ length: Math.min(6, queue.length) }, worker));
}

watch(
  () => brewfile.items.value.map(keyOf).join(','),
  () => void loadMissing(),
  { immediate: true }
);

const text = computed(() => toBrewfile(brewfile.items.value, { title: t('brewfile.fileTitle') }));

async function copyText() {
  try {
    await navigator.clipboard.writeText(text.value);
    toasts.push({ tone: 'success', title: t('brewfile.copied') });
  } catch {
    toasts.push({ tone: 'warning', title: t('toast.copyFailed'), duration: 0 });
  }
}

function download() {
  // text/plain makes browsers append .txt to Brewfile.
  const url = URL.createObjectURL(new Blob([text.value], { type: 'application/octet-stream' }));
  const anchor = Object.assign(document.createElement('a'), { href: url, download: 'Brewfile' });
  anchor.click();
  URL.revokeObjectURL(url);
}

// Drag sorting; keyboard/screen-reader users use Up/Down buttons.
const dragging = ref<number | null>(null);

function onDrop(index: number) {
  if (dragging.value !== null) brewfile.move(dragging.value, index);
  dragging.value = null;
}

usePageSeo({ title: () => t('brewfile.metaTitle'), description: () => t('brewfile.description'), noindex: true });
</script>

<template>
  <div class="mx-auto box-border flex max-w-960px flex-col gap-20px pt-14px">
    <PageHeader
      :title="t('brewfile.title')"
      :description="t('brewfile.description')"
      :meta="
        brewfile.loaded.value
          ? t('brewfile.count', { count: brewfile.count.value }, { plural: brewfile.count.value })
          : undefined
      "
    />
    <template v-if="brewfile.loaded.value">
      <OnEmpty
        v-if="!brewfile.count.value"
        icon="lucide:file-text"
        :title="t('brewfile.emptyTitle')"
        :description="t('brewfile.emptyDescription')"
      >
        <template #actions>
          <OnButton variant="primary" :href="localePath('/discover')" :link-as="NuxtLink">{{
            t('brewfile.browse')
          }}</OnButton>
        </template>
      </OnEmpty>
      <template v-else>
        <ol class="m-0 list-none rounded-big border border-solid border-line-subtle bg-surface-card p-0">
          <li
            v-for="(item, index) in brewfile.items.value"
            :key="keyOf(item)"
            class="px-16px"
            :class="dragging === index ? 'opacity-50' : ''"
            draggable="true"
            @dragstart="dragging = index"
            @dragend="dragging = null"
            @dragover.prevent
            @drop.prevent="onDrop(index)"
          >
            <OnAppRow
              :kind="item.kind"
              :token="item.token"
              :name="details[keyOf(item)]?.displayName ?? item.token"
              :src="details[keyOf(item)]?.iconUrl"
              :accent="details[keyOf(item)]?.accentColor"
              :meta="`${messages.kind[item.kind]} · ${item.token}`"
              :href="localePath(packagePath(item.token))"
              :link-as="NuxtLink"
            >
              <template #actions>
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
                  :disabled="index === brewfile.count.value - 1"
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
              <OnButton icon="copy" @click="copyText">{{ messages.action.copy }}</OnButton>
              <OnButton variant="primary" icon="download" @click="download">{{ t('brewfile.download') }}</OnButton>
            </div>
          </div>
          <pre
            class="m-0 overflow-x-auto rounded-default border border-solid border-line-subtle bg-surface-inset px-14px py-12px font-mono text-12px leading-[1.7] text-ink-secondary"
            >{{ text }}</pre
          >
        </section>
        <section class="rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-14px">
          <OnInstallCommand
            :title="t('brewfile.usage')"
            command="brew bundle --file=~/Downloads/Brewfile"
            :wrap-at="80"
          />
          <p class="m-0 mt-10px text-12.5px leading-[1.6] text-ink-tertiary">{{ t('brewfile.usageHint') }}</p>
        </section>
      </template>
    </template>
  </div>
</template>
