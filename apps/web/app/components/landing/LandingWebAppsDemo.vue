<script setup lang="ts">
// Web Apps demo: a small window with six site apps, one highlighted at a time. Names and icons are
// real; the running state is illustrative.
const APPS = [
  { name: 'ChatGPT', icon: '/webapp-icons/chatgpt.png' },
  { name: 'Claude', icon: '/webapp-icons/claude.png' },
  { name: 'Notion', icon: '/webapp-icons/notion.png' },
  { name: 'Figma', icon: '/webapp-icons/figma.png' },
  { name: 'YouTube', icon: '/webapp-icons/youtube.png' },
  { name: 'Spotify', icon: '/webapp-icons/spotify.png' }
] as const;

const { t } = useI18n();
const root = ref<HTMLElement>();
const step = useDemoLoop(root, [1500, 1500, 1500, 1500, 1500, 1500]);
const active = computed(() => step.value % APPS.length);
</script>

<template>
  <div ref="root" class="mx-auto flex w-full max-w-330px flex-col" inert aria-hidden="true">
    <div
      data-testid="webapps-demo"
      class="overflow-hidden rounded-panel border border-solid border-line-default bg-surface-card shadow-popover"
    >
      <div class="flex h-30px items-center gap-5px border-b border-b-solid border-line-subtle bg-surface-inset px-10px">
        <span class="h-7px w-7px rounded-full bg-macos-close"></span>
        <span class="h-7px w-7px rounded-full bg-macos-minimize"></span>
        <span class="h-7px w-7px rounded-full bg-macos-zoom"></span>
        <span class="ml-6px truncate text-10px font-600 text-ink-secondary">{{ t('landing.window.webapps') }}</span>
        <span
          class="ml-auto flex h-16px w-16px items-center justify-center rounded-tiny bg-surface-raised text-12px leading-none text-ink-tertiary"
          >+</span
        >
      </div>
      <div class="grid grid-cols-3 gap-6px px-10px py-12px">
        <div
          v-for="(app, index) in APPS"
          :key="app.name"
          class="flex flex-col items-center gap-4px rounded-default px-4px py-6px transition-colors duration-base ease-standard"
          :class="index === active ? 'bg-surface-raised' : ''"
        >
          <img :src="app.icon" :alt="app.name" class="h-32px w-32px rounded-[9px] object-cover" />
          <span class="max-w-full truncate text-10px text-ink-secondary">{{ app.name }}</span>
        </div>
      </div>
      <div
        class="flex h-28px items-center justify-between border-t border-t-solid border-line-subtle px-10px text-10px text-ink-tertiary"
      >
        <span class="truncate">{{ t('client.pages.webapps.demo.badge') }}</span>
        <span
          class="ml-6px shrink-0 rounded-full bg-button-primary-bg px-7px py-2px text-[9px] font-600 text-button-primary-text"
          >{{ t('client.pages.webapps.demo.new') }}</span
        >
      </div>
    </div>
  </div>
</template>
