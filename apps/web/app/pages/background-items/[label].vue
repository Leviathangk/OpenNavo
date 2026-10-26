<script setup lang="ts">
import { ApiError, unwrap } from '@opennavo/api';
import { formatDate } from '@opennavo/shared';
import { displayUrl, OnAppIcon, OnBreadcrumb, OnChip, OnErrorState, OnIcon } from '@opennavo/ui';
import {
  backgroundItemDecision,
  declaringApps,
  isLaunchdItem,
  isValidLabel,
  itemTypes,
  labelPath,
  launchdFolders,
  plainText,
  startupLink,
  uninstallLink,
  usableContent
} from '~/utils/backgroundItems';
import { packagePath } from '~/utils/packageView';
import { DESCRIPTION_WIDTH, fitSummary, withVariantNames } from '~/utils/seoTitles';

// One background item (F1): what it is (only from a verified description, with its source and check date),
// which apps declare it in Homebrew (candidates, not proof), its type and where such items usually live, how
// to turn it off in System Settings and how OpenNavo manages it, through a preview-only deep link. The page
// never says an item belongs to an app or is safe to delete: labels can be shared or copied, and only the
// signature, path and version on the user's own Mac tell. Robots follow the API's `indexable`.
const { t, locale } = useI18n();
const appLocale = computed(() => toAppLocale(locale.value));
const api = useApi();
const route = useRoute();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');

const notFound = () => createError({ statusCode: 404, statusMessage: 'Page not found', fatal: true });
const label = computed(() => String(route.params.label ?? ''));
if (!isValidLabel(label.value)) throw notFound();

const {
  data: item,
  error,
  refresh
} = await useAsyncData(
  () => `background-item:${label.value}:${locale.value}`,
  async () => {
    try {
      return await unwrap(api.GET('/background-items/{label}', { params: { path: { label: label.value } } }));
    } catch (reason) {
      // An unknown label, or one the API refuses, is a page that does not exist; outages stay retryable (503).
      if (reason instanceof ApiError && (reason.isNotFound || reason.status === 404 || reason.status === 400))
        throw createError({ statusCode: 404, statusMessage: 'Page not found' });
      throw reason;
    }
  },
  { transform: withVariantNames }
);
if (error.value?.statusCode === 404) throw notFound();

const content = computed(() => (item.value ? usableContent(item.value) : null));
const types = computed(() => (item.value ? itemTypes(item.value) : []));
// Each declaring app with its catalog page and the client's uninstall preview (never an uninstall itself).
const apps = computed(() =>
  (item.value ? declaringApps(item.value) : []).map(app => ({
    ...app,
    path: localePath(packagePath(app.package.token)),
    uninstall: app.declarations[0] ? uninstallLink(app.declarations[0]) : null
  }))
);
const startup = computed(() => (item.value ? startupLink(item.value) : null));
const launchd = computed(() => isLaunchdItem(types.value));
const folders = computed(() => launchdFolders(types.value));
const verifiedPaths = computed(() => content.value?.commonPaths ?? []);
const date = (value: string) => formatDate(value, { locale: appLocale.value });
const appNames = computed(() =>
  new Intl.ListFormat(appLocale.value, { type: 'conjunction' }).format(apps.value.map(app => app.package.displayName))
);

const path = computed(() => labelPath(label.value, localePath('/')));
// One address per label (the API's decision applied as robots, canonical and hreflang).
usePageDecision(() => (item.value ? backgroundItemDecision(item.value, appLocale.value, path.value) : undefined));
const breadcrumb = computed(() => [
  { label: t('nav.security'), href: localePath('/security') },
  { label: t('backgroundItems.title'), href: localePath('/background-items') },
  { label: label.value }
]);
useBreadcrumbSchema(breadcrumb);

const description = computed(() => {
  const text = content.value ? plainText(content.value.description) : '';
  const summary = text ? fitSummary(text, DESCRIPTION_WIDTH) : '';
  return summary || t('backgroundItems.item.metaDescription', { label: label.value, apps: appNames.value });
});
usePageSeo({ title: () => t('backgroundItems.item.metaTitle', { label: label.value }), description });
defineOgImage('OgListSatori', {
  locale: appLocale.value,
  label: t('backgroundItems.ogLabel'),
  title: label.value,
  description: description.value,
  icons: ogAppIcons(apps.value.map(app => app.package).filter(pkg => pkg.iconUrl)),
  ...useOgFrame()
});
</script>

<template>
  <OnErrorState v-if="error && !item" class="mt-24px" @retry="refresh()" />
  <article v-else-if="item" class="box-border flex max-w-860px flex-col gap-28px pt-6px">
    <OnBreadcrumb :items="breadcrumb" :link-as="NuxtLink" />
    <header class="flex flex-col gap-10px">
      <span class="text-13px font-600 text-brand-salmon">{{ t('backgroundItems.item.kicker') }}</span>
      <h1 class="m-0 break-all font-mono text-title2 text-ink-primary md:text-title1">{{ item.label }}</h1>
      <div class="flex flex-wrap gap-6px">
        <OnChip v-for="type in types" :key="type" size="md">{{ t(`backgroundItems.types.${type}`) }}</OnChip>
        <OnChip v-if="item.shared" tone="warning" size="md">{{ t('backgroundItems.shared') }}</OnChip>
      </div>
      <p class="m-0 text-14px leading-[1.7] text-ink-secondary">
        {{ t('backgroundItems.item.lede', { apps: appNames }, { plural: apps.length }) }}
      </p>
    </header>

    <p
      class="m-0 flex items-start gap-8px rounded-big bg-status-warning-subtle px-14px py-10px text-13px leading-[1.6] text-ink-secondary"
    >
      <OnIcon name="triangle-alert" :size="15" class="mt-3px shrink-0 text-status-warning" />
      <span>{{ t('backgroundItems.caution') }}</span>
    </p>

    <section class="flex flex-col gap-10px" aria-labelledby="background-what">
      <h2 id="background-what" class="m-0 text-17px font-600 text-ink-primary">
        {{ t('backgroundItems.item.what.title') }}
      </h2>
      <template v-if="content">
        <MarkdownContent size="md" :heading-level="3" :source="content.description" />
        <p class="m-0 flex flex-wrap items-center gap-x-8px gap-y-4px text-12.5px text-ink-tertiary">
          <span>{{ t('backgroundItems.item.source') }}</span>
          <a :href="content.evidence.sourceUrl" target="_blank" rel="noopener noreferrer" class="text-ink-secondary">{{
            displayUrl(content.evidence.sourceUrl)
          }}</a>
          <span v-if="content.evidence.checkedAt">{{
            t('backgroundItems.item.checkedOn', { date: date(content.evidence.checkedAt) })
          }}</span>
          <OnChip v-if="content.evidence.status === 'stale'" tone="warning" size="sm">{{
            t('backgroundItems.item.stale')
          }}</OnChip>
        </p>
      </template>
      <p v-else class="m-0 text-14px leading-[1.7] text-ink-secondary">{{ t('backgroundItems.item.what.none') }}</p>
    </section>

    <section class="flex flex-col gap-10px" aria-labelledby="background-apps">
      <h2 id="background-apps" class="m-0 text-17px font-600 text-ink-primary">
        {{ t('backgroundItems.item.apps.title') }}
      </h2>
      <p class="m-0 text-14px leading-[1.7] text-ink-secondary">{{ t('backgroundItems.item.apps.intro') }}</p>
      <p v-if="item.shared" class="m-0 text-13px leading-[1.6] text-status-warning">
        {{ t('backgroundItems.item.apps.shared', { count: apps.length }) }}
      </p>
      <ul class="m-0 list-none rounded-big border border-solid border-line-subtle p-0">
        <li
          v-for="app in apps"
          :key="app.package.token"
          class="flex items-start gap-12px border-t border-t-solid border-line-subtle px-16px py-14px first:border-t-0"
        >
          <OnAppIcon
            :kind="app.package.kind"
            :token="app.package.token"
            :name="app.package.displayName"
            :src="app.package.iconUrl"
            :variants="app.package.iconVariants"
            :accent="app.package.accentColor"
            :size="40"
          />
          <div class="flex min-w-0 flex-1 flex-col gap-6px">
            <NuxtLink
              :to="app.path"
              class="text-14px font-600 text-ink-primary no-underline underline-offset-3 hover:underline"
              >{{ app.package.displayName }}</NuxtLink
            >
            <ul class="m-0 flex list-none flex-col gap-4px p-0 text-12.5px leading-[1.6] text-ink-secondary">
              <li v-for="declaration in app.declarations" :key="`${declaration.kind}:${declaration.phase}`">
                <span>{{
                  `${t(`backgroundItems.item.apps.declaration.${declaration.kind}`)} · ${t(`backgroundItems.item.apps.phase.${declaration.phase}`)}`
                }}</span>
                <span class="ml-6px inline-flex flex-wrap items-center gap-x-6px text-ink-tertiary">
                  <a :href="declaration.evidence.sourceUrl" target="_blank" rel="noopener noreferrer">{{
                    t('backgroundItems.item.apps.source')
                  }}</a>
                  <span v-if="declaration.evidence.checkedAt">{{
                    t('backgroundItems.item.checkedOn', { date: date(declaration.evidence.checkedAt) })
                  }}</span>
                </span>
              </li>
            </ul>
            <a
              v-if="app.uninstall"
              :href="app.uninstall"
              class="self-start text-12.5px text-ink-secondary underline-offset-3 hover:text-ink-primary hover:underline"
              >{{ t('backgroundItems.item.apps.uninstallPreview') }}</a
            >
          </div>
        </li>
      </ul>
      <p v-if="apps.some(app => app.uninstall)" class="m-0 text-12.5px leading-[1.6] text-ink-tertiary">
        {{ t('backgroundItems.item.apps.uninstallHint') }}
      </p>
    </section>

    <section class="flex flex-col gap-10px" aria-labelledby="background-identity">
      <h2 id="background-identity" class="m-0 text-17px font-600 text-ink-primary">
        {{ t('backgroundItems.item.identity.title') }}
      </h2>
      <p class="m-0 text-14px leading-[1.7] text-ink-secondary">{{ t('backgroundItems.item.identity.intro') }}</p>
      <ul class="m-0 flex flex-col gap-6px pl-20px text-14px leading-[1.7] text-ink-secondary">
        <li v-for="check in item.identityChecks" :key="check">{{ t(`backgroundItems.item.identity.${check}`) }}</li>
      </ul>
    </section>

    <section class="flex flex-col gap-10px" aria-labelledby="background-where">
      <h2 id="background-where" class="m-0 text-17px font-600 text-ink-primary">
        {{ t('backgroundItems.item.where.title') }}
      </h2>
      <template v-if="verifiedPaths.length">
        <p class="m-0 text-14px leading-[1.7] text-ink-secondary">{{ t('backgroundItems.item.where.verified') }}</p>
        <ul class="m-0 flex list-none flex-col gap-4px p-0">
          <li v-for="entry in verifiedPaths" :key="entry" class="break-all font-mono text-12.5px text-ink-primary">
            {{ entry }}
          </li>
        </ul>
      </template>
      <template v-else-if="launchd">
        <p class="m-0 text-14px leading-[1.7] text-ink-secondary">
          {{ t('backgroundItems.item.where.launchd', { file: `${item.label}.plist` }) }}
        </p>
        <ul class="m-0 flex list-none flex-col gap-4px p-0">
          <li v-for="folder in folders" :key="folder" class="font-mono text-12.5px text-ink-primary">{{ folder }}</li>
        </ul>
      </template>
      <p
        v-if="!verifiedPaths.length && types.includes('login_item')"
        class="m-0 text-14px leading-[1.7] text-ink-secondary"
      >
        {{ t('backgroundItems.item.where.loginItem') }}
      </p>
      <p
        v-if="!verifiedPaths.length && types.includes('package_receipt')"
        class="m-0 text-14px leading-[1.7] text-ink-secondary"
      >
        {{ t('backgroundItems.item.where.receipt', { command: `pkgutil --pkg-info ${item.label}` }) }}
      </p>
      <p v-if="launchd" class="m-0 text-13px leading-[1.6] text-ink-tertiary">
        {{ t('backgroundItems.item.where.malware') }}
      </p>
    </section>

    <section class="flex flex-col gap-10px" aria-labelledby="background-turn-off">
      <h2 id="background-turn-off" class="m-0 text-17px font-600 text-ink-primary">
        {{ t('backgroundItems.item.turnOff.title') }}
      </h2>
      <ol class="m-0 flex flex-col gap-6px pl-20px text-14px leading-[1.7] text-ink-secondary">
        <li>{{ t('backgroundItems.item.turnOff.open') }}</li>
        <li>{{ t('backgroundItems.item.turnOff.find') }}</li>
        <li>{{ t('backgroundItems.item.turnOff.switch') }}</li>
      </ol>
      <p v-if="types.includes('login_item')" class="m-0 text-14px leading-[1.7] text-ink-secondary">
        {{ t('backgroundItems.item.turnOff.openAtLogin') }}
      </p>
      <p class="m-0 text-13px leading-[1.6] text-ink-tertiary">{{ t('backgroundItems.item.turnOff.note') }}</p>
    </section>

    <section
      class="flex flex-col gap-12px rounded-big border border-solid border-line-subtle bg-surface-card px-20px py-18px"
      aria-labelledby="background-opennavo"
    >
      <h2 id="background-opennavo" class="m-0 text-17px font-600 text-ink-primary">
        {{ t('backgroundItems.item.openNavo.title') }}
      </h2>
      <p class="m-0 text-14px leading-[1.7] text-ink-secondary">{{ t('backgroundItems.item.openNavo.body') }}</p>
      <OpenInOpenNavo
        v-if="startup"
        :href="startup"
        :label="t('backgroundItems.item.openNavo.button')"
        :hint="t('backgroundItems.item.openNavo.hint')"
      />
      <NuxtLink
        :to="localePath('/security')"
        class="self-start text-13px text-ink-secondary underline-offset-3 hover:text-ink-primary hover:underline"
        >{{ t('backgroundItems.item.openNavo.more') }}</NuxtLink
      >
    </section>
  </article>
</template>
