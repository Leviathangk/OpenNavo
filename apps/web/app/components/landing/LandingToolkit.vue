<script setup lang="ts">
// Mac tools (desktop Uninstall, Clean Up Mac and Monitor): three cards with small looping demos.
const props = defineProps<{ apps: readonly LandingApp[] }>();

const { t } = useI18n();
const first = computed(() => props.apps[0] ?? SAMPLE_APPS[0]);

const cards = ['uninstall', 'cleanup', 'monitor'] as const;
const delay = (key: (typeof cards)[number]) => ({ '--landing-delay': `${cards.indexOf(key) * 90}ms` });
</script>

<template>
  <section class="mx-auto box-border max-w-1240px px-24px">
    <div data-reveal>
      <LandingSectionHead
        align="center"
        :kicker="t('landing.toolkit.kicker')"
        :title="t('landing.toolkit.title')"
        :body="t('landing.toolkit.body')"
      />
    </div>
    <div class="mt-40px grid gap-12px md:grid-cols-2 lg:grid-cols-3">
      <LandingMoreCard
        tall
        :title="t('landing.toolkit.uninstall.title')"
        :body="t('landing.toolkit.uninstall.body')"
        data-reveal
        :style="delay('uninstall')"
      >
        <LandingUninstallDemo v-if="first" :app="first" />
      </LandingMoreCard>
      <LandingMoreCard
        tall
        :title="t('landing.toolkit.cleanup.title')"
        :body="t('landing.toolkit.cleanup.body')"
        data-reveal
        :style="delay('cleanup')"
      >
        <LandingCleanupDemo />
      </LandingMoreCard>
      <LandingMoreCard
        tall
        :title="t('landing.toolkit.monitor.title')"
        :body="t('landing.toolkit.monitor.body')"
        data-reveal
        :style="delay('monitor')"
      >
        <LandingMonitorDemo />
      </LandingMoreCard>
    </div>
  </section>
</template>
