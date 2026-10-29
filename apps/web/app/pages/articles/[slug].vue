<script setup lang="ts">
import { ApiError, unwrap } from '@opennavo/api';
import { formatDate, LOCALES } from '@opennavo/shared';
import { OnAppRow, OnBreadcrumb, OnErrorState, OnMarkdownView } from '@opennavo/ui';
import type { RenderedArticle } from '@opennavo/ui/markdown';
import { hreflangsFor } from '~~/shared/utils/hreflang';
import { packagePath } from '~/utils/packageView';
import { withVariantNames } from '~/utils/seoTitles';
import {
  ARTICLE_SLUG,
  articleDescription,
  articleIndexedLocales,
  articleRobots,
  articleTitle,
  blogPostingNode,
  roundupItems,
  showUpdated
} from '~/utils/article';

// Article page (WP17): the published revision with the full body in SSR. A former slug redirects
// permanently, a withdrawn article answers 410. Languages whose text is not ready show the source
// text, stay out of the index and out of hreflang.
const { t, locale } = useI18n();
const api = useApi();
const route = useRoute();
const localePath = useLocalePath();
const event = useRequestEvent();
const NuxtLink = resolveComponent('NuxtLink');
const siteUrl = useRuntimeConfig().public.siteUrl.replace(/\/$/, '');
const appLocale = computed(() => toAppLocale(locale.value));
const slug = computed(() => String(route.params.slug ?? ''));
// L1: robots and hreflang list only ready languages the site has open for indexing, with app.vue's fallback.
const settings = useSiteSettings();
const openLocales = computed(() => settings.value?.pages.openLocales ?? [...LOCALES]);

if (!ARTICLE_SLUG.test(slug.value))
  throw createError({ statusCode: 404, statusMessage: 'Article not found', fatal: true });

const { data, error, refresh } = await useAsyncData(
  () => `article:${slug.value}:${locale.value}`,
  () =>
    unwrap(api.GET('/articles/{slug}', { params: { path: { slug: slug.value } } })).catch((reason: unknown) => {
      if (reason instanceof ApiError && (reason.isNotFound || reason.status === 404))
        throw createError({ statusCode: 404, statusMessage: 'Article not found' });
      throw reason;
    }),
  { transform: withVariantNames }
);
if (error.value?.statusCode === 404)
  throw createError({ statusCode: 404, statusMessage: 'Article not found', fatal: true });
if (data.value?.state === 'moved') await navigateTo(localePath(`/articles/${data.value.slug}`), { redirectCode: 301 });
const withdrawn = computed(() => data.value?.state === 'withdrawn');
if (withdrawn.value && event) setResponseStatus(event, 410);
const article = computed(() => (data.value?.state === 'published' ? (data.value.article ?? null) : null));

// Rendered on the server and carried in the payload, so hydration does not load the renderer.
const { data: body } = await useAsyncData(
  () => `article-body:${slug.value}:${locale.value}:${article.value?.revision ?? 0}`,
  async (): Promise<RenderedArticle> => {
    const value = article.value;
    if (!value?.bodyMarkdown) return { segments: [], toc: [] };
    const { renderArticle } = await import('@opennavo/ui/markdown');
    return renderArticle(value.bodyMarkdown, { images: value.images, title: value.title });
  }
);

const apps = computed(() => new Map((article.value?.packages ?? []).map(pkg => [pkg.token, pkg])));
const cardTokens = computed(() =>
  (body.value?.segments ?? []).flatMap(segment => (segment.type === 'app' ? [segment.token] : []))
);
const typeLabel = computed(() => (article.value ? t(`articles.types.${article.value.type}`) : ''));
const textLang = computed(() =>
  article.value && article.value.locale !== appLocale.value ? article.value.locale : undefined
);
const sourceText = computed(() => Boolean(article.value && !article.value.availableLocales.includes(appLocale.value)));
const authorPath = computed(() =>
  article.value?.author ? localePath(`/authors/${article.value.author.slug}`) : undefined
);
const dates = computed(() => {
  const value = article.value;
  if (!value) return null;
  return {
    published: formatDate(value.publishedAt, { locale: appLocale.value }),
    updated: showUpdated(value) ? formatDate(value.updatedAt, { locale: appLocale.value }) : null
  };
});

const crumbs = computed(() => [
  { label: t('nav.articles'), href: localePath('/articles') },
  ...(article.value ? [{ label: typeLabel.value, href: `${localePath('/articles')}?type=${article.value.type}` }] : []),
  { label: article.value?.title ?? t('articles.withdrawnTitle') }
]);
useBreadcrumbSchema(crumbs);
const feedPath = useArticleFeed();

// Share image: the headline beside the apps it covers. Its path also stands in for a missing cover
// in structured data; kept in state so hydration sees the server's value.
const ogPaths = article.value
  ? defineOgImage('OgListSatori', {
      locale: appLocale.value,
      label: typeLabel.value,
      title: article.value.title,
      description: article.value.summary ?? undefined,
      icons: article.value.packages.slice(0, 4).map(pkg => ({
        name: pkg.displayName,
        token: pkg.token,
        src: pkg.iconUrl,
        accent: pkg.accentColor
      })),
      ...useOgFrame()
    })
  : [];
const shareImage = useState(`article-og:${slug.value}:${locale.value}`, () => ogPaths[0] ?? '');

type ArticleInput = NonNullable<Parameters<typeof defineArticle>[0]>;
const canonical = useCanonicalUrl();
useSchemaOrg(
  computed(() => {
    const value = article.value;
    if (!value) return [];
    const context = {
      siteUrl,
      pageLocale: toAppLocale(locale.value),
      image: value.cover?.url ?? (shareImage.value ? `${siteUrl}${shareImage.value}` : undefined),
      authorPath: authorPath.value,
      appPath: (token: string) => localePath(packagePath(token))
    };
    const items = roundupItems(value, cardTokens.value, context);
    return [
      defineArticle({
        ...(blogPostingNode(value, context) as ArticleInput),
        '@id': `${canonical.value}#article`,
        mainEntityOfPage: { '@id': `${canonical.value}#webpage` }
      } as ArticleInput),
      ...(items ? [defineItemList({ '@id': `${canonical.value}#list`, itemListElement: items })] : [])
    ];
  })
);
// T4: the page's WebPage has the article as its main entity.
usePageSchema(() => ({
  mainEntity: article.value ? `${canonical.value}#article` : undefined,
  dateModified: article.value?.updatedAt
}));

const hreflangs = useState<string[] | null>('page:hreflangs', () => null);
let ownedHreflangs: string[] | null = null;
watchEffect(() => {
  ownedHreflangs = article.value
    ? hreflangsFor(articleIndexedLocales(article.value, openLocales.value))
    : withdrawn.value
      ? []
      : null;
  hreflangs.value = ownedHreflangs;
});
onUnmounted(() => {
  if (toRaw(hreflangs.value) === ownedHreflangs) hreflangs.value = null;
});

usePageSeo({
  title: () => (article.value ? articleTitle(article.value) : t('articles.metaTitle')),
  description: () => (article.value ? articleDescription(article.value) : undefined)
});
// Only a noindex is written: an empty robots value removes the default meta tag while X-Robots-Tag keeps
// it, and the audit then sees them disagree.
useHead(() => {
  const robots = article.value ? articleRobots(article.value, appLocale.value, openLocales.value) : undefined;
  return robots ? { meta: [{ name: 'robots', content: robots, tagPriority: 'high' as const }] } : {};
});
useSeoMeta({
  ogType: () => (article.value ? 'article' : undefined),
  articlePublishedTime: () => article.value?.publishedAt,
  articleModifiedTime: () => article.value?.updatedAt
});
</script>

<template>
  <div class="box-border pt-6px">
    <ArticleWithdrawn v-if="withdrawn" />
    <OnErrorState v-else-if="error && !article" @retry="refresh()" />
    <!-- A reading column (and the table of contents beside it on wide screens), centered in the page. -->
    <div
      v-else-if="article"
      class="mx-auto flex w-full max-w-720px flex-col gap-14px xl:grid xl:max-w-[1016px] xl:grid-cols-[minmax(0,1fr)_240px] xl:items-start xl:gap-56px"
    >
      <article class="min-w-0" :lang="textLang" data-testid="article">
        <OnBreadcrumb :items="crumbs" :link-as="NuxtLink" />
        <header class="mt-14px flex flex-col gap-12px">
          <h1 class="m-0 text-28px font-600 leading-[1.25] tracking-[-0.02em] text-ink-primary md:text-32px">
            {{ article.title }}
          </h1>
          <p v-if="article.summary" class="m-0 text-16px leading-[1.7] text-ink-secondary">
            {{ article.summary }}
          </p>
          <div
            class="flex flex-wrap items-center gap-x-12px gap-y-4px text-13px text-ink-tertiary"
            data-testid="article-byline"
          >
            <NuxtLink
              v-if="article.author && authorPath"
              :to="authorPath"
              class="font-600 text-ink-secondary no-underline hover:underline"
              >{{ article.author.name }}</NuxtLink
            >
            <NuxtLink
              v-else
              :to="localePath('/about')"
              class="font-600 text-ink-secondary no-underline hover:underline"
              >{{ t('articles.editorial') }}</NuxtLink
            >
            <time v-if="dates" :datetime="article.publishedAt">{{
              t('articles.published', { date: dates.published })
            }}</time>
            <time v-if="dates?.updated" :datetime="article.updatedAt">{{
              t('articles.updated', { date: dates.updated })
            }}</time>
            <span>{{ t('articles.readingTime', { minutes: article.readingMinutes }) }}</span>
          </div>
          <p
            v-if="sourceText"
            class="m-0 rounded-default bg-surface-inset px-12px py-8px text-13px text-ink-secondary"
            :lang="appLocale"
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
          fetchpriority="high"
          decoding="async"
          class="mt-20px block h-auto w-full rounded-big bg-surface-inset"
        />
        <aside
          v-if="article.testNote"
          class="mt-20px rounded-default border border-solid border-line-subtle px-14px py-10px text-13px leading-[1.7] text-ink-secondary"
          data-testid="article-test-note"
        >
          <strong class="text-ink-primary">{{ t('articles.testNote') }}</strong>
          {{ article.testNote }}
        </aside>
        <nav
          v-if="(body?.toc.length ?? 0) > 1"
          class="mt-20px rounded-default border border-solid border-line-subtle px-14px py-10px xl:hidden"
          :aria-label="t('articles.toc')"
        >
          <details>
            <summary class="cursor-pointer text-13px font-600 text-ink-primary">{{ t('articles.toc') }}</summary>
            <ol class="m-0 mt-8px flex list-none flex-col gap-6px p-0 text-13px">
              <li v-for="heading in body?.toc" :key="heading.id">
                <a :href="`#${heading.id}`" class="text-ink-secondary no-underline hover:text-ink-primary">{{
                  heading.text
                }}</a>
              </li>
            </ol>
          </details>
        </nav>
        <div class="mt-24px" data-testid="article-body">
          <template v-for="(segment, index) in body?.segments ?? []" :key="index">
            <OnMarkdownView v-if="segment.type === 'html'" :html="segment.html" size="article" />
            <ArticleAppCard v-else-if="apps.get(segment.token)" :pkg="apps.get(segment.token)!" />
          </template>
        </div>
        <section v-if="article.sources.length" class="mt-32px" aria-labelledby="article-sources">
          <h2 id="article-sources" class="m-0 text-15px font-600 text-ink-primary">{{ t('articles.sources') }}</h2>
          <ol class="m-0 mt-8px flex list-decimal flex-col gap-6px pl-20px text-13px text-ink-secondary">
            <li v-for="source in article.sources" :key="source.url">
              <a
                :href="source.url"
                target="_blank"
                rel="noopener noreferrer"
                class="text-status-info no-underline hover:underline"
                >{{ source.title }}</a
              >
            </li>
          </ol>
        </section>
        <section
          v-if="article.author"
          class="mt-32px flex flex-col gap-4px rounded-big border border-solid border-line-subtle px-16px py-14px"
          data-testid="article-author"
        >
          <NuxtLink :to="authorPath" class="text-14px font-600 text-ink-primary no-underline hover:underline">{{
            article.author.name
          }}</NuxtLink>
          <span v-if="article.author.role" class="text-12px text-ink-tertiary">{{ article.author.role }}</span>
          <p v-if="article.author.bio" class="m-0 mt-4px text-13px leading-[1.7] text-ink-secondary">
            {{ article.author.bio }}
          </p>
        </section>
        <section v-if="article.related.length" class="mt-32px flex flex-col gap-10px" aria-labelledby="article-related">
          <h2 id="article-related" class="m-0 text-15px font-600 text-ink-primary">{{ t('articles.related') }}</h2>
          <ArticleCard v-for="card in article.related" :key="card.slug" :card="card" compact :heading-level="3" />
        </section>
        <p class="mt-32px text-12px text-ink-tertiary">
          <a :href="feedPath" class="text-ink-tertiary no-underline hover:text-ink-secondary">{{
            t('articles.rss')
          }}</a>
        </p>
      </article>
      <aside class="flex min-w-0 flex-col gap-24px xl:sticky xl:top-64px">
        <nav v-if="(body?.toc.length ?? 0) > 1" class="hidden flex-col gap-8px xl:flex" :aria-label="t('articles.toc')">
          <h2 class="m-0 text-13px font-600 text-ink-primary">{{ t('articles.toc') }}</h2>
          <ol class="m-0 flex list-none flex-col gap-6px p-0 text-13px">
            <li v-for="heading in body?.toc" :key="heading.id">
              <a :href="`#${heading.id}`" class="text-ink-tertiary no-underline hover:text-ink-primary">{{
                heading.text
              }}</a>
            </li>
          </ol>
        </nav>
        <section
          v-if="article.packages.length"
          class="flex flex-col"
          aria-labelledby="article-apps"
          data-testid="article-apps"
        >
          <h2 id="article-apps" class="m-0 text-13px font-600 text-ink-primary">{{ t('articles.mentionedApps') }}</h2>
          <OnAppRow
            v-for="pkg in article.packages"
            :key="pkg.token"
            :kind="pkg.kind"
            :token="pkg.token"
            :name="pkg.displayName"
            :src="pkg.iconUrl"
            :accent="pkg.accentColor"
            :href="localePath(packagePath(pkg.token))"
            :link-as="NuxtLink"
          >
            <template #actions>
              <GetMenu :kind="pkg.kind" :token="pkg.token" :name="pkg.displayName" :link="pkg.link" />
            </template>
          </OnAppRow>
        </section>
      </aside>
    </div>
  </div>
</template>
