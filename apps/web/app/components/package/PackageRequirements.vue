<script setup lang="ts">
import { formatDate } from '@opennavo/shared';
import { displayUrl, OnChip } from '@opennavo/ui';
import { aroundDate, shownRequirements, isoDate, macosText } from '~/utils/packageFacts';
import type { PackagePage, RequirementFact } from '~/utils/packageFacts';

// System requirements (T2): facts.requirements as a table, one row per distinct declaration (Homebrew's per
// chip and macOS release, the developer's statements, per-version compatibility). Every row names its source
// and check date; a stale row reads "as of" its date, and a developer statement under review stays beside
// Homebrew's record marked pending. A minimum is never read as support for future releases, and the macOS
// label of a Homebrew build is not a minimum. Downloads and their full SHA-256 come from the same rows.
const props = defineProps<{ pkg: PackagePage }>();

const { t, locale } = useI18n();
const appLocale = computed(() => toAppLocale(locale.value));

const archLabel = (arch: string) => (arch === 'arm64' || arch === 'x86_64' ? t(`package.arch.${arch}`) : arch);

function chips(row: RequirementFact): string {
  const archs = row.architectures.length
    ? row.architectures.map(archLabel).join(' · ')
    : t('package.requirements.allChips');
  if (row.appleSilicon === 'yes') return `${archs} · ${t('package.facts.support.native')}`;
  if (row.appleSilicon === 'no') return `${archs} · ${t('package.requirements.notNative')}`;
  return archs;
}

function macos(row: RequirementFact): string {
  const range = macosText({ minimum: row.minimumMacOS, maximum: row.maximumMacOS }, t);
  if (range) return range;
  // The macOS label of a Homebrew build says which releases it is for, not what it requires.
  return row.platformMacOS.length ? t('package.requirements.builtFor', { versions: row.platformMacOS.join(', ') }) : '';
}

const rows = computed(() =>
  shownRequirements(props.pkg).map((row, index) => {
    const evidence = row.evidence;
    const homebrew = Boolean(props.pkg.sourceUrl) && evidence.sourceUrl === props.pkg.sourceUrl;
    const checkedAt = evidence.checkedAt;
    return {
      key: `${index}:${row.versions.join(',')}:${row.architectures.join(',')}`,
      macos: macos(row),
      versions: row.versions.join(', '),
      chips: chips(row),
      download: row.downloadUrl,
      sha256: row.sha256,
      source: homebrew ? 'Homebrew' : displayUrl(evidence.sourceUrl),
      sourceUrl: evidence.sourceUrl,
      pending: evidence.status === 'needs_review',
      stale: evidence.status === 'stale',
      checked: checkedAt
        ? {
            iso: isoDate(checkedAt),
            date: formatDate(checkedAt, { locale: appLocale.value }),
            ...aroundDate(t, evidence.status === 'stale' ? 'package.facts.asOf' : 'package.facts.checkedOn')
          }
        : null
    };
  })
);

const conflicts = computed(() => props.pkg.facts.macosSupplement?.conflicts ?? []);
</script>

<template>
  <section
    v-if="rows.length"
    id="requirements"
    class="rounded-big border border-solid border-line-subtle bg-surface-card px-18px py-16px"
    aria-labelledby="requirements-title"
    data-testid="package-requirements"
  >
    <h2 id="requirements-title" class="m-0 text-13.5px font-600 text-ink-primary">
      {{ t('package.requirements.title') }}
    </h2>
    <div class="mt-8px overflow-x-auto">
      <table class="w-full border-collapse text-left text-12.5px leading-[1.5]">
        <thead>
          <tr class="text-11.5px text-ink-tertiary">
            <th scope="col" class="py-6px pr-12px font-500">macOS</th>
            <th scope="col" class="py-6px pr-12px font-500">{{ t('package.requirements.version') }}</th>
            <th scope="col" class="py-6px pr-12px font-500">{{ t('package.quick.chips') }}</th>
            <th scope="col" class="py-6px pr-12px font-500">{{ t('package.requirements.download') }}</th>
            <th scope="col" class="py-6px font-500">{{ t('package.requirements.source') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in rows" :key="row.key" class="border-t border-t-solid border-line-subtle align-top">
            <td class="py-8px pr-12px text-ink-primary">{{ row.macos || '—' }}</td>
            <td class="py-8px pr-12px text-ink-secondary [overflow-wrap:anywhere]">{{ row.versions || '—' }}</td>
            <td class="py-8px pr-12px text-ink-secondary">{{ row.chips }}</td>
            <td class="max-w-280px py-8px pr-12px">
              <template v-if="row.download">
                <a
                  :href="row.download"
                  rel="nofollow noopener noreferrer"
                  class="text-ink-secondary underline-offset-2 [overflow-wrap:anywhere] hover:text-ink-primary"
                  >{{ displayUrl(row.download) }}</a
                >
                <span v-if="row.sha256" class="mt-2px block text-11px text-ink-tertiary"
                  >SHA-256
                  <code class="font-mono text-component-mono-text [overflow-wrap:anywhere]">{{
                    row.sha256
                  }}</code></span
                >
              </template>
              <template v-else>—</template>
            </td>
            <td class="py-8px text-ink-tertiary">
              <a
                :href="row.sourceUrl"
                target="_blank"
                rel="nofollow noopener noreferrer"
                class="text-ink-secondary underline-offset-2 [overflow-wrap:anywhere] hover:text-ink-primary"
                >{{ row.source }}</a
              >
              <span v-if="row.checked" class="block"
                >{{ row.checked.before }}<time :datetime="row.checked.iso">{{ row.checked.date }}</time
                >{{ row.checked.after }}</span
              >
              <OnChip v-if="row.pending" class="mt-4px" tone="warning">{{ t('package.facts.pending') }}</OnChip>
              <OnChip v-else-if="row.stale" class="mt-4px" tone="warning">{{ t('package.facts.stale') }}</OnChip>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <p v-if="conflicts.length" class="m-0 mt-8px text-12px leading-[1.6] text-ink-secondary">
      {{
        t('package.facts.conflict', {
          fields: conflicts.map(code => t(`package.facts.conflictFields.${code}`)).join(', ')
        })
      }}
    </p>
  </section>
</template>
