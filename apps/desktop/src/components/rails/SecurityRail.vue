<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatBytes, formatRelativeTime } from '@opennavo/shared';
import { OnCard, OnIcon, OnRailPanel } from '@opennavo/ui';
import { useAppLocale } from '@/composables/useAppLocale';
import { useSecurityStore } from '@/stores/security';

// Security rail (plan §6, mockup E): engine and database, protection switches at a glance, and what the module does
// not do, so nobody mistakes it for a complete antivirus.
const { t } = useI18n();
const { appLocale } = useAppLocale();
const store = useSecurityStore();
const engine = computed(() => store.overview?.engine ?? null);
const protection = computed(() => store.overview?.protection ?? null);
</script>

<template>
  <OnRailPanel>
    <OnCard padding="md" class="flex flex-col gap-8px">
      <h2 class="m-0 flex items-center gap-7px text-14px font-600 text-ink-primary">
        <OnIcon name="shield" :size="15" />{{ t('security.rail.engine') }}
      </h2>
      <dl v-if="engine" class="m-0 flex flex-col gap-4px text-12.5px">
        <div class="flex justify-between gap-8px">
          <dt class="text-ink-tertiary">{{ t('security.rail.version') }}</dt>
          <dd class="m-0 text-ink-primary">{{ t('security.rail.engineVersion', { version: engine.version }) }}</dd>
        </div>
        <div class="flex justify-between gap-8px">
          <dt class="text-ink-tertiary">{{ t('security.rail.database') }}</dt>
          <dd class="m-0 text-right text-ink-primary">
            {{
              engine.db
                ? `${engine.db.daily ?? '—'} · ${engine.db.updatedAt ? formatRelativeTime(engine.db.updatedAt, { locale: appLocale }) : '—'}`
                : t('security.rail.noDatabase')
            }}
          </dd>
        </div>
        <div v-if="engine.db" class="flex justify-between gap-8px">
          <dt class="text-ink-tertiary">{{ t('security.rail.size') }}</dt>
          <dd class="m-0 text-ink-primary">{{ formatBytes(engine.db.bytes, { locale: appLocale }) }}</dd>
        </div>
        <div class="flex justify-between gap-8px">
          <dt class="text-ink-tertiary">{{ t('security.rail.engineState') }}</dt>
          <dd class="m-0 text-ink-primary">
            {{
              engine.running
                ? t('security.rail.running', {
                    size: engine.residentBytes ? formatBytes(engine.residentBytes, { locale: appLocale }) : '—'
                  })
                : t('security.rail.idle')
            }}
          </dd>
        </div>
      </dl>
    </OnCard>
    <OnCard v-if="protection" padding="md" class="flex flex-col gap-6px">
      <h2 class="m-0 flex items-center gap-7px text-14px font-600 text-ink-primary">
        <OnIcon name="shield-check" :size="15" />{{ t('security.rail.protection') }}
      </h2>
      <p class="m-0 text-12.5px text-ink-secondary">
        {{ t('security.protection.startupAlerts.title') }} ·
        {{ protection.startupAlerts ? t('security.rail.on') : t('security.rail.off') }}
      </p>
      <p class="m-0 text-12.5px text-ink-secondary">
        {{ t('security.protection.watchDownloads.title') }} ·
        {{ protection.watchDownloads ? t('security.rail.on') : t('security.rail.off') }}
      </p>
    </OnCard>
    <OnCard padding="md" class="flex flex-col gap-6px">
      <h2 class="m-0 flex items-center gap-7px text-14px font-600 text-ink-primary">
        <OnIcon name="info" :size="15" />{{ t('security.rail.limitsTitle') }}
      </h2>
      <p class="m-0 text-12px leading-[1.6] text-ink-tertiary">{{ t('security.rail.limits') }}</p>
    </OnCard>
  </OnRailPanel>
</template>
