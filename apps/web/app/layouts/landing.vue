<script setup lang="ts">
import { OnToastRegion, OnTopNav } from '@opennavo/ui';

// Landing shell (05 §11.4, 08 §10.14): full-width OnTopNav/footer without store sidebars.
// Share Command-K search, language suggestions, and toasts with store pages.
const { t } = useI18n();
const localePath = useLocalePath();
const palette = useCommandPalette();
const toasts = useToasts();
const NuxtLink = resolveComponent('NuxtLink');

// Security (the antivirus page) sits beside the store entries and is marked while on it.
const route = useRoute();
const items = computed(() =>
  (['discover', 'categories', 'rankings', 'collections', 'security'] as const).map(key => ({
    label: t(`nav.${key}`),
    href: localePath(`/${key}`),
    active: key === 'security' && route.path === localePath('/security')
  }))
);
</script>

<template>
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
      <!-- Mobile cannot fit language names; language selection moves to the footer. -->
      <template #extra>
        <span class="hidden sm:inline-flex"><LanguageSwitch /></span>
      </template>
    </OnTopNav>
    <main>
      <slot />
    </main>
    <LandingFooter />
    <ClientOnly><LanguageSuggestion /></ClientOnly>
    <CommandPalette />
    <OnToastRegion :items="toasts.items.value" @dismiss="toasts.dismiss" />
  </div>
</template>
