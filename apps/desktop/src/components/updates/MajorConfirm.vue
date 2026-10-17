<script setup lang="ts">
import { computed, onBeforeUnmount } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatVersionChange } from '@opennavo/shared';
import { OnButton, OnModal } from '@opennavo/ui';
import { useMajorConfirmStore } from '@/stores/majorConfirm';

const { t } = useI18n();
const store = useMajorConfirmStore();
onBeforeUnmount(() => store.choose(false));

const majors = computed(() => store.pending?.majors ?? []);
const title = computed(() =>
  majors.value.length === 1
    ? t('updates.major.titleOne', { name: majors.value[0]!.name })
    : t('updates.major.title', { count: majors.value.length }, { plural: majors.value.length })
);
</script>

<template>
  <OnModal
    :open="store.pending !== null"
    :title="title"
    :description="t('updates.major.body')"
    size="md"
    role="alertdialog"
    initial-focus="[data-major-skip]"
    @close="store.choose(false)"
  >
    <ul class="m-0 flex list-none flex-col gap-4px p-0">
      <li v-for="item in majors" :key="`${item.source}:${item.appId ?? item.token}`" class="text-13px">
        <span class="font-600 text-ink-primary">{{ item.name }}</span>
        <span class="ml-8px text-ink-tertiary tabular-nums">{{
          formatVersionChange(item.installedVersion, item.currentVersion)
        }}</span>
      </li>
    </ul>
    <p v-if="store.pending?.others" class="mb-0 mt-14px text-13px leading-relaxed text-ink-secondary">
      {{ t('updates.major.others') }}
    </p>
    <template #footer>
      <OnButton variant="ghost" data-major-skip @click="store.choose(false)">{{ t('updates.major.skip') }}</OnButton>
      <OnButton variant="primary" data-major-confirm @click="store.choose(true)">{{
        t('updates.major.confirm')
      }}</OnButton>
    </template>
  </OnModal>
</template>
