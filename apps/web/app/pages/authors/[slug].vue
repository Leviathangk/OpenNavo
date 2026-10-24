<script setup lang="ts">
import { ApiError, unwrap } from '@opennavo/api';
import { OnBreadcrumb, OnEmpty, OnErrorState, OnPagination } from '@opennavo/ui';
import { pageCount as countPages } from '~/utils/catalogQuery';
import { ARTICLE_PAGE_SIZE, ARTICLE_SLUG, authorEntityId } from '~/utils/article';

// Author page (WP17): the profile bylines link to, with the author's published articles. Article
// structured data points its author at the Person defined here. Authors without published articles
// are kept out of the index (and out of the sitemap).
const { t, locale } = useI18n();
const api = useApi();
const route = useRoute();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');
const siteUrl = useRuntimeConfig().public.siteUrl.replace(/\/$/, '');
const { hrefWith } = useCatalogQuery({ page: 1 });
const { page, assertExists } = useListPage();
const slug = computed(() => String(route.params.slug ?? ''));

if (!ARTICLE_SLUG.test(slug.value) || slug.value.length > 80)
  throw createError({ statusCode: 404, statusMessage: 'Author not found', fatal: true });

const { data, error, refresh, status } = await useAsyncData(
  () => `author:${slug.value}:${page.value}:${locale.value}`,
  () =>
    unwrap(
      api.GET('/authors/{slug}', {
        params: { path: { slug: slug.value }, query: { current: page.value, size: ARTICLE_PAGE_SIZE } }
      })
    ).catch((reason: unknown) => {
      if (reason instanceof ApiError && (reason.isNotFound || reason.status === 404))
        throw createError({ statusCode: 404, statusMessage: 'Author not found' });
      throw reason;
    })
);
if (error.value?.statusCode === 404)
  throw createError({ statusCode: 404, statusMessage: 'Author not found', fatal: true });

const author = computed(() => data.value?.author ?? null);
const total = computed(() => data.value?.articles.total ?? 0);
assertExists(() => data.value?.articles.total, ARTICLE_PAGE_SIZE, status);
const pageCount = computed(() => countPages(total.value, ARTICLE_PAGE_SIZE));

const crumbs = computed(() => [
  { label: t('nav.articles'), href: localePath('/articles') },
  { label: author.value?.name ?? slug.value }
]);
useBreadcrumbSchema(crumbs);
useArticleFeed();

const personId = authorEntityId(siteUrl, slug.value);
type PersonInput = NonNullable<Parameters<typeof definePerson>[0]>;
useSchemaOrg(
  computed(() => {
    const value = author.value;
    if (!value) return [];
    const person: Record<string, unknown> = {
      '@id': personId,
      name: value.name,
      url: `${siteUrl}${localePath(`/authors/${value.slug}`)}`
    };
    if (value.role) person.jobTitle = value.role;
    if (value.bio) person.description = value.bio;
    if (value.avatarUrl) person.image = value.avatarUrl;
    if (value.links.length) person.sameAs = value.links.map(link => link.url);
    return [definePerson(person as PersonInput)];
  })
);
usePageSchema(() => ({ type: 'ProfilePage', mainEntity: author.value ? personId : undefined }));
useItemListSchema(
  () =>
    (data.value?.articles.records ?? []).map(card => ({
      name: card.title,
      path: localePath(`/articles/${card.slug}`)
    })),
  () => (page.value - 1) * ARTICLE_PAGE_SIZE + 1,
  () => data.value?.articles.total,
  { asPage: false }
);

usePageSeo({
  title: () => t('authors.metaTitle', { name: author.value?.name ?? slug.value }),
  page,
  description: () => author.value?.bio ?? t('authors.description', { name: author.value?.name ?? slug.value })
});
// An author without articles has nothing of their own to index. Only a noindex is written: an empty robots
// value removes the default meta tag while X-Robots-Tag keeps it, and the audit then sees them disagree.
useHead(() =>
  data.value && !total.value
    ? { meta: [{ name: 'robots', content: 'noindex, follow', tagPriority: 'high' as const }] }
    : {}
);
if (author.value)
  defineOgImage('OgListSatori', {
    locale: toAppLocale(locale.value),
    label: t('authors.label'),
    title: author.value.name,
    description: author.value.role ?? undefined,
    ...useOgFrame()
  });
</script>

<template>
  <div class="box-border flex flex-col gap-16px pt-6px">
    <OnErrorState v-if="error && !data" @retry="refresh()" />
    <template v-else-if="author">
      <OnBreadcrumb :items="crumbs" :link-as="NuxtLink" />
      <header class="flex items-start gap-16px" data-testid="author-profile">
        <img
          v-if="author.avatarUrl"
          :src="author.avatarUrl"
          alt=""
          width="72"
          height="72"
          class="block h-72px w-72px shrink-0 rounded-full bg-surface-inset object-cover"
        />
        <div class="flex min-w-0 flex-col gap-6px">
          <h1 class="m-0 text-28px font-600 leading-[1.2] text-ink-primary">{{ author.name }}</h1>
          <span v-if="author.role" class="text-13px text-ink-tertiary">{{ author.role }}</span>
          <p v-if="author.bio" class="m-0 max-w-[72ch] text-14px leading-[1.7] text-ink-secondary">{{ author.bio }}</p>
          <ul
            v-if="author.links.length"
            class="m-0 flex list-none flex-wrap gap-12px p-0 text-13px"
            :aria-label="t('authors.links')"
          >
            <li v-for="link in author.links" :key="link.url">
              <a
                :href="link.url"
                target="_blank"
                rel="me noopener noreferrer"
                class="text-status-info no-underline hover:underline"
                >{{ link.title }}</a
              >
            </li>
          </ul>
        </div>
      </header>
      <section class="flex flex-col gap-12px" aria-labelledby="author-articles">
        <h2 id="author-articles" class="m-0 text-15px font-600 text-ink-primary">
          {{ t('authors.count', { count: total }, { plural: total }) }}
        </h2>
        <OnEmpty v-if="!total" icon="lucide:newspaper" :title="t('authors.empty')" />
        <ArticleCard v-for="card in data?.articles.records ?? []" :key="card.slug" :card="card" :heading-level="3" />
      </section>
      <OnPagination
        v-if="pageCount > 1"
        class="mt-12px self-center"
        :page="page"
        :page-count="pageCount"
        :href-for="target => hrefWith({ page: target })"
        :link-as="NuxtLink"
      />
    </template>
  </div>
</template>
