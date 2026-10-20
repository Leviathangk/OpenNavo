<script setup lang="ts">
import { BRAND } from '@opennavo/shared';
import { BRAND_FACTS } from '~~/shared/utils/brandFacts';

// G4: what OpenNavo is, in the one sentence the README, the structured data and /llms.txt use too, then
// plain facts that set it apart from the Homebrew command line and the Mac App Store. Each cell states one
// fact backed by a public source listed under the table (checked 2026-10-19): no scores, and no claim that
// the three replace one another.
const { t } = useI18n();

const TOOLS = ['opennavo', 'homebrew', 'appStore'] as const;
const ROWS = ['use', 'source', 'scope'] as const;
const SOURCES = [
  { key: 'readme', url: `${BRAND.repository}#introduction` },
  { key: 'brewManpage', url: 'https://docs.brew.sh/Manpage' },
  { key: 'brewTaps', url: 'https://docs.brew.sh/Taps' },
  {
    key: 'appleInstall',
    url: 'https://support.apple.com/guide/app-store/install-and-uninstall-purchased-apps-fir0fb69db23/mac'
  },
  { key: 'appleDownload', url: 'https://support.apple.com/guide/mac-help/download-apps-mchlgtd_apps01/mac' },
  { key: 'appleUpdate', url: 'https://support.apple.com/guide/app-store/update-purchased-apps-fir9b01adda3/mac' },
  { key: 'appleOther', url: 'https://support.apple.com/en-us/102629' }
] as const;

const toolName = (tool: (typeof TOOLS)[number]) =>
  tool === 'opennavo' ? BRAND.name : t(`landing.about.compare.${tool}`);
</script>

<template>
  <section class="mx-auto box-border w-full max-w-1240px px-24px">
    <div data-reveal>
      <LandingSectionHead
        align="center"
        :kicker="t('landing.about.kicker')"
        :title="t('landing.about.title')"
        :body="t(BRAND_FACTS.definitionKey)"
      />
    </div>
    <div class="mt-40px overflow-x-auto" data-reveal style="--landing-delay: 80ms">
      <table class="landing-compare w-full min-w-640px border-collapse text-left text-14px leading-[1.6]">
        <caption class="pb-12px text-start text-13px text-ink-tertiary">
          {{
            t('landing.about.compare.caption')
          }}
        </caption>
        <thead>
          <tr>
            <td></td>
            <th v-for="tool in TOOLS" :key="tool" scope="col" class="text-15px font-600 text-ink-primary">
              {{ toolName(tool) }}
            </th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in ROWS" :key="row">
            <th scope="row" class="w-180px font-500 text-ink-primary">{{ t(`landing.about.compare.rows.${row}`) }}</th>
            <td v-for="tool in TOOLS" :key="tool" class="text-ink-secondary">
              {{ t(`landing.about.compare.cells.${tool}.${row}`) }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <p class="m-0 mt-16px text-12.5px leading-[1.8] text-ink-tertiary">
      <!-- No space after the label: a margin separates it, so a full-width colon (来源：) gets no extra gap. -->
      <span class="me-4px">{{ t('landing.about.compare.sources') }}</span
      ><template v-for="(source, index) in SOURCES" :key="source.key">
        <a
          :href="source.url"
          target="_blank"
          rel="noopener noreferrer"
          class="text-ink-tertiary underline decoration-line-strong underline-offset-3 hover:text-ink-secondary"
          >{{ t(`landing.about.compare.links.${source.key}`) }}</a
        ><span v-if="index < SOURCES.length - 1" aria-hidden="true"> · </span>
      </template>
    </p>
  </section>
</template>

<style>
.landing-compare th,
.landing-compare td {
  padding: 14px 20px 14px 0;
  border-bottom: 1px solid var(--on-border-subtle);
  vertical-align: top;
}

.landing-compare thead th {
  padding-bottom: 10px;
}
</style>
