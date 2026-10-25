<script setup lang="ts">
import { computed, ref, useId } from 'vue';
import type { CaskArtifact } from '@opennavo/api';
import { interpolatePlural } from '@opennavo/shared';
import OnButton from './OnButton.vue';
import OnCodeText from './OnCodeText.vue';
import { useUiLocale, useUiMessages } from '../composables/locale';

// Font collections declare hundreds of files, one entry each. A long list shows its first entries until the
// visitor asks for the rest, which is only ever rendered in the browser and so stays out of server HTML.
const COLLAPSE_ABOVE = 30;
const COLLAPSED_COUNT = 20;

const props = defineProps<{ entries: readonly CaskArtifact[] }>();
const messages = useUiMessages();
const locale = useUiLocale();
const listId = useId();
const expanded = ref(false);
const collapsible = computed(() => props.entries.length > COLLAPSE_ABOVE);
const shown = computed(() =>
  collapsible.value && !expanded.value ? props.entries.slice(0, COLLAPSED_COUNT) : props.entries
);
const toggleLabel = computed(() =>
  expanded.value
    ? messages.value.about.collapse
    : interpolatePlural(
        messages.value.collection.viewAll,
        { count: props.entries.length },
        props.entries.length,
        locale.value
      )
);
</script>

<template>
  <section
    v-if="entries.length"
    class="min-w-0 rounded-big border border-solid border-line-subtle bg-surface-card p-16px"
  >
    <h2 class="m-0 text-13.5px font-600 text-ink-primary">{{ messages.cask.declarations }}</h2>
    <ul :id="listId" class="m-0 mt-12px flex list-none flex-col gap-12px p-0">
      <li
        v-for="(entry, index) in shown"
        :key="index"
        class="min-w-0 border-t border-t-solid border-line-subtle pt-12px text-12.5px"
      >
        <p class="m-0 text-ink-primary">
          {{ messages.cask[entry.phase] }} <code class="font-mono text-ink-tertiary">{{ entry.type }}</code>
        </p>
        <p v-if="entry.sources.length" class="m-0 mt-4px text-ink-secondary">
          {{ messages.cask.source }}: <span class="font-mono"><OnCodeText :text="entry.sources.join(' · ')" /></span>
        </p>
        <p v-if="entry.target" class="m-0 mt-4px text-ink-secondary">
          {{ messages.cask.target }}: <span class="font-mono"><OnCodeText :text="entry.target" /></span>
        </p>
        <details class="mt-8px">
          <summary
            class="cursor-pointer text-ink-secondary hover:text-ink-primary focus-visible:outline-none focus-visible:shadow-focus-ring"
          >
            {{ messages.cask.declaration }}
          </summary>
          <pre
            class="m-0 mt-8px max-w-full overflow-x-auto whitespace-pre-wrap font-mono text-12px text-ink-secondary"
          ><OnCodeText :text="JSON.stringify(entry.declaration, null, 2)" /></pre>
        </details>
      </li>
    </ul>
    <OnButton
      v-if="collapsible"
      size="sm"
      class="mt-12px"
      :aria-controls="listId"
      :aria-expanded="expanded ? 'true' : 'false'"
      @click="expanded = !expanded"
    >
      {{ toggleLabel }}
    </OnButton>
  </section>
</template>
