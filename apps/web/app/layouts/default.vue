<script setup lang="ts">
import { OnAppShell, OnToastRegion } from '@opennavo/ui';

// Web shell (05 §11.2, ADR-017): shared OnAppShell with document scrolling; supply web sidebar, toolbar,
// mobile menu, rail, footer. definePageMeta({ rail }) selects the rail, currently Discover only.
const { t } = useI18n();
const route = useRoute();
const toasts = useToasts();
const rail = computed(() => route.meta.rail);
const palette = useCommandPalette();
useCommandPaletteShortcut();
</script>

<template>
  <a
    href="#main-content"
    class="sr-only focus:not-sr-only focus:fixed focus:left-16px focus:top-16px focus:z-100 focus:rounded-default focus:bg-surface-card focus:p-12px focus:text-ink-primary focus:shadow-focus-ring"
    >{{ t('nav.skipToContent') }}</a
  >
  <OnAppShell scroll="document" :has-rail="Boolean(rail)">
    <template #compactBar>
      <SiteCompactBar />
    </template>
    <template #sidebar>
      <SiteSidebar />
    </template>
    <template #toolbar>
      <SiteToolbar />
    </template>
    <slot />
    <template #rail>
      <SiteDiscoverRail v-if="rail === 'discover'" />
    </template>
    <template #footer>
      <SiteFooter />
    </template>
    <template #overlay>
      <ClientOnly><LanguageSuggestion /></ClientOnly>
      <OpenInAppDialog />
      <LazyCommandPalette v-if="palette.open.value" />
      <OnToastRegion :items="toasts.items.value" @dismiss="toasts.dismiss" />
    </template>
  </OnAppShell>
</template>
