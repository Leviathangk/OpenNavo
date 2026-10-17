<script setup lang="ts">
import { computed, ref, watch } from 'vue';

defineOptions({ name: 'PackageIcon' });

interface Props {
  url?: string | null;
  name: string;
  kind: 'cask' | 'formula' | 'link';
  size?: number;
}

const props = withDefaults(defineProps<Props>(), { url: null, size: 32 });

// Missing/failed icons: initial letter for apps (casks and link apps), terminal prompt for command-line tools (NAvatar supplies themed backgrounds).
const failed = ref(false);
watch(
  () => props.url,
  () => {
    failed.value = false;
  }
);
const initial = computed(() => (props.kind === 'formula' ? '>_' : (props.name.trim()[0] ?? '?').toUpperCase()));
</script>

<template>
  <NAvatar
    v-if="url && !failed"
    :size="size"
    :src="url"
    :img-props="{ alt: name, loading: 'lazy' }"
    object-fit="contain"
    color="transparent"
    class="shrink-0"
    @error="failed = true"
  />
  <NAvatar v-else :size="size" class="shrink-0 font-mono text-12px font-600" aria-hidden="true">{{ initial }}</NAvatar>
</template>
