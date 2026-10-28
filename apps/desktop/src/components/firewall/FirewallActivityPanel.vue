<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatRelativeTime } from '@opennavo/shared';
import { OnAppIcon, OnButton, OnChip, OnConfirm, OnEmpty, OnSegmented, OnSkeleton } from '@opennavo/ui';
import type { FirewallAction, FirewallEvent } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useFirewallStore } from '@/stores/firewall';
import { appIcon, endpoint, isBundle } from './meta';

// Connections the firewall decided without an existing allow rule (firewall plan §4.5): blocks, answers, automatic
// allows, and what passed while OpenNavo was not running to ask. Repeats within a minute share one row. Each row can
// turn into a rule in one click: for its app, or for its destination when a narrower rule or a block list decided it.
const { t } = useI18n();
const toasts = useToasts();
const { errorText } = usePackageState();
const { appLocale } = useAppLocale();
const store = useFirewallStore();

const loadError = ref<unknown>(null);
/** Rows wait for the rules behind them, so their buttons don't change wording once those arrive. */
const rulesReady = ref(store.rulesLoaded);
const loadingMore = ref(false);
const clearing = ref(false);
const confirmClear = ref(false);
/** What just got a rule from here (an app, or an app and destination), so those rows' buttons turn into a note. */
const ruled = ref(new Map<string, FirewallAction>());
/** Rows whose rule is being saved, so a double click saves it once. */
const saving = ref(new Set<string>());

onUnmounted(store.follow('activity', 'rules'));
onMounted(async () => {
  const rules = store.rulesLoaded ? undefined : store.loadRules().catch(() => undefined);
  try {
    await store.loadActivity();
  } catch (error) {
    loadError.value = error;
  }
  await rules;
  rulesReady.value = true;
});

const view = computed<'all' | 'blocked'>(() => (store.blockedOnly ? 'blocked' : 'all'));
const views = computed(() =>
  (['all', 'blocked'] as const).map(value => ({
    value,
    label: t(`firewall.activity.filters.${value}`)
  }))
);

async function setView(value: 'all' | 'blocked') {
  try {
    await store.loadActivity(value === 'blocked');
  } catch (error) {
    toasts.push({
      tone: 'danger',
      title: t('errors.actionFailed'),
      description: errorText(error)
    });
  }
}

async function more() {
  loadingMore.value = true;
  try {
    await store.loadMoreActivity();
  } catch (error) {
    toasts.push({
      tone: 'danger',
      title: t('errors.actionFailed'),
      description: errorText(error)
    });
  } finally {
    loadingMore.value = false;
  }
}

const destination = (event: FirewallEvent) => event.remoteHost ?? event.remoteAddress;
const detail = (event: FirewallEvent) =>
  [
    event.remoteHost ? event.remoteAddress : null,
    t('firewall.activity.port', { port: event.port }),
    event.protocol.toUpperCase()
  ]
    .filter(Boolean)
    .join(' · ');

const rulesById = computed(() => new Map(store.rules.map(rule => [rule.id, rule])));
/**
 * Where the opposite of what happened applies. Rust checks rules for an app and destination first, then rules for every
 * app, then block lists, and only then rules for a whole app: after one of the earlier ones decided a row, a rule for
 * the whole app would change nothing, so the row gets a rule for its own destination and port instead.
 */
function targetOf(event: FirewallEvent) {
  const rule = event.ruleId ? rulesById.value.get(event.ruleId) : undefined;
  const narrow =
    event.blocklistId !== null ||
    (event.ruleId !== null && (!rule || rule.app === null || rule.remote !== null || rule.port !== null));
  // Exact IP and hostname rules have equal priority, with block winning a tie. Reuse the
  // matched exact target so saveRule replaces it, rather than adding an ineffective host allow.
  const remote = rule?.remote;
  const exact =
    remote &&
    !remote.startsWith('*.') &&
    (!remote.includes('/') || remote.endsWith(remote.includes(':') ? '/128' : '/32'));
  const peer = remote?.split('/')[0];
  const isAddress = peer && (peer.includes(':') || /^\d+(\.\d+){3}$/.test(peer));
  const canonical = (value: string) => {
    try {
      // URL canonicalizes IPv6 compression; zones identify the local interface only.
      const host = value.replace(/^\[|\]$/g, '').split('%')[0]!;
      return new URL(`http://${host.includes(':') ? `[${host}]` : host}`).hostname;
    } catch {
      return value.toLowerCase().replace(/\.$/, '');
    }
  };
  const stillMatches =
    exact &&
    peer &&
    (rule.app === null || rule.app.key === event.app.key) &&
    (rule.port === null || rule.port === event.port) &&
    (isAddress
      ? canonical(peer) === canonical(event.remoteAddress)
      : event.remoteHost !== null && canonical(peer) === canonical(event.remoteHost));
  return narrow ? { remote: stillMatches ? remote : destination(event), port: event.port } : null;
}
// A rule for the whole app settles all of its rows; one for a destination only the rows going there.
const keyOf = (event: FirewallEvent, target: ReturnType<typeof targetOf>) =>
  target ? `${event.app.key}\n${target.remote}\n${target.port}` : event.app.key;
const rows = computed(() =>
  store.activity.map(event => {
    const target = targetOf(event);
    return { event, target, key: keyOf(event, target) };
  })
);
const opposite = (event: FirewallEvent): FirewallAction => (event.action === 'block' ? 'allow' : 'block');

async function ruleFor(event: FirewallEvent) {
  if (saving.value.has(event.id)) return;
  saving.value = new Set(saving.value).add(event.id);
  try {
    // Rules that could not be read with the page are needed to tell what decided the row.
    if (event.ruleId && !store.rulesLoaded) await store.loadRules();
    const target = targetOf(event);
    const key = keyOf(event, target);
    const action = opposite(event);
    const saved = await store.saveRule({
      id: null,
      appKey: event.app.key,
      appPath: null,
      action,
      remote: target?.remote ?? null,
      port: target?.port ?? null
    });
    if (!saved) return;
    ruled.value = new Map(ruled.value).set(key, action);
    toasts.push({
      tone: 'success',
      title: target
        ? t(`firewall.activity.ruled.${action}Destination`, {
            name: event.app.name,
            destination: endpoint(target.remote, target.port)
          })
        : t(`firewall.activity.ruled.${action}`, { name: event.app.name })
    });
  } catch (error) {
    toasts.push({
      tone: 'danger',
      title: t('errors.actionFailed'),
      description: errorText(error)
    });
  } finally {
    const next = new Set(saving.value);
    next.delete(event.id);
    saving.value = next;
  }
}

async function clear() {
  clearing.value = true;
  try {
    if (!(await store.clearActivity())) return;
    confirmClear.value = false;
  } catch (error) {
    toasts.push({
      tone: 'danger',
      title: t('errors.actionFailed'),
      description: errorText(error)
    });
  } finally {
    clearing.value = false;
  }
}
</script>

<template>
  <div class="flex flex-col gap-12px">
    <div class="flex flex-wrap items-center gap-10px">
      <OnSegmented
        :model-value="view"
        :options="views"
        size="sm"
        :aria-label="t('firewall.activity.filterLabel')"
        @update:model-value="setView"
      />
      <span class="text-12px text-ink-tertiary">{{ t('firewall.activity.hint') }}</span>
      <OnButton
        v-if="store.activity.length"
        class="ml-auto"
        variant="ghost"
        size="sm"
        icon="trash"
        @click="confirmClear = true"
        >{{ t('firewall.activity.clear') }}</OnButton
      >
    </div>

    <div v-if="!(store.activityLoaded && rulesReady) && !loadError" class="flex flex-col gap-8px" aria-hidden="true">
      <OnSkeleton v-for="index in 6" :key="index" height="46px" radius="default" />
    </div>
    <p v-else-if="loadError && !store.activityLoaded" class="m-0 text-13px text-status-danger" role="alert">
      {{ errorText(loadError) }}
    </p>
    <template v-else-if="store.activity.length">
      <ul class="m-0 list-none rounded-big border border-solid border-line-subtle p-0">
        <li
          v-for="{ event, target, key } in rows"
          :key="event.id"
          class="flex items-center gap-12px border-t border-t-solid border-line-subtle px-14px py-9px first:border-t-0"
          :data-event="event.id"
        >
          <OnAppIcon
            v-if="isBundle(event.app)"
            kind="cask"
            :token="event.app.bundleId ?? event.app.key"
            :name="event.app.name"
            :src="appIcon(event.app)"
            :size="28"
          />
          <OnAppIcon v-else kind="formula" :token="event.app.name" :name="event.app.name" :size="28" />
          <div class="min-w-0 flex-1">
            <p class="m-0 flex min-w-0 items-center gap-6px text-13px">
              <span class="shrink-0 font-600 text-ink-primary">{{ event.app.name }}</span>
              <span class="shrink-0 text-ink-tertiary">→</span>
              <span class="min-w-0 truncate font-mono text-12.5px text-ink-primary" :title="destination(event)">{{
                destination(event)
              }}</span>
            </p>
            <p class="m-0 truncate text-11.5px text-ink-tertiary">
              {{ detail(event) }} ·
              {{ t(`firewall.activity.reason.${event.reason}`) }}
            </p>
          </div>
          <span class="hidden w-110px shrink-0 text-right text-11.5px text-ink-tertiary md:block">
            {{ formatRelativeTime(event.at, { locale: appLocale }) }}
            <template v-if="event.count > 1">
              ·
              {{ t('firewall.activity.count', { count: event.count }) }}</template
            >
          </span>
          <OnChip :tone="event.action === 'allow' ? 'success' : 'danger'" size="sm" class="shrink-0">{{
            t(`firewall.activity.action.${event.action}`)
          }}</OnChip>
          <span class="flex min-w-108px shrink-0 justify-end">
            <span v-if="ruled.get(key)" class="text-11.5px text-ink-tertiary">{{
              t(`firewall.activity.ruledShort.${ruled.get(key)}`)
            }}</span>
            <OnButton v-else variant="ghost" size="xs" :loading="saving.has(event.id)" @click="ruleFor(event)">{{
              t(`firewall.activity.${opposite(event)}${target ? 'Destination' : 'App'}`)
            }}</OnButton>
          </span>
        </li>
      </ul>
      <OnButton
        v-if="store.activityMore"
        class="self-center"
        variant="ghost"
        size="sm"
        :loading="loadingMore"
        @click="more"
      >
        {{ t('firewall.activity.more') }}
      </OnButton>
    </template>
    <OnEmpty
      v-else
      :title="store.blockedOnly ? t('firewall.activity.emptyBlocked') : t('firewall.activity.empty')"
      :description="t('firewall.activity.emptyBody')"
      icon="lucide:clock"
    />

    <OnConfirm
      :open="confirmClear"
      :title="t('firewall.activity.clearTitle')"
      :description="t('firewall.activity.clearBody')"
      :confirm-label="t('firewall.activity.clear')"
      tone="danger"
      :loading="clearing"
      @confirm="clear"
      @cancel="confirmClear = false"
    />
  </div>
</template>
