<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatCount, formatRelativeTime } from '@opennavo/shared';
import { OnButton, OnConfirm, OnEmpty, OnIcon, OnSkeleton, OnToggle } from '@opennavo/ui';
import type { FirewallBlocklist } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useFirewallStore } from '@/stores/firewall';
import { pickListFile } from '@/utils/files';

// Block lists (firewall plan §4.6): connections to a listed site or address are blocked without asking. Lists come
// from an https address (refreshed daily) or a file on this Mac; hosts files and one entry per line both work.
const { t, te } = useI18n();
const toasts = useToasts();
const { errorText } = usePackageState();
const { appLocale } = useAppLocale();
const store = useFirewallStore();

const loadError = ref<unknown>(null);
const source = ref('');
const adding = ref(false);
const removing = ref<FirewallBlocklist | null>(null);

onUnmounted(store.follow('blocklists'));
onMounted(async () => {
  try {
    await store.loadBlocklists();
  } catch (error) {
    loadError.value = error;
  }
});

// Sources go to Rust as typed, only trimmed: it accepts https addresses in any case and paths starting with ~/ too.
async function add(value = source.value.trim()) {
  // A second Return while a list is still being added would start the same add again, and report it as failed.
  if (!value || adding.value) return;
  adding.value = true;
  try {
    const list = await store.addBlocklist(value);
    if (!list) return;
    source.value = '';
    toasts.push({
      tone: 'success',
      title: t('firewall.blocklists.added', {
        name: list.name,
        count: formatCount(list.entries, { locale: appLocale.value })
      })
    });
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('firewall.blocklists.addFailed'), description: errorText(error) });
  } finally {
    adding.value = false;
  }
}

async function addFile() {
  const path = await pickListFile();
  if (path) await add(path);
}

async function run(action: () => Promise<unknown>) {
  try {
    await action();
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

async function confirmRemove() {
  const list = removing.value;
  if (!list) return;
  await run(async () => {
    if (await store.removeBlocklist(list.id)) removing.value = null;
  });
}

const failure = (list: FirewallBlocklist) => {
  if (!list.error) return null;
  const key = `errors.${list.error}`;
  return te(key) ? t(key) : list.error;
};
const isUrl = (list: FirewallBlocklist) => /^https:\/\//i.test(list.source);
</script>

<template>
  <div class="flex flex-col gap-12px">
    <p class="m-0 text-12.5px leading-[1.6] text-ink-secondary">{{ t('firewall.blocklists.intro') }}</p>
    <form class="flex flex-wrap items-center gap-8px" @submit.prevent="add()">
      <input
        v-model="source"
        type="text"
        spellcheck="false"
        autocapitalize="off"
        :aria-label="t('firewall.blocklists.source')"
        :placeholder="t('firewall.blocklists.placeholder')"
        class="box-border h-30px min-w-240px flex-1 rounded-small border border-solid border-line-default bg-surface-control px-8px font-mono text-12.5px text-ink-primary outline-none focus-visible:shadow-focus-ring"
      />
      <OnButton type="submit" variant="primary" size="sm" icon="plus" :loading="adding" :disabled="!source.trim()">{{
        t('firewall.blocklists.add')
      }}</OnButton>
      <OnButton size="sm" icon="folder" :disabled="adding" @click="addFile">{{
        t('firewall.blocklists.chooseFile')
      }}</OnButton>
    </form>

    <div v-if="!store.blocklistsLoaded && !loadError" class="flex flex-col gap-8px" aria-hidden="true">
      <OnSkeleton v-for="index in 2" :key="index" height="56px" radius="default" />
    </div>
    <p v-else-if="loadError && !store.blocklistsLoaded" class="m-0 text-13px text-status-danger" role="alert">
      {{ errorText(loadError) }}
    </p>
    <ul
      v-else-if="store.blocklists.length"
      class="m-0 list-none rounded-big border border-solid border-line-subtle p-0"
    >
      <li
        v-for="list in store.blocklists"
        :key="list.id"
        class="flex items-center gap-12px border-t border-t-solid border-line-subtle px-14px py-10px first:border-t-0"
        :data-blocklist="list.id"
      >
        <OnToggle
          :model-value="list.enabled"
          :disabled="store.busy.has(list.id)"
          :aria-label="t('firewall.blocklists.toggle', { name: list.name })"
          @update:model-value="(on: boolean) => run(() => store.setBlocklist(list.id, on))"
        />
        <OnIcon :name="isUrl(list) ? 'globe' : 'file-text'" :size="16" class="shrink-0 text-ink-tertiary" />
        <div class="min-w-0 flex-1">
          <p class="m-0 truncate text-13px font-600 text-ink-primary">{{ list.name }}</p>
          <p class="m-0 truncate font-mono text-11.5px text-ink-tertiary" :title="list.source">{{ list.source }}</p>
          <p v-if="failure(list)" class="m-0 mt-2px text-12px text-status-danger" role="status">{{ failure(list) }}</p>
        </div>
        <span class="hidden w-170px shrink-0 text-right text-11.5px text-ink-tertiary md:block">
          {{
            t(
              'firewall.blocklists.entries',
              { count: formatCount(list.entries, { locale: appLocale }) },
              { plural: list.entries }
            )
          }}
          <template v-if="list.updatedAt">
            ·
            {{ t('firewall.blocklists.updated', { time: formatRelativeTime(list.updatedAt, { locale: appLocale }) }) }}
          </template>
        </span>
        <OnButton
          variant="ghost"
          size="xs"
          icon="refresh-cw"
          icon-only
          :aria-label="t('firewall.blocklists.refresh', { name: list.name })"
          :loading="store.busy.has(list.id)"
          @click="run(() => store.refreshBlocklist(list.id))"
        />
        <OnButton
          variant="ghost"
          size="xs"
          icon="trash"
          icon-only
          :aria-label="t('firewall.blocklists.remove', { name: list.name })"
          :disabled="store.busy.has(list.id)"
          @click="removing = list"
        />
      </li>
    </ul>
    <OnEmpty
      v-else
      :title="t('firewall.blocklists.empty')"
      :description="t('firewall.blocklists.emptyBody')"
      icon="lucide:shield"
    />
    <p class="m-0 flex items-start gap-6px text-12px leading-[1.55] text-ink-tertiary">
      <OnIcon name="info" :size="13" class="mt-2px shrink-0" />{{ t('firewall.blocklists.boundary') }}
    </p>

    <OnConfirm
      :open="removing !== null"
      :title="t('firewall.blocklists.removeTitle')"
      :description="removing ? t('firewall.blocklists.removeBody', { name: removing.name }) : undefined"
      :confirm-label="t('firewall.blocklists.removeAction')"
      tone="danger"
      @confirm="confirmRemove"
      @cancel="removing = null"
    />
  </div>
</template>
