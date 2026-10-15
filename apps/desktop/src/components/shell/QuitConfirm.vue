<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnConfirm } from '@opennavo/ui';
import { commands, events, unwrap } from '@/ipc/client';

// Quit confirmation (06 §4.3, 08 §12.5): Rust intercepts quitting with active tasks and sends app:quit-requested only to the main window.
// On consent, app_quit interrupts tasks and exits within ten seconds; cancel keeps running.
const { t } = useI18n();
const open = ref(false);
const count = ref(0);
const quitting = ref(false);
let stop: (() => void) | undefined;

onMounted(async () => {
  stop = await events.appQuitRequested.listen(event => {
    count.value = event.payload.running + event.payload.queued;
    open.value = true;
  });
});
onBeforeUnmount(() => stop?.());

async function quit() {
  quitting.value = true;
  try {
    await unwrap(commands.appQuit());
  } finally {
    quitting.value = false;
    open.value = false;
  }
}
</script>

<template>
  <OnConfirm
    v-model:open="open"
    tone="danger"
    :title="t('quit.title', { n: count }, { plural: count })"
    :description="t('quit.description')"
    :confirm-label="t('quit.confirm')"
    :cancel-label="t('quit.cancel')"
    :loading="quitting"
    @confirm="quit"
    @cancel="commands.appCancelQuit()"
  />
</template>
