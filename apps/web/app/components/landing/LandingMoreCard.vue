<script setup lang="ts">
// One More grid cell: demo above, heading/description below; tall gives the toolkit demos more room.
defineProps<{ title: string; body: string; tall?: boolean }>();
defineSlots<{ default?: () => unknown }>();
</script>

<template>
  <article
    class="flex min-w-0 flex-col overflow-hidden rounded-huge border border-solid border-line-subtle bg-surface-card"
  >
    <div
      class="landing-more-stage relative flex items-center justify-center overflow-hidden border-b border-b-solid border-line-subtle px-20px"
      :class="tall ? 'h-280px' : 'h-228px'"
    >
      <LandingDemoViewport class="relative w-full"><slot /></LandingDemoViewport>
    </div>
    <div class="flex flex-col gap-8px px-22px pb-24px pt-20px">
      <h3 class="m-0 text-headline text-ink-primary">{{ title }}</h3>
      <p class="m-0 text-14px leading-[1.65] text-ink-secondary">{{ body }}</p>
    </div>
  </article>
</template>

<style>
/* Soft violet reflection at demo bottom echoes the hero glow. */
.landing-more-stage {
  background:
    radial-gradient(120% 90% at 50% 120%, color-mix(in srgb, var(--on-brand-violet) 16%, transparent), transparent 70%),
    var(--on-surface-card);
}
</style>
