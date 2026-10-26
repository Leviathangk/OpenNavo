<script setup lang="ts">
import { formatDate } from '~/utils/packageDate';
import { LOCALE_METADATA } from '@opennavo/shared';
import { displayUrl } from '@opennavo/ui';
import { isoDate } from '~/utils/packageFacts';
import type { PackagePage } from '~/utils/packageFacts';
import { packagePath } from '~/utils/packageView';
import { hasTroubleshootingPage, issueAnchor, verifiedIssues } from '~/utils/troubleshooting';

// Known issues (F7, app-page block): verified issues from facts.issues, each with the app and macOS versions it
// applies to, the steps, links to existing community threads (never copied here) and its source and check
// date. When the app has its own troubleshooting page, this block lists the first few and links there, so the
// two never compete for the same search. Nothing here is a discussion or user post.
const props = defineProps<{ pkg: PackagePage }>();

const PREVIEW = 3;

const { t, locale } = useI18n();
const localePath = useLocalePath();
const appLocale = computed(() => toAppLocale(locale.value));

const all = computed(() => verifiedIssues(props.pkg));
const hasPage = computed(() => hasTroubleshootingPage(props.pkg));
const list = (values: readonly string[]) =>
  new Intl.ListFormat(appLocale.value, { type: 'conjunction' }).format(values);

const issues = computed(() =>
  (hasPage.value ? all.value.slice(0, PREVIEW) : all.value).map((issue, index) => ({
    key: `${index}:${issue.title}`,
    anchor: issueAnchor(index),
    title: issue.title,
    symptom: issue.symptom,
    // An English issue on another language's page keeps its language.
    lang: issue.locale === appLocale.value ? undefined : LOCALE_METADATA[issue.locale].hreflang,
    applies: t('package.issues.applies', {
      name: props.pkg.displayName,
      versions: list(issue.appVersions),
      macos: list(issue.macosVersions.map(version => `macOS ${version}`))
    }),
    steps: issue.steps,
    links: issue.communityLinks.filter(url => url.startsWith('https://')),
    source: issue.evidence.sourceUrl,
    checked: issue.evidence.checkedAt
      ? {
          iso: isoDate(issue.evidence.checkedAt),
          date: formatDate(issue.evidence.checkedAt, { locale: appLocale.value })
        }
      : null
  }))
);
const pageHref = computed(() => localePath(packagePath(props.pkg.token, 'troubleshooting')));
</script>

<template>
  <section
    v-if="issues.length"
    id="issues"
    class="rounded-big border border-solid border-line-subtle bg-surface-card px-18px py-16px"
    aria-labelledby="issues-title"
    data-testid="package-issues"
  >
    <h2 id="issues-title" class="m-0 text-13.5px font-600 text-ink-primary">
      {{ t('package.issues.title', { name: pkg.displayName }) }}
    </h2>
    <p class="m-0 mt-4px text-12px leading-[1.6] text-ink-tertiary">{{ t('package.issues.hint') }}</p>
    <article
      v-for="issue in issues"
      :key="issue.key"
      class="mt-10px border-t border-t-solid border-line-subtle pt-10px first-of-type:border-t-0"
      :lang="issue.lang"
    >
      <h3 class="m-0 text-13px font-600 text-ink-primary">
        <NuxtLink
          v-if="hasPage"
          :to="`${pageHref}#${issue.anchor}`"
          class="text-ink-primary no-underline hover:underline"
          >{{ issue.title }}</NuxtLink
        >
        <template v-else>{{ issue.title }}</template>
      </h3>
      <p class="m-0 mt-2px text-13px leading-[1.6] text-ink-secondary">{{ issue.symptom }}</p>
      <p class="m-0 mt-2px text-12px text-ink-tertiary">{{ issue.applies }}</p>
      <template v-if="!hasPage">
        <h4 class="m-0 mt-6px text-12.5px font-600 text-ink-primary">{{ t('package.issues.steps') }}</h4>
        <ol class="m-0 mt-4px flex flex-col gap-4px pl-18px text-13px leading-[1.6] text-ink-secondary">
          <li v-for="(step, index) in issue.steps" :key="index">{{ step }}</li>
        </ol>
        <p v-if="issue.links.length" class="m-0 mt-6px text-12px text-ink-tertiary">
          {{ `${t('package.issues.community')} `
          }}<template v-for="(url, index) in issue.links" :key="url"
            >{{ index ? ' · ' : ''
            }}<a
              :href="url"
              target="_blank"
              rel="nofollow noopener noreferrer"
              class="text-ink-secondary underline-offset-2 hover:text-ink-primary"
              >{{ displayUrl(url) }}</a
            ></template
          >
        </p>
      </template>
      <p class="m-0 mt-4px text-11.5px text-ink-tertiary">
        <a
          :href="issue.source"
          target="_blank"
          rel="nofollow noopener noreferrer"
          class="text-ink-secondary underline-offset-2 hover:text-ink-primary"
          >{{ displayUrl(issue.source) }}</a
        ><template v-if="issue.checked">
          · <time :datetime="issue.checked.iso">{{ issue.checked.date }}</time></template
        >
      </p>
    </article>
    <NuxtLink
      v-if="hasPage"
      :to="pageHref"
      class="mt-10px inline-flex text-12.5px font-600 text-ink-primary no-underline hover:underline"
    >
      {{ t('package.issues.all', { count: all.length }, { plural: all.length }) }}
    </NuxtLink>
  </section>
</template>
