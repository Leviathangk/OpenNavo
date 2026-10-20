<script setup lang="ts">
import { isThirdPartyTap } from '@opennavo/shared';
import { displayUrl, OnCaskArtifacts } from '@opennavo/ui';
import type { PackagePage } from '~/utils/packageFacts';

// Install details on the overview (T2, H9): what only the details tab used to show, now in full in the
// overview's HTML so the tab can redirect here: the tap, former tokens, installer packages and commands,
// keg-only, the complete download address and SHA-256, the Homebrew definition, formulae.brew.sh and the
// source repository, then every install, uninstall and clean-up declaration as Homebrew writes it.
const props = defineProps<{ pkg: PackagePage }>();

const { t } = useI18n();

const overview = computed(() => props.pkg.facts.overview);
const rows = computed(() => {
  const value = props.pkg;
  const artifacts = overview.value.artifacts;
  const list: { key: string; label: string; value: string; href?: string; mono?: boolean }[] = [
    { key: 'tap', label: t('package.details.tap'), value: value.tap, mono: true }
  ];
  if (overview.value.aliases.length)
    list.push({
      key: 'aliases',
      label: t('package.details.aliases'),
      value: overview.value.aliases.join(', '),
      mono: true
    });
  if (artifacts.pkgs.length)
    list.push({ key: 'pkgs', label: t('package.details.pkgs'), value: artifacts.pkgs.join(', '), mono: true });
  if (artifacts.binaries.length)
    list.push({ key: 'binaries', label: t('package.info.binaries'), value: artifacts.binaries.join(' '), mono: true });
  if (artifacts.uninstallNotes?.length)
    list.push({ key: 'uninstall', label: t('package.info.uninstall'), value: artifacts.uninstallNotes.join('; ') });
  if (value.kegOnly)
    list.push({ key: 'keg', label: t('package.details.kegOnly'), value: t('package.details.kegOnlyValue') });
  if (overview.value.downloadUrl)
    list.push({
      key: 'download',
      label: t('package.details.download'),
      value: overview.value.downloadUrl,
      href: overview.value.downloadUrl,
      mono: true
    });
  if (overview.value.sha256) list.push({ key: 'sha256', label: 'SHA-256', value: overview.value.sha256, mono: true });
  list.push({
    key: 'source',
    label: t('package.details.source'),
    value: displayUrl(value.sourceUrl),
    href: value.sourceUrl
  });
  // formulae.brew.sh only lists Homebrew's own taps.
  if (!isThirdPartyTap(value.tap))
    list.push({
      key: 'formulae',
      label: 'formulae.brew.sh',
      value: displayUrl(value.formulaeUrl),
      href: value.formulaeUrl
    });
  if (value.repoUrl)
    list.push({ key: 'repo', label: t('package.details.repo'), value: displayUrl(value.repoUrl), href: value.repoUrl });
  return list;
});
const entries = computed(() => overview.value.artifacts.entries ?? []);
</script>

<template>
  <section
    id="details"
    class="rounded-big border border-solid border-line-subtle bg-surface-card px-18px py-16px"
    aria-labelledby="details-title"
    data-testid="package-install-facts"
  >
    <h2 id="details-title" class="m-0 text-13.5px font-600 text-ink-primary">{{ t('package.tabs.details') }}</h2>
    <dl
      class="m-0 mt-8px grid grid-cols-1 gap-x-16px gap-y-2px text-12.5px leading-[1.6] sm:grid-cols-[max-content_minmax(0,1fr)] sm:gap-y-6px"
    >
      <template v-for="row in rows" :key="row.key">
        <dt class="mt-6px text-ink-tertiary first:mt-0 sm:mt-0">{{ row.label }}</dt>
        <dd
          class="m-0 min-w-0 text-ink-primary [overflow-wrap:anywhere]"
          :class="row.mono ? 'font-mono text-12px' : ''"
        >
          <a
            v-if="row.href"
            :href="row.href"
            target="_blank"
            rel="nofollow noopener noreferrer"
            class="text-ink-primary underline-offset-2 hover:underline"
            >{{ row.value }}</a
          >
          <template v-else>{{ row.value }}</template>
        </dd>
      </template>
    </dl>
  </section>
  <OnCaskArtifacts v-if="entries.length" :entries="entries" />
</template>
