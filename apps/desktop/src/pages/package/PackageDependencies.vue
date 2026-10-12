<script setup lang="ts">
import { useAppLocale as useFormattingLocale } from '@/composables/useAppLocale';
import { computed, ref } from 'vue';
import { RouterLink } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { unwrap } from '@opennavo/api';
import { formatCount } from '@opennavo/shared';
import {
  architectures,
  OnAppRow,
  OnChip,
  OnEmpty,
  OnKeyValueList,
  OnCaskPlatformSelect,
  selectedCaskPlatform,
  useUiMessages
} from '@opennavo/ui';
import type { OnKeyValueItem } from '@opennavo/ui';
import RequestError from '@/components/common/RequestError.vue';
import DependencyTree from '@/components/package/DependencyTree.vue';
import { api } from '@/api';
import { useAppLocale } from '@/composables/useAppLocale';
import { useRemoteLoader } from '@/composables/useRemoteLoader';
import { usePackage } from '@/composables/usePackageDetail';

const { appLocale: formattingLocale } = useFormattingLocale();

// Dependencies and conflicts (08 §10.7, API); prompt for connectivity offline.
const { t } = useI18n();
const { appLocale } = useAppLocale();
const { kind, token, detail } = usePackage();

const messages = useUiMessages();
const platformTag = ref('');
const platforms = computed(() => detail.value?.platforms ?? []);
const platform = computed(() =>
  selectedCaskPlatform({ platforms: platforms.value, version: detail.value?.version ?? '' }, platformTag.value)
);
const activePlatformTag = computed(() => platform.value?.tag);

const { data, error } = useRemoteLoader(
  'dependencies',
  () =>
    unwrap(
      api.GET('/packages/{kind}/{token}/dependencies', {
        params: {
          path: { kind: kind.value, token: token.value },
          query: { platform: activePlatformTag.value }
        }
      })
    ),
  [kind, token, appLocale, activePlatformTag]
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
  if (!value || kind.value !== 'cask') return [];
  if (platforms.value.length) return [];
  const items: OnKeyValueItem[] = [];
  const dependsOn = value.dependsOn;
  if (dependsOn?.macos) items.push({ key: 'macos', label: t('package.deps.macos'), value: dependsOn.macos });
  const arch = (
    dependsOn?.arch?.length ? dependsOn.arch : architectures(detail.value?.supports ?? { arm64: false, x86_64: false })
  ).map(item => t(`package.arch.${item}`));
  items.push({
    key: 'arch',
    label: t('package.info.arch'),
    value:
      detail.value?.supports.status !== 'known'
        ? messages.value.cask.unknown
        : arch.join(' · ') || messages.value.cask.noMac
  });
  if (dependsOn?.formulae.length)
    items.push({
      key: 'formulae',
      label: t('package.deps.formulae'),
      value: dependsOn.formulae.join(' '),
      mono: true
    });
  if (dependsOn?.casks.length)
    items.push({
      key: 'casks',
      label: t('package.deps.casks'),
      value: dependsOn.casks.join(' '),
      mono: true
    });
  return items;
});

const conflicts = computed(() => {
  const value = platform.value?.conflictsWith ?? data.value?.conflictsWith;
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
    !platforms.value.length &&
    !groups.value.runtime.length &&
    !groups.value.build.length &&
    !groups.value.other.length &&
    !requirements.value.length &&
    !conflicts.value.length &&
    !data.value.dependents.count
);
</script>

<template>
  <p v-if="!data && !error" role="status">{{ t('common.loading') }}</p>
  <RequestError v-if="error && !data" :error="error" />
  <div v-else-if="data" class="grid grid-cols-[minmax(0,1fr)_320px] gap-20px pt-4px">
    <div class="flex min-w-0 flex-col gap-16px">
      <OnCaskPlatformSelect
        v-if="platforms.length"
        :model-value="platform?.tag"
        :platforms="platforms"
        @update:model-value="platformTag = $event"
      />
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
        <!-- Build dependencies start collapsed; expand only top-level rows, not entire subtrees. -->
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
            <!-- Only apps have details pages; command-line tools show names only (ADR-018). -->
            <RouterLink v-if="item.kind === 'cask'" :to="`/package/cask/${item.token}`" class="no-underline">
              <OnChip size="md" tone="warning" outline>{{ item.token }}</OnChip>
            </RouterLink>
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
              :href="item.kind === 'cask' ? `/package/cask/${item.token}` : undefined"
              :link-as="RouterLink"
            />
          </li>
        </ul>
      </section>
    </aside>
  </div>
</template>
