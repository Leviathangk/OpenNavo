<script setup lang="ts">
import { formatRelativeTime, selectAssets } from '@opennavo/shared';
import { OnButton } from '@opennavo/ui';
import { trackEvent } from '~/utils/analytics';
import { githubReleaseUrl, httpsUrl, linkNotes } from '~/utils/linkApp';
import type { LinkInfo } from '~/utils/linkApp';

// Download panel for link apps (store/security plan §4, mockup A): OpenNavo hosts nothing. It lists the official
// download page and, for GitHub projects, the latest release's files; every button opens the developer's address.
// Only the Mac installers that suit the visitor's Mac are listed; the other chip's and the files for other systems
// wait behind "other installers". The page is rendered before the chip is known, so it first lists both chips.
const props = defineProps<{ link: LinkInfo }>();

const { t, locale } = useI18n();
const appLocale = computed(() => toAppLocale(locale.value));

const official = computed(() => httpsUrl(props.link.downloadUrl));
const release = computed(() => props.link.github?.latest ?? null);
const platform = useVisitorPlatform();
// Only a Mac has a chip to match; anyone else, or a browser that does not say, gets the installers of both chips.
const chip = computed(() => (platform.value?.system === 'mac' ? platform.value.chip : null));
const files = computed(() => (release.value?.assets ?? []).filter(asset => httpsUrl(asset.url)));
const choice = computed(() => selectAssets(files.value, chip.value));
const releaseUrl = computed(() => githubReleaseUrl(props.link));
const notes = computed(() => linkNotes(props.link.notes, appLocale.value));
// Why the list is what it is, said once the visitor's system is known.
const note = computed(() => {
  const visitor = platform.value;
  if (!visitor || !choice.value.narrowed) return '';
  if (visitor.system !== 'mac') return t('package.link.notMac');
  return chip.value
    ? t('package.link.filtered', { chip: t(`package.link.arch.${chip.value}`) })
    : t('package.link.chipUnknown');
});
</script>

<template>
  <section
    class="flex flex-col gap-10px rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-14px"
    aria-labelledby="link-download"
  >
    <h2 id="link-download" class="m-0 text-13.5px font-600 text-ink-primary">{{ t('package.link.downloadTitle') }}</h2>
    <OnButton
      v-if="official"
      variant="primary"
      icon="arrow-up-right"
      :href="official"
      target="_blank"
      rel="noopener noreferrer"
      @click="trackEvent('get_app')"
    >
      {{ t('package.link.officialDownload') }}
    </OnButton>
    <template v-if="release">
      <p class="m-0 text-12px text-ink-tertiary">
        {{
          t('package.link.release', {
            version: release.version,
            time: formatRelativeTime(release.publishedAt, { locale: appLocale })
          })
        }}
      </p>
      <p v-if="note" class="m-0 text-11.5px text-ink-tertiary">{{ note }}</p>
      <ul v-if="choice.main.length" class="m-0 flex list-none flex-col p-0">
        <LinkAssetRow v-for="asset in choice.main" :key="asset.name" :asset="asset" />
      </ul>
      <details v-if="choice.rest.length" class="text-12px">
        <summary class="cursor-pointer select-none text-ink-secondary">
          {{ t('package.link.others', { count: choice.rest.length }) }}
        </summary>
        <ul class="m-0 mt-4px flex list-none flex-col p-0">
          <LinkAssetRow v-for="asset in choice.rest" :key="asset.name" :asset="asset" />
        </ul>
      </details>
      <OnButton
        v-if="releaseUrl"
        variant="ghost"
        size="sm"
        icon="arrow-up-right"
        :href="releaseUrl"
        target="_blank"
        rel="noopener noreferrer"
      >
        {{ t('package.link.allFiles') }}
      </OnButton>
    </template>
    <OnButton
      v-else-if="releaseUrl"
      :variant="official ? 'secondary' : 'primary'"
      icon="arrow-up-right"
      :href="releaseUrl"
      target="_blank"
      rel="noopener noreferrer"
      @click="trackEvent('get_app')"
    >
      {{ t('package.link.githubReleases') }}
    </OnButton>
    <p v-if="notes" class="m-0 text-12px leading-[1.55] text-ink-secondary">{{ notes }}</p>
    <p class="m-0 text-11.5px leading-[1.55] text-ink-tertiary">{{ t('package.link.notHosted') }}</p>
  </section>
</template>
