<script setup lang="ts">
import { ApiError, unwrap } from '@opennavo/api';
import { APP_LOCALES, LOCALE_METADATA, formatBytes, formatDate, formatVersion } from '@opennavo/shared';
import { OnButton, OnChip, OnIcon, OnInstallCommand, OnKeyValueList, useUiMessages } from '@opennavo/ui';
import { trackEvent } from '~/utils/analytics';
import { decorative } from '~/utils/decorative';
import { DESKTOP_BREW_COMMAND } from '~/utils/download';
import { BRAND_FACTS } from '~~/shared/utils/brandFacts';

// Download (05 §7.4, 08 §10.14): client icon on glow/orbits, Universal DMG, six feature cards,
// install steps beside release details, latest notes.
const { t, locale } = useI18n();
const api = useApi();
const toasts = useToasts();
const localePath = useLocalePath();
const messages = useUiMessages();
const NuxtLink = resolveComponent('NuxtLink');
const appLocale = computed(() => toAppLocale(locale.value));

// Orbit icons come from the homepage data. They are decorative (S7): a failed homepage request only
// leaves the client icon, and the page keeps its 200.
const [{ data: release, error }, { data: home }] = await Promise.all([
  useAsyncData(
    () => `desktop-release:${locale.value}`,
    () =>
      unwrap(api.GET('/desktop/releases/latest', { params: { query: { channel: 'stable' } } })).catch(
        (reason: unknown) => {
          // API 404 before the first release means Coming soon, not an error.
          if (reason instanceof ApiError && (reason.isNotFound || reason.status === 404)) return null;
          throw reason;
        }
      )
  ),
  useAsyncData(
    () => `download:home:${locale.value}`,
    () => decorative(unwrap(api.GET('/home')), null, 'download orbit icons')
  )
]);

const dmg = computed(() => release.value?.downloads.find(item => item.target === 'dmg-universal'));

const orbitApps = computed(() =>
  pickShowcaseApps([home.value?.popularApps, home.value?.recentlyUpdated], 6).filter(pkg => pkg.iconUrl)
);

const changelog = computed(() => {
  const value = release.value;
  if (!value) return [];
  const recent = value.recentReleases?.length
    ? value.recentReleases
    : [{ version: value.version, pubDate: value.pubDate, notes: value.notes }];
  return recent.filter(entry => entry.notes).slice(0, 5);
});

const FEATURE_ICONS = {
  install: 'download',
  updates: 'circle-arrow-down',
  security: 'shield-check',
  uninstall: 'trash',
  cleanup: 'eraser',
  monitor: 'activity'
} as const;

const features = computed(() =>
  (Object.keys(FEATURE_ICONS) as (keyof typeof FEATURE_ICONS)[]).map(key => ({
    key,
    icon: FEATURE_ICONS[key],
    title: t(`download.features.${key}.title`),
    body: t(`download.features.${key}.body`)
  }))
);

const steps = computed(() =>
  (['open', 'drag', 'setup'] as const).map(key => ({
    key,
    title: t(`download.steps.${key}.title`),
    body: t(`download.steps.${key}.body`)
  }))
);

// Only the Universal DMG is offered, so one build covers both chip families.
const details = computed(() => {
  const value = release.value;
  const file = dmg.value;
  if (!value || !file) return [];
  return [
    { key: 'version', label: t('download.details.version'), value: formatVersion(value.version) },
    {
      key: 'released',
      label: t('download.details.released'),
      value: formatDate(value.pubDate, { locale: appLocale.value })
    },
    {
      key: 'size',
      label: t('download.details.size'),
      value: formatBytes(file.bytes, { locale: appLocale.value })
    },
    {
      key: 'requires',
      label: t('download.details.requires'),
      value: t('download.details.requiresValue', { macos: value.minMacos })
    },
    { key: 'chip', label: t('download.details.chip'), value: t('download.details.universal') }
  ];
});

// Hero summary wraps only between items, keeping values such as "macOS 13.0" on one line.
const metaParts = computed(() => {
  const value = release.value;
  const file = dmg.value;
  if (!value || !file) return [];
  return t('download.meta', {
    version: formatVersion(value.version),
    size: formatBytes(file.bytes, { locale: appLocale.value }),
    macos: value.minMacos,
    date: formatDate(value.pubDate, { locale: appLocale.value })
  }).split(' · ');
});

// Keep both ends of the checksum readable; the title and copy action carry the full value.
const shortSha = computed(() => {
  const sha = dmg.value?.sha256 ?? '';
  return sha.length > 20 ? `${sha.slice(0, 10)}…${sha.slice(-10)}` : sha;
});

async function copySha(sha: string) {
  try {
    await navigator.clipboard.writeText(sha);
    toasts.push({ tone: 'success', title: t('download.shaCopied') });
  } catch {
    toasts.push({ tone: 'warning', title: t('toast.copyFailed'), description: sha, duration: 0 });
  }
}

// Copying the client's brew command is a download too.
function onCopied(command: string) {
  toasts.push({ tone: 'success', title: t('toast.copied'), description: command });
  trackEvent('download_client');
}

usePageSeo({ title: () => t('download.metaTitle'), description: () => t('download.description') });

// Share image (WP13): OpenNavo itself, with the brand icon.
defineOgImage('OgDefaultSatori', {
  locale: appLocale.value,
  variant: 'home',
  title: t('og.downloadTitle'),
  description: t('og.downloadDescription'),
  ...useOgFrame()
});

// WP10: OpenNavo's own app, kept apart from catalog apps by its own @id and published by the site
// organization. No offer: the resolver would add a validity date nobody stated. G3: the description,
// license and languages are the brand facts the homepage, README and /llms.txt state too.
type SoftwareAppInput = NonNullable<Parameters<typeof defineSoftwareApp>[0]>;
const siteUrl = useRuntimeConfig().public.siteUrl.replace(/\/$/, '');
const settings = useSiteSettings();
// T4: the download page is about the desktop app, whose node has a stable @id across languages.
const canonical = useCanonicalUrl();
usePageSchema(() => ({ mainEntity: release.value && dmg.value ? `${siteUrl}/#desktop-app` : undefined }));
useSchemaOrg(
  computed(() => {
    const value = release.value;
    if (!value || !dmg.value) return [];
    return [
      defineSoftwareApp({
        '@id': `${siteUrl}/#desktop-app`,
        mainEntityOfPage: { '@id': `${canonical.value}#webpage` },
        name: settings.value?.identity.name || 'OpenNavo',
        operatingSystem: 'macOS',
        applicationCategory: 'UtilitiesApplication',
        softwareVersion: value.version,
        ...(value.minMacos ? { softwareRequirements: `macOS >= ${value.minMacos}` } : {}),
        description: t(BRAND_FACTS.definitionKey),
        license: `https://spdx.org/licenses/${BRAND_FACTS.license}.html`,
        isAccessibleForFree: true,
        inLanguage: APP_LOCALES.map(code => LOCALE_METADATA[code].hreflang),
        downloadUrl: dmg.value.url,
        url: `${siteUrl}${localePath('/download')}`,
        image: `${siteUrl}/icon-512.png`,
        publisher: { '@id': `${siteUrl}/#identity` }
      } as SoftwareAppInput)
    ];
  })
);
</script>

<template>
  <div class="flex flex-col gap-40px md:gap-48px">
    <!-- Bleed through main padding so the hero spans the content column. -->
    <DownloadHero :apps="orbitApps" class="-mx-16px -mt-6px md:-mx-28px">
      <h1 class="download-balance m-0 mt-28px text-display text-ink-primary md:mt-32px md:text-section">
        {{ t('download.title') }}
      </h1>
      <p class="download-balance m-0 mt-16px max-w-600px text-15px leading-[1.7] text-ink-secondary md:text-16px">
        {{ t('download.description') }}
      </p>
      <div v-if="release && dmg" class="mt-32px flex flex-col items-center gap-14px">
        <OnButton
          variant="primary"
          size="lg"
          shape="round"
          icon="download"
          :href="dmg.url"
          @click="trackEvent('download_client')"
        >
          {{ t('download.button') }}
        </OnButton>
        <p class="download-balance m-0 text-12.5px text-ink-tertiary tabular-nums">
          <template v-for="(part, index) in metaParts" :key="index">
            <template v-if="index"> · </template>
            <span class="whitespace-nowrap">{{ part }}</span>
          </template>
        </p>
      </div>
      <div v-else-if="!error" class="mt-28px flex flex-col items-center gap-12px">
        <span
          class="box-border inline-flex h-30px items-center gap-8px rounded-full border border-solid border-line-default bg-surface-card px-14px text-13px font-500 text-ink-primary"
        >
          <OnIcon name="clock" :size="14" class="text-brand-salmon" />
          {{ t('download.comingTitle') }}
        </span>
        <p class="download-balance m-0 max-w-480px text-13px leading-[1.6] text-ink-secondary">
          {{ t('download.comingDescription') }}
        </p>
        <OnButton variant="secondary" shape="round" class="mt-4px" :href="localePath('/discover')" :link-as="NuxtLink">
          {{ t('landing.hero.browse') }}
          <OnIcon name="arrow-right" :size="15" />
        </OnButton>
      </div>
    </DownloadHero>

    <div class="mx-auto box-border flex w-full max-w-1000px flex-col gap-48px md:gap-56px">
      <ul class="m-0 grid list-none grid-cols-1 gap-12px p-0 md:grid-cols-3">
        <li
          v-for="feature in features"
          :key="feature.key"
          class="rounded-big border border-solid border-line-subtle bg-surface-card px-20px pb-20px pt-18px"
        >
          <span
            class="flex h-36px w-36px items-center justify-center rounded-default bg-accent-salmon-subtle text-brand-salmon"
          >
            <OnIcon :name="feature.icon" :size="18" />
          </span>
          <h2 class="m-0 mt-14px text-15px font-600 text-ink-primary">{{ feature.title }}</h2>
          <p class="m-0 mt-6px text-13px leading-[1.65] text-ink-secondary">{{ feature.body }}</p>
        </li>
      </ul>

      <div
        v-if="release && dmg"
        class="grid grid-cols-1 items-start gap-12px lg:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)]"
      >
        <section class="rounded-big border border-solid border-line-subtle bg-surface-card px-20px py-20px md:px-24px">
          <h2 class="m-0 text-headline text-ink-primary">{{ t('download.steps.title') }}</h2>
          <ol class="download-steps m-0 mt-18px flex list-none flex-col gap-18px p-0">
            <li v-for="(step, index) in steps" :key="step.key" class="relative flex gap-14px">
              <span
                class="box-border flex h-28px w-28px shrink-0 items-center justify-center rounded-full border border-solid border-line-default bg-surface-raised text-12.5px font-600 text-ink-primary tabular-nums"
                aria-hidden="true"
              >
                {{ index + 1 }}
              </span>
              <div class="min-w-0 pt-4px">
                <h3 class="m-0 text-14px font-600 text-ink-primary">{{ step.title }}</h3>
                <p class="m-0 mt-4px text-13px leading-[1.6] text-ink-secondary">{{ step.body }}</p>
              </div>
            </li>
          </ol>
          <div class="mt-22px border-t border-t-solid border-line-subtle pt-18px">
            <OnInstallCommand
              :title="t('download.brewTitle')"
              :command="DESKTOP_BREW_COMMAND"
              :wrap-at="80"
              @copied="onCopied"
            />
          </div>
        </section>

        <section
          class="rounded-big border border-solid border-line-subtle bg-surface-card px-20px pb-10px pt-20px md:px-24px"
        >
          <h2 class="m-0 text-headline text-ink-primary">{{ t('download.details.title') }}</h2>
          <OnKeyValueList class="mt-8px" :items="details" />
          <div
            class="flex min-w-0 items-center justify-between gap-16px border-t border-t-solid border-line-subtle py-9px"
          >
            <span class="shrink-0 text-12.5px text-ink-tertiary">SHA-256</span>
            <button
              type="button"
              class="m-0 inline-flex min-w-0 items-center gap-6px rounded-tiny border-none bg-transparent p-0 font-mono text-12px text-ink-primary outline-none transition-colors duration-fast hover:text-ink-secondary focus-visible:shadow-focus-ring"
              :title="dmg.sha256"
              :aria-label="t('download.copySha')"
              @click="copySha(dmg.sha256)"
            >
              <span class="truncate">{{ shortSha }}</span>
              <OnIcon name="copy" :size="13" class="text-ink-tertiary" />
            </button>
          </div>
        </section>
      </div>

      <!-- Changelog: latest five versions, or just latest when older backends provide only one. -->
      <section v-if="release && changelog.length" class="flex flex-col gap-16px">
        <h2 class="m-0 text-title2 text-ink-primary">{{ t('download.changelog') }}</h2>
        <ol class="download-timeline relative m-0 flex list-none flex-col gap-12px p-0 pl-32px">
          <li v-for="entry in changelog" :key="entry.version" class="relative">
            <span
              class="download-timeline-dot"
              :class="entry.version === release.version ? 'download-timeline-dot--latest' : ''"
              aria-hidden="true"
            ></span>
            <article
              class="rounded-big border border-solid px-20px py-16px"
              :class="
                entry.version === release.version ? 'download-release--latest' : 'border-line-subtle bg-surface-card'
              "
            >
              <header class="flex flex-wrap items-center gap-x-10px gap-y-4px">
                <h3 class="m-0 text-15px font-600 text-ink-primary">{{ formatVersion(entry.version) }}</h3>
                <OnChip v-if="entry.version === release.version" tone="accent">{{ messages.version.latest }}</OnChip>
                <time class="text-12.5px text-ink-tertiary" :datetime="entry.pubDate">
                  {{ formatDate(entry.pubDate, { locale: appLocale }) }}
                </time>
              </header>
              <MarkdownContent class="mt-8px" :heading-level="4" :source="entry.notes" />
            </article>
          </li>
        </ol>
      </section>
    </div>
  </div>
</template>

<style>
/* Even line lengths keep CJK copy from leaving a lone final character. */
.download-balance {
  text-wrap: balance;
}

/* Chinese copy wraps at spaces and punctuation rather than inside words, breaking anywhere only if a phrase cannot fit. */
:lang(zh) .download-balance {
  word-break: keep-all;
  overflow-wrap: anywhere;
}

/* Connector from each step number down to the next. */
.download-steps > li:not(:last-child)::before {
  content: '';
  position: absolute;
  top: 34px;
  bottom: -12px;
  left: 13.5px;
  width: 1px;
  background: var(--on-border-default);
}

/* Timeline matches the app version history (OnVersionTimeline) on the page surface. */
.download-timeline::before {
  content: '';
  position: absolute;
  top: 12px;
  bottom: 0;
  left: 11px;
  width: 1px;
  background: linear-gradient(180deg, var(--on-component-timeline-line), var(--on-surface-raised) 70%, transparent);
}

.download-timeline-dot {
  position: absolute;
  top: 21px;
  left: -27px;
  width: 11px;
  height: 11px;
  border-radius: 50%;
  background-color: var(--on-component-timeline-dot);
  box-shadow: 0 0 0 3px var(--on-surface-base);
}

.download-timeline-dot--latest {
  background-color: var(--on-brand-salmon);
  box-shadow:
    0 0 0 3px var(--on-surface-base),
    0 0 0 7px color-mix(in srgb, var(--on-brand-salmon) 15%, transparent);
}

.download-release--latest {
  border-color: color-mix(in srgb, var(--on-brand-salmon) 22%, transparent);
  background:
    linear-gradient(180deg, color-mix(in srgb, var(--on-brand-salmon) 5%, transparent), transparent 96px),
    var(--on-surface-card);
}
</style>
