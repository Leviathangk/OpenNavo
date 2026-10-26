<script setup lang="ts">
// More details grid (08 §10.14): handoff, source probes, Brewfile, Command-K, six languages, privacy.
const props = defineProps<{ apps: readonly LandingApp[]; count: number | null }>();

const { t } = useI18n();
const first = computed(() => props.apps[0] ?? SAMPLE_APPS[0]);
const vscode = computed(() => props.apps.find(app => app.token === 'visual-studio-code') ?? null);

const cards = ['handoff', 'mirrors', 'brewfile', 'search', 'languages', 'privacy'] as const;
const delay = (key: (typeof cards)[number]) => ({ '--landing-delay': `${(cards.indexOf(key) % 3) * 90}ms` });
</script>

<template>
  <section class="mx-auto box-border max-w-1240px px-24px">
    <div data-reveal>
      <LandingSectionHead align="center" :kicker="t('landing.more.kicker')" :title="t('landing.more.title')" />
    </div>
    <div class="mt-40px grid gap-12px md:grid-cols-2 lg:grid-cols-3">
      <LandingMoreCard
        :title="t('landing.more.handoff.title')"
        :body="t('landing.more.handoff.body')"
        data-reveal
        :style="delay('handoff')"
      >
        <LazyLandingHandoffDemo v-if="first" :app="first" />
      </LandingMoreCard>
      <LandingMoreCard
        :title="t('landing.more.mirrors.title')"
        :body="t('landing.more.mirrors.body')"
        data-reveal
        :style="delay('mirrors')"
      >
        <LazyLandingMirrorDemo />
      </LandingMoreCard>
      <LandingMoreCard
        :title="t('landing.more.brewfile.title')"
        :body="t('landing.more.brewfile.body')"
        data-reveal
        :style="delay('brewfile')"
      >
        <LazyLandingBrewfileDemo :apps="apps" />
      </LandingMoreCard>
      <LandingMoreCard
        :title="t('landing.more.search.title')"
        :body="t('landing.more.search.body')"
        data-reveal
        :style="delay('search')"
      >
        <LazyLandingSearchDemo :app="vscode" />
      </LandingMoreCard>
      <LandingMoreCard
        :title="t('landing.more.languages.title')"
        :body="t('landing.more.languages.body')"
        data-reveal
        :style="delay('languages')"
      >
        <LazyLandingLanguageDemo :count="count" />
      </LandingMoreCard>
      <LandingMoreCard
        :title="t('landing.more.privacy.title')"
        :body="t('landing.more.privacy.body')"
        data-reveal
        :style="delay('privacy')"
      >
        <LazyLandingPrivacyDemo />
      </LandingMoreCard>
    </div>
  </section>
</template>
