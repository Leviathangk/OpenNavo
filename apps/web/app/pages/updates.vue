<script setup lang="ts">
import { OnIcon, OnInstallCommand } from '@opennavo/ui';
import ClientFeaturePage from '~/components/client/ClientFeaturePage.vue';
import { SAMPLE_APPS } from '~/utils/landing';
import { desktopAppId } from '~/utils/structuredData';

// Updates are computed from the apps on this Mac, which a browser cannot see. The page explains the
// app's scheduled checks and offers the read-only Terminal equivalent for people who prefer it.
const { t, locale } = useI18n();
const toasts = useToasts();
const apps = SAMPLE_APPS.slice(0, 3);
const siteUrl = useRuntimeConfig().public.siteUrl.replace(/\/$/, '');
const UPDATE_COMMAND = 'brew outdated --cask --greedy';

const appPoints = computed(() => [
  t('landing.updates.points.schedule'),
  t('landing.updates.points.verify'),
  t('landing.updates.points.rollback'),
  t('landing.updates.points.pin')
]);

function onCopied(command: string) {
  toasts.push({ tone: 'success', title: t('toast.copied'), description: command });
}

usePageSeo({
  title: () => `${t('landing.window.updates')} · OpenNavo`,
  description: () => t('landing.updates.body').replaceAll('**', '')
});
defineOgImage('OgDefaultSatori', {
  locale: toAppLocale(locale.value),
  variant: 'home',
  title: `${t('landing.window.updates')} · OpenNavo`,
  description: t('landing.updates.body').replaceAll('**', ''),
  ...useOgFrame()
});
usePageSchema({ about: desktopAppId(siteUrl) });
</script>

<template>
  <ClientFeaturePage
    feature="updates"
    route="updates"
    :title="t('landing.window.updates')"
    :lede="t('landing.updates.body').replaceAll('**', '')"
    :app-points="appPoints"
  >
    <template #preview>
      <LandingTrayDemo :apps="apps" />
    </template>
    <template #extra>
      <section class="rounded-big border border-solid border-line-subtle bg-surface-card p-18px">
        <div class="flex items-start gap-10px">
          <span
            class="mt-1px flex h-28px w-28px shrink-0 items-center justify-center rounded-default bg-surface-raised text-ink-tertiary"
          >
            <OnIcon name="square-terminal" :size="15" />
          </span>
          <div class="min-w-0">
            <h2 class="m-0 text-14px font-600 text-ink-primary">{{ t('client.pages.updates.commandTitle') }}</h2>
            <p class="m-0 mt-3px text-12.5px leading-[1.6] text-ink-secondary">
              {{ t('client.pages.updates.commandNote') }}
            </p>
          </div>
        </div>
        <OnInstallCommand
          class="mt-14px"
          :title="t('client.pages.updates.commandLabel')"
          :command="UPDATE_COMMAND"
          :wrap-at="70"
          @copied="onCopied"
        />
      </section>
    </template>
  </ClientFeaturePage>
</template>
