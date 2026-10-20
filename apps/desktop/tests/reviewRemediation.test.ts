import { afterEach, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { OnConfirm, OnMenu } from '@opennavo/ui';
import { setup } from './helpers';
import { i18n } from '@/i18n';
import { commands, unwrap } from '@/ipc/client';
import type { AvScanResult, AvProgress, MasStatus, LocalApp } from '@/ipc/bindings';
import { useToasts } from '@/composables/useToasts';
import { useSecurityStore } from '@/stores/security';
import { useMasStore } from '@/stores/mas';
import { useLocalAppsStore, useUpdatesStore } from '@/stores';
import SecurityPage from '@/pages/SecurityPage.vue';
import SecurityQuarantinePanel from '@/components/security/SecurityQuarantinePanel.vue';
import MasAlsoPanel from '@/components/appstore/MasAlsoPanel.vue';
import MasGetButton from '@/components/appstore/MasGetButton.vue';
import { requireDone } from '@/components/security/outcome';

const wrappers: VueWrapper[] = [];
afterEach(() => {
  wrappers.splice(0).forEach(w => w.unmount());
  useToasts().clear();
  vi.restoreAllMocks();
  history.replaceState(null, '', '/');
  document.body.innerHTML = '';
});
const savedScan = (scanId = 'persisted'): AvScanResult => ({
  scanId,
  kind: 'custom',
  state: 'done',
  startedAt: 1,
  finishedAt: 2,
  filesScanned: 1,
  bytesScanned: 68,
  skipped: { dataless: 0, tooLarge: 0, unreadable: 0, excluded: 0 },
  metrics: {
    cachedFiles: 0,
    workers: 1,
    preparationMs: 0,
    hashMs: 0,
    engineMs: 0,
    queueMs: 0,
    inspectionMs: 0,
    cleanupMs: 0,
    persistenceMs: 0,
    slowFiles: 0
  },
  deniedRoots: [],
  findings: [
    {
      id: 'finding',
      path: '/tmp/eicar.com',
      signature: 'Eicar-Test-Signature',
      sizeBytes: 68,
      sha256: 'a'.repeat(64),
      ownerApp: null,
      requiresAdmin: false,
      status: 'open'
    }
  ],
  errorCode: null
});
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(r => {
    resolve = r;
  });
  return { promise, resolve };
}
const ok = <T>(data: T) => ({ status: 'ok' as const, data });

it.each(['E_PROTECTED', 'E_ADMIN_CANCELED', 'E_UNRECOGNIZED'])(
  'shows a real quarantine failure for %s and keeps the finding open',
  async code => {
    const ctx = await setup('/security/scan');
    const store = useSecurityStore();
    store.scan = savedScan();
    vi.spyOn(commands, 'avFindingsAct').mockResolvedValue(
      ok({ done: [], failed: [{ id: 'finding', code }], restorable: false })
    );
    vi.spyOn(commands, 'avScanGet').mockResolvedValue(ok(savedScan()));
    const wrapper = mount(SecurityPage, { global: { plugins: [ctx.pinia, ctx.router, i18n] } });
    wrappers.push(wrapper);
    await flushPromises();
    await wrapper
      .findAll('button')
      .find(b => b.text() === '隔离')!
      .trigger('click');
    await flushPromises();
    expect(store.scan?.findings[0]?.status).toBe('open');
    expect(useToasts().items.value.at(-1)?.tone).toBe('danger');
    expect(useToasts().items.value.some(t => t.tone === 'success')).toBe(false);
  }
);
it.each(['delete', 'trust'])('reports the per-item %s failure from the finding menu', async action => {
  const ctx = await setup('/security/scan');
  const store = useSecurityStore();
  store.scan = savedScan();
  vi.spyOn(commands, 'avFindingsAct').mockResolvedValue(
    ok({ done: [], failed: [{ id: 'finding', code: 'E_PROTECTED' }], restorable: false })
  );
  vi.spyOn(commands, 'avScanGet').mockResolvedValue(ok(savedScan()));
  const wrapper = mount(SecurityPage, { global: { plugins: [ctx.pinia, ctx.router, i18n] } });
  wrappers.push(wrapper);
  await flushPromises();
  wrapper.findComponent(OnMenu).vm.$emit('select', action);
  await flushPromises();
  expect(useToasts().items.value.at(-1)?.tone).toBe('danger');
});
it('requires the selected ID in done, including partial results and empty cancellation', () => {
  const outcome = { done: ['one'], failed: [{ id: 'two', code: 'E_PROTECTED' }], restorable: true };
  expect(() => requireDone(outcome, 'one')).not.toThrow();
  expect(() => requireDone(outcome, 'two')).toThrow();
  expect(() => requireDone({ done: [], failed: [], restorable: false }, 'one')).toThrow();
});

it('hydrates saved findings on a fresh visit and merges concurrent overview loads', async () => {
  const ctx = await setup('/security/scan');
  const base = await unwrap(commands.securityOverview());
  vi.spyOn(commands, 'securityOverview').mockResolvedValue(
    ok({
      ...base,
      lastScan: {
        scanId: 'persisted',
        kind: 'quick',
        state: 'done',
        finishedAt: 2,
        filesScanned: 1,
        threats: 1
      }
    })
  );
  const get = vi.spyOn(commands, 'avScanGet').mockResolvedValue(ok(savedScan()));
  const store = useSecurityStore();
  await Promise.all([store.loadOverview(), store.loadOverview()]);
  expect(get).toHaveBeenCalledTimes(1);
  const wrapper = mount(SecurityPage, { global: { plugins: [ctx.pinia, ctx.router, i18n] } });
  wrappers.push(wrapper);
  await flushPromises();
  expect(wrapper.text()).toContain('Eicar-Test-Signature');
  expect(wrapper.findAll('button').some(b => b.text() === '隔离')).toBe(true);
  expect(get).toHaveBeenCalledTimes(1);
});
it('retries a failed persisted detail without starting another scan', async () => {
  await setup('/security/scan');
  const base = await unwrap(commands.securityOverview());
  vi.spyOn(commands, 'securityOverview').mockResolvedValue(
    ok({
      ...base,
      lastScan: {
        scanId: 'persisted',
        kind: 'quick',
        state: 'done',
        finishedAt: 2,
        filesScanned: 1,
        threats: 1
      }
    })
  );
  const get = vi
    .spyOn(commands, 'avScanGet')
    .mockRejectedValueOnce(new Error('offline'))
    .mockResolvedValue(ok(savedScan()));
  const start = vi.spyOn(commands, 'avScanStart');
  const store = useSecurityStore();
  await expect(store.loadOverview()).rejects.toThrow('offline');
  await store.loadOverview();
  expect(get).toHaveBeenCalledTimes(2);
  expect(store.scan?.scanId).toBe('persisted');
  expect(start).not.toHaveBeenCalled();
});
it.each(['start', 'progress', 'finished'])('a late persisted detail cannot replace a new %s', async mode => {
  await setup('/security/scan');
  const base = await unwrap(commands.securityOverview());
  vi.spyOn(commands, 'securityOverview').mockResolvedValue(
    ok({
      ...base,
      lastScan: {
        scanId: 'persisted',
        kind: 'quick',
        state: 'done',
        finishedAt: 2,
        filesScanned: 1,
        threats: 1
      }
    })
  );
  const old = deferred<ReturnType<typeof ok<AvScanResult>>>();
  vi.spyOn(commands, 'avScanGet').mockImplementation(id =>
    id === 'persisted' ? old.promise : Promise.resolve(ok(savedScan('new')))
  );
  vi.spyOn(commands, 'avScanStart').mockResolvedValue(ok({ scanId: 'new' }));
  const store = useSecurityStore();
  const loading = store.loadOverview();
  await flushPromises();
  if (mode === 'start') await store.startScan('quick');
  if (mode === 'finished') store.applyFinished(savedScan('new'));
  if (mode === 'progress') {
    store.applyProgress({ scanId: 'new', findings: 1 } as AvProgress);
    await flushPromises();
  }
  old.resolve(ok(savedScan()));
  await loading;
  expect(store.scan?.scanId).toBe('new');
});
it('an empty overview does not ask for nonexistent scan detail', async () => {
  await setup('/security/scan');
  const base = await unwrap(commands.securityOverview());
  vi.spyOn(commands, 'securityOverview').mockResolvedValue(ok({ ...base, lastScan: null }));
  const get = vi.spyOn(commands, 'avScanGet');
  await useSecurityStore().loadOverview();
  expect(get).not.toHaveBeenCalled();
});

it.each(['ok', 'missing'])('direct catalog Get initializes MAS %s and chooses the correct action', async engine => {
  history.replaceState(null, '', '/?mock=mas-engine');
  const ctx = await setup('/package/cask/markedit');
  const base = await unwrap(commands.masStatus());
  const status = vi
    .spyOn(commands, 'masStatus')
    .mockResolvedValue(ok({ ...base, engine: engine as MasStatus['engine'] }));
  const open = vi.spyOn(commands, 'masOpenStore');
  const enqueue = vi.spyOn(commands, 'taskEnqueue');
  const wrapper = mount(MasAlsoPanel, {
    props: { bundleIds: ['app.cyan.markedit'] },
    global: { plugins: [ctx.pinia, ctx.router, i18n] }
  });
  wrappers.push(wrapper);
  await flushPromises();
  const get = wrapper.findAll('button').find(b => b.text() === '获取')!;
  await Promise.all([get.trigger('click'), get.trigger('click')]);
  await flushPromises();
  expect(status).toHaveBeenCalledTimes(1);
  if (engine === 'ok') {
    expect(enqueue).toHaveBeenCalledTimes(1);
    expect(open).not.toHaveBeenCalled();
  } else {
    expect(open).toHaveBeenCalledTimes(1);
    expect(enqueue).not.toHaveBeenCalled();
  }
});
it('MAS status calls merge, failures retry, and stale region responses do not overwrite the reset', async () => {
  await setup('/');
  const base = await unwrap(commands.masStatus());
  const store = useMasStore();
  const request = deferred<ReturnType<typeof ok<MasStatus>>>();
  const status = vi
    .spyOn(commands, 'masStatus')
    .mockReturnValueOnce(request.promise)
    .mockRejectedValueOnce(new Error('offline'))
    .mockResolvedValue(ok(base));
  const first = store.ensureStatus();
  const second = store.ensureStatus();
  expect(status).toHaveBeenCalledTimes(1);
  store.reset();
  await expect(store.ensureStatus()).rejects.toThrow('offline');
  await store.ensureStatus();
  request.resolve(ok({ ...base, engine: 'missing' }));
  expect(await Promise.all([first, second])).toEqual([base, base]);
  expect(store.status).toEqual(base);
});

it('quarantine purge surfaces a per-item failure and retains the entry', async () => {
  const ctx = await setup('/security/quarantine');
  const entry = {
    id: 'vault',
    name: 'Fixture',
    originalPath: '/tmp/Fixture',
    sizeBytes: 1,
    sha256: 'a'.repeat(64),
    reason: { kind: 'user' as const },
    quarantinedAt: 1
  };
  vi.spyOn(commands, 'quarantineList').mockResolvedValue(ok([entry]));
  vi.spyOn(commands, 'quarantineDelete').mockResolvedValue(
    ok({ done: [], failed: [{ id: 'vault', code: 'E_PROTECTED' }], restorable: false })
  );
  const wrapper = mount(SecurityQuarantinePanel, {
    global: { plugins: [ctx.pinia, ctx.router, i18n] }
  });
  wrappers.push(wrapper);
  await flushPromises();
  await wrapper
    .findAll('button')
    .find(b => b.text() === i18n.global.t('security.quarantine.delete'))!
    .trigger('click');
  wrapper.findComponent(OnConfirm).vm.$emit('confirm');
  await flushPromises();
  expect(useToasts().items.value.at(-1)?.tone).toBe('danger');
  expect(useSecurityStore().quarantine).toEqual([entry]);
});

it('two catalog panels merge status initialization and retry an action after a failed read', async () => {
  history.replaceState(null, '', '/?mock=mas-engine');
  const ctx = await setup('/package/cask/markedit');
  const base = await unwrap(commands.masStatus());
  const initial = deferred<ReturnType<typeof ok<MasStatus>>>();
  const status = vi
    .spyOn(commands, 'masStatus')
    .mockReturnValueOnce(initial.promise)
    .mockRejectedValueOnce(new Error('offline'))
    .mockResolvedValue(ok(base));
  const enqueue = vi.spyOn(commands, 'taskEnqueue');
  const open = vi.spyOn(commands, 'masOpenStore');
  for (let i = 0; i < 2; i++) {
    wrappers.push(
      mount(MasAlsoPanel, {
        props: { bundleIds: ['app.cyan.markedit'] },
        global: { plugins: [ctx.pinia, ctx.router, i18n] }
      })
    );
  }
  await flushPromises();
  expect(status).toHaveBeenCalledTimes(1);
  initial.resolve(ok(base));
  await flushPromises();
  useMasStore().reset();
  const get = wrappers[0]!.findAll('button').find(b => b.text() === '获取')!;
  await get.trigger('click');
  await flushPromises();
  expect(useToasts().items.value.at(-1)?.tone).toBe('danger');
  expect(enqueue).not.toHaveBeenCalled();
  expect(open).not.toHaveBeenCalled();
  await get.trigger('click');
  await flushPromises();
  expect(enqueue).toHaveBeenCalledTimes(1);
  expect(open).not.toHaveBeenCalled();
  expect(status).toHaveBeenCalledTimes(3);
});

it('an accepted Open stays Open across delayed status/local reads; a later explicit Update upgrades', async () => {
  const ctx = await setup('/appstore/497799835');
  const [app] = await unwrap(commands.masLookup([497799835]));
  if (!app) throw new Error('Missing Xcode fixture');
  const mas = useMasStore();
  mas.installed = await unwrap(commands.masInstalled());
  const [baseUpdate] = await unwrap(commands.updatesList());
  if (!baseUpdate) throw new Error('Missing update fixture');
  const updates = useUpdatesStore();
  const acceptedUpdate = {
    ...baseUpdate,
    source: 'mas' as const,
    token: String(app.adamId),
    ignored: false,
    informationalUrl: null
  };
  updates.items = [acceptedUpdate];
  const upgrade = vi.spyOn(updates, 'upgrade').mockResolvedValue([]);
  const locals = await unwrap(commands.localAppsList());
  const local = locals.find(item => item.bundleId === app.bundleId);
  if (!local) throw new Error('Missing local Xcode fixture');
  useLocalAppsStore().loaded = false;
  const localRead = deferred<ReturnType<typeof ok<LocalApp[]>>>();
  vi.spyOn(commands, 'localAppsList').mockReturnValue(localRead.promise);
  const open = vi.spyOn(commands, 'localAppOpen').mockResolvedValue(ok(null));
  const baseStatus = await unwrap(commands.masStatus());
  const statusRead = deferred<ReturnType<typeof ok<MasStatus>>>();
  vi.spyOn(commands, 'masStatus').mockReturnValue(statusRead.promise);
  const wrapper = mount(MasGetButton, { props: { app }, global: { plugins: [ctx.pinia, ctx.router, i18n] } });
  wrappers.push(wrapper);
  expect(wrapper.find('button').text()).toMatch(/Open|打开/);
  await Promise.all([wrapper.find('button').trigger('click'), wrapper.find('button').trigger('click')]);
  statusRead.resolve(ok({ ...baseStatus, engine: 'ok' }));
  await flushPromises();
  expect(upgrade).not.toHaveBeenCalled();
  localRead.resolve(ok(locals));
  await flushPromises();
  expect(open).toHaveBeenCalledExactlyOnceWith(local.id);
  expect(upgrade).not.toHaveBeenCalled();
  expect(wrapper.find('button').text()).toMatch(/Update|更新/);
  await wrapper.find('button').trigger('click');
  await flushPromises();
  expect(upgrade).toHaveBeenCalledExactlyOnceWith([acceptedUpdate]);
  expect(open).toHaveBeenCalledTimes(1);
});

it.each(['unknown', 'missing', 'failed'])(
  'paid Get opens Store with %s engine status and never enqueues',
  async mode => {
    const ctx = await setup('/appstore/424389933');
    const [app] = await unwrap(commands.masLookup([424389933]));
    if (!app) throw new Error('Missing paid fixture');
    expect(app.price).toBeGreaterThan(0);
    const mas = useMasStore();
    if (mode === 'missing') mas.status = { ...(await unwrap(commands.masStatus())), engine: 'missing' };
    const status = vi.spyOn(commands, 'masStatus').mockRejectedValue(new Error('engine unavailable'));
    if (mode === 'failed') await expect(mas.ensureStatus()).rejects.toThrow('engine unavailable');
    const callsBeforeClick = status.mock.calls.length;
    const open = vi.spyOn(commands, 'masOpenStore').mockResolvedValue(ok(null));
    const enqueue = vi.spyOn(commands, 'taskEnqueue');
    const wrapper = mount(MasGetButton, { props: { app }, global: { plugins: [ctx.pinia, ctx.router, i18n] } });
    wrappers.push(wrapper);
    await Promise.all([wrapper.find('button').trigger('click'), wrapper.find('button').trigger('click')]);
    await flushPromises();
    expect(open).toHaveBeenCalledExactlyOnceWith(app.adamId);
    expect(status).toHaveBeenCalledTimes(callsBeforeClick);
    expect(enqueue).not.toHaveBeenCalled();
    expect(useToasts().items.value).toHaveLength(0);
  }
);

it('Cron conflict is local feedback and leaves unrelated startup rows visible', async () => {
  const ctx = await setup('/security/startup');
  const base = await unwrap(commands.startupScan());
  vi.spyOn(commands, 'startupScan').mockResolvedValue(
    ok({ ...base, missing: ['cron'], cronError: 'E_STARTUP_CHANGED' })
  );
  const wrapper = mount(SecurityPage, { global: { plugins: [ctx.pinia, ctx.router, i18n] } });
  wrappers.push(wrapper);
  await flushPromises();
  expect(wrapper.text()).toContain(i18n.global.t('errors.E_STARTUP_CHANGED'));
  expect(wrapper.text()).toContain('com.helper.updater');
  expect(wrapper.text()).not.toContain(i18n.global.t('security.startup.grant'));
});
