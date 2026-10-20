<script setup lang="ts">
import { unwrap } from '@opennavo/api';
import { OnBreadcrumb, OnButton, OnHighlightText, OnIcon } from '@opennavo/ui';
import { decorative } from '~/utils/decorative';
import { packagePath } from '~/utils/packageView';
import { TOPICS } from '~/utils/securityTopics';
import type { SecurityTopic } from '~/utils/securityTopics';
import { brandlessTitle } from '~/utils/seoTitles';

// A Mac security topic page (F8) in the landing shell, like /security: what the protection is, how OpenNavo
// provides or complements it, what it does not do, questions, related catalog apps and the download. Every
// title and lead is written for the searches of its own language (securityTopics.<topic>), not translated
// word for word. No absolute claims; other products are named for facts only. FAQ answers are plain page
// content: Google no longer shows FAQ rich results here, so there is no FAQPage markup.
const props = defineProps<{ topic: SecurityTopic }>();

const { t, locale } = useI18n();
const api = useApi();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');
const config = computed(() => TOPICS[props.topic]);
const key = (path: string) => `securityTopics.${props.topic}.${path}`;
const section = (name: string, path: string) => key(`sections.${name}.${path}`);

const root = ref<HTMLElement>();
useRevealOnScroll(root);

// Related apps link to their catalog pages only when the catalog has them (decorative: never a 503).
const { data: listed } = await useAsyncData(
  () => `security-topic-apps:${props.topic}`,
  () =>
    decorative(
      unwrap(
        api.POST('/packages/lookup', {
          body: { items: config.value.apps.map(app => ({ kind: 'cask' as const, token: app.token })) }
        })
      ).then(items => items.filter(item => item.found).map(item => item.token)),
      [] as string[],
      `security topic apps ${props.topic}`
    )
);
const apps = computed(() =>
  config.value.apps
    .filter(app => listed.value?.includes(app.token))
    .map(app => ({ ...app, path: localePath(packagePath(app.token)) }))
);

const breadcrumb = computed(() => [
  { label: t('nav.security'), href: localePath('/security') },
  { label: t(key('breadcrumb')) }
]);
useBreadcrumbSchema(breadcrumb);

usePageSeo({ title: () => t(key('metaTitle')), description: () => t(key('description')) });
defineOgImage('OgListSatori', {
  locale: toAppLocale(locale.value),
  label: t('securityTopics.label'),
  title: brandlessTitle(t(key('metaTitle'))),
  description: t(key('description')),
  ...useOgFrame()
});
</script>

<template>
  <div ref="root" class="landing flex flex-col gap-96px pb-56px md:gap-120px">
    <section class="relative isolate overflow-hidden">
      <div class="security-topic-glow pointer-events-none absolute left-1/2 rounded-full" aria-hidden="true"></div>
      <div
        class="relative mx-auto box-border flex max-w-1000px flex-col items-center px-24px pt-40px text-center md:pt-64px"
      >
        <OnBreadcrumb :items="breadcrumb" :link-as="NuxtLink" />
        <span
          class="mt-28px inline-flex h-30px items-center gap-6px rounded-full bg-status-success-subtle px-12px text-13px font-600 text-status-success"
        >
          <OnIcon :name="config.icon" :size="15" />{{ t(key('hero.kicker')) }}
        </span>
        <h1 class="security-topic-title m-0 mt-20px text-display text-ink-primary md:text-section">
          {{ t(key('hero.title')) }}
        </h1>
        <OnHighlightText
          as="p"
          :text="t(key('hero.lede'))"
          class="m-0 mt-20px max-w-720px text-16px leading-[1.7] text-ink-secondary md:text-18px"
        />
        <div class="mt-28px flex flex-wrap justify-center gap-12px">
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
          <OnButton variant="secondary" size="lg" shape="round" :href="localePath('/security')" :link-as="NuxtLink">
            {{ t('securityTopics.overview') }}
            <OnIcon name="arrow-right" :size="15" />
          </OnButton>
        </div>
        <p class="m-0 mt-16px text-caption text-ink-tertiary">{{ t('landing.hero.meta') }}</p>
      </div>
    </section>

    <section
      v-for="part in config.sections"
      :key="part.key"
      class="mx-auto box-border w-full max-w-1040px px-24px"
      :aria-labelledby="`topic-${part.key}`"
      data-reveal
    >
      <h2 :id="`topic-${part.key}`" class="security-topic-title m-0 text-title1 text-ink-primary md:text-section">
        {{ t(section(part.key, 'title')) }}
      </h2>
      <OnHighlightText
        v-if="part.body"
        as="p"
        :text="t(section(part.key, 'body'))"
        class="m-0 mt-14px max-w-760px text-15.5px leading-[1.7] text-ink-secondary"
      />
      <ul v-if="part.cards" class="m-0 mt-28px grid list-none gap-12px p-0 sm:grid-cols-2">
        <li
          v-for="card in part.cards"
          :key="card"
          class="flex flex-col gap-8px rounded-big border border-solid border-line-subtle bg-surface-card px-20px pb-20px pt-18px"
        >
          <h3 class="m-0 text-15px font-600 text-ink-primary">{{ t(section(part.key, `cards.${card}.title`)) }}</h3>
          <p class="m-0 text-13.5px leading-[1.65] text-ink-secondary">
            {{ t(section(part.key, `cards.${card}.body`)) }}
          </p>
        </li>
      </ul>
      <ul v-if="part.items" class="m-0 mt-24px flex max-w-820px list-none flex-col gap-12px p-0">
        <li
          v-for="item in part.items"
          :key="item"
          class="flex items-start gap-10px text-15px leading-[1.7] text-ink-secondary"
        >
          <OnIcon name="info" :size="17" class="mt-4px shrink-0 text-ink-tertiary" />
          <span>{{ t(section(part.key, `items.${item}`)) }}</span>
        </li>
      </ul>
      <ol
        v-if="part.steps"
        class="m-0 mt-24px flex max-w-820px flex-col gap-12px pl-22px text-15px leading-[1.7] text-ink-secondary"
      >
        <li v-for="step in part.steps" :key="step">{{ t(section(part.key, `steps.${step}`)) }}</li>
      </ol>
      <p v-if="part.note" class="m-0 mt-18px max-w-760px text-13.5px leading-[1.7] text-ink-tertiary">
        {{ t(section(part.key, 'note')) }}
      </p>
    </section>

    <section
      v-if="apps.length"
      class="mx-auto box-border w-full max-w-1040px px-24px"
      aria-labelledby="topic-apps"
      data-reveal
    >
      <h2 id="topic-apps" class="m-0 text-title2 text-ink-primary">{{ t('securityTopics.apps.title') }}</h2>
      <p class="m-0 mt-10px max-w-760px text-14px leading-[1.7] text-ink-secondary">
        {{ t('securityTopics.apps.body') }}
      </p>
      <ul class="m-0 mt-16px flex list-none flex-wrap gap-8px p-0">
        <li v-for="app in apps" :key="app.token">
          <NuxtLink
            :to="app.path"
            class="inline-flex h-32px items-center gap-6px rounded-full border border-solid border-line-subtle bg-surface-card px-14px text-13.5px text-ink-primary no-underline hover:border-line-strong"
            >{{ app.name }}<OnIcon name="arrow-right" :size="13" class="text-ink-tertiary"
          /></NuxtLink>
        </li>
      </ul>
    </section>

    <section class="mx-auto box-border w-full max-w-860px px-24px" aria-labelledby="topic-faq" data-reveal>
      <h2 id="topic-faq" class="m-0 text-center text-title1 text-ink-primary md:text-section">
        {{ t('securityTopics.faqTitle') }}
      </h2>
      <div class="mt-32px rounded-big border border-solid border-line-subtle bg-surface-card px-20px py-6px">
        <details
          v-for="(item, index) in config.faq"
          :key="item"
          class="security-topic-faq border-t border-t-solid border-line-subtle py-14px first-of-type:border-t-0"
          :open="index === 0"
        >
          <summary
            class="flex cursor-pointer list-none items-center justify-between gap-16px rounded-tiny outline-none focus-visible:shadow-focus-ring"
          >
            <h3 class="m-0 text-15px font-600 text-ink-primary">{{ t(key(`faq.${item}.q`)) }}</h3>
            <OnIcon name="chevron-down" :size="16" class="security-topic-chevron shrink-0 text-ink-tertiary" />
          </summary>
          <p class="m-0 mt-10px text-14px leading-[1.7] text-ink-secondary">{{ t(key(`faq.${item}.a`)) }}</p>
        </details>
      </div>
    </section>

    <SecurityTopicLinks :title="t('securityTopics.linksTitle')" :current="topic" />

    <LandingCta :title="t(key('cta.title'))" :body="t(key('cta.body'))" />
  </div>
</template>

<style>
.security-topic-glow {
  top: 40px;
  width: 960px;
  height: 960px;
  margin-left: -480px;
  background: radial-gradient(
    closest-side,
    color-mix(in srgb, var(--on-status-success) 14%, transparent),
    color-mix(in srgb, var(--on-brand-violet) 10%, transparent) 55%,
    transparent
  );
}

.security-topic-title {
  text-wrap: balance;
}

:lang(zh) .security-topic-title {
  word-break: keep-all;
  overflow-wrap: anywhere;
}

.security-topic-faq summary::-webkit-details-marker {
  display: none;
}

.security-topic-faq[open] .security-topic-chevron {
  rotate: 180deg;
}

.security-topic-chevron {
  transition: rotate var(--on-motion-duration-base) var(--on-motion-easing-standard);
}
</style>
