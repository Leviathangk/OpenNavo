<script setup lang="ts">
import { OnToastRegion, OnTopNav } from '@opennavo/ui';

// Landing shell (05 §11.4, 08 §10.14): full-width OnTopNav/footer without store sidebars.
// Share Command-K search, language suggestions, and toasts with store pages.
const { t } = useI18n();
const localePath = useLocalePath();
const palette = useCommandPalette();
useCommandPaletteShortcut();
const toasts = useToasts();
const NuxtLink = resolveComponent('NuxtLink');

// Security (the antivirus page) sits beside the store entries and is marked while on it.
const route = useRoute();
const items = computed(() =>
  (['discover', 'categories', 'rankings', 'collections', 'security'] as const).map(key => ({
    label: t(`nav.${key}`),
    href: localePath(`/${key}`),
    // The security topics (/security/firewall …) belong to it too.
    active:
      key === 'security' &&
      (route.path === localePath('/security') || route.path.startsWith(`${localePath('/security')}/`))
  }))
);
</script>

<template>
  <a
    href="#main-content"
    class="sr-only focus:not-sr-only focus:fixed focus:left-16px focus:top-16px focus:z-100 focus:rounded-default focus:bg-surface-card focus:p-12px focus:text-ink-primary focus:shadow-focus-ring"
    >{{ t('nav.skipToContent') }}</a
  >
  <div class="min-h-screen bg-surface-page font-sans text-ink-primary">
    <OnTopNav
      :items="items"
      :home-href="localePath('/')"
      :download-href="localePath('/download')"
      :download-label="t('nav.download')"
      :search-label="t('nav.search')"
      search-shortcut="⌘K"
      :label="t('nav.label')"
      :link-as="NuxtLink"
      @search="palette.open.value = true"
    >
      <!-- Mobile cannot fit language names; theme and language selection move to the footer. -->
      <template #extra>
        <div class="hidden items-center gap-2px sm:flex">
          <ThemeSwitch />
          <LanguageSwitch />
        </div>
      </template>
    </OnTopNav>
    <main id="main-content" tabindex="-1">
      <slot />
    </main>
    <LandingFooter />
    <ClientOnly><LanguageSuggestion /></ClientOnly>
    <LazyCommandPalette v-if="palette.open.value" />
    <OnToastRegion :items="toasts.items.value" @dismiss="toasts.dismiss" />
  </div>
</template>
