<script setup lang="ts">
import { OnButton, OnIcon } from '@opennavo/ui';

// Home security section (store/security plan §6): the antivirus leads the page right after the hero, with its own
// green glow, the Virus Scan demo, the six checks, a link to /security and one line on what it cannot do.
const { t } = useI18n();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');
</script>

<template>
  <section class="relative isolate overflow-x-clip">
    <div class="landing-security-glow pointer-events-none absolute left-1/2 rounded-full" aria-hidden="true"></div>
    <div class="relative mx-auto box-border max-w-1240px px-24px">
      <div data-reveal>
        <LandingSectionHead
          align="center"
          :kicker="t('landing.security.kicker')"
          :title="t('landing.security.title')"
          :body="t('landing.security.body')"
        />
      </div>
      <!-- The scan is staged with made-up files; the caption says so where everyone can read it. -->
      <figure class="m-0 mt-48px" data-reveal style="--landing-delay: 80ms">
        <LandingDemoViewport style="aspect-ratio: 1120 / 580">
          <LazyLandingSecurityDemo />
        </LandingDemoViewport>
        <figcaption class="mt-12px text-center text-12.5px leading-[1.6] text-ink-tertiary">
          {{ t('landing.security.demo.caption') }}
        </figcaption>
      </figure>
      <LandingSecurityPoints class="mt-48px" />
      <div class="mt-36px flex flex-col items-center gap-16px text-center" data-reveal>
        <OnButton variant="secondary" shape="round" :href="localePath('/security')" :link-as="NuxtLink">
          {{ t('landing.security.more') }}
          <OnIcon name="arrow-right" :size="15" />
        </OnButton>
        <p class="m-0 max-w-720px text-12.5px leading-[1.6] text-ink-tertiary">{{ t('landing.security.note') }}</p>
      </div>
    </div>
  </section>
</template>

<style>
/* A green glow behind the scan demo marks the security section apart from the violet store sections. */
.landing-security-glow {
  top: 140px;
  width: 1280px;
  height: 900px;
  margin-left: -640px;
  background: radial-gradient(
    closest-side,
    color-mix(in srgb, var(--on-status-success) 14%, transparent),
    color-mix(in srgb, var(--on-brand-violet) 10%, transparent) 55%,
    transparent
  );
}
</style>
