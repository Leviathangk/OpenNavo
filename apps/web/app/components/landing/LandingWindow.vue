<script setup lang="ts">
import type { PackageSummary } from '@opennavo/api';
import { formatPercent } from '@opennavo/shared';
import { OnAppCard, OnAppIcon, OnBadge, OnIcon, OnLogo, OnNavItem, useUiMessages } from '@opennavo/ui';
import type { GetState } from '@opennavo/ui';

// Hero desktop demo (08 §10.14): use Discover's 1120×680 layout (mockup 02), real shared components/homepage data,
// then scale to container width, preserving the same scene on mobile. Treat as one image for screen readers; inner content is inert.
// Loop: second card Get → scan the download (the desktop install scan) → progress → Open, with the toolbar task pill.
const props = defineProps<{
  apps: readonly PackageSummary[];
  categories: readonly { slug: string; name: string }[];
}>();

const WIDTH = 1120;
const TARGET = 1;
// Steps: 0 Get (representative frame), 1 scanning the download, 2–6 installing, 7 installed.
const DURATIONS = [2600, 1200, 280, 280, 280, 280, 360, 2800] as const;
const PROGRESS = [0, 0, 8, 34, 61, 86, 100, 100] as const;
const SCANNING = 1;

const { t, locale } = useI18n();
const messages = useUiMessages();
const formattingLocale = computed(() => toAppLocale(locale.value));

const frame = ref<HTMLElement>();
const step = useDemoLoop(frame, DURATIONS);
const cards = computed(() => props.apps.slice(0, 6));
// Shorten the window for one card row to avoid excessive empty space.
const height = computed(() => (cards.value.length > 3 ? 680 : 520));
const featured = computed(() => props.apps[0]);
const target = computed(() => cards.value[TARGET]);
const progress = computed(() => PROGRESS[step.value] ?? 0);
const scanning = computed(() => step.value === SCANNING);
const running = computed(() => step.value > SCANNING && step.value <= 6);
const busy = computed(() => scanning.value || running.value);

function stateOf(index: number): GetState {
  if (index !== TARGET || step.value === 0) return 'get';
  return busy.value ? 'running' : 'open';
}

const chips = computed(() => [t('home.allCategories'), ...props.categories.slice(0, 5).map(item => item.name)]);

const nav = computed(() => [
  {
    key: 'browse',
    label: t('nav.browse'),
    items: [
      { key: 'discover', label: t('nav.discover'), icon: 'lucide:compass', active: true },
      { key: 'categories', label: t('nav.categories'), icon: 'lucide:layout-grid' },
      { key: 'rankings', label: t('nav.rankings'), icon: 'lucide:chart-no-axes-column' }
    ]
  },
  {
    key: 'mine',
    label: t('landing.window.mine'),
    items: [
      { key: 'installed', label: t('landing.window.installed'), icon: 'lucide:package' },
      {
        key: 'updates',
        label: t('landing.window.updates'),
        icon: 'lucide:circle-arrow-down',
        badge: 3
      },
      { key: 'cleanup', label: t('landing.window.cleanup'), icon: 'lucide:eraser' },
      { key: 'monitor', label: t('landing.window.monitor'), icon: 'lucide:activity' },
      { key: 'security', label: t('landing.window.security'), icon: 'lucide:shield-check' }
    ]
  }
]);

// Progress ring: radius seven, stroke two, matching OnGetButton but reduced for the toolbar pill.
const RING = 2 * Math.PI * 7;
</script>

<template>
  <div
    ref="frame"
    class="landing-window-frame relative mx-auto w-full max-w-1120px"
    :class="cards.length > 3 ? 'landing-window-fade' : ''"
    role="img"
    :aria-label="t('landing.window.label')"
    :style="{ aspectRatio: `${WIDTH} / ${height}` }"
  >
    <svg class="absolute inset-0 h-full w-full" :viewBox="`0 0 ${WIDTH} ${height}`" aria-hidden="true">
      <foreignObject :width="WIDTH" :height="height">
        <div class="landing-window" :style="{ height: `${height}px` }" inert aria-hidden="true">
          <div
            class="grid h-full grid-cols-[224px_minmax(0,1fr)] overflow-hidden rounded-big border border-solid border-line-default bg-surface-base shadow-window"
          >
            <aside class="flex min-h-0 flex-col border-r border-r-solid border-line-sidebar bg-surface-sidebar px-12px">
              <div class="flex h-52px shrink-0 items-center gap-8px pl-6px">
                <span class="h-12px w-12px rounded-full bg-macos-close"></span>
                <span class="h-12px w-12px rounded-full bg-macos-minimize"></span>
                <span class="h-12px w-12px rounded-full bg-macos-zoom"></span>
              </div>
              <div class="px-8px pb-18px pt-4px"><OnLogo wordmark /></div>
              <div class="flex flex-col gap-12px border-t border-t-solid border-line-sidebar pt-14px">
                <div v-for="group in nav" :key="group.key" class="flex flex-col gap-2px">
                  <div class="px-8px pb-6px pt-2px text-label text-ink-tertiary">
                    {{ group.label }}
                  </div>
                  <OnNavItem
                    v-for="item in group.items"
                    :key="item.key"
                    as="span"
                    :icon="item.icon"
                    :active="'active' in item && item.active"
                    :badge="'badge' in item ? item.badge : undefined"
                  >
                    {{ item.label }}
                  </OnNavItem>
                </div>
              </div>
              <div class="mt-auto border-t border-t-solid border-line-sidebar py-10px">
                <OnNavItem as="span" icon="lucide:sliders-horizontal">{{ t('landing.window.settings') }}</OnNavItem>
              </div>
            </aside>

            <div class="flex min-h-0 min-w-0 flex-col">
              <div class="flex h-52px shrink-0 items-center gap-14px pl-18px pr-20px">
                <span class="flex items-center gap-10px text-ink-tertiary">
                  <OnIcon name="chevron-left" :size="16" />
                  <OnIcon name="chevron-right" :size="16" />
                </span>
                <span class="flex-1"></span>
                <span
                  class="box-border flex h-32px w-300px items-center gap-8px rounded-default border border-solid border-line-subtle bg-component-search-bg px-10px text-13px text-ink-tertiary"
                >
                  <OnIcon name="search" :size="15" />
                  <span class="min-w-0 flex-1 truncate">{{ t('nav.search') }}</span>
                  <kbd class="rounded-tiny bg-surface-chip px-6px py-1px font-sans text-11px">⌘K</kbd>
                </span>
                <span
                  class="landing-window-task box-border flex h-32px items-center gap-8px rounded-full border border-solid border-line-subtle bg-component-search-bg pl-8px pr-12px text-12px text-ink-secondary"
                  :class="busy ? 'opacity-100' : 'opacity-0'"
                >
                  <template v-if="scanning">
                    <span class="grid h-18px w-18px place-items-center text-status-success">
                      <OnIcon name="shield-check" :size="15" />
                    </span>
                    <span class="max-w-220px truncate">{{
                      t('landing.window.scanning', { name: target?.displayName })
                    }}</span>
                  </template>
                  <template v-else>
                    <svg viewBox="0 0 18 18" class="h-18px w-18px -rotate-90">
                      <circle cx="9" cy="9" r="7" fill="none" stroke-width="2" class="stroke-accent-coral-subtle" />
                      <circle
                        cx="9"
                        cy="9"
                        r="7"
                        fill="none"
                        stroke-width="2"
                        stroke-linecap="round"
                        class="landing-window-ring stroke-brand-coral"
                        :stroke-dasharray="RING"
                        :stroke-dashoffset="RING * (1 - progress / 100)"
                      />
                    </svg>
                    <span class="font-600 text-ink-primary tabular-nums">{{
                      formatPercent(progress / 100, { locale: formattingLocale })
                    }}</span>
                    <span class="max-w-180px truncate">{{
                      t('landing.window.installing', { name: target?.displayName })
                    }}</span>
                  </template>
                </span>
              </div>

              <div class="flex min-h-0 flex-col gap-20px px-28px pt-6px">
                <section
                  v-if="featured"
                  class="relative box-border h-176px shrink-0 overflow-hidden rounded-huge border border-solid border-line-subtle bg-component-hero-bg"
                >
                  <div class="landing-window-hero-stage absolute rounded-full"></div>
                  <span class="landing-window-hero-icon absolute">
                    <OnAppIcon
                      kind="cask"
                      :token="featured.token"
                      :name="featured.displayName"
                      :src="featured.iconUrl"
                      :variants="featured.iconVariants"
                      :accent="featured.accentColor"
                      :size="96"
                      sizes="(min-width: 1168px) 96px, calc(8.5714vw - 4.1143px)"
                    />
                  </span>
                  <div class="relative box-border flex h-full max-w-480px flex-col justify-center pl-28px">
                    <span
                      ><OnBadge star>{{ t('home.popularApps') }}</OnBadge></span
                    >
                    <span
                      class="mt-12px truncate text-30px font-600 leading-[1.1] tracking-[-0.03em] text-ink-primary"
                      >{{ featured.displayName }}</span
                    >
                    <span v-if="featured.summary" class="mt-6px truncate text-14px text-brand-salmon">{{
                      featured.summary
                    }}</span>
                    <span class="mt-16px flex gap-8px">
                      <span
                        class="inline-flex h-28px items-center gap-6px rounded-full bg-button-primary-bg px-12px text-12.5px font-600 text-button-primary-text"
                      >
                        <OnIcon name="download" :size="14" />
                        {{ messages.getButton.get }}
                      </span>
                      <span
                        class="inline-flex h-28px items-center gap-6px rounded-full bg-button-secondary-bg px-12px text-12.5px font-600 text-button-secondary-text"
                      >
                        {{ messages.hero.viewDetails }}
                        <OnIcon name="arrow-up-right" :size="14" />
                      </span>
                    </span>
                  </div>
                </section>

                <div class="flex shrink-0 gap-8px overflow-hidden">
                  <span
                    v-for="(chip, index) in chips"
                    :key="index"
                    class="box-border inline-flex h-30px shrink-0 items-center whitespace-nowrap rounded-full border border-solid px-14px text-12.5px"
                    :class="
                      index === 0
                        ? 'border-transparent bg-button-primary-bg font-600 text-button-primary-text'
                        : 'border-line-default text-ink-secondary'
                    "
                    >{{ chip }}</span
                  >
                </div>

                <div class="flex shrink-0 items-baseline gap-8px">
                  <span class="text-headline text-ink-primary">{{ t('home.popularApps') }}</span>
                  <span class="text-12px text-ink-tertiary">{{ t('home.popularAppsHint') }}</span>
                  <span class="ml-auto flex items-center gap-4px text-12px text-ink-secondary">
                    {{ t('home.viewRankings') }}
                    <OnIcon name="arrow-right" :size="13" />
                  </span>
                </div>

                <div class="grid grid-cols-3 gap-12px">
                  <OnAppCard
                    v-for="(pkg, index) in cards"
                    :key="pkg.token"
                    :pkg="pkg"
                    :state="stateOf(index)"
                    :progress="index === TARGET && running ? progress : undefined"
                  />
                </div>
              </div>
            </div>
          </div>
        </div>
      </foreignObject>
    </svg>
  </div>
</template>

<style>
/* The SVG viewBox scales the HTML demo exactly from first paint, without hydration measurements. */
.landing-window {
  width: 1120px;
  height: 680px;
}

/* Fade the bottom with two card rows to suggest more content; leave the animated first row unaffected. */
.landing-window-fade {
  mask: linear-gradient(180deg, var(--on-text-primary) 72%, transparent 98%);
}

.landing-window-task {
  transition: opacity var(--on-motion-duration-base) var(--on-motion-easing-standard);
}

.landing-window-ring {
  transition: stroke-dashoffset var(--on-motion-duration-slow) var(--on-motion-easing-standard);
}

/* Header glow/icon matches OnFeatureHero, scaled to a 176-high card. */
.landing-window-hero-stage {
  right: -150px;
  top: -170px;
  width: 520px;
  height: 520px;
  background: var(--on-effect-hero-glow);
  animation: landing-breathe 8s ease-in-out infinite;
}

.landing-window-hero-icon {
  right: 92px;
  top: 40px;
  transform: rotate(-8deg);
  filter: drop-shadow(var(--on-shadow-float-icon));
}
</style>
