<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatBytes, formatRelativeTime } from '@opennavo/shared';
import { OnButton, OnChip } from '@opennavo/ui';
import type { LinkInfo, LinkOpenTarget, ReleaseAsset } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useEnvStore } from '@/stores';
import { useLinksStore } from '@/stores/links';

// Download panel for link apps (store/security plan §4, mockup A): OpenNavo hosts nothing. It lists the official
// download page and, for GitHub projects, the latest release's files with the one for this Mac's chip first.
const props = defineProps<{ token: string; link: LinkInfo }>();

const { t } = useI18n();
const { appLocale, pick } = useAppLocale();
const env = useEnvStore();
const links = useLinksStore();
const toasts = useToasts();
const { errorText } = usePackageState();

const appleSilicon = computed(() => ['arm64', 'aarch64'].includes(env.info?.arch ?? 'arm64'));
const release = computed(() => props.link.github?.latest ?? null);
// Only positively identified macOS installers can be recommended. A ZIP's
// architecture alone says nothing about its operating system.
const isMacInstaller = (asset: ReleaseAsset) => {
  if (/(^|[^a-z0-9])(linux|windows|win32|win64|win|ubuntu|debian|android)(?=[^a-z0-9]|$)/i.test(asset.name)) {
    return false;
  }
  return (
    asset.format === 'dmg' ||
    asset.format === 'pkg' ||
    (asset.format === 'zip' && /(^|[^a-z0-9])(mac|macos|macosx|osx|darwin)(?=[^a-z0-9]|$)/i.test(asset.name))
  );
};
const fits = (asset: ReleaseAsset) =>
  asset.arch === 'universal' || asset.arch === (appleSilicon.value ? 'arm64' : 'x86_64');
const assets = computed(() => {
  const rank = (asset: ReleaseAsset) => (isMacInstaller(asset) ? 0 : 2) + (fits(asset) ? 0 : 1);
  return (release.value?.assets ?? []).toSorted((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name));
});
const recommended = computed(() => assets.value.find(asset => isMacInstaller(asset) && fits(asset))?.name ?? null);
const notes = computed(() => (props.link.notes ? pick(props.link.notes, '', 'en-US') : ''));

const archLabel = (asset: ReleaseAsset) => (asset.arch ? t(`package.link.arch.${asset.arch}`) : null);

async function open(target: LinkOpenTarget) {
  try {
    await links.open(props.token, target);
  } catch (error) {
    toasts.push({
      tone: 'danger',
      title: t('errors.actionFailed'),
      description: errorText(error)
    });
  }
}
</script>

<template>
  <section
    class="flex flex-col gap-10px rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-14px"
    aria-labelledby="link-download"
  >
    <h2 id="link-download" class="m-0 text-13.5px font-600 text-ink-primary">
      {{ t('package.link.downloadTitle') }}
    </h2>
    <OnButton v-if="link.downloadUrl" variant="primary" icon="arrow-up-right" @click="open('download')">
      {{ t('package.link.officialDownload') }}
    </OnButton>
    <template v-if="release">
      <p class="m-0 text-12px text-ink-tertiary">
        {{
          t('package.link.release', {
            version: release.version,
            time: formatRelativeTime(release.publishedAt, {
              locale: appLocale
            })
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
            <p class="m-0 truncate font-mono text-12px text-ink-primary">
              {{ asset.name }}
            </p>
            <p class="m-0 flex flex-wrap items-center gap-6px text-11.5px text-ink-tertiary">
              <span>{{ formatBytes(asset.size, { locale: appLocale }) }}</span>
              <span v-if="archLabel(asset)">· {{ archLabel(asset) }}</span>
              <OnChip v-if="asset.name === recommended" tone="success" size="sm">{{
                t('package.link.recommended')
              }}</OnChip>
            </p>
          </div>
          <OnButton
            :variant="asset.name === recommended && !link.downloadUrl ? 'primary' : 'secondary'"
            size="xs"
            icon="arrow-up-right"
            @click="open({ asset: asset.name })"
          >
            {{ t('package.link.download') }}
          </OnButton>
        </li>
      </ul>
      <OnButton variant="ghost" size="sm" icon="arrow-up-right" @click="open('github')">{{
        t('package.link.allFiles')
      }}</OnButton>
    </template>
    <OnButton v-else-if="link.github" variant="secondary" icon="arrow-up-right" @click="open('github')">
      {{ t('package.link.githubReleases') }}
    </OnButton>
    <p v-if="notes" class="m-0 text-12px leading-[1.55] text-ink-secondary">
      {{ notes }}
    </p>
    <p class="m-0 text-11.5px leading-[1.55] text-ink-tertiary">
      {{ t('package.link.notHosted') }}
    </p>
  </section>
</template>
