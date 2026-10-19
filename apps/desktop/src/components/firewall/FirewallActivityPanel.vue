<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatRelativeTime } from '@opennavo/shared';
import { OnAppIcon, OnButton, OnChip, OnConfirm, OnEmpty, OnSegmented, OnSkeleton } from '@opennavo/ui';
import type { FirewallAction, FirewallEvent } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useFirewallStore } from '@/stores/firewall';
import { appIcon, isBundle } from './meta';

// Connections the firewall decided without an existing allow rule (firewall plan §4.5): blocks, answers, automatic
// allows, and what passed while OpenNavo was not running to ask. Repeats within a minute share one row. Each row can
// turn into a rule for its app in one click.
const { t } = useI18n();
const toasts = useToasts();
const { errorText } = usePackageState();
const { appLocale } = useAppLocale();
const store = useFirewallStore();

const loadError = ref<unknown>(null);
const loadingMore = ref(false);
const clearing = ref(false);
const confirmClear = ref(false);
/** Rows whose app just got a rule from here, so the button turns into a note. */
const ruled = ref(new Map<string, FirewallAction>());

onMounted(async () => {
  try {
    await store.loadActivity();
  } catch (error) {
    loadError.value = error;
  }
});

const view = computed<'all' | 'blocked'>(() => (store.blockedOnly ? 'blocked' : 'all'));
const views = computed(() =>
  (['all', 'blocked'] as const).map(value => ({ value, label: t(`firewall.activity.filters.${value}`) }))
);

async function setView(value: 'all' | 'blocked') {
  try {
    await store.loadActivity(value === 'blocked');
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

async function more() {
  loadingMore.value = true;
  try {
    await store.loadMoreActivity();
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
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

/** The opposite of what happened becomes the app's rule for any destination. */
async function ruleFor(event: FirewallEvent) {
  const action: FirewallAction = event.action === 'block' ? 'allow' : 'block';
  try {
    await store.saveRule({ id: null, appKey: event.app.key, appPath: null, action, remote: null, port: null });
    ruled.value = new Map(ruled.value).set(event.app.key, action);
    toasts.push({
      tone: 'success',
      title: t(`firewall.activity.ruled.${action}`, { name: event.app.name })
    });
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

async function clear() {
  clearing.value = true;
  try {
    await store.clearActivity();
    confirmClear.value = false;
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
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

    <div v-if="!store.activityLoaded && !loadError" class="flex flex-col gap-8px" aria-hidden="true">
      <OnSkeleton v-for="index in 6" :key="index" height="46px" radius="default" />
    </div>
    <p v-else-if="loadError && !store.activityLoaded" class="m-0 text-13px text-status-danger" role="alert">
      {{ errorText(loadError) }}
    </p>
    <template v-else-if="store.activity.length">
      <ul class="m-0 list-none rounded-big border border-solid border-line-subtle p-0">
        <li
          v-for="event in store.activity"
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
              {{ detail(event) }} · {{ t(`firewall.activity.reason.${event.reason}`) }}
            </p>
          </div>
          <span class="hidden w-110px shrink-0 text-right text-11.5px text-ink-tertiary md:block">
            {{ formatRelativeTime(event.at, { locale: appLocale }) }}
            <template v-if="event.count > 1"> · {{ t('firewall.activity.count', { count: event.count }) }}</template>
          </span>
          <OnChip :tone="event.action === 'allow' ? 'success' : 'danger'" size="sm" class="shrink-0">{{
            t(`firewall.activity.action.${event.action}`)
          }}</OnChip>
          <span class="flex w-108px shrink-0 justify-end">
            <span v-if="ruled.get(event.app.key)" class="text-11.5px text-ink-tertiary">{{
              t(`firewall.activity.ruledShort.${ruled.get(event.app.key)}`)
            }}</span>
            <OnButton v-else variant="ghost" size="xs" @click="ruleFor(event)">{{
              event.action === 'block' ? t('firewall.activity.allowApp') : t('firewall.activity.blockApp')
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
