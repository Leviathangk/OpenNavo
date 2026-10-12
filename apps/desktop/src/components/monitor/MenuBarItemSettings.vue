<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnCard, OnToggle } from '@opennavo/ui';
import type { MenuBarStyle } from '@/ipc/bindings';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import SettingRow from '@/components/settings/SettingRow.vue';
import { useMonitorStore } from '@/stores/monitor';
import type { DisplayModule } from '@/stores/monitor';
import MenuBarStylePicker from './MenuBarStylePicker.vue';
import MenuBarWidget from './MenuBarWidget.vue';

// One module's menu bar item (§12): its own switch and style; any number of items can be on.
const props = defineProps<{ module: DisplayModule }>();

const { t } = useI18n();
const store = useMonitorStore();
const toasts = useToasts();
const { errorText } = usePackageState();

const item = computed(() => store.item(props.module));

async function update(patch: { enabled?: boolean; style?: MenuBarStyle }) {
  try {
    await store.setMenuBar(props.module, patch);
    if (patch.enabled != null)
      toasts.push({
        tone: 'success',
        title: patch.enabled
          ? t('monitor.menuBar.added', { name: t(`monitor.modules.${props.module}`) })
          : t('monitor.menuBar.removed', { name: t(`monitor.modules.${props.module}`) }),
        description: patch.enabled ? t('monitor.menuBar.addedBody') : undefined
      });
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}
</script>

<template>
  <section class="flex flex-col gap-8px">
    <h3 class="m-0 text-13px font-600 text-ink-primary">{{ t('monitor.menuBar.title') }}</h3>
    <OnCard padding="none">
      <SettingRow
        :label="t('monitor.menuBar.show')"
        :hint="t('monitor.menuBar.showHint')"
        :label-id="`monitor-menubar-${module}`"
      >
        <!-- A flex box, so the item's baseline cannot add a line's height above it. -->
        <span class="flex rounded-[7px] border border-solid border-line-default bg-[rgba(38,38,40,0.96)] px-6px py-2px">
          <MenuBarWidget :module="module" />
        </span>
        <OnToggle
          :model-value="item.enabled"
          :aria-labelledby="`monitor-menubar-${module}`"
          @update:model-value="value => update({ enabled: value })"
        />
      </SettingRow>
      <SettingRow :label="t('monitor.menuBar.style')" :hint="t('monitor.menuBar.styleHint')">
        <MenuBarStylePicker
          :module="module"
          :model-value="item.style"
          @update:model-value="value => update({ style: value })"
        />
      </SettingRow>
    </OnCard>
  </section>
</template>
