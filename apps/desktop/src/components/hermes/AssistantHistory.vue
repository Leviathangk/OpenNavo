<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { OnIcon } from '@opennavo/ui';
import { useHermesStore } from '@/stores/hermes';
import { useHermesContent } from '@/composables/useHermesContent';
import type { HermesMessage } from '@/stores/hermes';

const props = defineProps<{ open: boolean }>();
const emit = defineEmits<{ close: []; select: [id: string] }>();

const store = useHermesStore();
const content = useHermesContent();
const search = ref('');
const root = ref<HTMLElement>();
const editingId = ref('');
const editTitle = ref('');

const icons: Record<string, string> = {
  rectangle: 'lucide:mouse-pointer-click',
  cleanup: 'lucide:hard-drive',
  updates: 'lucide:circle-arrow-down',
  explain: 'lucide:app-window'
};

function textOf(message: HermesMessage | undefined): string {
  if (!message) return '';
  if (message.textKey)
    return (
      (content.value.messages as Record<string, string>)[message.textKey.replace(/^messages\./, '')] ?? message.textKey
    );
  return message.text ?? '';
}

function historyItem(key?: string): { title: string; preview: string } | undefined {
  if (!key) return undefined;
  const id = key.replace(/^history\./, '').split('.')[0] ?? '';
  return (content.value.history.items as Record<string, { title: string; preview: string }>)[id];
}

function titleOf(id: string): string {
  const conversation = store.conversations.find(item => item.id === id);
  if (conversation?.title) return conversation.title;
  const curated = historyItem(conversation?.titleKey);
  if (curated) return curated.title;
  const firstUser = conversation?.messages.find(item => item.role === 'user');
  return textOf(firstUser) || content.value.history.empty;
}

function previewOf(id: string): string {
  const conversation = store.conversations.find(item => item.id === id);
  const curated = historyItem(conversation?.previewKey);
  if (curated) return curated.preview;
  const lastAssistant = [...(conversation?.messages ?? [])].reverse().find(item => item.role === 'assistant');
  return textOf(lastAssistant) || textOf(conversation?.messages[0]) || '';
}

function startOfDay(value: number): number {
  const date = new Date(value);
  date.setHours(0, 0, 0, 0);
  return date.getTime();
}

function groupOf(value: number): 'today' | 'yesterday' | 'earlier' {
  const today = startOfDay(Date.now());
  const day = startOfDay(value);
  if (day === today) return 'today';
  if (day === today - 86_400_000) return 'yesterday';
  return 'earlier';
}

const filtered = computed(() => {
  const query = search.value.trim().toLowerCase();
  return store.history
    .map(conversation => {
      const title = titleOf(conversation.id);
      const preview = previewOf(conversation.id);
      return { conversation, title, preview };
    })
    .filter(item => !query || `${item.title} ${item.preview}`.toLowerCase().includes(query));
});

const groups = computed(() => {
  const buckets: Record<'today' | 'yesterday' | 'earlier', typeof filtered.value> = {
    today: [],
    yesterday: [],
    earlier: []
  };
  for (const item of filtered.value) buckets[groupOf(item.conversation.updatedAt)].push(item);
  return [
    { key: 'today', label: content.value.history.today, items: buckets.today },
    { key: 'yesterday', label: content.value.history.yesterday, items: buckets.yesterday },
    { key: 'earlier', label: content.value.history.earlier, items: buckets.earlier }
  ].filter(group => group.items.length > 0);
});

function startRename(id: string) {
  const conversation = store.conversations.find(item => item.id === id);
  if (!conversation) return;
  editingId.value = id;
  editTitle.value = titleOf(id);
}

function commitRename() {
  if (!editingId.value) return;
  store.renameConversation(editingId.value, editTitle.value);
  editingId.value = '';
  editTitle.value = '';
}

function removeConversation(id: string) {
  if (!window.confirm(content.value.history.deleteConfirm)) return;
  store.deleteConversation(id);
}

function clearHistory() {
  if (!window.confirm(content.value.history.clearConfirm)) return;
  store.clearHistory();
  close();
}

function close() {
  emit('close');
}

function onDocumentPointer(event: MouseEvent) {
  if (!props.open) return;
  const target = event.target;
  if (!(target instanceof Element)) return;
  if (target.closest('[data-hermes-history]') || target.closest('[data-hermes-history-toggle]')) return;
  close();
}

function onKeydown(event: KeyboardEvent) {
  if (props.open && event.key === 'Escape') close();
}

watch(
  () => props.open,
  async open => {
    if (!open) return;
    search.value = '';
    await nextTick();
    (root.value?.querySelector('#hermes-history-search') as HTMLInputElement | null)?.focus();
  }
);

onMounted(() => {
  document.addEventListener('click', onDocumentPointer);
  document.addEventListener('keydown', onKeydown);
});
onBeforeUnmount(() => {
  document.removeEventListener('click', onDocumentPointer);
  document.removeEventListener('keydown', onKeydown);
});
</script>

<template>
  <div
    v-if="open"
    ref="root"
    class="absolute right-0 top-64px z-20 flex max-h-520px w-340px max-w-[calc(100vw-32px)] flex-col overflow-hidden rounded-big border border-solid border-line-strong bg-surface-popover shadow-popover"
    data-hermes-history
    role="dialog"
    :aria-label="content.history.title"
  >
    <div class="flex items-center gap-8px px-14px pb-8px pt-12px">
      <h2 class="m-0 text-12.5px font-600 text-ink-primary">{{ content.history.title }}</h2>
      <kbd
        class="ml-auto rounded-tiny border border-solid border-line-subtle px-5px py-2px font-sans text-11px text-ink-tertiary"
        >⌘Y</kbd
      >
      <button
        v-if="store.history.length"
        type="button"
        class="ml-auto text-11px text-ink-tertiary outline-none hover:text-ink-secondary focus-visible:shadow-focus-ring"
        @click="store.exportHistory()"
      >
        {{ content.history.exportHistory }}
      </button>
      <button
        v-if="store.history.length"
        type="button"
        class="text-11px text-ink-tertiary outline-none hover:text-status-danger focus-visible:shadow-focus-ring"
        @click="clearHistory"
      >
        {{ content.history.clear }}
      </button>
      <button
        type="button"
        class="flex h-24px w-24px items-center justify-center rounded-small text-ink-tertiary outline-none hover:bg-surface-raised hover:text-ink-primary focus-visible:shadow-focus-ring"
        :aria-label="content.history.close"
        @click="close"
      >
        <OnIcon name="lucide:x" :size="14" />
      </button>
    </div>
    <label
      class="mx-12px mb-6px flex h-30px items-center gap-7px rounded-small border border-solid border-line-subtle bg-surface-inset px-8px text-ink-tertiary"
    >
      <OnIcon name="lucide:search" :size="14" />
      <input
        id="hermes-history-search"
        v-model="search"
        type="search"
        class="min-w-0 flex-1 border-none bg-transparent font-sans text-11px text-ink-primary outline-none placeholder:text-ink-tertiary"
        :placeholder="content.history.search"
        :aria-label="content.history.search"
      />
    </label>
    <div class="min-h-0 overflow-y-auto px-6px pb-6px">
      <div v-if="!groups.length" class="px-14px py-28px text-center text-11.5px text-ink-tertiary">
        {{ content.history.empty }}
      </div>
      <div v-for="group in groups" :key="group.key">
        <div class="px-8px pb-4px pt-8px text-11px font-600 tracking-[0.04em] text-ink-tertiary">{{ group.label }}</div>
        <div
          v-for="item in group.items"
          :key="item.conversation.id"
          class="group flex w-full items-start gap-9px rounded-small px-8px py-8px text-left outline-none transition-colors hover:bg-surface-raised"
          :class="{ 'bg-surface-raised': item.conversation.id === store.activeId }"
          role="button"
          tabindex="0"
          @click="
            emit('select', item.conversation.id);
            close();
          "
          @keydown.enter="
            emit('select', item.conversation.id);
            close();
          "
        >
          <span
            class="flex h-24px w-24px shrink-0 items-center justify-center rounded-small"
            :class="
              item.conversation.id === store.activeId
                ? 'bg-accent-coral-subtle text-ink-accent'
                : 'bg-surface-raised text-ink-tertiary'
            "
          >
            <OnIcon :name="icons[item.conversation.id] ?? 'lucide:message-circle'" :size="14" />
          </span>
          <span class="min-w-0 flex-1">
            <input
              v-if="editingId === item.conversation.id"
              v-model="editTitle"
              class="w-full rounded-tiny border border-solid border-line-default bg-surface-inset px-4px text-11.5px text-ink-primary outline-none"
              autofocus
              @click.stop
              @keydown.enter.stop="commitRename"
              @keydown.esc.stop="editingId = ''"
              @blur="commitRename"
            />
            <b v-else class="block truncate text-11.5px font-600 text-ink-primary">{{ item.title }}</b>
            <span class="mt-1px block truncate text-11px text-ink-tertiary">{{ item.preview }}</span>
          </span>
          <span class="flex shrink-0 items-center gap-2px">
            <button
              type="button"
              class="flex h-20px w-20px items-center justify-center rounded-tiny text-ink-tertiary opacity-0 outline-none hover:bg-surface-control hover:text-ink-primary focus-visible:opacity-100 group-hover:opacity-100"
              :aria-label="content.history.rename"
              @click.stop="startRename(item.conversation.id)"
            >
              <OnIcon name="lucide:pencil" :size="11" />
            </button>
            <button
              type="button"
              class="flex h-20px w-20px items-center justify-center rounded-tiny text-ink-tertiary opacity-0 outline-none hover:bg-surface-control hover:text-status-danger focus-visible:opacity-100 group-hover:opacity-100"
              :aria-label="content.history.delete"
              @click.stop="removeConversation(item.conversation.id)"
            >
              <OnIcon name="lucide:trash-2" :size="11" />
            </button>
          </span>
        </div>
      </div>
    </div>
    <div
      class="flex items-center gap-5px border-t border-t-solid border-line-subtle px-14px py-8px text-11px text-ink-tertiary"
    >
      <OnIcon name="lucide:lock" :size="12" />
      {{ content.history.local }}
    </div>
  </div>
</template>
