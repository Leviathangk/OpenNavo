<script setup lang="ts">
import { unwrap } from '@opennavo/api';
import { OnCommandPalette, useUiMessages } from '@opennavo/ui';
import type { OnCommandGroup, OnCommandItem } from '@opennavo/ui';
import { paletteGroups } from '~/utils/commandPalette';
import type { SuggestItem } from '~/utils/commandPalette';
import { packagePath } from '~/utils/packageView';
import { variantName } from '~/utils/seoTitles';

// Web Command-K (05 §7): Go to (all results first) and Apps groups only; suggestions from suggest API.
const { t } = useI18n();
const api = useApi();
const localePath = useLocalePath();
const messages = useUiMessages();
const { copy } = useCopyCommand();
const { open } = useCommandPalette();

const query = ref('');
const suggestions = ref<SuggestItem[]>([]);
const loading = ref(false);
let timer: ReturnType<typeof setTimeout> | undefined;
let sequence = 0;

// Request after 150 ms of idle typing; discard stale earlier responses.
watch(query, value => {
  clearTimeout(timer);
  const q = value.trim();
  if (!q) {
    suggestions.value = [];
    loading.value = false;
    return;
  }
  loading.value = true;
  timer = setTimeout(async () => {
    const current = ++sequence;
    try {
      const items = await unwrap(api.GET('/search/suggest', { params: { query: { q: q.slice(0, 64), limit: 8 } } }));
      // A variant cask's suggestion says its variant, as its page does.
      if (current === sequence)
        suggestions.value = items.map(item =>
          item.type === 'package' && item.token ? { ...item, name: variantName(item.name, item.token) } : item
        );
    } catch {
      if (current === sequence) suggestions.value = [];
    } finally {
      if (current === sequence) loading.value = false;
    }
  }, 150);
});

watch(open, value => {
  if (!value) query.value = '';
});

const groups = computed<OnCommandGroup[]>(() => paletteGroups(query.value, suggestions.value, t));

function targetOf(item: OnCommandItem): string | null {
  const [type, ...rest] = item.key.split(':');
  if (type === 'package') return localePath(packagePath(rest.slice(1).join(':')));
  if (type === 'category') return localePath(`/categories/${rest.join(':')}`);
  if (type === 'page') return localePath(rest.join(':'));
  if (type === 'search') return localePath(`/search?q=${encodeURIComponent(rest.join(':'))}`);
  return null;
}

function onSelect(item: OnCommandItem) {
  const target = targetOf(item);
  open.value = false;
  if (target) void navigateTo(target);
}

function onCopy(item: OnCommandItem) {
  // App Store rows exist only in the desktop palette and have no brew command.
  if (!item.app || item.app.kind === 'mas') return;
  void copy(item.app.kind, item.app.token);
}

// The Command-K shortcut lives in the layouts (useCommandPaletteShortcut), which mount this lazily.
onBeforeUnmount(() => clearTimeout(timer));

const hints = computed(() => [
  messages.value.palette.hintSelect,
  messages.value.palette.hintOpen,
  messages.value.palette.hintCopy
]);
</script>

<template>
  <OnCommandPalette
    v-model:open="open"
    v-model:query="query"
    :groups="groups"
    :loading="loading"
    :hints="hints"
    @select="onSelect"
    @copy="onCopy"
  />
</template>
