<script setup lang="ts">
import { computed, ref } from 'vue';
import type { ReleaseEntry } from '@opennavo/api';
import { interpolatePlural } from '@opennavo/shared';
import { OnButton } from '@opennavo/ui';
import PackageVersionEntry from './PackageVersionEntry.vue';
import { formatReleaseVersion } from '@opennavo/shared';
import { formatDate } from '~/utils/packageDate';
import { mergeReleaseEntries, releaseAnchorId, releaseHasNotes, releaseCompatibilityText } from '~/utils/packageFacts';
import { useUiLocale, useUiMessages } from '@opennavo/ui';
import type { OnVersionCompatibilityLabels } from '@opennavo/ui';

export interface OnVersionTimelineProps {
  entries: readonly ReleaseEntry[];
  /** Current installed desktop version: hollow green dot and Current installation. */
  installedVersion?: string | null;
  /** Initially expanded entry count; collapse the rest (08 §10.6). */
  expandedCount?: number;
  showOriginal?: boolean;
  /** Entries offering Update to this version on desktop. */
  canUpdate?: (entry: ReleaseEntry) => boolean;
  /** Unloaded older-version count; positive values show Load N earlier versions below. */
  moreCount?: number;
  /** Entries with local annotations receive the local slot; if omitted, render whenever slot exists. */
  hasLocal?: (entry: ReleaseEntry) => boolean;
  headingLevel?: 2 | 3 | 4;
  /** Web (F6): passed to every entry for its macOS compatibility row. */
  compatibilityLabels?: OnVersionCompatibilityLabels;
}

const props = withDefaults(defineProps<OnVersionTimelineProps>(), {
  installedVersion: null,
  expandedCount: 3,
  moreCount: 0,
  headingLevel: 3
});

const emit = defineEmits<{
  update: [entry: ReleaseEntry];
  openSource: [entry: ReleaseEntry, event: MouseEvent];
  more: [];
}>();

defineSlots<{
  local?: (props: { entry: ReleaseEntry }) => unknown;
  markdown?: (props: { entry: ReleaseEntry; source: string }) => unknown;
}>();

const messages = useUiMessages();
const locale = useUiLocale();
const entries = computed(() => mergeReleaseEntries(props.entries));
// Preserve every original fragment, even when a build is folded into its base release.
function aliases(entry: ReleaseEntry) {
  const base = entry.version.split(',')[0]!.trim();
  return [
    ...new Set(props.entries.filter(original => original.version.split(',')[0]!.trim() === base).map(releaseAnchorId))
  ].filter(id => id !== releaseAnchorId(entry));
}
const text = computed(() => messages.value.version);

// Versions manually toggled, inverted relative to default expansion.
const toggled = ref(new Set<string>());
const defaultCollapsed = (index: number) => index >= props.expandedCount;
const isCollapsed = (entry: ReleaseEntry, index: number) =>
  defaultCollapsed(index) !== toggled.value.has(entry.version);

function toggle(version: string) {
  const next = new Set(toggled.value);
  if (next.has(version)) next.delete(version);
  else next.add(version);
  toggled.value = next;
}

// The installed build has its own entry when it has notes of its own; otherwise it shares its base version's entry.
const installedKey = computed(() => {
  const installed = props.installedVersion;
  if (!installed) return null;
  if (props.entries.some(entry => entry.version === installed)) return installed;
  const base = installed.split(',')[0]?.trim() ?? '';
  return props.entries.some(entry => entry.version === base) ? base : null;
});

function marker(entry: ReleaseEntry, index: number) {
  if (entry.isLatest) return 'latest';
  if (entry.version === installedKey.value) return 'installed';
  return isCollapsed(entry, index) ? 'minor' : 'default';
}
</script>

<template>
  <div class="flex flex-col gap-16px">
    <ol
      class="on-version-timeline relative m-0 flex list-none flex-col gap-16px p-0 pl-36px"
      :aria-label="text.timeline"
    >
      <li v-for="(entry, index) in entries" :key="entry.version" class="relative">
        <span v-for="id in aliases(entry)" :id="id" :key="id" aria-hidden="true"></span>
        <span
          class="on-version-timeline__dot"
          :class="[
            `on-version-timeline__dot--${marker(entry, index)}`,
            { 'on-version-timeline__dot--collapsed': isCollapsed(entry, index) }
          ]"
          aria-hidden="true"
        ></span>
        <PackageVersionEntry
          v-if="releaseHasNotes(entry)"
          :entry="entry"
          :installed="entry.version === installedKey"
          :collapsed="isCollapsed(entry, index)"
          :collapsible="!defaultCollapsed(index) ? false : !isCollapsed(entry, index)"
          :show-original="showOriginal"
          :can-update="canUpdate?.(entry) ?? false"
          :heading-level="headingLevel"
          :compatibility-labels="compatibilityLabels ? { ...compatibilityLabels, unknown: '' } : undefined"
          @toggle="toggle(entry.version)"
          @update="emit('update', entry)"
          @open-source="event => emit('openSource', entry, event)"
        >
          <template v-if="$slots.local && (hasLocal?.(entry) ?? true)" #local>
            <slot name="local" :entry="entry" />
          </template>
          <template v-if="$slots.markdown" #markdown="{ source }">
            <slot name="markdown" :entry="entry" :source="source" />
          </template>
        </PackageVersionEntry>
        <div
          v-else
          :id="releaseAnchorId(entry)"
          class="flex flex-wrap items-baseline gap-x-12px gap-y-4px border-b border-b-solid border-line-subtle py-8px text-13px"
        >
          <span class="font-600 text-ink-primary">{{ formatReleaseVersion(entry) }}</span>
          <span
            v-if="compatibilityLabels && releaseCompatibilityText(entry.compatibility, compatibilityLabels)"
            class="text-ink-tertiary"
            >{{ releaseCompatibilityText(entry.compatibility, compatibilityLabels) }}</span
          >
          <time
            v-if="entry.publishedAt || entry.brewCommittedAt"
            :datetime="entry.publishedAt || entry.brewCommittedAt || undefined"
            class="text-ink-tertiary"
            >{{ formatDate((entry.publishedAt || entry.brewCommittedAt)!, { locale }) }}</time
          >
          <a
            v-if="entry.sourceUrl"
            :href="entry.sourceUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="text-ink-secondary"
            >{{ text.sources[entry.source] }}</a
          >
        </div>
      </li>
    </ol>
    <OnButton v-if="moreCount > 0" variant="secondary" class="self-center" @click="emit('more')">
      {{ interpolatePlural(text.showEarlier, { count: moreCount }, moreCount, locale) }}
    </OnButton>
  </div>
</template>

<style>
.on-version-timeline {
  /* Dot rings match timeline background to interrupt the vertical line; web can override its different page surface. */
  --on-version-timeline-bg: var(--on-surface-base);
}

.on-version-timeline::before {
  content: '';
  position: absolute;
  top: 12px;
  bottom: 0;
  left: 12px;
  width: 1px;
  background: linear-gradient(180deg, var(--on-component-timeline-line), var(--on-surface-raised) 70%, transparent);
}

.on-version-timeline__dot {
  position: absolute;
  top: 24px;
  left: -30.5px;
  width: 13px;
  height: 13px;
  box-sizing: border-box;
  border-radius: 50%;
  background-color: var(--on-component-timeline-dot);
  box-shadow: 0 0 0 3px var(--on-version-timeline-bg);
}

.on-version-timeline__dot--collapsed {
  top: 20px;
}

.on-version-timeline__dot--minor {
  left: -28px;
  width: 8px;
  height: 8px;
}

.on-version-timeline__dot--latest {
  background-color: var(--on-brand-salmon);
  box-shadow:
    0 0 0 3px var(--on-version-timeline-bg),
    0 0 0 7px color-mix(in srgb, var(--on-brand-salmon) 15%, transparent);
}

.on-version-timeline__dot--installed {
  border: 3px solid var(--on-status-success);
  background-color: var(--on-version-timeline-bg);
  box-shadow:
    0 0 0 3px var(--on-version-timeline-bg),
    0 0 0 6px color-mix(in srgb, var(--on-status-success) 15%, transparent);
}
</style>
