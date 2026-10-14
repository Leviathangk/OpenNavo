<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { RouterLink, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { ApiError, unwrap } from '@opennavo/api';
import { formatDate } from '@opennavo/shared';
import { OnBreadcrumb, OnButton, OnEmpty, OnMarkdownView, OnSkeleton } from '@opennavo/ui';
import { renderArticle } from '@opennavo/ui/markdown';
import ArticleCard from '@/components/article/ArticleCard.vue';
import PackageCard from '@/components/package/PackageCard.vue';
import RequestError from '@/components/common/RequestError.vue';
import { api } from '@/api';
import { useAppLocale } from '@/composables/useAppLocale';
import { useLoader } from '@/composables/useLoader';
import { useToasts } from '@/composables/useToasts';
import { fetchClientConfig } from '@/composables/useClientConfig';
import { ARTICLE_SLUG } from '@/utils/article';
import { openExternal } from '@/utils/external';

const props = defineProps<{ slug: string }>();
const { t } = useI18n();
const { appLocale } = useAppLocale();
const router = useRouter();
const toasts = useToasts();
const original = ref(false);
const root = ref<HTMLElement>();
watch(
  [() => props.slug, appLocale],
  () => {
    original.value = false;
  },
  { flush: 'sync' }
);
// Re-read on every visit and refresh: publication/withdrawal state must not stay in a session cache.
const { data, error, loading } = useLoader(async () => {
  let slug = props.slug;
  const visited = new Set<string>();
  while (ARTICLE_SLUG.test(slug) && !visited.has(slug) && visited.size < 5) {
    visited.add(slug);
    const value = await unwrap(api.GET('/articles/{slug}', { params: { path: { slug } } }));
    if (value.state !== 'moved') return value;
    slug = value.slug;
  }
  throw new ApiError({ code: '1002', msg: 'Article not found', status: 404, requestId: null });
}, [() => props.slug, appLocale]);
watch(data, value => {
  if (value && value.slug !== props.slug) void router.replace({ path: `/articles/${value.slug}` });
});
const translated = computed(() => (data.value?.state === 'published' ? data.value.article : undefined));
const {
  data: source,
  error: sourceError,
  loading: sourceLoading
} = useLoader(async () => {
  const value = translated.value;
  if (!original.value || !value) return undefined;
  return unwrap(
    api.GET('/articles/{slug}', {
      params: { path: { slug: value.slug }, query: { locale: value.sourceLocale } }
    })
  );
}, [original, translated]);
const article = computed(() =>
  original.value && source.value?.state === 'published' ? source.value.article : translated.value
);
const withdrawn = computed(
  () => data.value?.state === 'withdrawn' || (original.value && source.value?.state === 'withdrawn')
);
const body = computed(() => renderArticle(article.value?.bodyMarkdown ?? '', { images: article.value?.images ?? [] }));
const apps = computed(() => new Map((article.value?.packages ?? []).map(pkg => [pkg.token, pkg])));
const sourceText = computed(() => article.value && !article.value.availableLocales.includes(appLocale.value));
const crumbs = computed(() => [
  { label: t('articles.title'), href: '/articles' },
  { label: article.value?.title ?? t('articles.title') }
]);
function goHeading(id: string) {
  const target = root.value?.querySelector<HTMLElement>(`[id="${CSS.escape(id)}"]`);
  target?.scrollIntoView({ block: 'start' });
  target?.setAttribute('tabindex', '-1');
  target?.focus({ preventScroll: true });
}
async function followLink(event: MouseEvent) {
  if (event.defaultPrevented || !(event.target instanceof Element)) return;
  const link = event.target.closest('a');
  if (!link) return;
  const href = link.getAttribute('href') ?? '';
  // Vue Router links use #/ paths; article anchors must not replace the hash router's URL.
  if (href.startsWith('#') && !href.startsWith('#/')) {
    event.preventDefault();
    try {
      goHeading(decodeURIComponent(href.slice(1)));
    } catch {
      /* Invalid fragments are ignored. */
    }
  } else if (href.startsWith('/') && !href.startsWith('//')) {
    event.preventDefault();
    const localPath = href.replace(/^\/(en|zh|ja|es|pt|ru)(?=\/)/, '');
    if (/^\/(articles|collections)(\/|\?|$)/.test(localPath)) {
      await router.push(localPath);
    } else if (/^\/apps\/[^/?#]+/.test(localPath)) {
      await router.push(localPath.replace('/apps/', '/package/cask/'));
    } else {
      try {
        const config = await fetchClientConfig();
        await openExternal(new URL(href, config.links.web).href);
      } catch {
        toasts.push({ tone: 'danger', title: t('errors.actionFailed') });
      }
    }
  } else if (/^https:\/\//i.test(href)) {
    event.preventDefault();
    try {
      await openExternal(href);
    } catch {
      toasts.push({ tone: 'danger', title: t('errors.actionFailed') });
    }
  } else if (!href.startsWith('#/')) {
    // The native opener only permits HTTPS; never navigate the webview to an arbitrary scheme.
    event.preventDefault();
  }
}
</script>

<template>
  <div ref="root" class="mx-auto min-w-0 max-w-960px pb-24px" @click="followLink">
    <OnBreadcrumb :items="crumbs" :link-as="RouterLink" class="mb-20px" />
    <RequestError v-if="error && !data" :error="error" />
    <OnEmpty
      v-else-if="withdrawn"
      :title="t('articles.withdrawnTitle')"
      :description="t('articles.withdrawnBody')"
      icon="lucide:circle-alert"
    >
      <template #actions>
        <RouterLink to="/articles" class="text-status-info underline outline-none focus-visible:shadow-focus-ring">{{
          t('articles.backToList')
        }}</RouterLink>
      </template>
    </OnEmpty>
    <div
      v-else-if="!article"
      role="status"
      :aria-label="t('common.loading')"
      :aria-busy="loading"
      class="flex flex-col gap-20px"
    >
      <OnSkeleton class="h-40px w-3/4" /><OnSkeleton class="h-20px w-full" /><OnSkeleton class="h-240px w-full" />
    </div>
    <article v-else :lang="article.locale" class="min-w-0">
      <header class="flex flex-col gap-16px">
        <h1 class="m-0 break-words text-30px font-600 leading-[1.25] tracking-[-0.025em] text-ink-primary">
          {{ article.title }}
        </h1>
        <p v-if="article.summary" class="m-0 max-w-[72ch] text-16px leading-[1.75] text-ink-secondary">
          {{ article.summary }}
        </p>
        <div class="flex flex-wrap items-center gap-x-12px gap-y-6px text-12px text-ink-tertiary">
          <span class="font-600 text-ink-secondary">{{ article.author?.name ?? t('articles.editorial') }}</span>
          <span>{{ t(`articles.types.${article.type}`) }}</span>
          <time :datetime="article.publishedAt">{{
            t('articles.published', {
              date: formatDate(article.publishedAt, { locale: appLocale })
            })
          }}</time>
          <time
            v-if="article.updatedAt.slice(0, 10) !== article.publishedAt.slice(0, 10)"
            :datetime="article.updatedAt"
            >{{ t('articles.updated', { date: formatDate(article.updatedAt, { locale: appLocale }) }) }}</time
          >
          <span>{{ t('articles.readingTime', { minutes: article.readingMinutes }) }}</span>
          <span v-if="article.machineTranslated">{{ t('articles.machineTranslated') }}</span>
          <OnButton
            v-if="translated?.machineTranslated"
            size="sm"
            variant="ghost"
            :loading="sourceLoading"
            @click="original = !original"
            >{{ t(original ? 'articles.viewTranslation' : 'articles.viewOriginal') }}</OnButton
          >
        </div>
        <RequestError v-if="sourceError" :error="sourceError" />
        <p
          v-if="sourceText"
          :lang="appLocale"
          class="m-0 rounded-default bg-surface-inset p-12px text-13px text-ink-secondary"
          data-testid="article-source-text"
        >
          {{ t('articles.sourceText') }}
        </p>
      </header>
      <img
        v-if="article.cover"
        :src="article.cover.url"
        :alt="article.cover.alt"
        :width="article.cover.width ?? undefined"
        :height="article.cover.height ?? undefined"
        decoding="async"
        class="mt-24px block h-auto max-h-400px w-full rounded-big bg-surface-inset object-cover"
      />
      <div class="mt-28px flex min-w-0 flex-col gap-32px xl:flex-row-reverse xl:items-start">
        <nav
          v-if="body.toc.length > 1"
          :aria-label="t('articles.toc')"
          class="min-w-0 xl:sticky xl:top-20px xl:w-180px xl:shrink-0"
        >
          <details open>
            <summary class="cursor-pointer text-13px font-600 text-ink-primary">
              {{ t('articles.toc') }}
            </summary>
            <ol class="m-0 mt-8px flex list-none flex-col gap-6px p-0">
              <li v-for="heading in body.toc" :key="heading.id">
                <button
                  type="button"
                  class="w-full rounded-tiny border-none bg-transparent py-4px text-left text-13px text-ink-secondary outline-none hover:text-ink-primary focus-visible:shadow-focus-ring"
                  @click="goHeading(heading.id)"
                >
                  {{ heading.text }}
                </button>
              </li>
            </ol>
          </details>
        </nav>
        <div class="min-w-0 flex-1">
          <aside
            v-if="article.testNote"
            class="mb-24px rounded-default bg-surface-inset p-12px text-13px leading-[1.7] text-ink-secondary"
          >
            <strong>{{ t('articles.testNote') }}</strong> {{ article.testNote }}
          </aside>
          <div class="max-w-[76ch]" data-testid="article-body">
            <template v-for="(segment, index) in body.segments" :key="index">
              <OnMarkdownView v-if="segment.type === 'html'" :html="segment.html" size="article" />
              <div v-else-if="apps.get(segment.token)" class="my-20px">
                <PackageCard :pkg="apps.get(segment.token)!" />
                <p class="m-0 mt-8px text-12px text-ink-tertiary">
                  {{ t('articles.appCardNote') }}
                </p>
              </div>
            </template>
          </div>
          <section v-if="article.sources.length" class="mt-32px" aria-labelledby="article-sources">
            <h2 id="article-sources" class="m-0 text-15px font-600 text-ink-primary">
              {{ t('articles.sources') }}
            </h2>
            <ol class="mt-12px flex flex-col gap-8px pl-20px text-13px">
              <li v-for="citation in article.sources" :key="citation.url">
                <a
                  :href="citation.url"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="break-words text-status-info underline outline-none focus-visible:shadow-focus-ring"
                  >{{ citation.title }}</a
                >
              </li>
            </ol>
          </section>
          <section v-if="article.author" class="mt-32px border-t border-t-solid border-line-subtle pt-20px">
            <h2 class="m-0 text-15px font-600 text-ink-primary">{{ article.author.name }}</h2>
            <p v-if="article.author.role" class="mt-6px text-12px text-ink-tertiary">
              {{ article.author.role }}
            </p>
            <p v-if="article.author.bio" class="text-14px leading-[1.7] text-ink-secondary">
              {{ article.author.bio }}
            </p>
          </section>
          <section v-if="article.packages.length" class="mt-32px" aria-labelledby="article-apps">
            <h2 id="article-apps" class="m-0 mb-12px text-15px font-600 text-ink-primary">
              {{ t('articles.mentionedApps') }}
            </h2>
            <div class="grid gap-12px sm:grid-cols-2">
              <PackageCard v-for="pkg in article.packages" :key="pkg.token" :pkg="pkg" />
            </div>
          </section>
          <section v-if="article.related.length" class="mt-32px" aria-labelledby="article-related">
            <h2 id="article-related" class="m-0 text-15px font-600 text-ink-primary">
              {{ t('articles.related') }}
            </h2>
            <ArticleCard v-for="card in article.related" :key="card.slug" :card="card" compact :heading-level="3" />
          </section>
          <RouterLink
            to="/articles"
            class="mt-28px inline-block text-13px text-status-info underline outline-none focus-visible:shadow-focus-ring"
            >{{ t('articles.backToList') }}</RouterLink
          >
        </div>
      </div>
    </article>
  </div>
</template>
