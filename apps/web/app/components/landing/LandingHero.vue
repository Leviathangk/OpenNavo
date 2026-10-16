<script setup lang="ts">
import type { PackageSummary } from '@opennavo/api';
import { formatCount } from '@opennavo/shared';
import { OnAppIcon, OnBadge, OnButton, OnHighlightText, OnIcon } from '@opennavo/ui';
import type { OnAppIconSize } from '@opennavo/ui';

// Hero (08 §10.14): mockup glow/concentric dashed circles with slow breathing/rotation, badge, two-line heading, introduction, download/browse,
// then desktop demo. Wide screens show real floating app icons with subtle pointer parallax; text and window geometry remain static from first paint.
const props = defineProps<{
  apps: readonly PackageSummary[];
  count: number | null;
  categories: readonly { slug: string; name: string }[];
}>();

const { t, locale } = useI18n();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');
const formattingLocale = computed(() => toAppLocale(locale.value));

// The heading's two lines read as one sentence. Vue drops whitespace-only text between the block
// spans, so the separating space ends the first line (on screen it vanishes at the line end);
// Chinese and Japanese lines join without one.
const titleLead = computed(() => {
  const title = t('landing.hero.title');
  return /[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\u3000-\u303f\uff00-\uffef]$/u.test(title)
    ? title
    : `${title} `;
});

const lede = computed(() =>
  props.count
    ? t(
        'landing.hero.lede',
        { count: formatCount(props.count, { locale: formattingLocale.value }) },
        { plural: props.count }
      )
    : t('landing.hero.ledeFallback')
);

// Four per side, positioned relative to hero top; depth is parallax pixels, larger means nearer.
const SLOTS: {
  side: 'left' | 'right';
  x: string;
  y: number;
  size: OnAppIconSize;
  rotate: number;
  depth: number;
  duration: number;
  delay: number;
  opacity: number;
}[] = [
  { side: 'left', x: '6%', y: 128, size: 64, rotate: -10, depth: 18, duration: 7, delay: 0, opacity: 1 },
  { side: 'right', x: '6.5%', y: 150, size: 64, rotate: 9, depth: 18, duration: 7.6, delay: -2.4, opacity: 1 },
  { side: 'left', x: '12.5%', y: 300, size: 54, rotate: 8, depth: 30, duration: 8.4, delay: -3, opacity: 0.9 },
  { side: 'right', x: '12.5%', y: 322, size: 54, rotate: -8, depth: 30, duration: 8, delay: -5, opacity: 0.9 },
  { side: 'left', x: '4%', y: 468, size: 46, rotate: -5, depth: 12, duration: 6.6, delay: -1.5, opacity: 0.75 },
  { side: 'right', x: '4.5%', y: 490, size: 46, rotate: 6, depth: 12, duration: 7.2, delay: -4, opacity: 0.75 },
  { side: 'left', x: '13.5%', y: 590, size: 40, rotate: 12, depth: 24, duration: 9, delay: -6, opacity: 0.6 },
  { side: 'right', x: '13.5%', y: 604, size: 40, rotate: -12, depth: 24, duration: 8.8, delay: -2, opacity: 0.6 }
];

const floats = computed(() =>
  props.apps.slice(0, SLOTS.length).map((pkg, index) => {
    const slot = SLOTS[index] as (typeof SLOTS)[number];
    return {
      pkg,
      size: slot.size,
      style: {
        [slot.side]: slot.x,
        top: `${slot.y}px`,
        opacity: String(slot.opacity),
        '--landing-depth': String(slot.depth)
      },
      bob: {
        '--landing-rotate': `${slot.rotate}deg`,
        animationDuration: `${slot.duration}s`,
        animationDelay: `${slot.delay}s`
      }
    };
  })
);

// Listen for parallax only with fine pointers and allowed motion; write CSS variables at most once per frame.
const root = ref<HTMLElement>();
useLandingParallax(root);
</script>

<template>
  <section ref="root" class="landing-hero relative isolate overflow-hidden">
    <div class="pointer-events-none absolute inset-0" aria-hidden="true">
      <div class="landing-hero-glow absolute left-1/2 rounded-full"></div>
      <svg class="landing-hero-rings absolute left-1/2" width="1500" height="1300" viewBox="0 0 1500 1300" fill="none">
        <g class="landing-hero-ring">
          <circle cx="750" cy="1020" r="260" />
          <circle cx="750" cy="1020" r="420" />
          <circle cx="750" cy="1020" r="580" />
          <circle cx="750" cy="1020" r="740" />
        </g>
      </svg>
    </div>

    <!-- Fade covers only glow/circles, beneath floating icons, text, and demo window. -->
    <div class="landing-hero-fade pointer-events-none absolute inset-x-0 bottom-0" aria-hidden="true"></div>

    <div v-if="floats.length" class="pointer-events-none absolute inset-0 hidden xl:block" aria-hidden="true">
      <span v-for="item in floats" :key="item.pkg.token" class="landing-float absolute" :style="item.style">
        <span class="landing-float-bob block" :style="item.bob">
          <OnAppIcon
            kind="cask"
            :token="item.pkg.token"
            :name="item.pkg.displayName"
            :src="item.pkg.iconUrl"
            :accent="item.pkg.accentColor"
            :size="item.size"
          />
        </span>
      </span>
    </div>

    <div
      class="relative mx-auto box-border flex max-w-1160px flex-col items-center px-24px pt-64px text-center md:pt-96px"
    >
      <span>
        <OnBadge star>{{ t('landing.hero.badge') }}</OnBadge>
      </span>
      <h1 class="landing-hero-title m-0 mt-24px text-display text-ink-primary md:mt-28px md:text-section lg:text-hero">
        <span class="block">{{ titleLead }}</span>
        <span class="block text-ink-tertiary">{{ t('landing.hero.titleAccent') }}</span>
      </h1>
      <OnHighlightText
        as="p"
        :text="lede"
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
        <OnButton variant="secondary" size="lg" shape="round" :href="localePath('/discover')" :link-as="NuxtLink">
          {{ t('landing.hero.browse') }}
          <OnIcon name="arrow-right" :size="15" />
        </OnButton>
      </div>
      <p class="m-0 mt-16px text-caption text-ink-tertiary">
        {{ t('landing.hero.meta') }}
      </p>
      <div class="mt-12px flex flex-col items-center gap-0 sm:flex-row sm:gap-24px">
        <a
          href="https://github.com/opennavo/OpenNavo"
          target="_blank"
          rel="noopener noreferrer"
          class="inline-flex min-h-44px items-center gap-10px whitespace-nowrap rounded-tiny text-14px text-ink-primary no-underline outline-none transition-colors duration-fast ease-standard hover:text-ink-secondary focus-visible:shadow-focus-ring active:text-ink-secondary"
        >
          <svg aria-hidden="true" width="20" height="20" viewBox="0 0 24 24" fill="currentColor" class="shrink-0">
            <path
              d="M12 .5C5.37.5 0 5.87 0 12.5c0 5.3 3.438 9.8 8.205 11.385.6.11.82-.26.82-.577v-2.234c-3.338.726-4.043-1.416-4.043-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.09-.745.083-.729.083-.729 1.205.085 1.839 1.237 1.839 1.237 1.07 1.835 2.807 1.305 3.492.998.108-.776.418-1.305.762-1.605-2.665-.305-5.467-1.334-5.467-5.931 0-1.31.469-2.381 1.236-3.221-.124-.303-.536-1.524.117-3.176 0 0 1.008-.322 3.301 1.23A11.52 11.52 0 0 1 12 6.301c1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.655 1.652.243 2.873.12 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .32.216.694.825.576C20.565 22.296 24 17.798 24 12.5c0-6.63-5.373-12-12-12Z"
            />
          </svg>
          <span>{{ t('landing.hero.github') }}</span>
          <OnIcon name="arrow-up-right" :size="14" />
        </a>
        <span aria-hidden="true" class="hidden h-16px w-1px bg-ink-tertiary sm:block"></span>
        <a
          href="https://x.com/opennavo"
          target="_blank"
          rel="noopener noreferrer"
          class="inline-flex min-h-44px items-center gap-10px whitespace-nowrap rounded-tiny text-14px text-ink-secondary no-underline outline-none transition-colors duration-fast ease-standard hover:text-ink-primary focus-visible:shadow-focus-ring active:text-ink-primary"
        >
          <svg aria-hidden="true" width="20" height="20" viewBox="0 0 24 24" fill="currentColor">
            <path
              d="M18.901 1.153h3.68l-8.04 9.19L24 22.846h-7.406l-5.8-7.584-6.64 7.584H.47l8.6-9.835L0 1.154h7.594l5.243 6.932 6.064-6.933Zm-1.29 19.49h2.039L6.487 3.24H4.3l13.31 17.403Z"
            />
          </svg>
          <span>{{ t('landing.hero.followOnX', { account: '@opennavo' }) }}</span>
          <OnIcon name="arrow-up-right" :size="14" />
        </a>
      </div>
    </div>

    <div v-if="apps.length >= 3" class="landing-window-stage relative mx-auto mt-16px box-border max-w-1168px px-24px">
      <div>
        <LandingWindow :apps="apps" :categories="categories" />
      </div>
    </div>
    <div v-else class="h-96px"></div>
  </section>
</template>

<style>
/* Center with negative margins, reserving transform for breathing scale, parallax translation, and rotation around each center. */
.landing-hero-glow {
  top: 180px;
  width: 1100px;
  height: 1100px;
  margin-left: -550px;
  translate: calc(var(--landing-px, 0) * -10px) calc(var(--landing-py, 0) * -10px);
  background: var(--on-effect-cover-glow);
  transition: translate 1.2s var(--on-motion-easing-standard);
  animation: landing-breathe 8s ease-in-out infinite;
}

/* Slowly rotate the whole dashed-circle SVG on the compositor, making dashes flow without per-frame repainting. */
.landing-hero-rings {
  top: -120px;
  margin-left: -750px;
  translate: calc(var(--landing-px, 0) * -6px) calc(var(--landing-py, 0) * -6px);
  transform-origin: 750px 1020px;
  transition: translate 1.2s var(--on-motion-easing-standard);
  animation: landing-spin 240s linear infinite;
}

.landing-hero-ring {
  stroke: var(--on-effect-ring-stroke);
  stroke-dasharray: var(--on-effect-ring-dasharray);
}

.landing-float {
  translate: calc(var(--landing-px, 0) * var(--landing-depth) * -1px)
    calc(var(--landing-py, 0) * var(--landing-depth) * -1px);
  transition: translate 0.9s var(--on-motion-easing-standard);
  filter: drop-shadow(var(--on-shadow-float-icon));
}

.landing-float-bob {
  transform: rotate(var(--landing-rotate));
  animation: landing-bob 8s ease-in-out infinite alternate;
}

.landing-window-stage {
  perspective: 2000px;
}

/* Heading is wider than body for unspaced languages such as Japanese; balance lines to avoid a lone final character. */

.landing-hero-title span {
  text-wrap: balance;
}

.landing-hero-fade {
  height: 220px;
  background: linear-gradient(transparent, var(--on-surface-page));
}

@keyframes landing-bob {
  from {
    transform: translateY(-7px) rotate(var(--landing-rotate));
  }
  to {
    transform: translateY(7px) rotate(calc(var(--landing-rotate) + 3deg));
  }
}

@keyframes landing-spin {
  to {
    rotate: 360deg;
  }
}
</style>
