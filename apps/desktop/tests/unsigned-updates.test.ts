import { useRunningAppsStore } from '@/stores/runningApps';
import { defineComponent } from 'vue';
import { useLocalAppMenu } from '@/composables/useLocalAppMenu';
import type { Task } from '@/ipc/bindings';
import UpdateReviewConfirm from '@/components/updates/UpdateReviewConfirm.vue';
import { useUpdateReviewConfirmStore } from '@/stores/updateReviewConfirm';
import { updateChallenge, updateErrorKey } from '@/ipc/updateErrors';
import { retryOptions } from '@/stores/permissionPrompt';
import { afterEach, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import UnsignedUpdateConfirm from '@/components/updates/UnsignedUpdateConfirm.vue';
import { i18n } from '@/i18n';
import { commands, unwrap } from '@/ipc/client';
import { useTasksStore } from '@/stores/tasks';
import { useUnsignedUpdateConfirmStore } from '@/stores/unsignedUpdateConfirm';
import { setup } from './helpers';

let context: Awaited<ReturnType<typeof setup>>;
const target = { kind: 'app' as const, token: 'local-iterm2' };
async function init() {
  history.replaceState({}, '', '/?mock=update-sources');
  context = await setup('/updates', { tickMs: 1 });
  const rows = await unwrap(commands.updatesList());
  const row = {
    ...rows.find(item => item.appId === target.token)!,
    name: 'LX Music',
    source: 'electron' as const,
    installedVersion: '2.12.5',
    currentVersion: '2.12.6',
    downloadHost: 'github.com',
    unsignedUpdate: 'release-fingerprint',
    running: false,
    relaunchPending: false
  };
  vi.spyOn(commands, 'updatesList').mockResolvedValue({ status: 'ok', data: [row] });
  return row;
}
afterEach(async () => {
  useUpdateReviewConfirmStore().choose(false);
  useUnsignedUpdateConfirmStore().choose(false);
  await vi.waitFor(() => expect(context?.state.active ?? []).toHaveLength(0), { timeout: 5000 });
  vi.restoreAllMocks();
  history.replaceState({}, '', '/');
});

it('prompts before enqueue and passes only the current release confirmation', async () => {
  await init();
  const enqueue = vi.spyOn(commands, 'taskEnqueue');
  const result = useTasksStore().enqueue('upgrade', target, 'manual', { source: 'electron' });
  await flushPromises();
  expect(useUnsignedUpdateConfirmStore().pending?.[0]?.name).toBe('LX Music');
  expect(enqueue).not.toHaveBeenCalled();
  useUnsignedUpdateConfirmStore().choose(true);
  await result;
  expect(enqueue).toHaveBeenCalledWith(
    'upgrade',
    target,
    expect.objectContaining({ unsignedUpdate: 'release-fingerprint' }),
    'manual'
  );
});

it('cancel skips only unsigned apps in a batch', async () => {
  await init();
  const enqueue = vi.spyOn(commands, 'taskEnqueue');
  const result = useTasksStore().enqueueMany('upgrade', [target, { kind: 'cask', token: 'vlc' }]);
  await flushPromises();
  useUnsignedUpdateConfirmStore().choose(false);
  const tasks = await result;
  expect(tasks.map(task => task.target?.token)).toEqual(['vlc']);
  expect(enqueue).toHaveBeenCalledTimes(1);
});

it('retry prompts again instead of reusing a previous task confirmation', async () => {
  await init();
  const enqueue = vi.spyOn(commands, 'taskEnqueue');
  const result = useTasksStore().enqueue('upgrade', target, 'retry', {
    source: 'electron',
    unsignedUpdate: 'previous-consent'
  });
  await flushPromises();
  expect(useUnsignedUpdateConfirmStore().pending).not.toBeNull();
  useUnsignedUpdateConfirmStore().choose(false);
  expect(await result).toBeNull();
  expect(enqueue).not.toHaveBeenCalled();
});

it.each(['schedule', 'bundle'] as const)('unattended %s never grants consent', async trigger => {
  await init();
  const enqueue = vi.spyOn(commands, 'taskEnqueue');
  expect(
    await useTasksStore().enqueue('upgrade', target, trigger, {
      source: 'electron',
      unsignedUpdate: 'previous-consent'
    })
  ).toBeNull();
  expect(enqueue).not.toHaveBeenCalled();
  expect(useUnsignedUpdateConfirmStore().pending).toBeNull();
});

it('signed updates do not prompt or forward saved unsigned consent', async () => {
  const row = await init();
  vi.mocked(commands.updatesList).mockResolvedValue({ status: 'ok', data: [{ ...row, unsignedUpdate: undefined }] });
  const enqueue = vi.spyOn(commands, 'taskEnqueue');
  await useTasksStore().enqueue('upgrade', target, 'retry', { source: 'electron', unsignedUpdate: 'previous-consent' });
  expect(useUnsignedUpdateConfirmStore().pending).toBeNull();
  expect(enqueue.mock.calls[0]?.[2].unsignedUpdate).toBeUndefined();
});

it('dialog explains identity limitations, shows app/version/source and defaults to cancel', async () => {
  const row = await init();
  const wrapper = mount(UnsignedUpdateConfirm, {
    global: { plugins: [context.pinia, context.router, i18n] },
    attachTo: document.body
  });
  const result = useUnsignedUpdateConfirmStore().ask([row]);
  await flushPromises();
  await vi.waitFor(() => expect(document.activeElement?.hasAttribute('data-unsigned-cancel')).toBe(true));
  const dialog = document.querySelector('[role="alertdialog"]')!;
  expect(dialog.textContent).toContain('无法确认开发者身份');
  for (const value of ['LX Music', '2.12.5', '2.12.6', 'github.com', '仍要安装'])
    expect(dialog.textContent).toContain(value);
  document.querySelector<HTMLButtonElement>('[data-unsigned-cancel]')!.click();
  expect(await result).toBe(false);
  wrapper.unmount();
});

const consent = { key: 'a'.repeat(64), warnings: ['unsigned_developer', 'non_newer'] as const };
function challenge() {
  return { key: consent.key, warnings: [...consent.warnings] };
}

it.each([true, false])('artifact review requires a fresh choice (accepted=%s)', async accepted => {
  await init();
  const enqueue = vi.spyOn(commands, 'taskEnqueue');
  const result = useTasksStore().enqueue('upgrade', target, 'retry', {
    source: 'electron',
    verification: challenge(),
    reopen: true
  });
  await flushPromises();
  expect(useUpdateReviewConfirmStore().pending?.consent).toEqual(challenge());
  expect(useUnsignedUpdateConfirmStore().pending).toBeNull();
  expect(enqueue).not.toHaveBeenCalled();
  useUpdateReviewConfirmStore().choose(accepted);
  await result;
  if (accepted)
    expect(enqueue).toHaveBeenCalledWith(
      'upgrade',
      target,
      expect.objectContaining({ verification: challenge(), unsignedUpdate: 'release-fingerprint', reopen: true }),
      'retry'
    );
  else expect(enqueue).not.toHaveBeenCalled();
  expect(useUnsignedUpdateConfirmStore().pending).toBeNull();
});

it('only a live attended failure opens the review, and duplicate events do not repeat it', async () => {
  await init();
  const tasks = useTasksStore();
  const task = {
    ...context.state.history[0]!,
    id: 'review-live',
    op: 'upgrade',
    target,
    trigger: 'manual',
    options: { source: 'electron' },
    state: 'running',
    percent: null
  } as Task;
  const failed = {
    ...task,
    state: 'failed',
    error: {
      code: 'E_UPDATE_CONFIRMATION',
      message: 'update_confirmation_required',
      detail: JSON.stringify(challenge())
    }
  } as Task;
  const ask = vi.spyOn(useUpdateReviewConfirmStore(), 'ask');
  tasks.applyUpdate(failed);
  await flushPromises();
  expect(ask).not.toHaveBeenCalled();
  tasks.applyUpdate(task);
  tasks.applyUpdate(failed);
  tasks.applyUpdate(failed);
  await flushPromises();
  expect(ask).toHaveBeenCalledTimes(1);
  useUpdateReviewConfirmStore().choose(false);
  await flushPromises();
  tasks.applyUpdate({ ...task, id: 'schedule-review', trigger: 'schedule' });
  tasks.applyUpdate({ ...failed, id: 'schedule-review', trigger: 'schedule' });
  await flushPromises();
  expect(ask).toHaveBeenCalledTimes(1);
});

it('historical retry never keeps a previous artifact approval without a current challenge', async () => {
  await init();
  const task = {
    ...context.state.history[0]!,
    op: 'upgrade',
    target,
    options: { verification: challenge() },
    error: { code: 'E_UPDATE_CHANGED', message: 'update_bundle_changed' }
  } as Task;
  expect(retryOptions(task).verification).toBeUndefined();
  task.error = {
    code: 'E_UPDATE_CONFIRMATION',
    message: 'update_confirmation_required',
    detail: JSON.stringify(challenge())
  };
  expect(retryOptions(task).verification).toEqual(challenge());
});

it('review dialog shows each reason and cancel dismisses without granting consent', async () => {
  const row = await init();
  const wrapper = mount(UpdateReviewConfirm, {
    global: { plugins: [context.pinia, context.router, i18n] },
    attachTo: document.body
  });
  const result = useUpdateReviewConfirmStore().ask(row, challenge());
  await flushPromises();
  await vi.waitFor(() => expect(document.activeElement?.hasAttribute('data-review-cancel')).toBe(true));
  const dialog = document.querySelector('[role="alertdialog"]')!;
  for (const text of ['LX Music', '2.12.5', '2.12.6', 'github.com', '开发者签名', '重装或降级'])
    expect(dialog.textContent).toContain(text);
  document.querySelector<HTMLButtonElement>('[data-review-cancel]')!.click();
  expect(await result).toBe(false);
  wrapper.unmount();
});

it('error mapping distinguishes digest, signatures, changed files and unavailable assessment', () => {
  for (const [message, code] of [
    ['release_download_digest', 'E_UPDATE_DIGEST'],
    ['invalid_bundle_signature', 'E_UPDATE_SIGNATURE'],
    ['eddsa_verification', 'E_UPDATE_FEED_SIGNATURE'],
    ['backup_verification', 'E_UPDATE_CHANGED']
  ]) {
    expect(updateErrorKey({ code: 'E_UPDATE_INTEGRITY', message: message! })).toBe(`errors.${code}`);
  }
  expect(updateErrorKey({ code: 'E_UPDATE_VERIFY_UNAVAILABLE', message: 'notarization_check_unavailable' })).toBe(
    'errors.E_UPDATE_VERIFY_UNAVAILABLE'
  );
  expect(updateChallenge({ code: 'E_UPDATE_CONFIRMATION', message: '', detail: '{bad' })).toBeUndefined();
  expect(
    updateChallenge({
      code: 'E_UPDATE_CONFIRMATION',
      message: '',
      detail: JSON.stringify({ key: 'a'.repeat(64), warnings: ['unknown'] })
    })
  ).toBeUndefined();
});

it('installed-app reinstall resolves a native candidate and uses the normal confirmation flow', async () => {
  const row = await init();
  const apps = await unwrap(commands.localAppsList());
  const app = { ...apps[0]!, id: target.token, updater: 'electron' as const, protectedReason: null };
  const prepare = vi.spyOn(commands, 'updatePrepareReinstall').mockResolvedValue({ status: 'ok', data: row });
  const enqueue = vi.spyOn(commands, 'taskEnqueue');
  let menu!: ReturnType<typeof useLocalAppMenu>;
  const wrapper = mount(
    defineComponent({
      setup() {
        menu = useLocalAppMenu();
        return () => null;
      }
    }),
    { global: { plugins: [context.pinia, context.router, i18n] } }
  );
  expect(menu.itemsFor(app).some(item => item.key === 'reinstall')).toBe(true);
  expect(menu.itemsFor({ ...app, updater: 'mas' }).some(item => item.key === 'reinstall')).toBe(false);
  const result = menu.select(app, app.name, 'reinstall');
  await flushPromises();
  expect(prepare).toHaveBeenCalledWith(target.token);
  expect(useUnsignedUpdateConfirmStore().pending).not.toBeNull();
  useUnsignedUpdateConfirmStore().choose(true);
  await result;
  expect(enqueue).toHaveBeenCalledWith(
    'upgrade',
    target,
    expect.objectContaining({ source: 'electron', unsignedUpdate: 'release-fingerprint' }),
    'manual'
  );
  wrapper.unmount();
});

it('native retry and reinstall also ask to quit a currently running app', async () => {
  const row = await init();
  vi.mocked(commands.updatesList).mockResolvedValue({
    status: 'ok',
    data: [{ ...row, running: true, unsignedUpdate: undefined }]
  });
  const ask = vi.spyOn(useRunningAppsStore(), 'ask').mockResolvedValue({ action: 'quit', reopen: true });
  const enqueue = vi.spyOn(commands, 'taskEnqueue');
  await useTasksStore().enqueue('upgrade', target, 'retry', { source: 'electron' });
  expect(ask).toHaveBeenCalledWith([{ target, name: 'LX Music' }], false);
  expect(enqueue).toHaveBeenCalledWith(
    'upgrade',
    target,
    expect.objectContaining({ quitRunning: true, reopen: true }),
    'retry'
  );
});
