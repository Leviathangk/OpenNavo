import { ref } from 'vue';
import { defineStore } from 'pinia';
import type { SourceFilter } from './localApps';

/** Installed page filters, shared with its rail ("Unused" card) and kept while browsing other pages. */
export const useInstalledFilterStore = defineStore('installedFilter', () => {
  const source = ref<SourceFilter>('all');
  const unusedOnly = ref(false);
  const query = ref('');

  function showUnused(value: boolean) {
    unusedOnly.value = value;
    if (value) source.value = 'all';
  }

  return { source, unusedOnly, query, showUnused };
});
