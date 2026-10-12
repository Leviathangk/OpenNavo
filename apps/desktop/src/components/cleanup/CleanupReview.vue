<script setup lang="ts">
import { computed, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatBytes } from '@opennavo/shared';
import { OnButton, OnChip, OnIcon } from '@opennavo/ui';
import { commands, unwrap } from '@/ipc/client';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { RISKS, useCleanupStore } from '@/stores/cleanup';
import type { CleanupEntry } from '@/stores/cleanup';
import CleanupCategoryCard from './CleanupCategoryCard.vue';
import CleanupConfirm from './CleanupConfirm.vue';
import CleanupNotices from './CleanupNotices.vue';
import { RISK_TONES } from './meta';

// Review (cleanup proposal §03 ③): totals, notices, categories by risk tier, then confirm.
const { t } = useI18n();
const { appLocale } = useAppLocale();
const store = useCleanupStore();
const toasts = useToasts();
const { errorText } = usePackageState();

const confirmOpen = ref(false);
const format = (bytes: number) => formatBytes(bytes, { locale: appLocale.value });
const totals = computed(() => store.totals);
const sections = computed(() =>
  RISKS.map(risk => ({
    risk,
    categories: store.categories.filter(category => category.risk === risk)
  })).filter(section => section.categories.length)
);
const footnote = computed(() => {
  const { nowBytes, trashBytes } = totals.value;
  if (nowBytes && trashBytes) return t('cleanup.footer.both', { delete: format(nowBytes), trash: format(trashBytes) });
  if (nowBytes) return t('cleanup.footer.deleteOnly', { size: format(nowBytes) });
  if (trashBytes) return t('cleanup.footer.trashOnly');
  return t('cleanup.footer.nothing');
});

const failed = (error: unknown) =>
  toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });

async function reveal(entry: CleanupEntry) {
  if (!store.report) return;
  await unwrap(commands.cleanupReveal(store.report.reportId, entry.item.id)).catch(failed);
}

async function protect(entry: CleanupEntry) {
  try {
    const rule = await store.protect(entry);
    if (!rule) return;
    toasts.pushAction(
      {
        tone: 'success',
        title: t('cleanup.toasts.protected'),
        description: t('cleanup.toasts.protectedBody'),
        actionLabel: t('cleanup.toasts.undo')
      },
      () => void store.unprotect(rule, entry).catch(failed)
    );
  } catch (error) {
    failed(error);
  }
}

async function run() {
  try {
    await store.confirm();
  } catch (error) {
    failed(error);
  } finally {
    confirmOpen.value = false;
  }
}
</script>

<template>
  <section
    class="mb-12px flex items-center gap-22px rounded-big border border-solid border-line-subtle bg-surface-card py-16px pl-20px pr-18px"
  >
    <div class="shrink-0">
      <div class="mb-6px text-12px text-ink-tertiary">{{ t('cleanup.summary.reclaimable') }}</div>
      <div class="whitespace-nowrap text-30px font-600 leading-none tracking-[-0.02em] tabular-nums text-ink-primary">
        {{ format(totals.reclaimable) }}
      </div>
      <div class="mt-6px text-12px text-ink-tertiary">
        {{ t('cleanup.summary.excludesPersonal') }}
      </div>
      <div v-if="store.inUse" class="mt-2px flex items-center gap-4px text-12px text-ink-tertiary">
        <OnIcon name="power" :size="12" class="shrink-0" />{{
          t('cleanup.summary.inUse', { size: format(store.inUse) })
        }}
      </div>
    </div>
    <span class="w-1px self-stretch bg-line-subtle" aria-hidden="true"></span>
    <div class="min-w-0 flex-1">
      <div class="mb-6px text-12px text-ink-tertiary">
        {{ t('cleanup.summary.selected', { count: totals.count }, { plural: totals.count }) }}
      </div>
      <div class="whitespace-nowrap text-20px font-600 leading-none tracking-[-0.01em] tabular-nums text-ink-primary">
        {{ format(totals.bytes) }}
      </div>
      <div class="mt-6px text-12px leading-[1.45] text-ink-tertiary">
        {{
          t('cleanup.summary.breakdown', {
            delete: format(totals.nowBytes),
            trash: format(totals.trashBytes)
          })
        }}
      </div>
    </div>
    <div class="flex shrink-0 flex-col items-stretch gap-6px">
      <OnButton variant="ghost" size="sm" @click="store.onlyRebuildable()">{{
        t('cleanup.summary.onlyRebuildable')
      }}</OnButton>
      <OnButton variant="ghost" size="sm" @click="store.recommended()">{{ t('cleanup.summary.recommended') }}</OnButton>
    </div>
  </section>

  <CleanupNotices />

  <template v-for="section in sections" :key="section.risk">
    <div class="mx-2px mb-8px mt-18px flex items-center gap-8px">
      <OnChip size="sm" :tone="RISK_TONES[section.risk]">{{ t(`cleanup.risks.${section.risk}`) }}</OnChip>
      <span class="text-12px text-ink-tertiary">{{ t(`cleanup.riskHints.${section.risk}`) }}</span>
    </div>
    <div class="flex flex-col gap-8px">
      <CleanupCategoryCard
        v-for="category in section.categories"
        :key="category.key"
        :category="category"
        @reveal="reveal"
        @protect="protect"
      />
    </div>
  </template>

  <div class="sticky bottom-12px z-sticky mt-14px">
    <div
      class="flex items-center gap-12px rounded-big border border-solid border-line-default bg-component-toast-bg py-10px pl-16px pr-10px shadow-toast backdrop-blur-[20px]"
      role="region"
      :aria-label="t('cleanup.title')"
    >
      <span class="min-w-0 flex-1">
        <span class="block text-13px font-600 text-ink-primary">{{
          t('cleanup.footer.selected', { count: totals.count, size: format(totals.bytes) }, { plural: totals.count })
        }}</span>
        <span class="block truncate text-12px text-ink-tertiary">{{ footnote }}</span>
      </span>
      <OnButton
        variant="danger"
        icon="trash"
        :disabled="!totals.count || store.stage !== 'review'"
        @click="confirmOpen = true"
        >{{ t('cleanup.footer.clean', { size: format(totals.bytes) }) }}</OnButton
      >
    </div>
  </div>

  <CleanupConfirm v-model:open="confirmOpen" @confirm="run" />
</template>
