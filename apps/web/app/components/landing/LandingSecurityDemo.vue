<script setup lang="ts">
import { formatBytes, formatCount } from '@opennavo/shared';
import { OnIcon, OnLogo, OnNavItem, useUiMessages } from '@opennavo/ui';

// Security demo (desktop Security › Virus Scan, store/security plan §6): a quick scan counts files, finds a threat in
// Downloads, quarantines it and finishes. Rebuilt from the desktop layout at a fixed 1120×580 and scaled through the
// SVG viewBox like LandingWindow, so the scene matches from first paint. The finished scan (with the quarantined
// threat) is the representative frame for SSR and reduced motion; screen readers get one label.
const WIDTH = 1120;
const HEIGHT = 580;
// Steps 0–5 scan (2 finds the threat, 4 quarantines it), 6 is the finished scan.
const DURATIONS = [1400, 1000, 1800, 1300, 1700, 1000, 3800] as const;
const DONE = 6;
const FOUND = 2;
const QUARANTINED = 4;
const TOTAL_FILES = 18236;
const PROGRESS = [
  { files: 2184, bytes: 612e6, path: '~/Downloads/meeting-notes.pdf' },
  { files: 6912, bytes: 1.82e9, path: '~/Desktop/Screenshot 2026-10-18 at 09.41.png' },
  { files: 9705, bytes: 2.61e9, path: '~/Downloads/Invoice-1018.pdf.dmg' },
  { files: 13288, bytes: 3.38e9, path: '~/Library/LaunchAgents/com.google.keystone.agent.plist' },
  { files: 15206, bytes: 3.91e9, path: '/private/var/folders/7k/T/TemporaryItems/update.zip' },
  { files: 16940, bytes: 4.27e9, path: '/Applications/Raycast.app/Contents/MacOS/Raycast' }
] as const;
// The engine's name is a product name, shown as-is in every language (desktop security.rail.engineVersion).
const ENGINE = 'ClamAV';
// A disk image dressed up as a document, a common lure; names are made up.
const THREAT = {
  name: 'Invoice-1018.pdf.dmg',
  path: '~/Downloads/Invoice-1018.pdf.dmg',
  signature: 'Osx.Trojan.Generic-9967352-0'
};

const { t, locale } = useI18n();
const messages = useUiMessages();
const formattingLocale = computed(() => toAppLocale(locale.value));

const frame = ref<HTMLElement>();
const step = useDemoLoop(frame, DURATIONS, DONE);
const done = computed(() => step.value === DONE);
const found = computed(() => step.value >= FOUND);
const quarantined = computed(() => step.value >= QUARANTINED);
const progress = computed(() => PROGRESS[Math.min(step.value, PROGRESS.length - 1)] ?? PROGRESS[0]);

const status = computed(() =>
  done.value
    ? t('landing.security.demo.done', { count: formatCount(TOTAL_FILES, { locale: formattingLocale.value }) })
    : t('landing.security.demo.running', {
        count: formatCount(progress.value.files, { locale: formattingLocale.value }),
        size: formatBytes(progress.value.bytes, { locale: formattingLocale.value })
      })
);

const tabs = computed(() =>
  (['overview', 'scan', 'startup', 'protection', 'quarantine'] as const).map(key => ({
    key,
    label: t(`landing.security.demo.tabs.${key}`),
    count: key === 'startup' ? 2 : key === 'quarantine' && quarantined.value ? 1 : undefined
  }))
);

const scans = computed(() =>
  (['quick', 'full', 'custom'] as const).map(key => ({
    key,
    duration: t(`landing.security.demo.${key}.duration`),
    title: t(`landing.security.demo.${key}.title`),
    body: t(`landing.security.demo.${key}.body`),
    action: t(`landing.security.demo.${key}.action`)
  }))
);

const nav = computed(() => [
  {
    key: 'browse',
    label: t('nav.browse'),
    items: [
      { key: 'discover', label: t('nav.discover'), icon: 'lucide:compass' },
      { key: 'categories', label: t('nav.categories'), icon: 'lucide:layout-grid' },
      { key: 'rankings', label: t('nav.rankings'), icon: 'lucide:chart-no-axes-column' }
    ]
  },
  {
    key: 'mine',
    label: t('landing.window.mine'),
    items: [
      { key: 'installed', label: t('landing.window.installed'), icon: 'lucide:package' },
      { key: 'updates', label: t('landing.window.updates'), icon: 'lucide:circle-arrow-down', badge: 3 },
      { key: 'cleanup', label: t('landing.window.cleanup'), icon: 'lucide:eraser' },
      { key: 'monitor', label: t('landing.window.monitor'), icon: 'lucide:activity' },
      { key: 'security', label: t('landing.window.security'), icon: 'lucide:shield-check', active: true }
    ]
  }
]);

const protection = computed(() =>
  (['downloads', 'startupAlerts', 'installScan'] as const).map(key => t(`landing.security.demo.${key}`))
);
</script>

<template>
  <div
    ref="frame"
    class="relative mx-auto w-full max-w-1120px"
    role="img"
    :aria-label="t('landing.security.demo.label')"
    :style="{ aspectRatio: `${WIDTH} / ${HEIGHT}` }"
  >
    <svg class="absolute inset-0 h-full w-full" :viewBox="`0 0 ${WIDTH} ${HEIGHT}`" aria-hidden="true">
      <foreignObject :width="WIDTH" :height="HEIGHT">
        <div class="landing-security-window" inert aria-hidden="true">
          <div
            class="grid h-full grid-cols-[204px_minmax(0,1fr)_236px] overflow-hidden rounded-big border border-solid border-line-default bg-surface-base shadow-window"
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
                  <div class="px-8px pb-6px pt-2px text-label text-ink-tertiary">{{ group.label }}</div>
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

            <div class="relative flex min-h-0 min-w-0 flex-col px-24px">
              <div class="flex h-52px shrink-0 items-center gap-10px text-ink-tertiary">
                <OnIcon name="chevron-left" :size="16" />
                <OnIcon name="chevron-right" :size="16" />
              </div>
              <div class="flex flex-col gap-4px">
                <span class="text-26px font-600 leading-[1.15] tracking-[-0.02em] text-ink-primary">{{
                  t('landing.security.demo.title')
                }}</span>
                <span class="truncate text-13px text-ink-tertiary">{{ t('landing.security.demo.subtitle') }}</span>
              </div>

              <div class="mt-14px flex items-end gap-x-26px border-b border-b-solid border-line-subtle px-4px">
                <span
                  v-for="tab in tabs"
                  :key="tab.key"
                  class="relative inline-flex h-38px items-center whitespace-nowrap text-13.5px font-500"
                  :class="tab.key === 'scan' ? 'text-ink-primary' : 'text-ink-tertiary'"
                >
                  {{ tab.label }}
                  <span v-if="tab.count" class="ml-5px text-11.5px text-ink-tertiary tabular-nums">{{
                    tab.count
                  }}</span>
                  <span
                    v-if="tab.key === 'scan'"
                    class="absolute inset-x-0 -bottom-1px h-2px rounded-full bg-component-tab-indicator"
                  ></span>
                </span>
              </div>

              <div class="mt-16px grid grid-cols-3 gap-10px">
                <div
                  v-for="scan in scans"
                  :key="scan.key"
                  class="flex flex-col gap-5px rounded-big border border-solid border-line-subtle bg-surface-card px-14px pb-14px pt-12px"
                >
                  <span class="truncate text-11px text-ink-tertiary">{{ scan.duration }}</span>
                  <span class="text-15px font-600 text-ink-primary">{{ scan.title }}</span>
                  <span class="landing-security-clamp flex-1 text-12px leading-[1.5] text-ink-secondary">{{
                    scan.body
                  }}</span>
                  <span
                    class="mt-4px inline-flex h-26px items-center self-start rounded-default px-11px text-12px font-600"
                    :class="
                      scan.key === 'quick'
                        ? done
                          ? 'bg-button-primary-bg text-button-primary-text'
                          : 'bg-button-disabled-bg text-button-disabled-text'
                        : done
                          ? 'bg-button-secondary-bg text-button-secondary-text'
                          : 'bg-button-disabled-bg text-button-disabled-text'
                    "
                    >{{ scan.action }}</span
                  >
                </div>
              </div>

              <div class="mt-12px rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-14px">
                <div class="flex items-center gap-10px">
                  <span
                    class="grid h-22px w-22px shrink-0 place-items-center rounded-full"
                    :class="done ? 'bg-status-success-subtle text-status-success' : 'text-ink-accent'"
                  >
                    <OnIcon
                      :name="done ? 'check' : 'loader-circle'"
                      :size="14"
                      :class="done ? '' : 'landing-security-spin'"
                    />
                  </span>
                  <span class="min-w-0 flex-1 truncate text-14px font-600 text-ink-primary tabular-nums">{{
                    status
                  }}</span>
                  <span
                    v-if="!done"
                    class="inline-flex h-26px items-center rounded-default border border-solid border-line-default px-10px text-12px text-ink-secondary"
                    >{{ messages.action.cancel }}</span
                  >
                </div>
                <template v-if="!done">
                  <div class="relative mt-10px h-4px overflow-hidden rounded-full bg-accent-coral-subtle">
                    <span class="landing-security-meter absolute top-0 h-full w-30% rounded-full bg-brand-coral"></span>
                  </div>
                  <p class="m-0 mt-8px truncate font-mono text-11.5px text-ink-tertiary">{{ progress.path }}</p>
                </template>
              </div>

              <div class="mt-14px">
                <span class="text-14px font-600 text-ink-primary">{{
                  t('landing.security.demo.findings', { count: found ? 1 : 0 })
                }}</span>
                <div
                  v-if="found"
                  class="landing-security-finding mt-8px flex items-center gap-12px rounded-big border border-solid border-line-subtle px-14px py-10px"
                >
                  <span
                    class="grid h-32px w-32px shrink-0 place-items-center rounded-default bg-status-danger-subtle text-status-danger"
                  >
                    <OnIcon name="shield" :size="16" />
                  </span>
                  <span class="flex min-w-0 flex-1 flex-col gap-2px">
                    <span class="flex min-w-0 items-center gap-6px text-13px font-600 text-ink-primary">
                      <span class="truncate">{{ THREAT.name }}</span>
                      <span
                        class="inline-flex h-20px shrink-0 items-center rounded-full bg-status-danger-subtle px-7px text-11px font-500 text-status-danger"
                        >{{ THREAT.signature }}</span
                      >
                    </span>
                    <span class="truncate font-mono text-11.5px text-ink-tertiary">{{ THREAT.path }}</span>
                  </span>
                  <span
                    v-if="quarantined"
                    class="inline-flex h-20px shrink-0 items-center gap-4px rounded-full bg-status-success-subtle px-8px text-11.5px font-500 text-status-success"
                  >
                    <OnIcon name="check" :size="12" />{{ t('landing.security.demo.quarantined') }}
                  </span>
                  <span
                    v-else
                    class="inline-flex h-28px shrink-0 items-center rounded-default bg-button-accent-bg px-12px text-12.5px font-600 text-button-accent-text"
                    :class="step === QUARANTINED - 1 ? 'landing-security-press' : ''"
                    >{{ t('landing.security.demo.quarantine') }}</span
                  >
                  <span class="grid h-28px w-28px shrink-0 place-items-center text-ink-tertiary">
                    <OnIcon name="ellipsis" :size="16" />
                  </span>
                </div>
              </div>

              <div
                class="landing-security-toast absolute bottom-20px right-20px flex items-center gap-10px rounded-panel border border-solid border-line-default bg-surface-popover px-14px py-10px shadow-toast"
                :class="step === QUARANTINED ? 'opacity-100' : 'opacity-0'"
              >
                <span
                  class="grid h-20px w-20px place-items-center rounded-full bg-status-success-subtle text-status-success"
                >
                  <OnIcon name="check" :size="12" />
                </span>
                <span class="text-13px font-500 text-ink-primary">{{
                  t('landing.security.demo.toast', { name: THREAT.name })
                }}</span>
              </div>
            </div>

            <aside
              class="flex min-h-0 flex-col gap-14px border-l border-l-solid border-line-subtle bg-surface-rail px-14px pt-64px"
            >
              <div
                class="flex flex-col gap-8px rounded-big border border-solid border-line-subtle bg-surface-card p-14px"
              >
                <span class="flex items-center gap-7px text-14px font-600 text-ink-primary">
                  <OnIcon name="shield" :size="15" />{{ t('landing.security.demo.engine') }}
                </span>
                <span class="text-15px font-600 text-ink-primary">{{ ENGINE }}</span>
                <span class="flex justify-between gap-8px text-12.5px">
                  <span class="text-ink-tertiary">{{ t('landing.security.demo.database') }}</span>
                  <span class="text-ink-primary">{{ t('landing.security.demo.databaseValue') }}</span>
                </span>
              </div>
              <div
                class="flex flex-col gap-8px rounded-big border border-solid border-line-subtle bg-surface-card p-14px"
              >
                <span class="flex items-center gap-7px text-14px font-600 text-ink-primary">
                  <OnIcon name="shield-check" :size="15" />{{ t('landing.security.demo.protection') }}
                </span>
                <span v-for="label in protection" :key="label" class="flex items-center gap-8px text-12.5px">
                  <span class="h-6px w-6px shrink-0 rounded-full bg-status-success"></span>
                  <span class="min-w-0 flex-1 truncate text-ink-secondary">{{ label }}</span>
                  <span class="shrink-0 text-status-success">{{ t('landing.security.demo.on') }}</span>
                </span>
              </div>
            </aside>
          </div>
        </div>
      </foreignObject>
    </svg>
  </div>
</template>

<style>
/* Fixed scene size; the SVG viewBox scales it to the container. */
.landing-security-window {
  width: 1120px;
  height: 580px;
}

.landing-security-clamp {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 3;
  overflow: hidden;
}

/* Indeterminate scan bar, as the desktop progress meter; reduced motion leaves it still. */
.landing-security-meter {
  animation: landing-security-meter 1.4s var(--on-motion-easing-standard) infinite;
}

@keyframes landing-security-meter {
  from {
    left: -30%;
  }
  to {
    left: 100%;
  }
}

.landing-security-spin {
  animation: landing-security-spin 1s linear infinite;
}

@keyframes landing-security-spin {
  to {
    rotate: 360deg;
  }
}

.landing-security-finding {
  animation: landing-security-in 0.4s var(--on-motion-easing-emphasized) both;
}

@keyframes landing-security-in {
  from {
    opacity: 0;
    transform: translateY(6px);
  }
}

/* The Quarantine button dims as if clicked just before the row turns quarantined. */
.landing-security-press {
  filter: brightness(0.82);
  transform: scale(0.97);
  transition:
    filter var(--on-motion-duration-base) var(--on-motion-easing-standard),
    transform var(--on-motion-duration-base) var(--on-motion-easing-standard);
}

.landing-security-toast {
  transition: opacity var(--on-motion-duration-base) var(--on-motion-easing-standard);
}
</style>
