<script setup lang="ts">
import { computed, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnAppIcon, OnButton, OnChip, OnIcon, OnToggle } from '@opennavo/ui';
import type { StartupAction, StartupItem } from '@/ipc/bindings';
import StartupItemControl from './StartupItemControl.vue';
import {
  appTitle,
  developer,
  ownerIcon,
  programName,
  running,
  shortPath,
  signatureKey,
  signatureTone,
  switchable,
  TRUST_TONE,
  worstTrust
} from './meta';
import type { StartupApp } from './meta';

// One app and everything it starts: its icon and name, who signed it, and whether it runs. One switch turns all of an
// app's startup items on or off; the app opens in place to switch each item on its own.
const props = defineProps<{ app: StartupApp; busy: ReadonlySet<string> }>();
const emit = defineEmits<{
  select: [item: StartupItem];
  act: [item: StartupItem, action: StartupAction];
  actMany: [items: StartupItem[], action: 'enable' | 'disable'];
}>();

const { t } = useI18n();
const open = ref(false);

const items = computed(() => props.app.items);
const single = computed(() => (items.value.length === 1 ? items.value[0] : null));
const kind = (item: StartupItem) => t(`security.startup.category.${item.category}`);
const subtitle = computed(() => {
  const item = single.value;
  if (!props.app.owner && item) return shortPath(item.program ? [item.program, ...item.args].join(' ') : item.path, 80);
  const what = !item
    ? [...new Set(items.value.map(kind))].join(' · ')
    : item.name === props.app.owner?.name
      ? kind(item)
      : `${kind(item)} · ${item.name}`;
  return props.app.file ? `${props.app.file} · ${what}` : what;
});

const trust = computed(() => worstTrust(items.value));
const signer = computed(() => items.value.map(developer).find(Boolean) ?? null);
// The signature that most needs a look speaks for the whole app.
const TONES = { danger: 0, warning: 1 } as const;
const toneRank = (item: StartupItem) => {
  const tone = signatureTone(item);
  return tone ? TONES[tone] : 2;
};
const signature = computed(() => items.value.reduce((pick, item) => (toneRank(item) < toneRank(pick) ? item : pick)));
const toneClass = (item: StartupItem) => {
  const tone = signatureTone(item);
  return tone === 'danger' ? 'text-status-danger' : tone === 'warning' ? 'text-status-warning' : 'text-ink-tertiary';
};

const state = computed(() => {
  const list = items.value;
  if (list.every(item => item.state === list[0].state)) return t(`security.startup.state.${list[0].state}`);
  const count = list.filter(running).length;
  if (count === list.length) return t('security.startup.state.enabled');
  if (count === 0) return t('security.startup.state.disabled');
  // The total is on the button that opens the list.
  return t('security.startup.stateMixed', { count }, { plural: count });
});

// The app's switch: on while any of its switchable items starts. Off switches all of them off; on, all back on.
const switches = computed(() => items.value.filter(switchable));
const switchedOn = computed(() => switches.value.some(running));
const switching = computed(() => switches.value.some(item => props.busy.has(item.id)));
function switchAll(on: boolean) {
  const targets = switches.value.filter(item => running(item) !== on);
  if (targets.length) emit('actMany', targets, on ? 'enable' : 'disable');
}

function choose() {
  if (single.value) emit('select', single.value);
  else open.value = !open.value;
}
function actOnSingle(action: StartupAction) {
  if (single.value) emit('act', single.value, action);
}
</script>

<template>
  <li
    class="border-t border-t-solid border-line-subtle first:border-t-0"
    :class="items.some(item => item.isNew) ? 'bg-status-warning-subtle' : ''"
  >
    <div class="flex items-center gap-12px px-14px py-10px">
      <OnAppIcon
        v-if="app.owner"
        kind="cask"
        :token="app.owner.bundleId ?? app.owner.name"
        :name="app.owner.name"
        :src="ownerIcon(app.owner)"
        :size="32"
      />
      <OnAppIcon v-else kind="formula" :token="programName(items[0])" :name="items[0].name" :size="32" />
      <div class="min-w-0 flex-1">
        <p class="m-0 flex items-center gap-6px">
          <button
            type="button"
            class="m-0 min-w-0 cursor-pointer truncate border-none bg-transparent p-0 text-left font-sans text-13px font-600 text-ink-primary outline-none hover:underline focus-visible:underline"
            :aria-expanded="single ? undefined : open"
            @click="choose"
          >
            {{ appTitle(app) }}
          </button>
          <OnChip v-if="items.some(item => item.isNew)" tone="warning" size="sm">{{
            t('security.startup.new')
          }}</OnChip>
        </p>
        <p class="m-0 truncate text-11.5px text-ink-tertiary" :class="app.owner ? '' : 'font-mono'" :title="subtitle">
          {{ subtitle }}
        </p>
      </div>
      <span class="hidden w-200px shrink-0 md:block">
        <OnChip :tone="TRUST_TONE[trust]" size="sm" class="max-w-full" :title="signature.signature.signer ?? undefined">
          <span class="min-w-0 truncate">{{ signer ?? t(`security.startup.trust.${trust}`) }}</span>
        </OnChip>
        <span class="mt-2px block truncate text-11px" :class="toneClass(signature)">{{
          t(signatureKey(signature))
        }}</span>
      </span>
      <span class="w-90px shrink-0 text-12px text-ink-secondary">{{ state }}</span>
      <span class="flex w-130px shrink-0 items-center justify-end gap-6px">
        <StartupItemControl
          v-if="single"
          :item="single"
          :busy="busy.has(single.id)"
          :label="appTitle(app)"
          @act="actOnSingle"
        />
        <template v-else>
          <OnButton variant="ghost" size="xs" :aria-expanded="open" @click="open = !open">
            {{ t('security.startup.items', { count: items.length }, { plural: items.length }) }}
            <OnIcon :name="open ? 'chevron-up' : 'chevron-down'" :size="12" />
          </OnButton>
          <OnToggle
            v-if="switches.length"
            :model-value="switchedOn"
            :disabled="switching"
            :aria-label="t('security.startup.toggle', { name: appTitle(app) })"
            @update:model-value="switchAll"
          />
        </template>
      </span>
    </div>
    <ul v-if="!single && open" class="m-0 list-none p-0 pb-6px">
      <li v-for="item in items" :key="item.id" class="flex items-center gap-12px py-6px pl-58px pr-14px">
        <div class="min-w-0 flex-1">
          <p class="m-0 flex items-center gap-6px">
            <button
              type="button"
              class="m-0 min-w-0 cursor-pointer truncate border-none bg-transparent p-0 text-left font-sans text-12.5px font-500 text-ink-primary outline-none hover:underline focus-visible:underline"
              @click="emit('select', item)"
            >
              {{ item.name }}
            </button>
            <OnChip v-if="item.isNew" tone="warning" size="sm">{{ t('security.startup.new') }}</OnChip>
          </p>
          <p class="m-0 truncate text-11px text-ink-tertiary">
            {{ kind(item) }} · {{ t(`security.startup.scope.${item.scope}`) }}
          </p>
        </div>
        <span class="hidden w-200px shrink-0 truncate text-11px md:block" :class="toneClass(item)">{{
          t(signatureKey(item))
        }}</span>
        <span class="w-90px shrink-0 text-12px text-ink-secondary">{{
          t(`security.startup.state.${item.state}`)
        }}</span>
        <span class="flex w-130px shrink-0 justify-end">
          <StartupItemControl :item="item" :busy="busy.has(item.id)" @act="action => emit('act', item, action)" />
        </span>
      </li>
    </ul>
  </li>
</template>
