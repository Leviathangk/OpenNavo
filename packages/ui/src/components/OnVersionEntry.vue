<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import type { ReleaseEntry } from '@opennavo/api';
import { formatDate, formatReleaseVersion, formatTime, releaseTitleDetail } from '@opennavo/shared';
import OnButton from './OnButton.vue';
import OnChip from './OnChip.vue';
import OnCodeText from './OnCodeText.vue';
import OnIcon from './OnIcon.vue';
import { useUiLocale, useUiMessages } from '../composables/locale';
import { compatibilityText } from '../utils/compatibility';
import type { OnVersionCompatibilityLabels } from '../utils/compatibility';
import { isMachineTranslated, isPatchVersion, parseInline } from '../utils/version';

export interface OnVersionEntryProps {
  entry: ReleaseEntry;
  /** Current installed desktop version. */
  installed?: boolean;
  /** Collapsed single row: version, date, Expand. The body stays in the HTML inside a closed <details>. */
  collapsed?: boolean;
  /** Allow collapsing expanded entries that the timeline initially collapsed. */
  collapsible?: boolean;
  /** Original-language body selected: hide AI translated badge. */
  showOriginal?: boolean;
  /** Show Update to this version on desktop when newer than installed. */
  canUpdate?: boolean;
  headingLevel?: 2 | 3 | 4;
  /** Web (F6): copy for the version's macOS compatibility row, "unknown" without data; no row when omitted. */
  compatibilityLabels?: OnVersionCompatibilityLabels;
}

const props = withDefaults(defineProps<OnVersionEntryProps>(), { headingLevel: 3 });

const emit = defineEmits<{
  /** Update to this version click. */
  update: [];
  /** Expand/collapse. */
  toggle: [];
  /** Source-link click; desktop may preventDefault and open in the system browser. */
  openSource: [event: MouseEvent];
}>();

defineSlots<{
  /** Desktop local annotation in a dashed notice, e.g. updated through OpenNavo on Sep 26 at 10:12. */
  local?: () => unknown;
  /** Host renders Markdown-only bodies without highlights; renderer lives in @opennavo/ui/markdown. */
  markdown?: (props: { source: string }) => unknown;
}>();

const messages = useUiMessages();
const locale = useUiLocale();
const text = computed(() => messages.value.version);

function validDate(value: string | null | undefined): string | null {
  if (!value) return null;
  const parsed = new Date(value);
  return Number.isFinite(parsed.getTime()) && parsed.getUTCFullYear() > 1 ? value : null;
}

// Open state follows the collapsed prop and also records native <details> toggles, so the summary stays right
// when a host does not handle the toggle event (and the page still works before hydration or without JS).
const details = ref<HTMLDetailsElement>();
const open = ref(!props.collapsed);
watch(
  () => props.collapsed,
  value => {
    open.value = !value;
  }
);
const isCollapsed = computed(() => !open.value);
// As before, only collapsed entries and entries the timeline collapsed first can be toggled.
const toggleable = computed(() => isCollapsed.value || props.collapsible);

function onSummaryClick(event: MouseEvent) {
  if (!toggleable.value) event.preventDefault();
}

function onToggle() {
  const next = details.value?.open ?? open.value;
  // A toggle event also fires when the collapsed prop changes the open attribute; only report user toggles.
  if (next === open.value) return;
  open.value = next;
  emit('toggle');
}

const publishedAt = computed(() => validDate(props.entry.publishedAt));
const brewCommittedAt = computed(() => validDate(props.entry.brewCommittedAt));
const dateIso = computed(() => publishedAt.value ?? brewCommittedAt.value);
const date = computed(() =>
  dateIso.value ? formatDate(dateIso.value, { locale: locale.value, weekday: !isCollapsed.value }) : ''
);
const stamp = (at: string) => `${formatDate(at, { locale: locale.value })} ${formatTime(at)}`;

// Source row: upstream body source chart.series1, Homebrew adoption time chart.series2.
const sources = computed(() => {
  const items: {
    key: string;
    tone: 'upstream' | 'homebrew';
    label: string;
    at: string | null;
    href: string | null;
  }[] = [];
  const { source, sourceUrl } = props.entry;
  const published = publishedAt.value;
  const brewed = brewCommittedAt.value;
  if (source !== 'homebrew') {
    const at = source === 'github_release' && published ? published : null;
    items.push({ key: 'upstream', tone: 'upstream', label: text.value.sources[source], at, href: sourceUrl ?? null });
  }
  if (brewed) {
    items.push({ key: 'homebrew', tone: 'homebrew', label: text.value.sources.homebrew, at: brewed, href: null });
  }
  return items;
});

const machine = computed(() => !props.showOriginal && isMachineTranslated(props.entry));
const patch = computed(() => isPatchVersion(props.entry.version));
const sections = computed(() =>
  props.entry.sections.map(section => ({
    area: section.area,
    items: section.items.map(item => parseInline(item))
  }))
);
const heading = computed(() => `h${props.headingLevel}`);
// Anchors are stable only within one page: versions move between pages as new releases arrive.
const anchorId = computed(() =>
  props.entry.id ? `r-${props.entry.id}` : `v-${props.entry.version.replace(/[^0-9A-Za-z._-]+/g, '-')}`
);
// Notes shown in another language (source fallback or the original switch) carry their own lang.
const bodyLang = computed(() => {
  const value = props.entry.translation?.locale;
  return value && value !== locale.value ? value : undefined;
});
// F6: sourced bounds and Apple silicon support for this version, shown only when the host supplies the copy.
const compatibility = computed(() =>
  props.compatibilityLabels ? compatibilityText(props.entry.compatibility, props.compatibilityLabels) : null
);
// Entries for one build show a short build tag; the full build version stays in the tooltip.
const label = computed(() => formatReleaseVersion(props.entry));
// Editorial titles often only repeat the app name and version; show the ones that add to it.
const titleDetail = computed(() => releaseTitleDetail(props.entry.title, props.entry.version));
const fullVersion = computed(() => (props.entry.build ? props.entry.version : undefined));
</script>

<template>
  <article
    :id="anchorId"
    class="on-version-entry rounded-big border border-solid"
    :class="[
      entry.isLatest ? 'on-version-entry--latest' : 'border-line-subtle bg-surface-card',
      isCollapsed ? 'px-20px py-14px' : 'px-24px py-18px'
    ]"
  >
    <!-- Kept outside <summary>: a button inside it would be presentational to assistive technology. -->
    <OnButton
      v-if="canUpdate && !isCollapsed"
      variant="primary"
      size="sm"
      class="on-version-entry__update float-right ml-12px"
      @click="emit('update')"
      >{{ text.updateTo }}</OnButton
    >
    <details ref="details" :open="open" @toggle="onToggle">
      <summary
        class="on-version-entry__summary group flex w-full items-center rounded-tiny outline-none focus-visible:shadow-focus-ring"
        :class="[
          isCollapsed ? 'gap-12px' : 'flex-wrap gap-x-10px gap-y-6px',
          toggleable ? 'cursor-pointer' : 'cursor-default'
        ]"
        @click="onSummaryClick"
      >
        <component
          :is="heading"
          class="m-0 font-600 text-ink-primary"
          :class="isCollapsed ? 'text-14px' : 'text-17px'"
          :title="fullVersion"
          >{{ label }}</component
        >
        <template v-if="!isCollapsed">
          <OnChip v-if="entry.isLatest" tone="accent">{{ text.latest }}</OnChip>
          <OnChip v-if="installed" tone="success" dot>{{ text.installed }}</OnChip>
          <OnChip v-if="entry.isPrerelease" tone="warning" outline>{{ text.prerelease }}</OnChip>
          <OnChip v-else-if="patch" tone="outline">{{ text.patch }}</OnChip>
        </template>
        <time v-if="dateIso" :datetime="dateIso" class="on-version-entry__date text-12.5px text-ink-tertiary">{{
          date
        }}</time>
        <span
          v-if="toggleable"
          class="ml-auto flex items-center gap-4px text-12.5px text-ink-tertiary group-hover:text-ink-secondary"
        >
          {{ isCollapsed ? text.expand : text.collapse }}
          <OnIcon :name="isCollapsed ? 'lucide:chevron-down' : 'lucide:chevron-up'" :size="14" />
        </span>
      </summary>
      <p
        v-if="sources.length || machine"
        class="m-0 mt-8px flex flex-wrap items-center gap-x-16px gap-y-4px text-12.5px text-ink-tertiary"
      >
        <span v-for="item in sources" :key="item.key" class="flex items-center gap-6px">
          <span
            class="on-version-entry__source-dot"
            :class="`on-version-entry__source-dot--${item.tone}`"
            aria-hidden="true"
          ></span>
          <a
            v-if="item.href"
            :href="item.href"
            target="_blank"
            rel="noopener noreferrer nofollow"
            class="text-ink-tertiary no-underline hover:text-ink-secondary hover:underline"
            @click="event => emit('openSource', event)"
          >
            {{ item.label }} <time v-if="item.at" :datetime="item.at">{{ stamp(item.at) }}</time>
          </a>
          <span v-else
            >{{ item.label }} <time v-if="item.at" :datetime="item.at">{{ stamp(item.at) }}</time></span
          >
        </span>
        <OnChip v-if="machine" tone="outline">{{ text.machineTranslation }}</OnChip>
      </p>
      <p
        v-if="compatibility && compatibilityLabels"
        class="on-version-entry__compatibility m-0 mt-6px flex flex-wrap gap-x-8px gap-y-2px text-12.5px text-ink-tertiary"
      >
        <span class="text-ink-secondary">{{ compatibilityLabels.title }}</span>
        <span>{{ compatibility }}</span>
      </p>

      <div v-if="!entry.hasNotes" class="mt-12px flex flex-wrap items-center gap-12px text-13px text-ink-tertiary">
        <span>{{ text.noNotes }}</span>
        <a
          v-if="entry.sourceUrl"
          :href="entry.sourceUrl"
          target="_blank"
          rel="noopener noreferrer nofollow"
          class="flex items-center gap-2px text-ink-secondary no-underline hover:underline"
          @click="event => emit('openSource', event)"
        >
          {{ text.viewOriginal }}
          <OnIcon name="lucide:arrow-up-right" :size="13" />
        </a>
      </div>
      <div v-else :lang="bodyLang">
        <p v-if="titleDetail" class="m-0 mt-12px text-14px font-semibold text-ink-primary">{{ titleDetail }}</p>
        <p v-if="entry.summary" class="m-0 mt-12px whitespace-pre-line text-13px leading-[1.5] text-ink-secondary">
          {{ entry.summary }}
        </p>
        <dl v-if="sections.length" class="m-0 mt-14px grid grid-cols-[76px_minmax(0,1fr)] gap-x-24px gap-y-10px">
          <template v-for="section in sections" :key="section.area">
            <dt class="pt-1px text-12px leading-[1.6] text-ink-tertiary">{{ section.area }}</dt>
            <dd class="m-0 flex flex-col gap-4px text-13px leading-[1.5] text-ink-secondary">
              <span v-for="(item, index) in section.items" :key="index">
                <template v-for="(segment, part) in item" :key="part">
                  <strong v-if="segment.type === 'strong'" class="font-600 text-ink-primary">{{ segment.text }}</strong>
                  <code v-else-if="segment.type === 'code'" class="on-version-entry__code"
                    ><OnCodeText :text="segment.text"
                  /></code>
                  <template v-else>{{ segment.text }}</template>
                </template>
              </span>
            </dd>
          </template>
        </dl>
        <div v-else-if="entry.bodyMarkdown" class="mt-12px">
          <slot name="markdown" :source="entry.bodyMarkdown">
            <p v-if="!entry.summary" class="m-0 text-13px leading-[1.5] text-ink-secondary">{{ entry.bodyMarkdown }}</p>
          </slot>
        </div>
      </div>

      <div
        v-if="$slots.local"
        class="on-version-entry__local mt-14px flex items-center gap-8px rounded-default px-14px py-10px text-13px text-ink-secondary"
      >
        <OnIcon name="lucide:rotate-ccw-clock" :size="15" class="shrink-0 text-status-success" />
        <slot name="local" />
      </div>
    </details>
  </article>
</template>

<style>
/* The disclosure marker is replaced by the Expand/Collapse label. */
.on-version-entry__summary {
  list-style: none;
}

.on-version-entry__summary::-webkit-details-marker {
  display: none;
}

.on-version-entry--latest {
  border-color: color-mix(in srgb, var(--on-brand-salmon) 22%, transparent);
  background:
    linear-gradient(180deg, color-mix(in srgb, var(--on-brand-salmon) 5%, transparent), transparent 96px),
    var(--on-surface-card);
}

.on-version-entry__source-dot {
  width: 6px;
  height: 6px;
  flex-shrink: 0;
  border-radius: 1px;
}

.on-version-entry__source-dot--upstream {
  background-color: var(--on-chart-series1);
}

.on-version-entry__source-dot--homebrew {
  background-color: var(--on-chart-series2);
}

.on-version-entry__code {
  padding: 1px 6px;
  border-radius: var(--on-radius-tiny);
  background-color: var(--on-surface-inset);
  color: var(--on-component-mono-text);
  font-family: var(--on-font-family-mono);
  font-size: 12px;
}

.on-version-entry__local {
  border: 1px dashed color-mix(in srgb, var(--on-status-success) 28%, transparent);
  background-color: color-mix(in srgb, var(--on-status-success) 6%, transparent);
}
</style>
