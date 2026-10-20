<script setup lang="ts">
import { OnAppShell, OnToastRegion } from '@opennavo/ui';

// Web shell (05 §11.2, ADR-017): shared OnAppShell with document scrolling; supply web sidebar, toolbar,
// mobile menu, rail, footer. definePageMeta({ rail }) selects the rail, currently Discover only.
const route = useRoute();
const toasts = useToasts();
const rail = computed(() => route.meta.rail);
const palette = useCommandPalette();
useCommandPaletteShortcut();
</script>

<template>
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
