<script setup lang="ts">
import { formatBytes, formatRelativeTime } from '@opennavo/shared';
import { OnButton } from '@opennavo/ui';
import { trackEvent } from '~/utils/analytics';
import { githubReleaseUrl, httpsUrl, linkNotes, sortedAssets } from '~/utils/linkApp';
import type { LinkInfo, ReleaseAsset } from '~/utils/linkApp';

// Download panel for link apps (store/security plan §4, mockup A): OpenNavo hosts nothing. It lists the official
// download page and, for GitHub projects, the latest release's files; every button opens the developer's address.
const props = defineProps<{ link: LinkInfo }>();

const { t, locale } = useI18n();
const appLocale = computed(() => toAppLocale(locale.value));

const official = computed(() => httpsUrl(props.link.downloadUrl));
const release = computed(() => props.link.github?.latest ?? null);
const assets = computed(() => sortedAssets(release.value?.assets ?? []).filter(asset => httpsUrl(asset.url)));
const releaseUrl = computed(() => githubReleaseUrl(props.link));
const notes = computed(() => linkNotes(props.link.notes, appLocale.value));

const assetMeta = (asset: ReleaseAsset) =>
  [formatBytes(asset.size, { locale: appLocale.value }), asset.arch ? t(`package.link.arch.${asset.arch}`) : null]
    .filter(Boolean)
    .join(' · ');
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
      <ul v-if="assets.length" class="m-0 flex list-none flex-col p-0">
        <li
          v-for="asset in assets"
          :key="asset.name"
          class="flex items-center gap-8px border-t border-t-solid border-line-subtle py-8px first:border-t-0"
        >
          <div class="min-w-0 flex-1">
            <p class="m-0 truncate font-mono text-12px text-ink-primary">{{ asset.name }}</p>
            <p class="m-0 text-11.5px text-ink-tertiary">{{ assetMeta(asset) }}</p>
          </div>
          <OnButton
            size="xs"
            icon="arrow-up-right"
            :href="asset.url"
            target="_blank"
            rel="noopener noreferrer"
            @click="trackEvent('get_app')"
          >
            {{ t('package.link.download') }}
          </OnButton>
        </li>
      </ul>
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
