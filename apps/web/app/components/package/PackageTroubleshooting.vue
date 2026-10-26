<script setup lang="ts">
import { formatDate } from '~/utils/packageDate';
import type { PackageKind } from '@opennavo/shared';
import { displayUrl, OnButton, OnChip } from '@opennavo/ui';
import { packagePath } from '~/utils/packageView';
import { hasTroubleshootingPage, issueAnchor, troubleshootingDecision, verifiedIssues } from '~/utils/troubleshooting';

// Troubleshooting (F7): an app's known issues, each with the app and macOS versions it applies to, the steps
// that fix or work around it, its source and the day it was checked, plus links to existing community
// threads (never copied here). It opens only with enough verified issues; otherwise the address is a 404, so
// there is never an empty page. It is not a forum: no DiscussionForumPosting, no user posts, and reports
// sent through Feedback stay private.
const props = defineProps<{ kind: PackageKind }>();

const { t, locale } = useI18n();
const appLocale = computed(() => toAppLocale(locale.value));
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');

const { data: pkg, error } = await usePackage(props.kind);
// An outage keeps the layout's retryable error state (503); a missing page is a 404.
if (!error.value && !hasTroubleshootingPage(pkg.value))
  throw createError({ statusCode: 404, statusMessage: 'Page not found', fatal: true });

const issues = computed(() => verifiedIssues(pkg.value));
const name = computed(() => pkg.value?.displayName ?? '');
// Issues fall back to English where this language has none yet; the page says so and stays noindex.
const fallback = computed(() => issues.value.some(issue => issue.locale !== appLocale.value));
const date = (value: string) => formatDate(value, { locale: appLocale.value });
const list = (values: readonly string[]) =>
  new Intl.ListFormat(appLocale.value, { type: 'conjunction' }).format(values);
const macos = (values: readonly string[]) => list(values.map(version => `macOS ${version}`));

usePageDecision(() => (pkg.value ? troubleshootingDecision(pkg.value, appLocale.value) : undefined));
usePageSeo({
  title: () => t('troubleshooting.metaTitle', { name: name.value }),
  description: () =>
    t(
      'troubleshooting.metaDescription',
      { name: name.value, titles: list(issues.value.slice(0, 2).map(issue => issue.title)) },
      { plural: issues.value.length }
    )
});

// Reports go to the private feedback form, prefilled with this app; nothing is published.
const reportPath = computed(() =>
  pkg.value
    ? localePath({ path: '/feedback', query: { type: 'other', kind: pkg.value.kind, token: pkg.value.token } })
    : localePath('/feedback')
);
</script>

<template>
  <section v-if="pkg && issues.length" class="flex flex-col gap-18px pt-4px" aria-labelledby="troubleshooting-title">
    <div class="flex flex-col gap-8px">
      <h2 id="troubleshooting-title" class="m-0 text-17px font-600 text-ink-primary">
        {{ t('troubleshooting.title', { count: issues.length }, { plural: issues.length }) }}
      </h2>
      <p class="m-0 max-w-760px text-14px leading-[1.7] text-ink-secondary">
        {{ t('troubleshooting.intro', { name }) }}
      </p>
      <p v-if="fallback" class="m-0 text-13px text-ink-tertiary">{{ t('troubleshooting.fallbackLanguage') }}</p>
    </div>

    <nav
      v-if="issues.length > 1"
      class="rounded-big border border-solid border-line-subtle bg-surface-card px-18px py-14px"
      :aria-label="t('troubleshooting.contents')"
    >
      <p class="m-0 text-12px font-600 text-ink-tertiary">{{ t('troubleshooting.contents') }}</p>
      <ol class="m-0 mt-8px flex flex-col gap-4px pl-20px text-13.5px leading-[1.6]">
        <li v-for="(issue, index) in issues" :key="index">
          <a :href="`#${issueAnchor(index)}`" class="text-ink-primary underline-offset-3 hover:underline">{{
            issue.title
          }}</a>
        </li>
      </ol>
    </nav>

    <article
      v-for="(issue, index) in issues"
      :id="issueAnchor(index)"
      :key="index"
      class="flex scroll-mt-80px flex-col gap-12px rounded-big border border-solid border-line-subtle bg-surface-card px-20px py-18px"
      :aria-labelledby="`${issueAnchor(index)}-title`"
    >
      <h3 :id="`${issueAnchor(index)}-title`" class="m-0 text-15.5px font-600 leading-[1.45] text-ink-primary">
        {{ issue.title }}
      </h3>
      <p class="m-0 text-14px leading-[1.7] text-ink-secondary">{{ issue.symptom }}</p>
      <dl class="m-0 grid gap-x-16px gap-y-4px text-13px leading-[1.6] sm:grid-cols-[auto_minmax(0,1fr)]">
        <dt class="text-ink-tertiary">{{ t('troubleshooting.appVersions', { name }) }}</dt>
        <dd class="m-0 text-ink-primary">{{ list(issue.appVersions) }}</dd>
        <dt class="text-ink-tertiary">{{ t('troubleshooting.macosVersions') }}</dt>
        <dd class="m-0 text-ink-primary">{{ macos(issue.macosVersions) }}</dd>
      </dl>
      <div class="flex flex-col gap-6px">
        <h4 class="m-0 text-13px font-600 text-ink-primary">{{ t('troubleshooting.steps') }}</h4>
        <ol class="m-0 flex flex-col gap-6px pl-20px text-14px leading-[1.7] text-ink-secondary">
          <li v-for="(step, stepIndex) in issue.steps" :key="stepIndex" class="whitespace-pre-line">{{ step }}</li>
        </ol>
      </div>
      <div v-if="issue.communityLinks.length" class="flex flex-col gap-6px">
        <h4 class="m-0 text-13px font-600 text-ink-primary">{{ t('troubleshooting.community') }}</h4>
        <ul class="m-0 flex list-none flex-col gap-4px p-0 text-13px">
          <li v-for="link in issue.communityLinks" :key="link">
            <a :href="link" target="_blank" rel="nofollow noopener noreferrer" class="text-ink-secondary">{{
              displayUrl(link)
            }}</a>
          </li>
        </ul>
        <p class="m-0 text-12px text-ink-tertiary">{{ t('troubleshooting.communityHint') }}</p>
      </div>
      <p class="m-0 flex flex-wrap items-center gap-x-8px gap-y-4px text-12.5px text-ink-tertiary">
        <span>{{ t('troubleshooting.source') }}</span>
        <a :href="issue.evidence.sourceUrl" target="_blank" rel="noopener noreferrer" class="text-ink-secondary">{{
          displayUrl(issue.evidence.sourceUrl)
        }}</a>
        <span v-if="issue.evidence.checkedAt">{{
          t('troubleshooting.checkedOn', { date: date(issue.evidence.checkedAt) })
        }}</span>
        <OnChip v-if="issue.locale !== appLocale" size="sm">{{ t('troubleshooting.english') }}</OnChip>
      </p>
    </article>

    <aside
      class="flex flex-col gap-10px rounded-big border border-solid border-line-subtle px-20px py-18px"
      aria-labelledby="troubleshooting-report"
    >
      <h2 id="troubleshooting-report" class="m-0 text-15px font-600 text-ink-primary">
        {{ t('troubleshooting.report.title') }}
      </h2>
      <p class="m-0 text-13.5px leading-[1.7] text-ink-secondary">{{ t('troubleshooting.report.body', { name }) }}</p>
      <div class="flex flex-wrap items-center gap-10px">
        <OnButton variant="secondary" size="sm" icon="file-text" :href="reportPath" :link-as="NuxtLink">{{
          t('troubleshooting.report.action')
        }}</OnButton>
        <NuxtLink
          :to="localePath(packagePath(pkg.token))"
          class="text-13px text-ink-secondary underline-offset-3 hover:text-ink-primary hover:underline"
          >{{ t('troubleshooting.report.back', { name }) }}</NuxtLink
        >
      </div>
    </aside>
  </section>
</template>
