<script setup lang="ts">
import { OnBreadcrumb, OnErrorState } from '@opennavo/ui';
import { packagePath } from '~/utils/packageView';
import { uninstallFacts } from '~/utils/packageFacts';
import { appEntityId } from '~/utils/structuredData';
import { uninstallPageOpen, uninstallPreviewLink } from '~/utils/uninstallPage';

// "How to uninstall {app} on Mac" (X2): an intent page of its own at /apps/{token}/uninstall, outside the
// app's tab layout so it has its own h1, breadcrumb and decision (the file sits outside pages/apps/[token]/
// for that reason; its path comes from definePageMeta). It opens only where the server's decision says so
// and is a 404 anywhere else, so each app × intent × language has one target page. The steps and files are
// the Homebrew definition's declarations (PackageUninstall), never a claim about a given Mac.
definePageMeta({ path: '/apps/:token/uninstall', name: 'apps-token-uninstall' });

const { t, locale } = useI18n();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');

const notFound = () => createError({ statusCode: 404, statusMessage: 'Page not found', fatal: true });
const { data: pkg, error, refresh, removed } = await usePackage('cask');
if (removed.value || (pkg.value && !uninstallPageOpen(pkg.value))) throw notFound();
watch(pkg, value => {
  if (value && !uninstallPageOpen(value)) showError(notFound());
});

// S5: the server's decision for this page: robots, hreflang (languages where it is open too), canonical.
usePageDecision(() => pkg.value?.indexing?.pages?.uninstall);

const name = computed(() => pkg.value?.displayName ?? '');
const overviewPath = computed(() => localePath(packagePath(pkg.value?.token ?? '')));
const preview = computed(() => (pkg.value ? uninstallPreviewLink(pkg.value.token) : null));
const zapCount = computed(() => (pkg.value ? uninstallFacts(pkg.value, 'zap').length : 0));

const breadcrumb = computed(() => {
  const category = pkg.value?.primaryCategory;
  return [
    { label: t('nav.discover'), href: localePath('/discover') },
    ...(category ? [{ label: category.name, href: localePath(`/categories/${category.slug}`) }] : []),
    { label: name.value, href: overviewPath.value },
    { label: t('package.uninstallPage.crumb') }
  ];
});
useBreadcrumbSchema(breadcrumb);
const siteUrl = useRuntimeConfig().public.siteUrl.replace(/\/$/, '');
usePageSchema(() =>
  pkg.value ? { about: { '@id': appEntityId(siteUrl, pkg.value.token), name: pkg.value.displayName } } : {}
);

const title = computed(() => t('package.uninstallPage.title', { name: name.value }));
const description = computed(() =>
  t('package.uninstallPage.description', { name: name.value, count: zapCount.value }, zapCount.value)
);
usePageSeo({ title: () => t('package.uninstallPage.metaTitle', { name: name.value }), description });
if (pkg.value)
  defineOgImage('OgListSatori', {
    locale: toAppLocale(locale.value),
    label: t('package.uninstallPage.crumb'),
    title: title.value,
    description: description.value,
    icons: ogAppIcons([pkg.value]),
    ...useOgFrame()
  });
</script>

<template>
  <OnErrorState v-if="error && !pkg" class="py-64px" @retry="refresh()" />
  <article v-else-if="pkg" class="box-border flex flex-col gap-24px pt-6px" aria-labelledby="uninstall-page-title">
    <OnBreadcrumb :items="breadcrumb" :link-as="NuxtLink" />
    <header class="flex flex-col gap-12px">
      <h1 id="uninstall-page-title" class="m-0 text-title1 text-ink-primary">{{ title }}</h1>
      <p class="m-0 max-w-760px text-14px leading-[1.7] text-ink-secondary">
        {{ t('package.uninstallPage.intro', { name }) }}
      </p>
    </header>
    <PackageUninstall :pkg="pkg" :homebrew="true" :guide="false" />
    <section
      class="max-w-760px rounded-big border border-solid border-line-subtle bg-surface-card px-18px py-16px"
      aria-labelledby="uninstall-opennavo"
    >
      <h2 id="uninstall-opennavo" class="m-0 text-15px font-600 text-ink-primary">
        {{ t('package.uninstallPage.withOpenNavo') }}
      </h2>
      <p class="m-0 mt-6px text-13px leading-[1.65] text-ink-secondary">
        {{ t('package.uninstallPage.withOpenNavoBody') }}
      </p>
      <p class="m-0 mt-12px flex flex-wrap gap-x-16px gap-y-8px text-13px">
        <a v-if="preview" :href="preview" class="text-ink-primary">{{ t('package.uninstallPage.open') }}</a>
        <NuxtLink :to="localePath('/download')" class="text-ink-secondary">{{
          t('package.uninstallPage.download')
        }}</NuxtLink>
      </p>
    </section>
    <p class="m-0 text-13px">
      <NuxtLink :to="overviewPath" class="text-ink-secondary">{{ t('package.uninstallPage.back', { name }) }}</NuxtLink>
    </p>
  </article>
</template>
