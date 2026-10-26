<script setup lang="ts">
import { formatDate } from '~/utils/packageDate';
import { aroundDate, isoDate, quickFacts } from '~/utils/packageFacts';
import type { PackagePage } from '~/utils/packageFacts';

// Quick facts (T2): a description list in the server-rendered HTML before every other block and the FAQ,
// built from the same verified facts as the price block, the requirements table, the questions and the
// JSON-LD. Commands stay one code element. The footnote gives the oldest check date of the facts above;
// each fact's own source and date are under Sources.
const props = defineProps<{ pkg: PackagePage }>();

const { t, locale } = useI18n();
const appLocale = computed(() => toAppLocale(locale.value));
const facts = computed(() => quickFacts(props.pkg, t, appLocale.value));
const checked = computed(() => {
  const at = facts.value.checkedAt;
  return at
    ? { iso: isoDate(at), date: formatDate(at, { locale: appLocale.value }), ...aroundDate(t, 'package.quick.checked') }
    : null;
});
// Chinese and Japanese run sentences together; other languages put a space before the link.
const gap = computed(() => (['zh-CN', 'zh-Hant', 'ja-JP'].includes(appLocale.value) ? '' : ' '));
</script>

<template>
  <section
    v-if="facts.rows.length"
    id="facts"
    class="rounded-big border border-solid border-line-subtle bg-surface-card px-18px py-14px"
    aria-labelledby="facts-title"
    data-testid="package-quick-facts"
  >
    <h2 id="facts-title" class="m-0 text-13.5px font-600 text-ink-primary">{{ t('package.quick.title') }}</h2>
    <dl
      class="m-0 mt-10px grid grid-cols-1 gap-x-18px gap-y-2px text-13px leading-[1.55] sm:grid-cols-[max-content_minmax(0,1fr)] sm:gap-y-8px"
    >
      <template v-for="row in facts.rows" :key="row.key">
        <dt class="mt-6px text-12.5px text-ink-tertiary first:mt-0 sm:mt-0">{{ row.label }}</dt>
        <dd class="m-0 min-w-0 text-ink-primary [overflow-wrap:anywhere]" :data-fact="row.key">
          <code
            v-if="row.code"
            class="rounded-tiny bg-surface-inset px-6px py-1px font-mono text-12px text-component-mono-text"
            >{{ row.value }}</code
          >
          <template v-else>{{ row.value }}</template>
          <template v-if="row.anchor">
            ·
            <a
              :href="`#${row.anchor}`"
              class="text-12px text-ink-secondary underline-offset-2 hover:text-ink-primary"
              >{{ row.anchorLabel }}</a
            ></template
          >
        </dd>
      </template>
    </dl>
    <p v-if="checked" class="m-0 mt-10px text-12px text-ink-tertiary">
      {{ checked.before }}<time :datetime="checked.iso">{{ checked.date }}</time
      >{{ `${checked.after}${gap}`
      }}<a href="#sources" class="text-ink-secondary underline-offset-2 hover:text-ink-primary">{{
        t('package.quick.sourcesLink')
      }}</a>
    </p>
  </section>
</template>
