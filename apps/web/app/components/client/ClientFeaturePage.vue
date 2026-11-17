<script setup lang="ts">
import { OnChip, OnIcon, OnPageHeader } from '@opennavo/ui';
import type { ClientRoute } from '@opennavo/shared';
import ClientCta from './ClientCta.vue';

// Shared page for a desktop destination the browser cannot run. The preview is decorative and labelled
// as sample data; the two lists say plainly what works here and what happens in the Mac app.
defineProps<{
  feature: string;
  route: ClientRoute;
  title: string;
  lede: string;
  appPoints: readonly string[];
}>();

const { t } = useI18n();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');

const webPoints = computed(() => [t('client.web.browse'), t('client.web.command'), t('client.web.brewfile')]);
</script>

<template>
  <div class="mx-auto box-border flex w-full max-w-980px flex-col gap-20px pb-8px">
    <OnPageHeader :title="title" :subtitle="lede">
      <div class="mt-12px flex flex-wrap items-center gap-8px">
        <OnChip tone="accent" icon="app-window">{{ t('client.requires') }}</OnChip>
        <span class="text-12.5px text-ink-tertiary">{{ t('sidebar.appHint') }}</span>
      </div>
    </OnPageHeader>

    <div class="grid gap-12px lg:grid-cols-[minmax(0,1.35fr)_minmax(0,1fr)]">
      <section
        class="relative order-2 flex min-h-320px flex-col overflow-hidden rounded-huge border border-solid border-line-subtle bg-surface-card px-18px pb-16px pt-14px lg:order-1"
        :aria-label="t('client.preview')"
      >
        <div class="flex items-center justify-between gap-10px">
          <span class="text-11px font-600 uppercase tracking-[0.08em] text-ink-tertiary">{{
            t('client.preview')
          }}</span>
          <span
            class="rounded-full border border-solid border-line-subtle px-7px py-2px text-[10px] font-500 text-ink-tertiary"
          >
            {{ t('client.sample') }}
          </span>
        </div>
        <div class="relative flex min-h-270px flex-1 items-center justify-center px-4px py-18px">
          <div class="client-preview-glow pointer-events-none absolute rounded-full" aria-hidden="true"></div>
          <LandingDemoViewport class="relative w-full"><slot name="preview" /></LandingDemoViewport>
        </div>
      </section>

      <!-- Put the download/handoff card before the sample preview on phones. -->
      <ClientCta class="order-1 lg:order-2" :feature="feature" :route="route" />
    </div>

    <div class="grid gap-12px md:grid-cols-2">
      <section class="rounded-big border border-solid border-line-subtle bg-surface-card p-18px">
        <h2 class="m-0 text-15px font-600 text-ink-primary">{{ t('client.webTitle') }}</h2>
        <ul class="m-0 mt-12px flex list-none flex-col gap-10px p-0">
          <li
            v-for="point in webPoints"
            :key="point"
            class="flex items-start gap-9px text-13px leading-[1.6] text-ink-secondary"
          >
            <span
              class="mt-2px inline-flex h-18px w-18px shrink-0 items-center justify-center rounded-full bg-status-success-subtle text-status-success"
              aria-hidden="true"
            >
              <OnIcon name="check" :size="12" />
            </span>
            <span class="min-w-0">{{ point }}</span>
          </li>
        </ul>
      </section>

      <section class="rounded-big border border-solid border-line-subtle bg-surface-card p-18px">
        <h2 class="m-0 text-15px font-600 text-ink-primary">{{ t('client.appTitle') }}</h2>
        <ul class="m-0 mt-12px flex list-none flex-col gap-10px p-0">
          <li
            v-for="point in appPoints"
            :key="point"
            class="flex items-start gap-9px text-13px leading-[1.6] text-ink-secondary"
          >
            <span
              class="mt-2px inline-flex h-18px w-18px shrink-0 items-center justify-center rounded-full bg-accent-salmon-subtle text-brand-salmon"
              aria-hidden="true"
            >
              <OnIcon name="check" :size="12" />
            </span>
            <span class="min-w-0">{{ point }}</span>
          </li>
        </ul>
      </section>
    </div>

    <section class="rounded-big border border-solid border-line-subtle bg-surface-card p-18px">
      <h2 class="m-0 text-15px font-600 text-ink-primary">{{ t('client.whyTitle') }}</h2>
      <p class="m-0 mt-8px max-w-760px text-13px leading-[1.65] text-ink-secondary">{{ t('client.why') }}</p>
      <p class="m-0 mt-8px text-12.5px text-ink-tertiary">{{ t('client.local') }}</p>
    </section>

    <slot name="extra" />

    <nav class="flex flex-wrap items-center gap-x-18px gap-y-8px text-12.5px" :aria-label="t('client.related')">
      <NuxtLink :to="localePath('/download')" class="text-ink-primary no-underline hover:underline">
        {{ t('nav.download') }}
      </NuxtLink>
      <NuxtLink
        :to="localePath('/apps')"
        class="text-ink-secondary no-underline hover:text-ink-primary hover:underline"
      >
        {{ t('nav.apps') }}
      </NuxtLink>
      <NuxtLink
        :to="{ path: localePath('/collections'), hash: '#list' }"
        class="text-ink-secondary no-underline hover:text-ink-primary hover:underline"
      >
        {{ t('nav.brewfile') }}
      </NuxtLink>
      <NuxtLink
        :to="localePath('/feedback')"
        class="text-ink-secondary no-underline hover:text-ink-primary hover:underline"
      >
        {{ t('nav.feedback') }}
      </NuxtLink>
    </nav>
  </div>
</template>

<style>
.client-preview-glow {
  left: 50%;
  top: 50%;
  width: 460px;
  height: 460px;
  margin: -230px 0 0 -230px;
  background: var(--on-effect-cover-glow);
  opacity: 0.35;
}
</style>
