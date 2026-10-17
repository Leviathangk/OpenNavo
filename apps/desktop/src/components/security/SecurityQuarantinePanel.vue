<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatBytes, formatRelativeTime } from '@opennavo/shared';
import { OnButton, OnChip, OnConfirm, OnEmpty, OnSkeleton } from '@opennavo/ui';
import type { QuarantineEntry } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useVirusTotal } from '@/composables/useVirusTotal';
import { useSecurityStore } from '@/stores/security';
import { shortPath } from './meta';
import { requireDone } from './outcome';

// Quarantine (plan §6.2): quarantined threats and removed startup items, with their execute bits off. Restore puts
// a file back where it was; Delete is permanent and asks first.
const { t } = useI18n();
const { appLocale } = useAppLocale();
const toasts = useToasts();
const { errorText } = usePackageState();
const store = useSecurityStore();
const virusTotal = useVirusTotal();
const loaded = ref(false);
const deleting = ref<QuarantineEntry | null>(null);

onMounted(async () => {
  await store.loadQuarantine().catch(() => undefined);
  loaded.value = true;
});

const reasonText = (entry: QuarantineEntry) =>
  entry.reason.kind === 'threat' ? entry.reason.signature : t(`security.quarantine.reason.${entry.reason.kind}`);

async function restore(entry: QuarantineEntry) {
  try {
    const outcome = await store.restore([entry.id]);
    requireDone(outcome, entry.id);
    toasts.push({
      tone: 'success',
      title: t('security.quarantine.restored', { name: entry.name })
    });
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

async function purge(entry: QuarantineEntry) {
  deleting.value = null;
  try {
    requireDone(await store.purge([entry.id]), entry.id);
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}
</script>

<template>
  <section aria-labelledby="quarantine-title">
    <h2 id="quarantine-title" class="sr-only">{{ t('security.tabs.quarantine') }}</h2>
    <ul v-if="store.quarantine.length" class="m-0 list-none rounded-big border border-solid border-line-subtle p-0">
      <li
        v-for="entry in store.quarantine"
        :key="entry.id"
        class="flex items-center gap-12px border-t border-t-solid border-line-subtle px-14px py-10px first:border-t-0"
      >
        <div class="min-w-0 flex-1">
          <p class="m-0 flex items-center gap-6px text-13px font-600 text-ink-primary">
            <span class="truncate">{{ entry.name }}</span>
            <OnChip :tone="entry.reason.kind === 'threat' ? 'danger' : 'warning'" size="sm">{{
              reasonText(entry)
            }}</OnChip>
          </p>
          <p class="m-0 truncate font-mono text-11.5px text-ink-tertiary">
            {{ shortPath(entry.originalPath, 80) }}
          </p>
        </div>
        <span class="hidden w-160px shrink-0 text-right text-12px text-ink-tertiary md:block">
          {{ formatBytes(entry.sizeBytes, { locale: appLocale }) }} ·
          {{ formatRelativeTime(entry.quarantinedAt, { locale: appLocale }) }}
        </span>
        <!-- A second opinion on a quarantined threat, by hash, once the user set up VirusTotal (D8). -->
        <OnButton
          v-if="store.virusTotalReady && entry.reason.kind === 'threat'"
          variant="ghost"
          size="sm"
          :loading="virusTotal.busy.value === entry.sha256"
          @click="virusTotal.lookup(entry.sha256)"
        >
          {{ t('security.virusTotal.lookup') }}
        </OnButton>
        <OnButton variant="secondary" size="sm" :disabled="store.busy.has(entry.id)" @click="restore(entry)">
          {{ t('security.quarantine.restore') }}
        </OnButton>
        <OnButton variant="danger" size="sm" :disabled="store.busy.has(entry.id)" @click="deleting = entry">
          {{ t('security.quarantine.delete') }}
        </OnButton>
      </li>
    </ul>
    <OnEmpty
      v-else-if="loaded"
      :title="t('security.quarantine.empty')"
      :description="t('security.quarantine.emptyHint')"
      icon="lucide:package"
    />
    <div v-else class="flex flex-col gap-8px" aria-hidden="true">
      <OnSkeleton v-for="index in 3" :key="index" height="52px" radius="default" />
    </div>
    <OnConfirm
      :open="Boolean(deleting)"
      :title="t('security.quarantine.deleteTitle', { name: deleting?.name ?? '' })"
      :description="t('security.quarantine.deleteBody')"
      :confirm-label="t('security.quarantine.delete')"
      tone="danger"
      @confirm="deleting && purge(deleting)"
      @cancel="deleting = null"
    />
  </section>
</template>
