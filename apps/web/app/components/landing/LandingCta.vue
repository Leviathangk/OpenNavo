<script setup lang="ts">
import { OnButton, OnIcon, OnInstallCommand } from '@opennavo/ui';
import { DESKTOP_BREW_COMMAND, desktopBrewCaskAvailable } from '~/utils/download';

// Landing conclusion (08 §10.14): glowing client icon, heading/copy, download/browse. Pages such as /security pass
// their own heading and copy.
const props = defineProps<{ title?: string; body?: string }>();

const brewAvailable = desktopBrewCaskAvailable(useRuntimeConfig().public);
const { t } = useI18n();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');
const toasts = useToasts();

function onCopied(command: string) {
  toasts.push({ tone: 'success', title: t('toast.copied'), description: command });
}
</script>

<template>
  <section class="relative isolate overflow-hidden pb-24px pt-48px">
    <div class="landing-cta-glow pointer-events-none absolute left-1/2 rounded-full" aria-hidden="true"></div>
    <div class="relative mx-auto box-border flex max-w-680px flex-col items-center px-24px text-center">
      <div data-reveal>
        <img
          class="landing-cta-icon block"
          src="/icon-512.png"
          width="112"
          height="112"
          alt=""
          loading="lazy"
          decoding="async"
        />
      </div>
      <h2 class="m-0 mt-28px text-title1 text-ink-primary md:text-section" data-reveal style="--landing-delay: 80ms">
        {{ props.title ?? t('landing.cta.title') }}
      </h2>
      <p
        class="m-0 mt-16px max-w-560px text-15.5px leading-[1.7] text-ink-secondary"
        data-reveal
        style="--landing-delay: 140ms"
      >
        {{ props.body ?? t('landing.cta.body') }}
      </p>
      <div class="mt-32px flex flex-wrap justify-center gap-12px" data-reveal style="--landing-delay: 200ms">
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
        <OnButton variant="secondary" size="lg" shape="round" :href="localePath('/discover')" :link-as="NuxtLink">
          {{ t('landing.hero.browse') }}
          <OnIcon name="arrow-right" :size="15" />
        </OnButton>
      </div>
      <div
        v-if="brewAvailable"
        class="mt-40px box-border w-full rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-14px text-left"
        data-reveal
        style="--landing-delay: 260ms"
      >
        <OnInstallCommand
          :title="t('download.brewTitle')"
          :command="DESKTOP_BREW_COMMAND"
          :wrap-at="80"
          @copied="onCopied"
        />
      </div>
    </div>
  </section>
</template>

<style>
.landing-cta-glow {
  top: -40px;
  width: 760px;
  height: 760px;
  margin-left: -380px;
  background: var(--on-effect-cover-glow);
  opacity: 0.8;
  animation: landing-breathe 8s ease-in-out infinite;
}

.landing-cta-icon {
  filter: drop-shadow(var(--on-shadow-float-icon));
  animation: landing-float 6s ease-in-out infinite alternate;
}

@keyframes landing-float {
  from {
    transform: translateY(-6px);
  }
  to {
    transform: translateY(6px);
  }
}
</style>
