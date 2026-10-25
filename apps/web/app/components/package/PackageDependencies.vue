<script setup lang="ts">
import { unwrap } from '@opennavo/api';
import type { PackageKind } from '@opennavo/shared';
import { formatCount, formatList } from '@opennavo/shared';
import { architectures, OnAppRow, OnChip, OnEmpty, OnErrorState, OnKeyValueList, useUiMessages } from '@opennavo/ui';
import type { OnKeyValueItem } from '@opennavo/ui';
import { homebrewRows } from '~/utils/packageFacts';
import { packageLink, packagePath } from '~/utils/packageView';
import { dependenciesDescription, withVariantNames } from '~/utils/seoTitles';

const { locale: useI18nLocale } = useI18n();
const formattingLocale = computed(() => toAppLocale(useI18nLocale.value));

// Dependencies/conflicts (08 §10.7).
const props = defineProps<{ kind: PackageKind }>();

const { t, locale } = useI18n();
const api = useApi();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');

const { data: pkg, token } = await usePackage(props.kind);
// Link apps have no Homebrew definition; the layout hides this tab for them (usePackage resolves the kind).
if (pkg.value?.kind === 'link') throw createError({ statusCode: 404, statusMessage: 'Page not found', fatal: true });

const messages = useUiMessages();
// Homebrew's per-platform declarations (T6: from facts.requirements, not the dropped platforms list).
const platformRows = computed(() => (pkg.value ? homebrewRows(pkg.value) : []));

const { data, error, refresh } = await useAsyncData(
  () => `dependencies:${props.kind}:${token.value}:${locale.value}`,
  () =>
    unwrap(
      api.GET('/packages/{kind}/{token}/dependencies', {
        params: { path: { kind: props.kind, token: token.value } }
      })
    ),
  { transform: withVariantNames }
);

const groups = computed(() => {
  const nodes = data.value?.dependencies ?? [];
  return {
    runtime: nodes.filter(node => node.type === 'runtime' || node.type === 'cask'),
    build: nodes.filter(node => node.type === 'build'),
    other: nodes.filter(node => !['runtime', 'cask', 'build'].includes(node.type))
  };
});

const requirements = computed<OnKeyValueItem[]>(() => {
  const value = data.value;
  if (!value || props.kind !== 'cask') return [];
  // Requirements differ per platform when a cask lists platforms, so one default list would mislead; the
  // overview's requirements table shows each platform's.
  if (platformRows.value.length) return [];
  const items: OnKeyValueItem[] = [];
  const dependsOn = value.dependsOn;
  if (dependsOn?.macos) items.push({ key: 'macos', label: t('package.deps.macos'), value: dependsOn.macos });
  const arch = (
    dependsOn?.arch?.length ? dependsOn.arch : architectures(pkg.value?.supports ?? { arm64: false, x86_64: false })
  ).map(item => t(`package.arch.${item}`));
  items.push({
    key: 'arch',
    label: t('package.info.arch'),
    value:
      pkg.value?.supports.status !== 'known'
        ? messages.value.cask.unknown
        : arch.join(' · ') || messages.value.cask.noMac
  });
  if (dependsOn?.formulae.length)
    items.push({ key: 'formulae', label: t('package.deps.formulae'), value: dependsOn.formulae.join(' '), mono: true });
  if (dependsOn?.casks.length)
    items.push({ key: 'casks', label: t('package.deps.casks'), value: dependsOn.casks.join(' '), mono: true });
  return items;
});

const conflicts = computed(() => {
  const value = data.value?.conflictsWith;
  return value
    ? [
        ...value.casks.map(name => ({ kind: 'cask' as const, token: name })),
        ...value.formulae.map(name => ({ kind: 'formula' as const, token: name }))
      ]
    : [];
});

const empty = computed(
  () =>
    data.value &&
    !groups.value.runtime.length &&
    !groups.value.build.length &&
    !groups.value.other.length &&
    !requirements.value.length &&
    !conflicts.value.length &&
    !data.value.dependents.count
);

// The dependencies tab is never indexed, with or without dependency relations; its links stay followable.
// The API's decision says so (the layout applies it); an older API without one gets the same rule here.
if (!pkg.value?.indexing?.pages?.dependencies) {
  useRobotsRule('noindex, follow');
}

usePageSeo({
  title: () => t('package.deps.metaTitle', { name: pkg.value?.displayName ?? token.value }),
  description: () =>
    dependenciesDescription(
      {
        displayName: pkg.value?.displayName ?? token.value,
        runtime: groups.value.runtime.length,
        build: groups.value.build.length,
        other: groups.value.other.length,
        conflicts: conflicts.value.length,
        dependents: data.value?.dependents.count ?? 0
      },
      t,
      values => formatList(values, { locale: formattingLocale.value }),
      value => formatCount(value, { locale: formattingLocale.value })
    )
});
</script>

<template>
  <OnErrorState v-if="error" @retry="refresh()" />
  <div v-else-if="data" class="grid grid-cols-1 gap-20px pt-4px lg:grid-cols-[minmax(0,1fr)_320px]">
    <div class="flex min-w-0 flex-col gap-16px">
      <OnEmpty
        v-if="empty"
        icon="lucide:boxes"
        :title="t('package.deps.emptyTitle')"
        :description="t('package.deps.emptyDescription')"
      />
      <section
        v-if="groups.runtime.length"
        class="rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-12px"
      >
        <h2 class="m-0 mb-4px text-13.5px font-600 text-ink-primary">
          {{ t('package.deps.runtime') }}
          <small class="text-12px font-400 text-ink-tertiary">{{ groups.runtime.length }}</small>
        </h2>
        <DependencyTree :nodes="groups.runtime" />
      </section>
      <section
        v-if="groups.build.length"
        class="rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-12px"
      >
        <h2 class="m-0 mb-4px text-13.5px font-600 text-ink-primary">
          {{ t('package.deps.build') }}
          <small class="text-12px font-400 text-ink-tertiary">{{ groups.build.length }}</small>
        </h2>
        <!-- Build dependencies start collapsed; expand top-level rows only, not subtrees. -->
        <DependencyTree :nodes="groups.build" :expand-depth="0" />
      </section>
      <section v-if="groups.other.length" class="flex flex-wrap items-center gap-6px">
        <span class="text-12.5px text-ink-tertiary">{{ t('package.deps.other') }}</span>
        <OnChip v-for="node in groups.other" :key="`${node.type}/${node.token}`" size="md">
          {{ node.name }} · {{ t(`package.deps.type.${node.type}`) }}
        </OnChip>
      </section>
      <section
        v-if="requirements.length"
        class="rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-4px"
      >
        <OnKeyValueList :items="requirements" />
      </section>
      <section
        v-if="conflicts.length"
        class="rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-12px"
      >
        <h2 class="m-0 text-13.5px font-600 text-ink-primary">{{ t('package.deps.conflicts') }}</h2>
        <ul class="m-0 mt-8px flex list-none flex-wrap gap-6px p-0">
          <li v-for="item in conflicts" :key="`${item.kind}/${item.token}`">
            <NuxtLink
              v-if="packageLink(item.kind, item.token)"
              :to="localePath(packagePath(item.token))"
              class="no-underline"
            >
              <OnChip size="md" tone="warning" outline>{{ item.token }}</OnChip>
            </NuxtLink>
            <OnChip v-else size="md" tone="warning" outline>{{ item.token }}</OnChip>
          </li>
        </ul>
        <p
          v-for="(reason, index) in data.conflictsWith.reasons ?? []"
          :key="index"
          class="m-0 mt-8px text-12.5px text-ink-tertiary"
        >
          {{ reason }}
        </p>
      </section>
    </div>
    <aside v-if="data.dependents.count" class="min-w-0">
      <section class="rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-12px">
        <h2 class="m-0 text-13.5px font-600 text-ink-primary">
          {{
            t(
              'package.deps.dependents',
              { count: formatCount(data.dependents.count, { locale: formattingLocale }) },
              { plural: data.dependents.count }
            )
          }}
        </h2>
        <ul class="m-0 mt-4px list-none p-0">
          <li v-for="item in data.dependents.top" :key="`${item.kind}/${item.token}`">
            <OnAppRow
              :kind="item.kind"
              :token="item.token"
              :name="item.displayName"
              :src="item.iconUrl"
              :accent="item.accentColor"
              :description="item.summary"
              :href="packageLink(item.kind, item.token) ? localePath(packagePath(item.token)) : undefined"
              :link-as="NuxtLink"
            />
          </li>
        </ul>
      </section>
    </aside>
  </div>
</template>
