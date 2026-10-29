<script setup lang="ts">
import { displayUrl, OnCodeText, OnIcon } from '@opennavo/ui';
import { installNote } from '~/utils/appAnswer';
import { downloadChoice } from '~/utils/packageFacts';
import type { PackagePage } from '~/utils/packageFacts';

// How to install {name} on Mac (casks only, T3): the command lives in the shared header command card;
// this section explains what Homebrew puts on the Mac, Homebrew's caveats, and a download without Homebrew
// (the same file, one per chip, or the official site when files vary) read from the requirements rows, whose
// full SHA-256 sits in the requirements table.
const props = defineProps<{ pkg: PackagePage }>();

const { t } = useI18n();

const note = computed(() => installNote(props.pkg));
const download = computed(() => downloadChoice(props.pkg));
const caveats = computed(() => props.pkg.facts.overview.caveats);
const sha256 = computed(() => props.pkg.facts.overview.sha256);
</script>

<template>
  <section
    id="install"
    class="rounded-big border border-solid border-line-subtle bg-surface-card px-18px py-16px"
    aria-labelledby="install-title"
    data-testid="package-install"
  >
    <h2 id="install-title" class="m-0 text-13.5px font-600 text-ink-primary">
      {{ t('package.install.title', { name: pkg.displayName }) }}
    </h2>
    <ol v-if="note" class="m-0 mt-8px flex flex-col gap-6px pl-18px text-13px leading-[1.6] text-ink-secondary">
      <li>
        {{
          note.key === 'installPkg' ? t('package.guide.installPkg', { pkg: note.pkg }) : t(`package.guide.${note.key}`)
        }}
      </li>
    </ol>
    <div v-if="caveats" class="mt-12px rounded-default border border-solid border-status-warning px-12px py-10px">
      <h3 class="m-0 text-12.5px font-600 text-status-warning">{{ t('package.caveats') }}</h3>
      <pre class="m-0 mt-6px whitespace-pre-wrap font-mono text-12px leading-[1.6] text-ink-secondary"><OnCodeText
        :text="caveats"
      /></pre>
    </div>
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
      <p v-if="download.kind !== 'official' && sha256" class="m-0 mt-4px text-12px text-ink-tertiary">
        {{ t('package.guide.sha') }}
      </p>
    </div>
  </section>
</template>
