<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useRoute } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { convertFileSrc, isTauri } from '@tauri-apps/api/core';
import { OnIcon } from '@opennavo/ui';
import type { PermissionGuideInfo } from '@/ipc/bindings';
import { commands, unwrap } from '@/ipc/client';
import { paneDone } from '@/stores/permissions';
import type { GuidePane } from '@/stores/permissions';
import { usePermissionsStore } from '@/stores';
import appIcon from '../../src-tauri/icons/128x128@2x.png';

// Permission overlay (06 §12.7): appears below System Settings. Drag the icon into the list above to add OpenNavo.
// macOS checks a development build as the app it was started from (a terminal such as Paseo), so the overlay names and
// drags that app; when it is not an .app bundle, instruct users to select with + instead. Show completion once the switch
// is on; Full Disk Access applies only after OpenNavo reopens, so "on, awaiting a reopen" completes too. Rust closes the
// window shortly afterward.
const { t } = useI18n();
const route = useRoute();
const permissions = usePermissionsStore();
const info = ref<PermissionGuideInfo | null>(null);
const frame = ref<HTMLElement | null>(null);

const draggable = computed(() => info.value?.draggable ?? false);
const owner = computed(() => info.value?.ownerName ?? 'OpenNavo');
const icon = computed(() => (info.value?.ownerIcon && isTauri() ? convertFileSrc(info.value.ownerIcon) : appIcon));

const pane = computed<GuidePane>(() =>
  route.params.pane === 'app_management' ? 'app_management' : 'full_disk_access'
);
const name = computed(() => (pane.value === 'app_management' ? 'appManagement' : 'fullDiskAccess'));
const done = computed(() => paneDone(pane.value, permissions.status));

// The window is as tall as the panel: longer translations wrap, so report the height whenever it changes.
let fitted = 0;
function fit() {
  const height = Math.ceil(frame.value?.getBoundingClientRect().height ?? 0);
  if (!height || height === fitted) return;
  fitted = height;
  void commands.permissionGuideFit(height);
}
const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(fit);

onMounted(async () => {
  if (frame.value) observer?.observe(frame.value);
  fit();
  info.value = await unwrap(commands.permissionGuideInfo()).catch(() => null);
});
onBeforeUnmount(() => observer?.disconnect());

function drag(event: MouseEvent) {
  if (!draggable.value || event.button !== 0) return;
  // This synchronous command runs on the main thread and must be sent while the mouse is held down.
  void commands.permissionGuideDrag();
}

function close() {
  void commands.permissionGuideClose();
}
</script>

<template>
  <div ref="frame" class="p-12px">
    <section
      class="relative flex min-h-88px min-w-0 items-center gap-14px rounded-huge border border-solid border-line-default bg-surface-card px-16px py-10px"
      role="dialog"
      :aria-label="t(`permission.guide.${name}`)"
    >
      <span
        v-if="done"
        class="grid h-56px w-56px shrink-0 place-items-center rounded-full bg-status-success-subtle text-status-success"
      >
        <OnIcon name="check" :size="26" />
      </span>
      <button
        v-else
        type="button"
        class="group relative m-0 grid h-60px w-60px shrink-0 place-items-center rounded-big border-none bg-transparent p-0 outline-none focus-visible:shadow-focus-ring"
        :class="draggable ? 'cursor-grab active:cursor-grabbing' : 'cursor-default'"
        :aria-label="t('permission.guide.dragLabel', { name: owner })"
        @mousedown="drag"
      >
        <img
          :src="icon"
          alt=""
          class="h-64px w-64px select-none transition-transform duration-fast ease-standard"
          :class="draggable ? 'group-hover:-translate-y-2px' : ''"
          draggable="false"
        />
        <span
          v-if="draggable"
          class="absolute -right-1px bottom-0 grid h-20px w-20px place-items-center rounded-full bg-brand-coral text-ink-on-accent"
          aria-hidden="true"
        >
          <OnIcon name="arrow-up" :size="12" />
        </span>
      </button>
      <div class="min-w-0 flex-1 pr-14px">
        <p class="m-0 text-11.5px font-500 text-ink-tertiary">
          {{ t(`permission.guide.${name}`) }}
        </p>
        <p class="m-0 mt-2px text-14px font-600 leading-[1.35] text-ink-primary">
          {{
            done
              ? t(`permission.guide.done.${name}`)
              : draggable
                ? t('permission.guide.dragTitle', { name: owner })
                : t('permission.guide.plusTitle', { name: owner })
          }}
        </p>
        <p class="m-0 mt-3px text-12px leading-[1.45] text-ink-tertiary">
          {{ done ? t('permission.guide.doneHint') : t('permission.guide.toggleHint', { name: owner }) }}
        </p>
      </div>
      <button
        type="button"
        class="absolute right-8px top-8px m-0 grid h-22px w-22px cursor-pointer place-items-center rounded-full border-none bg-transparent p-0 text-ink-tertiary outline-none hover:bg-surface-raised hover:text-ink-primary focus-visible:shadow-focus-ring"
        :aria-label="t('permission.guide.close')"
        @click="close"
      >
        <OnIcon name="x" :size="13" />
      </button>
    </section>
  </div>
</template>

<style>
/* Reset only the guidance document; the transparent window displays only the rounded panel, sized to it by Rust. */
html[data-opennavo-window='guide'] {
  background: transparent;
  color-scheme: normal;
}

html[data-opennavo-window='guide'] body {
  overflow: hidden;
  background: transparent;
}
</style>
