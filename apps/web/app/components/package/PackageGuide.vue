<script setup lang="ts">
import type { PackageDetail } from '@opennavo/api';
import { displayUrl, OnIcon, OnInstallCommand, parseInline } from '@opennavo/ui';
import { downloadChoice, hasZap, installNote } from '~/utils/appAnswer';
import { packagePath } from '~/utils/packageView';

// WP9 install and uninstall: what the Homebrew install does for this kind of cask, a download
// without Homebrew (the same file, one per chip, or the official site when files vary), the regular
// uninstall and, only when the definition lists files, the optional --zap clean-up with its risks.
const props = defineProps<{ pkg: PackageDetail }>();

const { t } = useI18n();
const localePath = useLocalePath();
const toasts = useToasts();

const note = computed(() => installNote(props.pkg));
const download = computed(() => downloadChoice(props.pkg));
const zap = computed(() => hasZap(props.pkg));
const detailsHref = computed(() => localePath(packagePath(props.pkg.token, 'details')));
const zapWarning = computed(() => parseInline(t('package.guide.zap')));

function onCopied(command: string) {
  toasts.push({ tone: 'success', title: t('toast.copied'), description: command });
}
</script>

<template>
  <section
    class="rounded-big border border-solid border-line-subtle bg-surface-card px-18px py-16px"
    aria-labelledby="package-guide-title"
    data-testid="package-guide"
  >
    <h2 id="package-guide-title" class="m-0 text-13.5px font-600 text-ink-primary">{{ t('package.guide.title') }}</h2>
    <p v-if="note" class="m-0 mt-8px text-13px leading-[1.6] text-ink-secondary">
      {{
        note.key === 'installPkg' ? t('package.guide.installPkg', { pkg: note.pkg }) : t(`package.guide.${note.key}`)
      }}
    </p>
    <div v-if="download" class="mt-12px text-13px leading-[1.6] text-ink-secondary">
      <h3 class="m-0 text-12.5px font-600 text-ink-primary">{{ t('package.guide.downloadTitle') }}</h3>
      <p class="m-0 mt-4px">
        <template v-if="download.kind === 'direct'">
          {{ t('package.guide.downloadDirect') }}
          <a :href="download.url" rel="nofollow noopener noreferrer" class="text-ink-primary">{{
            displayUrl(download.url)
          }}</a>
        </template>
        <template v-else-if="download.kind === 'arch'">
          {{ t('package.guide.downloadArch') }}
          <template v-for="(item, index) in download.items" :key="item.arch">
            <span v-if="index" aria-hidden="true"> · </span>
            <a :href="item.url" rel="nofollow noopener noreferrer" class="text-ink-primary">{{
              t(`package.arch.${item.arch}`)
            }}</a>
          </template>
        </template>
        <template v-else>
          {{ download.varies ? t('package.guide.downloadVaries') : t('package.guide.downloadOfficial') }}
          <a :href="download.url" target="_blank" rel="noopener noreferrer" class="text-ink-primary">
            {{ displayUrl(download.url) }}
            <OnIcon name="arrow-up-right" :size="13" />
          </a>
        </template>
      </p>
      <p v-if="download.kind !== 'official' && pkg.downloadSha256" class="m-0 mt-4px text-12px text-ink-tertiary">
        {{ t('package.guide.sha') }}
        <NuxtLink :to="detailsHref" class="text-ink-secondary">{{ t('package.guide.shaLink') }}</NuxtLink>
      </p>
    </div>
    <div class="mt-14px flex flex-col gap-8px">
      <OnInstallCommand
        :command="`brew uninstall --cask ${pkg.token}`"
        :title="t('package.guide.uninstallTitle')"
        @copied="onCopied"
      />
      <p class="m-0 text-12px leading-[1.6] text-ink-tertiary">{{ t('package.guide.uninstall') }}</p>
    </div>
    <div v-if="zap" class="mt-12px flex flex-col gap-8px" data-testid="package-zap">
      <OnInstallCommand
        :command="`brew uninstall --zap --cask ${pkg.token}`"
        :title="t('package.guide.zapTitle')"
        @copied="onCopied"
      />
      <p class="m-0 text-12px leading-[1.6] text-ink-tertiary">
        <template v-for="(part, index) in zapWarning" :key="index">
          <code
            v-if="part.type === 'code'"
            class="rounded-tiny bg-surface-inset px-4px font-mono text-11.5px text-component-mono-text"
            >{{ part.text }}</code
          >
          <template v-else>{{ part.text }}</template>
        </template>
        <NuxtLink :to="detailsHref" class="ml-4px text-ink-secondary">{{ t('package.guide.zapDetails') }}</NuxtLink>
      </p>
    </div>
  </section>
</template>
