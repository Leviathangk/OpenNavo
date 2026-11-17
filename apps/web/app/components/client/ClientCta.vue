<script setup lang="ts">
import { OnButton, OnIcon, OnLogo } from '@opennavo/ui';
import type { ClientRoute } from '@opennavo/shared';
import { trackEvent } from '~/utils/analytics';

// Download and handoff card for every client-only page. On a phone or another desktop the useful
// action is to open the page on a Mac, so the DMG download is not presented as if it could run there.
const props = defineProps<{ feature: string; route: ClientRoute }>();

const { t } = useI18n();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');
const toasts = useToasts();
const platform = useVisitorPlatform();
const { opening, openRoute } = useClientHandoff();

const nonMac = computed(() => platform.value !== null && platform.value.system !== 'mac');

function onDownload() {
  trackEvent('download_client', { feature: props.feature });
}

function openClient() {
  openRoute(props.route, props.feature);
}

async function copyLink() {
  try {
    await navigator.clipboard.writeText(window.location.href);
    toasts.push({ tone: 'success', title: t('client.copied') });
  } catch {
    toasts.push({ tone: 'warning', title: t('toast.copyFailed'), description: window.location.href, duration: 0 });
  }
}
</script>

<template>
  <aside
    data-testid="client-cta"
    class="flex h-full flex-col gap-14px rounded-huge border border-solid border-line-subtle bg-surface-card p-18px"
  >
    <div class="flex items-center gap-10px">
      <span
        class="flex h-38px w-38px shrink-0 items-center justify-center rounded-default bg-accent-salmon-subtle text-brand-salmon"
        aria-hidden="true"
      >
        <OnLogo :size="22" />
      </span>
      <div class="min-w-0">
        <h2 class="m-0 text-15px font-600 text-ink-primary">{{ t('sidebar.appTitle') }}</h2>
        <p class="m-0 mt-2px text-12px text-ink-tertiary">{{ t('sidebar.appHint') }}</p>
      </div>
    </div>

    <p v-if="nonMac" class="m-0 text-13px leading-[1.6] text-ink-secondary">{{ t('client.notMac') }}</p>
    <p v-else class="m-0 flex items-center gap-6px text-12.5px text-ink-secondary">
      <OnIcon name="check" :size="13" class="shrink-0 text-status-success" />
      <span>{{ t('landing.more.privacy.title') }}</span>
    </p>

    <div class="mt-auto flex flex-col gap-8px">
      <template v-if="nonMac">
        <OnButton variant="primary" size="lg" block icon="copy" @click="copyLink">
          {{ t('client.copyLink') }}
        </OnButton>
        <OnButton variant="secondary" block :href="localePath('/apps')" :link-as="NuxtLink">
          {{ t('nav.apps') }}
        </OnButton>
      </template>
      <template v-else>
        <OnButton
          variant="primary"
          size="lg"
          block
          icon="download"
          :href="localePath('/download')"
          :link-as="NuxtLink"
          @click="onDownload"
        >
          {{ t('download.button') }}
        </OnButton>
        <!-- Hide the Mac-only action until detection finishes, so a phone never flashes it. -->
        <OnButton
          v-if="platform"
          variant="secondary"
          block
          icon="arrow-up-right"
          :loading="opening"
          @click="openClient"
        >
          {{ opening ? t('client.opening') : t('client.open') }}
        </OnButton>
      </template>
      <p class="m-0 flex items-start gap-6px text-11.5px leading-[1.55] text-ink-tertiary">
        <OnIcon name="lock" :size="13" class="mt-1px shrink-0" />
        <span>{{ t('client.local') }}</span>
      </p>
    </div>
  </aside>
</template>
