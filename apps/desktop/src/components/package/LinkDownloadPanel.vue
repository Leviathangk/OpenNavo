<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatRelativeTime, selectAssets } from '@opennavo/shared';
import type { MacChip } from '@opennavo/shared';
import { OnButton } from '@opennavo/ui';
import type { LinkInfo, LinkOpenTarget } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useEnvStore } from '@/stores';
import { useLinksStore } from '@/stores/links';
import LinkAssetRow from './LinkAssetRow.vue';

// Download panel for link apps (store/security plan §4, mockup A): OpenNavo hosts nothing. It lists the official
// download page and, for GitHub projects, the latest release's Mac installers that fit this Mac's chip; the other
// chip's and the files for other systems wait behind "other installers".
const props = defineProps<{ token: string; link: LinkInfo }>();

const { t } = useI18n();
const { appLocale, pick } = useAppLocale();
const env = useEnvStore();
const links = useLinksStore();
const toasts = useToasts();
const { errorText } = usePackageState();

const chip = computed<MacChip>(() => (['arm64', 'aarch64'].includes(env.info?.arch ?? 'arm64') ? 'arm64' : 'x86_64'));
const release = computed(() => props.link.github?.latest ?? null);
// Only positively identified macOS installers that fit the chip are listed (shared with the website); when none can be
// told apart everything is listed, so an odd file name never leaves the panel empty.
const choice = computed(() => selectAssets(release.value?.assets ?? [], chip.value));
// One installer that fits is the recommendation. Several are different apps or formats of one release: none stands out.
const recommended = computed(() =>
  choice.value.narrowed && choice.value.main.length === 1 ? choice.value.main[0]!.name : null
);
const notes = computed(() => (props.link.notes ? pick(props.link.notes, '', 'en-US') : ''));

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
      <p v-if="choice.narrowed" class="m-0 text-11.5px text-ink-tertiary">
        {{ t('package.link.filtered', { chip: t(`package.link.arch.${chip}`) }) }}
      </p>
      <ul v-if="choice.main.length" class="m-0 flex list-none flex-col p-0">
        <LinkAssetRow
          v-for="asset in choice.main"
          :key="asset.name"
          :asset="asset"
          :recommended="asset.name === recommended"
          :primary="asset.name === recommended && !link.downloadUrl"
          @open="open({ asset: asset.name })"
        />
      </ul>
      <details v-if="choice.rest.length" class="text-12px">
        <summary class="cursor-pointer select-none text-ink-secondary">
          {{ t('package.link.others', { count: choice.rest.length }) }}
        </summary>
        <ul class="m-0 mt-4px flex list-none flex-col p-0">
          <LinkAssetRow
            v-for="asset in choice.rest"
            :key="asset.name"
            :asset="asset"
            :recommended="false"
            :primary="false"
            @open="open({ asset: asset.name })"
          />
        </ul>
      </details>
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
