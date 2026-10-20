<script setup lang="ts">
import { BRAND } from '@opennavo/shared';
import { OnButton, OnHighlightText, OnIcon } from '@opennavo/ui';
import { findCategoryPath } from '~/utils/categories';

// Security (store/security plan §6): the antivirus page in the landing shell. Hero with the Virus Scan demo, the six
// checks, scan modes, startup items, the system check-up, optional extras, privacy, questions and download. Every
// claim follows the desktop app's own copy, including what it cannot do yet (real-time blocking needs Apple's
// Endpoint Security entitlement). Google no longer shows FAQ rich results, so there is no FAQPage markup.
definePageMeta({ layout: 'landing' });

const { t, locale } = useI18n();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');
const formattingLocale = computed(() => toAppLocale(locale.value));

// Real catalog apps give the startup items demo real icons; names alone if the homepage request fails.
const { data: home } = await useHome();
const apps = computed(() =>
  withSampleApps(pickShowcaseApps([home.value?.popularApps, home.value?.recentlyUpdated], 6).map(toLandingApp), 3)
);

// C5: the security apps category leads the directory links, when the catalog has one.
const securityCategory = computed(() => {
  const node = findCategoryPath(home.value?.categories ?? [], 'security').at(-1);
  return node
    ? {
        key: 'security',
        label: t('browse.directory.category', { name: node.name }),
        href: localePath(`/categories/${node.slug}`),
        icon: 'shield-check'
      }
    : null;
});

// The heading's two lines read as one sentence, as on the home page (headingLead).
const titleLead = computed(() => headingLead(t('security.hero.title')));

const MODES = [
  { key: 'quick', icon: 'zap' },
  { key: 'full', icon: 'hard-drive' },
  { key: 'custom', icon: 'folder' },
  { key: 'schedule', icon: 'calendar' }
] as const;

const CATEGORIES = [
  'launch_items',
  'btm',
  'login_items',
  'cron',
  'browser_extensions',
  'kexts',
  'system_extensions',
  'app_extensions',
  'auth_plugins',
  'dir_services_plugins',
  'spotlight',
  'quicklook',
  'dock_tiles',
  'library_inserts',
  'library_proxies',
  'login_hooks',
  'periodic',
  'startup_scripts',
  'shell_config',
  'event_rules'
] as const;

const PRIVACY = [
  { key: 'local', icon: 'hard-drive' },
  { key: 'database', icon: 'shield-check' },
  { key: 'hashes', icon: 'lock' }
] as const;

const FAQ = ['need', 'database', 'performance', 'upload', 'realtime', 'coverage', 'price'] as const;

const root = ref<HTMLElement>();
useRevealOnScroll(root);

usePageSeo({ title: () => t('security.metaTitle'), description: () => t('security.description') });

defineOgImage('OgDefaultSatori', {
  locale: formattingLocale.value,
  variant: 'home',
  title: t('og.securityTitle'),
  description: t('og.securityDescription'),
  ...useOgFrame()
});
</script>

<template>
  <div ref="root" class="landing flex flex-col gap-112px pb-56px md:gap-144px">
    <section class="relative isolate overflow-hidden">
      <div class="security-hero-glow pointer-events-none absolute left-1/2 rounded-full" aria-hidden="true"></div>
      <div
        class="relative mx-auto box-border flex max-w-1160px flex-col items-center px-24px pt-64px text-center md:pt-96px"
      >
        <span
          class="inline-flex h-30px items-center gap-6px rounded-full bg-status-success-subtle px-12px text-13px font-600 text-status-success"
        >
          <OnIcon name="shield-check" :size="15" />{{ t('security.hero.kicker') }}
        </span>
        <h1 class="security-title m-0 mt-24px text-display text-ink-primary md:mt-28px md:text-section lg:text-hero">
          <span class="block">{{ titleLead }}</span>
          <span class="block text-ink-tertiary">{{ t('security.hero.titleAccent') }}</span>
        </h1>
        <OnHighlightText
          as="p"
          :text="t('security.hero.lede')"
          class="m-0 mt-24px max-w-720px text-16px leading-[1.7] text-ink-secondary md:mt-28px md:text-19px"
        />
        <div class="mt-32px flex flex-wrap justify-center gap-12px">
          <OnButton
            variant="primary"
            size="lg"
            shape="round"
            icon="download"
            :href="localePath('/download')"
            :link-as="NuxtLink"
          >
            {{ t('landing.hero.download') }}
          </OnButton>
          <OnButton
            variant="secondary"
            size="lg"
            shape="round"
            :href="BRAND.repository"
            target="_blank"
            rel="noopener noreferrer"
          >
            {{ t('security.hero.source') }}
            <OnIcon name="arrow-up-right" :size="15" />
          </OnButton>
        </div>
        <p class="m-0 mt-16px text-caption text-ink-tertiary">{{ t('landing.hero.meta') }}</p>
      </div>
      <div class="relative mx-auto mt-56px box-border max-w-1168px px-24px">
        <LandingSecurityDemo />
      </div>
    </section>

    <section class="mx-auto box-border w-full max-w-1240px px-24px">
      <div data-reveal>
        <LandingSectionHead
          align="center"
          :kicker="t('security.layers.kicker')"
          :title="t('security.layers.title')"
          :body="t('security.layers.body')"
        />
      </div>
      <LandingSecurityPoints class="mt-40px" />
    </section>

    <section
      class="mx-auto box-border grid w-full max-w-1240px items-start gap-40px px-24px lg:grid-cols-[minmax(0,5fr)_minmax(0,7fr)] lg:gap-64px"
    >
      <div data-reveal>
        <LandingSectionHead
          :kicker="t('security.scan.kicker')"
          :title="t('security.scan.title')"
          :body="t('security.scan.body')"
        />
      </div>
      <ul class="m-0 grid list-none gap-12px p-0 sm:grid-cols-2">
        <li
          v-for="(mode, index) in MODES"
          :key="mode.key"
          class="flex flex-col gap-8px rounded-big border border-solid border-line-subtle bg-surface-card px-20px pb-20px pt-18px"
          data-reveal
          :style="{ '--landing-delay': `${(index % 2) * 90}ms` }"
        >
          <span class="flex items-center justify-between gap-12px">
            <span
              class="grid h-36px w-36px place-items-center rounded-default bg-accent-salmon-subtle text-brand-salmon"
              aria-hidden="true"
            >
              <OnIcon :name="mode.icon" :size="18" />
            </span>
            <span class="text-12px text-ink-tertiary">{{ t(`security.scan.modes.${mode.key}.meta`) }}</span>
          </span>
          <h3 class="m-0 mt-4px text-15px font-600 text-ink-primary">
            {{ t(`security.scan.modes.${mode.key}.title`) }}
          </h3>
          <p class="m-0 text-13.5px leading-[1.65] text-ink-secondary">
            {{ t(`security.scan.modes.${mode.key}.body`) }}
          </p>
        </li>
      </ul>
    </section>

    <div class="flex flex-col gap-40px">
      <LandingFeature
        reverse
        :kicker="t('security.startup.kicker')"
        :title="t('security.startup.title')"
        :body="t('security.startup.body')"
        :points="[
          t('security.startup.points.trust'),
          t('security.startup.points.alerts'),
          t('security.startup.points.restore')
        ]"
      >
        <SecurityStartupDemo :apps="apps" />
      </LandingFeature>
      <section class="mx-auto box-border w-full max-w-1240px px-24px" data-reveal>
        <h3 class="m-0 text-13px font-600 text-ink-tertiary">{{ t('security.startup.categoriesTitle') }}</h3>
        <ul class="m-0 mt-14px flex list-none flex-wrap gap-8px p-0">
          <li
            v-for="category in CATEGORIES"
            :key="category"
            class="inline-flex h-30px items-center rounded-full border border-solid border-line-subtle bg-surface-card px-12px text-13px text-ink-secondary"
          >
            {{ t(`security.startup.categories.${category}`) }}
          </li>
        </ul>
      </section>
    </div>

    <LandingFeature
      :kicker="t('security.posture.kicker')"
      :title="t('security.posture.title')"
      :body="t('security.posture.body')"
    >
      <SecurityPostureDemo />
    </LandingFeature>

    <section class="mx-auto box-border w-full max-w-1240px px-24px">
      <div data-reveal>
        <LandingSectionHead align="center" :kicker="t('security.extras.kicker')" :title="t('security.extras.title')" />
      </div>
      <div class="mt-40px grid gap-12px md:grid-cols-2">
        <LandingMoreCard :title="t('security.extras.paste.title')" :body="t('security.extras.paste.body')" data-reveal>
          <SecurityPasteDemo />
        </LandingMoreCard>
        <LandingMoreCard
          :title="t('security.extras.virusTotal.title')"
          :body="t('security.extras.virusTotal.body')"
          data-reveal
          style="--landing-delay: 90ms"
        >
          <SecurityVirusTotalDemo />
        </LandingMoreCard>
      </div>
    </section>

    <section class="mx-auto box-border w-full max-w-1240px px-24px" data-reveal>
      <h2 class="m-0 text-center text-title1 text-ink-primary md:text-section">{{ t('security.privacy.title') }}</h2>
      <ul class="m-0 mt-40px grid list-none gap-12px p-0 md:grid-cols-3">
        <li
          v-for="point in PRIVACY"
          :key="point.key"
          class="flex flex-col gap-10px rounded-big border border-solid border-line-subtle bg-surface-card px-20px pb-20px pt-18px"
        >
          <span
            class="grid h-36px w-36px place-items-center rounded-default bg-status-success-subtle text-status-success"
            aria-hidden="true"
          >
            <OnIcon :name="point.icon" :size="18" />
          </span>
          <h3 class="m-0 text-15px font-600 text-ink-primary">{{ t(`security.privacy.points.${point.key}.title`) }}</h3>
          <p class="m-0 text-13.5px leading-[1.65] text-ink-secondary">
            {{ t(`security.privacy.points.${point.key}.body`) }}
          </p>
        </li>
      </ul>
    </section>

    <section class="mx-auto box-border w-full max-w-860px px-24px" aria-labelledby="security-faq-title" data-reveal>
      <h2 id="security-faq-title" class="m-0 text-center text-title1 text-ink-primary md:text-section">
        {{ t('security.faq.title') }}
      </h2>
      <div class="mt-32px rounded-big border border-solid border-line-subtle bg-surface-card px-20px py-6px">
        <details
          v-for="(item, index) in FAQ"
          :key="item"
          class="security-faq border-t border-t-solid border-line-subtle py-14px first-of-type:border-t-0"
          :open="index === 0"
        >
          <summary
            class="flex cursor-pointer list-none items-center justify-between gap-16px rounded-tiny outline-none focus-visible:shadow-focus-ring"
          >
            <h3 class="m-0 text-15px font-600 text-ink-primary">{{ t(`security.faq.items.${item}.q`) }}</h3>
            <OnIcon name="chevron-down" :size="16" class="security-faq-chevron shrink-0 text-ink-tertiary" />
          </summary>
          <p class="m-0 mt-10px text-14px leading-[1.7] text-ink-secondary">{{ t(`security.faq.items.${item}.a`) }}</p>
        </details>
      </div>
    </section>

    <!-- F8: the topic pages and the background items encyclopedia (F1), each one click from here. -->
    <SecurityTopicLinks :title="t('securityTopics.linksTitle')" />

    <!-- C5: entry points into the catalog's directories, led by the security apps category. -->
    <DirectoryLinks :lead="securityCategory" />

    <LandingCta :title="t('security.cta.title')" :body="t('security.cta.body')" />
  </div>
</template>

<style>
.security-hero-glow {
  top: 80px;
  width: 1100px;
  height: 1100px;
  margin-left: -550px;
  background: radial-gradient(
    closest-side,
    color-mix(in srgb, var(--on-status-success) 16%, transparent),
    color-mix(in srgb, var(--on-brand-violet) 12%, transparent) 55%,
    transparent
  );
}

.security-title span {
  text-wrap: balance;
}

.security-faq summary::-webkit-details-marker {
  display: none;
}

.security-faq[open] .security-faq-chevron {
  rotate: 180deg;
}

.security-faq-chevron {
  transition: rotate var(--on-motion-duration-base) var(--on-motion-easing-standard);
}
</style>
