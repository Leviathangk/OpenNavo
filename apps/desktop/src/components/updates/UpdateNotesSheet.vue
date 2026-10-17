<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnMarkdownView, OnSheet } from '@opennavo/ui';
import { OnMarkdown, sanitizeReleaseHtml } from '@opennavo/ui/markdown';
import type { OutdatedItem } from '@/ipc/bindings';

// Release notes an update source ships inline (store/security plan §11.4): Sparkle appcasts carry HTML, which is
// sanitized and never loads images; GitHub notes are Markdown; App Store notes are plain text.
const props = defineProps<{ item: OutdatedItem | null }>();
const open = defineModel<boolean>('open', { required: true });

const { t } = useI18n();

const notes = computed(() => {
  const value = props.item?.notes;
  return value && 'body' in value && value.body ? value : null;
});
const html = computed(() =>
  notes.value?.format === 'html' ? sanitizeReleaseHtml(notes.value.body ?? '', { headingLevel: 3 }) : ''
);
const title = computed(() =>
  props.item ? t('updates.notes.title', { name: props.item.name, version: props.item.currentVersion }) : ''
);
</script>

<template>
  <OnSheet v-model:open="open" :title="title">
    <template v-if="notes">
      <OnMarkdownView v-if="notes.format === 'html'" :html="html" />
      <OnMarkdown v-else-if="notes.format === 'markdown'" :source="notes.body ?? ''" />
      <p v-else class="m-0 whitespace-pre-wrap text-13px leading-[1.6] text-ink-secondary">{{ notes.body }}</p>
    </template>
  </OnSheet>
</template>
