<script setup lang="ts">
import type { PackagePage } from '~/utils/packageFacts';
import { packageLink, packagePath } from '~/utils/packageView';

// Dependencies and conflicts (T2): the summary the overview keeps of the dependencies page, only for casks
// that declare any (facts.overview counts them). Apps are links; command-line tools are names.
const props = defineProps<{ pkg: PackagePage }>();

const { t } = useI18n();
const localePath = useLocalePath();

const entries = (casks: readonly string[], formulae: readonly string[]) => [
  ...casks.map(token => ({
    key: `cask/${token}`,
    token,
    href: packageLink('cask', token) ? localePath(packagePath(token)) : undefined
  })),
  ...formulae.map(token => ({ key: `formula/${token}`, token, href: undefined }))
];

const groups = computed(() =>
  [
    {
      key: 'needs',
      label: t('package.depsSummary.needs'),
      items: entries(props.pkg.dependsOn?.casks ?? [], props.pkg.dependsOn?.formulae ?? [])
    },
    {
      key: 'conflicts',
      label: t('package.deps.conflicts'),
      items: entries(props.pkg.conflictsWith.casks, props.pkg.conflictsWith.formulae)
    }
  ].filter(group => group.items.length)
);
</script>

<template>
  <section
    v-if="groups.length"
    id="dependencies"
    class="rounded-big border border-solid border-line-subtle bg-surface-card px-18px py-16px"
    aria-labelledby="dependencies-title"
    data-testid="package-dependency-summary"
  >
    <h2 id="dependencies-title" class="m-0 text-13.5px font-600 text-ink-primary">
      {{ t('package.depsSummary.title') }}
    </h2>
    <dl class="m-0 mt-8px flex flex-col gap-8px text-13px leading-[1.6]">
      <template v-for="group in groups" :key="group.key">
        <div>
          <dt class="text-12px text-ink-tertiary">{{ group.label }}</dt>
          <dd class="m-0 text-ink-primary">
            <template v-for="(item, index) in group.items" :key="item.key"
              >{{ index ? ', ' : ''
              }}<NuxtLink v-if="item.href" :to="item.href" class="font-mono text-12.5px text-ink-primary">{{
                item.token
              }}</NuxtLink
              ><code v-else class="font-mono text-12.5px text-component-mono-text">{{ item.token }}</code></template
            >
          </dd>
        </div>
      </template>
    </dl>
    <NuxtLink
      :to="localePath(packagePath(pkg.token, 'dependencies'))"
      class="mt-8px inline-flex text-12.5px text-ink-secondary no-underline hover:text-ink-primary"
    >
      {{ t('package.depsSummary.more') }}
    </NuxtLink>
  </section>
</template>
