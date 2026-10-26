<script setup lang="ts">
// Decorative scenes have no catalog content. Keep the surrounding copy in SSR and
// reserve the scene's space; construct its DOM only near the viewport.
defineSlots<{ default?: () => unknown }>();
const root = ref<HTMLElement>();
const visible = ref(false);
let observer: IntersectionObserver | undefined;
onMounted(() => {
  if (!root.value) return;
  if (!('IntersectionObserver' in window)) {
    visible.value = true;
    return;
  }
  observer = new IntersectionObserver(
    entries => {
      if (!entries.some(entry => entry.isIntersecting)) return;
      visible.value = true;
      observer?.disconnect();
    },
    { rootMargin: '400px' }
  );
  observer.observe(root.value);
});
onBeforeUnmount(() => observer?.disconnect());
</script>

<template>
  <div ref="root"><slot v-if="visible" /></div>
</template>
