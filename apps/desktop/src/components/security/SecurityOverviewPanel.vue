<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { formatBytes, formatDate, formatRelativeTime } from '@opennavo/shared';
import { OnButton, OnCard, OnChip, OnIcon, OnSkeleton } from '@opennavo/ui';
import SignatureCheckCard from './SignatureCheckCard.vue';
import type { PostureCheck } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageState } from '@/composables/usePackageState';
import { useRefreshOnReturn } from '@/composables/useRefreshOnReturn';
import { useToasts } from '@/composables/useToasts';
import { useSecurityStore } from '@/stores/security';
import { openExternal } from '@/utils/external';
import { POSTURE_TONE } from './meta';

// Overview (plan §6.1, mockup E): what needs attention first, then the read-only check-up with a way to fix each item
// in System Settings, then engine, startup items and quarantine at a glance. OpenNavo never changes system settings.
const { t } = useI18n();
const { appLocale } = useAppLocale();
const router = useRouter();
const toasts = useToasts();
const { errorText } = usePackageState();
const store = useSecurityStore();
// Each check has a button into System Settings; what the user changed there shows as soon as they are back.
useRefreshOnReturn(() => store.loadOverview());

const overview = computed(() => store.overview);
const problems = computed(() => overview.value?.posture.filter(check => check.state === 'bad') ?? []);
const issues = computed(() => {
  const value = overview.value;
  if (!value) return [];
  const list = problems.value.map(check => t(`security.posture.${check.id}.bad`));
  if (value.startup.pendingReview)
    list.push(
      t('security.overview.newStartup', { count: value.startup.pendingReview }, { plural: value.startup.pendingReview })
    );
  // Attention counts open threats and new startup items; the difference is what scans left unhandled.
  const threats = value.attention - value.startup.pendingReview;
  if (threats > 0) list.push(t('security.overview.openThreats', { count: threats }, { plural: threats }));
  return list;
});
const ready = computed(() => Boolean(overview.value?.engine.db));
// Scheduled quick scans (S2): when they run, as set in Settings → Security.
const scheduleText = computed(() => {
  const { avSchedule, avScheduleTime, avScheduleWeekday } = store.securitySettings;
  if (avSchedule === 'off') return t('security.overview.scheduleOff');
  if (avSchedule === 'daily') return t('security.overview.scheduleDaily', { time: avScheduleTime });
  // 2024-01-01 was a Monday, so day n of that week is weekday n (Monday = 1).
  const day = new Intl.DateTimeFormat(appLocale.value, { weekday: 'long' }).format(
    new Date(2024, 0, avScheduleWeekday)
  );
  return t('security.overview.scheduleWeekly', { day, time: avScheduleTime });
});

// Sharing services Rust checks, by launchd label.
const SHARING_SERVICES = [
  ['com.openssh.sshd', 'ssh'],
  ['com.apple.smbd', 'files'],
  ['com.apple.screensharing', 'screen'],
  ['com.apple.RemoteDesktop.agent', 'remote']
] as const;

function detail(check: PostureCheck): string {
  const facts = check.facts;
  if (check.id === 'xprotect' && typeof facts.version === 'number')
    return t('security.posture.xprotect.detail', {
      version: facts.version,
      time: typeof facts.updatedAt === 'number' ? formatRelativeTime(facts.updatedAt, { locale: appLocale.value }) : '—'
    });
  if (check.id === 'sharing' && check.state === 'warn') {
    const on = SHARING_SERVICES.filter(([label]) => facts[label] === true).map(([, key]) =>
      t(`security.posture.sharing.services.${key}`)
    );
    if (on.length)
      return t('security.posture.sharing.detail', {
        services: new Intl.ListFormat(appLocale.value, { type: 'conjunction' }).format(on)
      });
  }
  // info is a finding with no risk, such as every sharing service being off.
  const tone = check.state === 'ok' || check.state === 'info' ? 'ok' : check.state === 'unknown' ? 'unknown' : 'bad';
  return t(`security.posture.${check.id}.${tone}`);
}

async function fix(check: PostureCheck) {
  if (!check.settingsUrl) return;
  try {
    await openExternal(check.settingsUrl);
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

async function quickScan() {
  try {
    await store.startScan('quick');
    await router.push('/security/scan');
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('security.scan.failed'), description: errorText(error) });
  }
}
</script>

<template>
  <div v-if="overview" class="flex flex-col gap-16px">
    <section
      class="flex flex-wrap items-center gap-16px rounded-huge border border-solid border-line-subtle px-20px py-18px"
      :class="issues.length ? 'bg-status-warning-subtle' : 'bg-status-success-subtle'"
      aria-live="polite"
    >
      <span
        class="grid h-48px w-48px shrink-0 place-items-center rounded-big"
        :class="
          issues.length
            ? 'bg-status-warning-subtle text-status-warning'
            : 'bg-status-success-subtle text-status-success'
        "
        aria-hidden="true"
      >
        <OnIcon :name="issues.length ? 'shield' : 'shield-check'" :size="24" />
      </span>
      <div class="min-w-0 flex-1">
        <h2 class="m-0 text-17px font-600 text-ink-primary">
          {{
            issues.length
              ? t('security.overview.attention', { count: issues.length }, { plural: issues.length })
              : t('security.overview.allGood')
          }}
        </h2>
        <p class="m-0 mt-2px text-13px text-ink-secondary">
          {{ issues.length ? issues.join(' · ') : t('security.overview.allGoodHint') }}
        </p>
      </div>
      <div class="flex shrink-0 gap-8px">
        <OnButton v-if="ready" variant="primary" icon="scan-search" :disabled="store.scanning" @click="quickScan">
          {{ t('security.scan.quick.action') }}
        </OnButton>
        <OnButton v-else variant="primary" icon="download" @click="router.push('/security/scan')">
          {{ t('security.engine.setUp') }}
        </OnButton>
        <OnButton variant="secondary" @click="router.push('/security/startup')">{{
          t('security.overview.viewStartup')
        }}</OnButton>
      </div>
    </section>

    <div class="grid grid-cols-1 gap-16px lg:grid-cols-[minmax(0,1fr)_300px]">
      <OnCard padding="none" as="section" aria-labelledby="security-posture">
        <h2 id="security-posture" class="m-0 px-16px pt-14px text-14px font-600 text-ink-primary">
          {{ t('security.overview.postureTitle') }}
        </h2>
        <p class="m-0 px-16px pt-2px text-12px text-ink-tertiary">{{ t('security.overview.postureHint') }}</p>
        <!-- Rows share one chip column (subgrid), so titles line up even when state labels differ in width (ru). -->
        <ul class="m-0 mt-8px grid list-none grid-cols-[auto_1fr] gap-x-12px p-0">
          <li
            v-for="check in overview.posture"
            :key="check.id"
            class="col-span-2 grid grid-cols-[subgrid] items-start gap-x-12px border-t border-t-solid border-line-subtle px-16px py-10px"
          >
            <OnChip :tone="POSTURE_TONE[check.state]" dot size="sm" class="mt-1px">{{
              t(`security.postureState.${check.state}`)
            }}</OnChip>
            <!-- The fix button sits under the text, so a risky check's explanation keeps the full width (long in ru). -->
            <div class="min-w-0 flex-1">
              <p class="m-0 text-13px font-500 text-ink-primary">{{ t(`security.posture.${check.id}.title`) }}</p>
              <p class="m-0 line-clamp-3 text-12px leading-[1.5] text-ink-tertiary" :title="detail(check)">
                {{ detail(check) }}
              </p>
              <OnButton
                v-if="check.settingsUrl && check.state !== 'ok'"
                class="mt-6px"
                size="xs"
                icon="arrow-up-right"
                @click="fix(check)"
              >
                {{ t('security.overview.openSettings') }}
              </OnButton>
            </div>
          </li>
        </ul>
      </OnCard>

      <div class="flex flex-col gap-12px">
        <OnCard padding="md" as="section" class="flex flex-col gap-6px">
          <h2 class="m-0 flex items-center gap-7px text-14px font-600 text-ink-primary">
            <OnIcon name="shield" :size="15" />{{ t('security.engine.title') }}
          </h2>
          <p class="m-0 text-12.5px text-ink-secondary">
            {{
              overview.engine.db
                ? t('security.engine.db', {
                    version: overview.engine.db.daily ?? '—',
                    time: overview.engine.db.updatedAt
                      ? formatRelativeTime(overview.engine.db.updatedAt, { locale: appLocale })
                      : '—'
                  })
                : overview.engine.updating
                  ? t('security.engine.downloading')
                  : t('security.engine.noDb')
            }}
          </p>
          <p class="m-0 text-12px text-ink-tertiary">
            {{
              overview.lastScan?.finishedAt
                ? t(
                    'security.engine.lastScan',
                    {
                      kind: t(`security.scan.${overview.lastScan.kind}.title`),
                      date: formatDate(overview.lastScan.finishedAt, { locale: appLocale }),
                      count: overview.lastScan.threats
                    },
                    { plural: overview.lastScan.threats }
                  )
                : t('security.engine.neverScanned')
            }}
          </p>
          <p class="m-0 text-12px text-ink-tertiary">{{ scheduleText }}</p>
        </OnCard>
        <OnCard padding="md" as="section" class="flex flex-col gap-6px">
          <h2 class="m-0 flex items-center gap-7px text-14px font-600 text-ink-primary">
            <OnIcon name="rotate-ccw-clock" :size="15" />{{ t('security.tabs.startup') }}
          </h2>
          <!-- Before the first read (it can take a minute or more) there is no count to show, not zero. -->
          <p class="m-0 text-12.5px text-ink-secondary">
            {{
              overview.startup.changedAt === null
                ? t('security.startup.notRead')
                : t(
                    'security.startup.summary',
                    { count: overview.startup.thirdParty },
                    { plural: overview.startup.thirdParty }
                  )
            }}
          </p>
          <p v-if="overview.startup.untrusted" class="m-0 text-12px text-status-warning">
            {{
              t(
                'security.startup.untrusted',
                { count: overview.startup.untrusted },
                { plural: overview.startup.untrusted }
              )
            }}
          </p>
        </OnCard>
        <OnCard padding="md" as="section" class="flex flex-col gap-6px">
          <h2 class="m-0 flex items-center gap-7px text-14px font-600 text-ink-primary">
            <OnIcon name="package" :size="15" />{{ t('security.tabs.quarantine') }}
          </h2>
          <p class="m-0 text-12.5px text-ink-secondary">
            {{
              overview.quarantine.count
                ? t(
                    'security.quarantine.summary',
                    {
                      count: overview.quarantine.count,
                      size: formatBytes(overview.quarantine.bytes, { locale: appLocale })
                    },
                    { plural: overview.quarantine.count }
                  )
                : t('security.quarantine.empty')
            }}
          </p>
        </OnCard>
        <SignatureCheckCard />
      </div>
    </div>
  </div>
  <div v-else class="flex flex-col gap-16px" aria-hidden="true">
    <OnSkeleton height="86px" radius="huge" />
    <OnSkeleton height="360px" radius="big" />
  </div>
</template>
