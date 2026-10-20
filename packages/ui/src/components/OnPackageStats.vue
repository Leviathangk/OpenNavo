<script setup lang="ts">
// Detail stats (08 §10.5, ADR-017): 30-day installs/rank, yearly installs, latest version/date, size, cadence.
// Host formats text; two columns on mobile. A description list (T2): each label is a dt and its value and
// hint are dds, styled like OnStatTile's detail cells, so the strip reads as facts rather than loose text.
export interface OnPackageStatsCell {
  key: string;
  label: string;
  value: string;
  unit?: string;
  hint?: string;
}

export interface OnPackageStatsProps {
  cells: readonly OnPackageStatsCell[];
  label: string;
}

defineProps<OnPackageStatsProps>();
</script>

<template>
  <section
    class="overflow-hidden rounded-big border border-solid border-line-subtle bg-surface-card"
    :aria-label="label"
  >
    <dl class="m-0 grid grid-cols-2 md:grid-cols-5">
      <div
        v-for="(cell, index) in cells"
        :key="cell.key"
        class="box-border flex min-w-0 flex-col gap-2px border-line-subtle px-18px py-14px md:border-l md:border-l-solid"
        :class="index === 0 ? 'md:border-l-0' : ''"
      >
        <dt class="break-words text-11.5px text-ink-tertiary">{{ cell.label }}</dt>
        <dd class="m-0 flex min-w-0 flex-wrap items-baseline gap-2px">
          <span class="break-words text-stat text-ink-primary">{{ cell.value }}</span>
          <span v-if="cell.unit" class="shrink-0 text-12px font-500 text-ink-tertiary">{{ cell.unit }}</span>
        </dd>
        <dd v-if="cell.hint" class="m-0 break-words text-11px text-ink-tertiary">{{ cell.hint }}</dd>
      </div>
    </dl>
  </section>
</template>
