<script setup lang="ts">
import { OnToolbar } from '@opennavo/ui';

// Web toolbar (05 §11.2): shared Command-K search, theme and locale selectors right; browser supplies back/forward.
const { t } = useI18n();
const palette = useCommandPalette();
</script>

<template>
  <OnToolbar :search-label="t('nav.search')" search-shortcut="⌘K" @search="palette.open.value = true">
    <template #end>
      <div class="flex items-center gap-2px">
        <ThemeSwitch />
        <LanguageSwitch />
      </div>
    </template>
  </OnToolbar>
</template>
