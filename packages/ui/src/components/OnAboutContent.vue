<script setup lang="ts">
import { computed, ref } from 'vue';
import type { PublicComponents } from '@opennavo/api';
import OnChip from './OnChip.vue';
import OnCodeText from './OnCodeText.vue';

type Content = PublicComponents['schemas']['AboutContent'];
const props = defineProps<{ content: Content; draftLabel: string }>();
const expanded = ref(new Set<string>());
function toggle(ecosystem: string, event: Event) {
  if ((event.target as HTMLDetailsElement).open) expanded.value.add(ecosystem);
  else expanded.value.delete(ecosystem);
}
const groups = computed(() => {
  const result = new Map<string, Content['modules']>();
  for (const item of props.content.modules) {
    const group = result.get(item.ecosystem) ?? [];
    group.push(item);
    result.set(item.ecosystem, group);
  }
  return [...result];
});
</script>

<template>
  <div class="flex min-w-0 flex-col gap-28px">
    <section
      v-for="section in content.sections"
      :id="section.key"
      :key="section.key"
      class="flex min-w-0 flex-col gap-10px"
    >
      <h2 class="m-0 flex items-center gap-8px text-headline text-ink-primary">
        {{ section.title }}
        <OnChip v-if="section.draft" tone="warning" outline>{{ draftLabel }}</OnChip>
      </h2>
      <p
        v-for="(text, index) in section.paragraphs"
        :key="index"
        class="m-0 whitespace-pre-line text-14px leading-[1.75] text-ink-secondary"
      >
        {{ text }}
      </p>
      <template v-if="section.key === 'openSource'">
        <details
          v-for="[ecosystem, modules] in groups"
          :key="ecosystem"
          class="border-b border-line-default py-8px"
          @toggle="toggle(ecosystem, $event)"
        >
          <summary
            class="cursor-pointer rounded-default text-14px text-ink-primary outline-none focus-visible:shadow-focus-ring"
          >
            {{ ecosystem }} · {{ modules.length }}
          </summary>
          <ul v-if="expanded.has(ecosystem)" class="m-0 list-none p-0">
            <li
              v-for="item in modules"
              :key="`${item.name}@${item.version}`"
              class="flex flex-wrap items-baseline justify-between gap-8px border-b border-line-default py-10px text-13px last:border-b-0"
            >
              <a
                :href="item.url"
                target="_blank"
                rel="noopener noreferrer"
                class="min-w-0 rounded-default text-ink-primary underline [overflow-wrap:anywhere] underline-offset-4 outline-none hover:text-accent focus-visible:shadow-focus-ring"
                >{{ item.name }} <span class="text-ink-secondary"><OnCodeText :text="item.version" /></span
              ></a>
              <span class="break-words text-ink-secondary">{{ item.license }}</span>
            </li>
          </ul>
        </details>
      </template>
    </section>
  </div>
</template>
