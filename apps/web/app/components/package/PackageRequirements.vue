<script setup lang="ts">
import { displayUrl, OnButton, OnChip } from '@opennavo/ui';
import { shownRequirements, macosText } from '~/utils/packageFacts';
import type { PackagePage, RequirementFact } from '~/utils/packageFacts';
import { requirementMobileCards } from '~/utils/packageRequirements';

// System requirements (T2): facts.requirements as a table, one row per distinct declaration (Homebrew's per
// chip and macOS release, the developer's statements, per-version compatibility). Every row names its source
// and links to its source; check dates live once in Sources. Statements pending verification are omitted. A minimum is never read as support for future releases, and the macOS
// label of a Homebrew build is not a minimum. Downloads and their full SHA-256 come from the same rows.
const props = defineProps<{ pkg: PackagePage }>();

const { t } = useI18n();
const toasts = useToasts();
async function copyChecksum(value: string) {
  try {
    await navigator.clipboard.writeText(value);
    toasts.push({ tone: 'success', title: t('toast.copied'), description: 'SHA-256' });
  } catch {
    toasts.push({ tone: 'warning', title: t('toast.copyFailed'), description: value, duration: 0 });
  }
}

const archLabel = (arch: string) => (arch === 'arm64' || arch === 'x86_64' ? t(`package.arch.${arch}`) : arch);

function chips(row: RequirementFact): string {
  const archs = row.architectures.length
    ? row.architectures.map(archLabel).join(' · ')
    : row.appleSilicon === 'yes' || row.appleSilicon === 'no'
      ? t('package.requirements.allChips')
      : t('package.requirements.unknownChip');
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
    return {
      key: `${index}:${row.versions.join(',')}:${row.architectures.join(',')}`,
      architectures: row.architectures,
      appleSilicon: row.appleSilicon,
      macos: macos(row),
      versions: row.versions.join(', '),
      chips: chips(row),
      download: row.downloadUrl,
      sha256: row.sha256,
      source: homebrew ? 'Homebrew' : displayUrl(evidence.sourceUrl),
      sourceUrl: evidence.sourceUrl,
      stale: evidence.status === 'stale'
    };
  })
);

const mobileRows = computed(() =>
  requirementMobileCards(rows.value, {
    allChips: t('package.requirements.allChips'),
    unknownChip: t('package.requirements.unknownChip'),
    native: t('package.facts.support.native'),
    notNative: t('package.requirements.notNative'),
    arch: archLabel
  })
);

async function copySha(sha: string) {
  if (typeof navigator === 'undefined') return;
  try {
    await navigator.clipboard.writeText(sha);
    toasts.push({ tone: 'success', title: t('toast.copied'), description: 'SHA-256' });
  } catch {
    toasts.push({ tone: 'warning', title: t('toast.copyFailed'), description: sha, duration: 0 });
  }
}

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
    <div class="mt-8px overflow-x-auto max-[640px]:hidden">
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
                  <code class="font-mono text-component-mono-text [overflow-wrap:anywhere]">{{ row.sha256 }}</code>
                  <OnButton
                    size="xs"
                    icon="copy"
                    :aria-label="t('download.copySha')"
                    @click="copyChecksum(row.sha256)"
                    >{{ t('common.copy') }}</OnButton
                  ></span
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
              <OnChip v-if="row.stale" class="mt-4px" tone="warning">{{ t('package.facts.stale') }}</OnChip>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div class="mt-8px hidden flex-col gap-8px max-[640px]:flex">
      <article
        v-for="row in mobileRows"
        :key="row.key"
        class="rounded-default border border-solid border-line-subtle bg-surface-base px-12px py-10px"
      >
        <div class="flex items-start justify-between gap-10px">
          <div>
            <h3 class="m-0 text-12.5px font-600 text-ink-primary" :aria-label="`${t('package.quick.chips')}: ${row.chips}`">
              {{ row.chips }}
            </h3>
            <p class="m-0 mt-2px text-11.5px text-ink-tertiary">
              {{ row.macos || '—' }} · {{ row.versions || '—'
              }}<template v-if="row.native"> · {{ row.native }}</template>
            </p>
          </div>
          <OnChip v-if="row.stale" tone="warning">{{ t('package.facts.stale') }}</OnChip>
        </div>
        <a
          v-if="row.download"
          :href="row.download"
          rel="nofollow noopener noreferrer"
          class="mt-8px block text-12px text-ink-secondary underline-offset-2 [overflow-wrap:anywhere] hover:text-ink-primary"
          >{{ displayUrl(row.download) }}</a
        >
        <div v-if="row.sha256" class="mt-6px flex items-center justify-between gap-8px">
          <code class="min-w-0 truncate font-mono text-11.5px text-component-mono-text"
            >SHA-256 {{ row.sha256.slice(0, 12) }}…</code
          >
          <button
            type="button"
            class="m-0 inline-flex min-h-24px items-center rounded-tiny border border-solid border-line-default bg-transparent px-7px text-11px text-ink-secondary outline-none hover:text-ink-primary focus-visible:shadow-focus-ring"
            :aria-label="t('download.copySha')"
            @click="copySha(row.sha256)"
          >
            {{ t('common.copy') }}
          </button>
        </div>
        <p class="m-0 mt-6px text-11px text-ink-tertiary [overflow-wrap:anywhere]">{{ row.source }}</p>
      </article>
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
