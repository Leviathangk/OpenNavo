<script setup lang="ts">
import { computed } from 'vue';
import { codeParts, wordPieces } from '../utils/codeText';

// Commands, paths and other code text wrap between words, never inside a token (08 §8.19). Browsers also
// break after hyphens, splitting visual-studio-code or --greedy, so each run of non-space characters is an
// inline-block that only breaks itself when it is wider than the whole line, then preferably after "/", "."
// or "=". Whitespace stays plain text, so copying and white-space: pre-wrap containers keep the original text.
const props = defineProps<{ text: string }>();

// Words become their line-break pieces; whitespace stays a string.
const parts = computed(() => codeParts(props.text).map(part => (part.trim() ? wordPieces(part) : part)));
</script>

<template>
  <template v-for="(part, index) in parts" :key="index"
    ><span v-if="typeof part !== 'string'" class="on-code-word"
      ><template v-for="(piece, at) in part" :key="at"><wbr v-if="at" />{{ piece }}</template></span
    ><template v-else>{{ part }}</template></template
  >
</template>

<style scoped>
.on-code-word {
  display: inline-block;
  max-width: 100%;
  overflow-wrap: anywhere;
}
</style>
