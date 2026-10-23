<script setup lang="ts">
import { OnButton, OnIcon, OnLogo, OnNavItem, useUiMessages } from '@opennavo/ui';

// Mobile top menu below 768 px (05 §11.2) contains wide sidebar download card, navigation, About, and locales.
const { t } = useI18n();
const route = useRoute();
const localePath = useLocalePath();
const messages = useUiMessages();
const palette = useCommandPalette();
const { groups, footer } = useSiteNav();
const NuxtLink = resolveComponent('NuxtLink');

const menuOpen = ref(false);
const menuButton = ref<HTMLButtonElement>();
const panelId = useId();

// Collapse after menu navigation or any other route change.
watch(
  () => route.fullPath,
  () => {
    menuOpen.value = false;
  }
);

function onKeydown(event: KeyboardEvent) {
  if (event.key !== 'Escape' || !menuOpen.value) return;
  menuOpen.value = false;
  menuButton.value?.focus();
}

onMounted(() => document.addEventListener('keydown', onKeydown));
onBeforeUnmount(() => document.removeEventListener('keydown', onKeydown));
</script>

<template>
  <div class="border-b border-b-solid border-line-subtle bg-surface-base">
    <div class="flex h-52px items-center gap-4px px-16px">
      <NuxtLink
        :to="localePath('/')"
        class="inline-flex rounded-tiny no-underline outline-none focus-visible:shadow-focus-ring"
        :aria-label="messages.topNav.home"
      >
        <OnLogo wordmark />
      </NuxtLink>
      <div class="flex-1"></div>
      <OnButton
        variant="ghost"
        size="sm"
        icon="search"
        icon-only
        :aria-label="t('nav.search')"
        @click="palette.open.value = true"
      />
      <button
        ref="menuButton"
        type="button"
        class="m-0 box-border inline-flex h-32px w-32px items-center justify-center rounded-default border-none bg-transparent p-0 text-ink-secondary outline-none transition-colors duration-fast hover:text-ink-primary focus-visible:shadow-focus-ring"
        :aria-expanded="menuOpen ? 'true' : 'false'"
        :aria-controls="panelId"
        :aria-label="menuOpen ? messages.topNav.closeMenu : messages.topNav.openMenu"
        @click="menuOpen = !menuOpen"
      >
        <OnIcon :name="menuOpen ? 'x' : 'menu'" :size="18" />
      </button>
    </div>
    <div
      v-show="menuOpen"
      :id="panelId"
      class="box-border flex max-h-[calc(100vh-52px)] flex-col gap-14px overflow-y-auto border-t border-t-solid border-line-subtle px-12px pb-16px pt-12px"
    >
      <DownloadCard />
      <nav class="flex flex-col gap-12px" :aria-label="t('nav.label')">
        <div v-for="group in groups" :key="group.key" class="flex flex-col gap-2px">
          <div class="px-8px pb-6px pt-2px text-label text-ink-tertiary">{{ group.label }}</div>
          <OnNavItem
            v-for="item in group.items"
            :key="item.key"
            :as="NuxtLink"
            :to="item.href"
            :icon="item.icon"
            :active="item.active"
            :count="item.count"
          >
            {{ item.label }}
          </OnNavItem>
        </div>
      </nav>
      <div class="flex flex-col gap-2px border-t border-t-solid border-line-subtle pt-10px">
        <OnNavItem
          v-for="item in footer"
          :key="item.key"
          :as="NuxtLink"
          :to="item.href"
          :icon="item.icon"
          :active="item.active"
        >
          {{ item.label }}
        </OnNavItem>
        <div class="flex items-center gap-2px self-start">
          <LanguageSwitch align="start" />
          <ThemeSwitch align="start" />
        </div>
      </div>
    </div>
  </div>
</template>
