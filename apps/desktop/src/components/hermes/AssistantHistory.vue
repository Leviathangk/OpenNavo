<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { OnIcon, runOnEnter } from '@opennavo/ui';
import { useHermesStore } from '@/stores/hermes';
import { useHermesContent } from '@/composables/useHermesContent';
import type { HermesConversation, HermesMessage, PanelHistorySlice } from '@/stores/hermes';
import { isMessagingSource, platformBadge, platformLabel } from '@/hermes/platforms';

const props = defineProps<{ open: boolean }>();
const emit = defineEmits<{ close: []; select: [id: string] }>();

const store = useHermesStore();
const content = useHermesContent();
const search = ref('');
const root = ref<HTMLElement>();
const editingId = ref('');
const editTitle = ref('');
/** Which slice of the host's store the popover is showing. */
const view = ref<PanelHistorySlice>('recents');
/** Rows revealed per gateway platform before its own "show more" step. */
const revealed = ref<Record<string, number>>({});
/** Whether the gesture that is about to fire `click` started inside the popover. */
const pressedInside = ref(false);

const icons: Record<string, string> = {
  rectangle: 'lucide:mouse-pointer-click',
  cleanup: 'lucide:hard-drive',
  updates: 'lucide:circle-arrow-down',
  explain: 'lucide:app-window'
};

const slices = computed<{ key: PanelHistorySlice; label: string }[]>(() => [
  { key: 'recents', label: content.value.history.views.recents },
  { key: 'messaging', label: content.value.history.views.gateways },
  { key: 'cron', label: content.value.history.views.scheduled },
  { key: 'archived', label: content.value.history.views.archived }
]);

/** Local installs have one flat list; the slice tabs only mean something on a panel host. */
const showViews = computed(() => store.panelActive);

function stateFor(key: PanelHistorySlice) {
  return store.panelSliceState(key);
}

const activeState = computed(() => (store.panelActive ? stateFor(view.value) : null));

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

function titleOf(conversation: HermesConversation): string {
  if (conversation.title) return conversation.title;
  const curated = historyItem(conversation.titleKey);
  if (curated) return curated.title;
  const firstUser = conversation.messages.find(item => item.role === 'user');
  return textOf(firstUser) || content.value.history.empty;
}

function previewOf(conversation: HermesConversation): string {
  const curated = historyItem(conversation.previewKey);
  if (curated) return curated.preview;
  // The panel ships its own preview line; the transcript is not read until a row is opened.
  if (conversation.panel) return conversation.panelPreview ?? '';
  const lastAssistant = [...conversation.messages].reverse().find(item => item.role === 'assistant');
  return textOf(lastAssistant) || textOf(conversation.messages[0]) || '';
}

function startOfDay(value: number): number {
  const date = new Date(value);
  date.setHours(0, 0, 0, 0);
  return date.getTime();
}

function dayOf(value: number): 'today' | 'yesterday' | 'earlier' {
  const today = startOfDay(Date.now());
  const day = startOfDay(value);
  if (day === today) return 'today';
  if (day === today - 86_400_000) return 'yesterday';
  return 'earlier';
}

interface HistoryRow {
  conversation: HermesConversation;
  title: string;
  preview: string;
}

interface HistoryGroup {
  key: string;
  /** Key into `revealed`, so each section remembers its own "show more" step. */
  revealKey: string;
  label: string;
  /** Two-letter platform badge, only for gateway sections. */
  badge?: string;
  rows: HistoryRow[];
  /** Rows hidden behind this section's "show more". */
  hidden: number;
}

/** Gateway threads reveal three rows at a time, like the panel's own sidebar, so one busy
 *  platform cannot dominate the popover before the user asks for it. */
const GATEWAY_ROWS = 3;
const GATEWAY_STEP = 10;

const rows = computed<HistoryRow[]>(() => {
  const query = search.value.trim().toLowerCase();
  const source = store.panelRowsFor(view.value);
  return source
    .map(conversation => ({ conversation, title: titleOf(conversation), preview: previewOf(conversation) }))
    .filter(item => !query || `${item.title} ${item.preview}`.toLowerCase().includes(query))
    .sort((left, right) => right.conversation.updatedAt - left.conversation.updatedAt);
});

/**
 * One section per key, in first-seen order, with the same reveal step everywhere: gateways
 * key on the platform, scheduled jobs on the job name (the host titles a run
 * `<job> · <timestamp>`, the way the panel's own Cron page groups them).
 */
function keyedGroups(
  items: HistoryRow[],
  prefix: string,
  keyOf: (row: HistoryRow) => string,
  labelOf: (key: string) => { label: string; badge?: string }
): HistoryGroup[] {
  const order: string[] = [];
  const buckets = new Map<string, HistoryRow[]>();
  for (const row of items) {
    const key = keyOf(row) || 'unknown';
    if (!buckets.has(key)) {
      buckets.set(key, []);
      order.push(key);
    }
    buckets.get(key)?.push(row);
  }
  const searching = search.value.trim().length > 0;
  return order.map(key => {
    const all = buckets.get(key) ?? [];
    const limit = searching ? all.length : (revealed.value[key] ?? GATEWAY_ROWS);
    const { label, badge } = labelOf(key);
    return {
      key: `${prefix}-${key}`,
      revealKey: key,
      label,
      badge,
      rows: all.slice(0, limit),
      hidden: Math.max(0, all.length - limit)
    };
  });
}

/** The host names a scheduled run `<job title> · <timestamp>`. */
function jobOf(row: HistoryRow): string {
  const [job = ''] = row.title.split(' · ');
  return job.trim();
}

const groups = computed<HistoryGroup[]>(() => {
  const visible = rows.value;
  if (view.value === 'messaging') {
    return keyedGroups(
      visible,
      'platform',
      row => row.conversation.panelSource ?? '',
      source => ({ label: platformLabel(source), badge: platformBadge(source) })
    );
  }
  if (view.value === 'cron') {
    return keyedGroups(visible, 'job', jobOf, job => ({ label: job }));
  }
  if (view.value === 'recents') {
    const buckets: Record<'today' | 'yesterday' | 'earlier', HistoryRow[]> = {
      today: [],
      yesterday: [],
      earlier: []
    };
    for (const row of visible) buckets[dayOf(row.conversation.updatedAt)].push(row);
    return [
      { key: 'today', revealKey: 'today', label: content.value.history.today, rows: buckets.today, hidden: 0 },
      {
        key: 'yesterday',
        revealKey: 'yesterday',
        label: content.value.history.yesterday,
        rows: buckets.yesterday,
        hidden: 0
      },
      { key: 'earlier', revealKey: 'earlier', label: content.value.history.earlier, rows: buckets.earlier, hidden: 0 }
    ].filter(group => group.rows.length > 0);
  }
  return visible.length ? [{ key: view.value, revealKey: view.value, label: '', rows: visible, hidden: 0 }] : [];
});

const shown = computed(() => groups.value.some(group => group.rows.length > 0));

function reveal(source: string, hidden: number) {
  revealed.value = {
    ...revealed.value,
    [source]: (revealed.value[source] ?? GATEWAY_ROWS) + Math.min(GATEWAY_STEP, hidden)
  };
}

function revealGroup(group: HistoryGroup) {
  reveal(group.revealKey, group.hidden);
}

function rowBadge(conversation: HermesConversation): string {
  // Inside a platform section the header already names the gateway; repeating the badge on all
  // three rows is noise. Everywhere else it is what tells a Telegram thread from a local chat.
  if (view.value === 'messaging') return '';
  if (conversation.panel && isMessagingSource(conversation.panelSource)) return platformBadge(conversation.panelSource);
  return '';
}

/** Only other profiles need a chip: the active one is implied by the rail. */
function showProfileChip(conversation: HermesConversation): boolean {
  if (!store.panelAllProfiles) return false;
  const profile = conversation.panelProfile;
  return Boolean(profile) && profile !== store.activeAgentProfileName;
}

function startRename(id: string, current: string) {
  editingId.value = id;
  editTitle.value = current;
}

function commitRename() {
  if (!editingId.value) return;
  void store.renameConversation(editingId.value, editTitle.value);
  editingId.value = '';
  editTitle.value = '';
}

function removeConversation(id: string) {
  if (!window.confirm(content.value.history.deleteConfirm)) return;
  void store.deleteConversation(id);
}

function toggleArchived(id: string, archived: boolean) {
  void store.setConversationArchived(id, archived);
}

function clearHistory() {
  if (!window.confirm(content.value.history.clearConfirm)) return;
  store.clearHistory();
  close();
}

function openView(next: PanelHistorySlice) {
  if (view.value === next) return;
  view.value = next;
  search.value = '';
  revealed.value = {};
  void store.loadPanelHistory(next);
}

function toggleAllProfiles() {
  revealed.value = {};
  // Rows are re-scoped, so the view on screen has to be read again: the store only reloads the
  // slice it is told to, and the user is looking at this one.
  void store.setPanelAllProfiles(!store.panelAllProfiles).then(() => store.loadPanelHistory(view.value));
}

function retry() {
  if (!store.panelActive) return;
  void store.refreshPanelHistory(view.value);
}

function loadMore() {
  void store.loadMorePanelHistory(view.value);
}

function close() {
  emit('close');
}

// A click that removes its own element ("show more", archive, restore) leaves a detached
// target by the time `click` bubbles to the document, so "did this start inside?" has to be
// answered on `pointerdown`, while the tree is still intact.
function onDocumentPointerDown(event: PointerEvent) {
  if (!props.open) return;
  const target = event.target;
  pressedInside.value =
    target instanceof Element &&
    Boolean(target.closest('[data-hermes-history]') || target.closest('[data-hermes-history-toggle]'));
}

function onDocumentPointer(event: MouseEvent) {
  if (!props.open) return;
  if (pressedInside.value) {
    pressedInside.value = false;
    return;
  }
  const target = event.target;
  if (!(target instanceof Element)) return;
  if (target.closest('[data-hermes-history]') || target.closest('[data-hermes-history-toggle]')) return;
  close();
}

function onKeydown(event: KeyboardEvent) {
  if (props.open && event.key === 'Escape') close();
}

watch(
  showViews,
  visible => {
    if (visible) return;
    // The slice tabs only exist on a panel host. Staying on `messaging`/`cron`/`archived` after a
    // switch to the local connection left the popover on a list it could no longer switch away
    // from (the tabs are hidden, and those views only return panel rows).
    view.value = 'recents';
    search.value = '';
    revealed.value = {};
  },
  { immediate: true }
);

watch(
  () => props.open,
  async open => {
    if (!open) return;
    // Opening the panel is the moment the list is worth reading: each slice is host state and is
    // cached per connection+profile+slice, so this only fetches a slice it has not read yet.
    void store.loadPanelHistory(view.value);
    search.value = '';
    revealed.value = {};
    await nextTick();
    (root.value?.querySelector('#hermes-history-search') as HTMLInputElement | null)?.focus();
  }
);

onMounted(() => {
  document.addEventListener('pointerdown', onDocumentPointerDown);
  document.addEventListener('click', onDocumentPointer);
  document.addEventListener('keydown', onKeydown);
});
onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onDocumentPointerDown);
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
        v-if="store.history.length && !store.panelActive"
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
    <div v-if="showViews" class="mx-12px mb-6px flex items-center gap-2px" data-hermes-history-views>
      <button
        v-for="item in slices"
        :key="item.key"
        type="button"
        class="rounded-small px-8px py-4px text-11px outline-none transition-colors focus-visible:shadow-focus-ring"
        :class="
          view === item.key
            ? 'bg-surface-raised font-600 text-ink-primary'
            : 'text-ink-tertiary hover:bg-surface-raised hover:text-ink-secondary'
        "
        :data-hermes-view="item.key"
        :aria-pressed="view === item.key"
        @click="openView(item.key)"
      >
        {{ item.label }}
      </button>
      <button
        type="button"
        class="ml-auto flex items-center gap-4px rounded-small px-6px py-4px text-10.5px outline-none focus-visible:shadow-focus-ring"
        :class="store.panelAllProfiles ? 'text-ink-accent' : 'text-ink-tertiary hover:text-ink-secondary'"
        :aria-pressed="store.panelAllProfiles"
        data-hermes-history-all-profiles
        @click="toggleAllProfiles"
      >
        <OnIcon name="lucide:users" :size="11" />
        {{ content.history.allProfiles }}
      </button>
    </div>
    <div class="min-h-0 overflow-y-auto px-6px pb-6px">
      <div
        v-if="activeState?.error"
        class="mx-8px my-10px rounded-small border border-solid border-line-subtle px-10px py-8px text-11px text-ink-secondary"
        data-hermes-history-error
      >
        <p class="m-0">{{ content.history.failed }}</p>
        <button
          type="button"
          class="mt-6px text-11px font-600 text-ink-accent outline-none focus-visible:shadow-focus-ring"
          @click="retry"
        >
          {{ content.history.retry }}
        </button>
      </div>
      <div
        v-else-if="!shown"
        class="px-14px py-28px text-center text-11.5px text-ink-tertiary"
        data-hermes-history-empty
      >
        {{ activeState?.loading ? content.history.loadingList : content.history.empty }}
      </div>
      <div v-for="group in groups" :key="group.key" :data-hermes-history-group="group.key">
        <div
          v-if="group.label"
          class="flex items-center gap-5px px-8px pb-4px pt-8px text-11px font-600 tracking-[0.04em] text-ink-tertiary"
        >
          <span
            v-if="group.badge"
            class="flex h-16px min-w-16px items-center justify-center rounded-tiny bg-surface-raised px-3px text-9px text-ink-secondary"
            >{{ group.badge }}</span
          >
          <span data-hermes-history-group-label>{{ group.label }}</span>
        </div>
        <div
          v-for="item in group.rows"
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
            class="flex h-24px w-24px shrink-0 items-center justify-center rounded-small text-9px font-600"
            :class="
              item.conversation.id === store.activeId
                ? 'bg-accent-coral-subtle text-ink-accent'
                : 'bg-surface-raised text-ink-tertiary'
            "
          >
            <template v-if="rowBadge(item.conversation)">{{ rowBadge(item.conversation) }}</template>
            <OnIcon v-else :name="icons[item.conversation.id] ?? 'lucide:message-circle'" :size="14" />
          </span>
          <span class="min-w-0 flex-1">
            <input
              v-if="editingId === item.conversation.id"
              v-model="editTitle"
              class="w-full rounded-tiny border border-solid border-line-default bg-surface-inset px-4px text-11.5px text-ink-primary outline-none"
              autofocus
              @click.stop
              @keydown.enter.stop="runOnEnter($event, commitRename)"
              @keydown.esc.stop="editingId = ''"
              @blur="commitRename"
            />
            <b v-else class="block truncate text-11.5px font-600 text-ink-primary">{{ item.title }}</b>
            <span class="mt-1px block truncate text-11px text-ink-tertiary">{{ item.preview }}</span>
            <span
              v-if="showProfileChip(item.conversation)"
              class="mt-2px inline-flex items-center gap-3px text-10px text-ink-tertiary"
              data-hermes-history-profile
            >
              <OnIcon name="lucide:users" :size="9" />
              {{ item.conversation.panelProfile }}
            </span>
          </span>
          <span class="flex shrink-0 items-center gap-2px">
            <button
              v-if="item.conversation.panel"
              type="button"
              class="flex h-20px w-20px items-center justify-center rounded-tiny text-ink-tertiary opacity-0 outline-none hover:bg-surface-control hover:text-ink-primary focus-visible:opacity-100 group-hover:opacity-100"
              :aria-label="item.conversation.panelArchived ? content.history.restore : content.history.archive"
              @click.stop="toggleArchived(item.conversation.id, !item.conversation.panelArchived)"
            >
              <OnIcon :name="item.conversation.panelArchived ? 'lucide:refresh-cw' : 'lucide:inbox'" :size="11" />
            </button>
            <button
              type="button"
              class="flex h-20px w-20px items-center justify-center rounded-tiny text-ink-tertiary opacity-0 outline-none hover:bg-surface-control hover:text-ink-primary focus-visible:opacity-100 group-hover:opacity-100"
              :aria-label="content.history.rename"
              @click.stop="startRename(item.conversation.id, item.title)"
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
        <button
          v-if="group.hidden > 0"
          type="button"
          class="mx-8px mb-4px flex w-[calc(100%-16px)] items-center gap-4px rounded-small px-8px py-4px text-left text-10.5px text-ink-tertiary outline-none hover:bg-surface-raised hover:text-ink-secondary focus-visible:shadow-focus-ring"
          :aria-label="content.history.showMore"
          @click="revealGroup(group)"
        >
          <OnIcon name="lucide:chevron-down" :size="10" />
          {{ content.history.showMore }}
          <span class="ml-auto">+{{ group.hidden }}</span>
        </button>
      </div>
      <button
        v-if="activeState?.hasMore"
        type="button"
        class="mx-8px mb-6px w-[calc(100%-16px)] rounded-small border border-solid border-line-subtle py-6px text-center text-10.5px text-ink-tertiary outline-none hover:bg-surface-raised hover:text-ink-secondary focus-visible:shadow-focus-ring"
        :disabled="activeState?.loading"
        @click="loadMore"
      >
        {{ content.history.loadMore }}
      </button>
    </div>
    <div
      class="flex flex-col gap-2px border-t border-t-solid border-line-subtle px-14px py-8px text-11px text-ink-tertiary"
    >
      <span class="flex items-center gap-5px">
        <OnIcon name="lucide:lock" :size="12" />
        {{ store.panelActive ? content.history.panel : content.history.local }}
      </span>
      <span v-if="activeState?.degraded" class="flex items-center gap-5px text-10.5px" data-hermes-history-degraded>
        <OnIcon name="lucide:triangle-alert" :size="11" />
        {{ content.history.degraded }}
      </span>
    </div>
  </div>
</template>
