import { ref } from 'vue';
import { defineStore } from 'pinia';
import type { CatalogStatus, CategoryItem } from '@/ipc/bindings';
import { commands, unwrap } from '@/ipc/client';

/** Local catalog (06 §7): sync state, categories, and revision counter; text sync also triggers page rereads. */
export const useCatalogStore = defineStore('catalog', () => {
  const status = ref<CatalogStatus | null>(null);
  const categories = ref<CategoryItem[]>([]);
  let categorySequence = 0;
  const revision = ref(0);

  async function loadStatus() {
    status.value = await unwrap(commands.catalogStatus());
  }

  async function loadCategories() {
    const current = ++categorySequence;
    const result = await unwrap(commands.catalogCategories());
    if (current === categorySequence) categories.value = result;
  }

  async function sync(force = false) {
    try {
      await unwrap(commands.catalogSync(force));
    } finally {
      // A source can commit useful data even when another source fails.
      await Promise.all([loadStatus(), loadCategories()]);
      revision.value += 1;
    }
  }

  return { status, categories, revision, loadStatus, loadCategories, sync };
});
