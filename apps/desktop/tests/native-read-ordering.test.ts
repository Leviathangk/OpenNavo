import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { commands, unwrap } from '@/ipc/client';
import type { Task, FirewallRule, FirewallBlocklist } from '@/ipc/bindings';
import { useTasksStore } from '@/stores/tasks';
import { useFirewallStore } from '@/stores/firewall';
import { setup } from './helpers';

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => {
    resolve = done;
  });
  return { promise, resolve };
}
const ok = <T>(data: T) => ({ status: 'ok' as const, data });
let task: Task;
beforeEach(async () => {
  const context = await setup();
  task = { ...context.state.history[0]!, id: 'running-fixture', target: null, state: 'running' };
});
afterEach(() => vi.restoreAllMocks());

it('merges task events during a read without losing unrelated active tasks', async () => {
  const old = deferred<ReturnType<typeof ok<Task[]>>>();
  vi.spyOn(commands, 'taskListActive').mockReturnValue(old.promise);
  const store = useTasksStore();
  const read = store.load();
  store.applyUpdate(task);
  store.applyUpdate({ ...task, state: 'succeeded' });
  store.applyUpdate({ ...task, id: 'new-task', percent: 50 });
  old.resolve(ok([task, { ...task, id: 'unrelated-task' }]));
  await read;
  expect(store.active.map(t => t.id).sort()).toEqual(['new-task', 'unrelated-task']);
  expect(store.active.find(t => t.id === 'new-task')?.percent).toBe(50);
  expect(store.finished.map(t => t.id)).toContain(task.id);
});

it('does not overwrite a newer task list with an older response', async () => {
  const old = deferred<ReturnType<typeof ok<Task[]>>>();
  vi.spyOn(commands, 'taskListActive').mockReturnValueOnce(old.promise).mockResolvedValueOnce(ok([]));
  const store = useTasksStore();
  const first = store.load();
  await store.load();
  old.resolve(ok([task]));
  await first;
  expect(store.active).toEqual([]);
});

const rule: FirewallRule = {
  id: 'rule-fixture',
  app: null,
  action: 'block',
  remote: null,
  port: null,
  until: 'always',
  pid: null,
  source: 'user',
  createdAt: 1,
  lastMatchAt: null,
  matchCount: 0
};
it('does not resurrect a deleted firewall rule from an in-flight read', async () => {
  const old = deferred<ReturnType<typeof ok<FirewallRule[]>>>();
  vi.spyOn(commands, 'firewallRules').mockReturnValue(old.promise);
  vi.spyOn(commands, 'firewallRuleDelete').mockResolvedValue(ok(null));
  const store = useFirewallStore();
  store.rules = [rule];
  const read = store.loadRules();
  await store.deleteRules([rule.id]);
  old.resolve(ok([rule]));
  await read;
  expect(store.rules).toEqual([]);
});

it('keeps a saved rule instead of restoring its old action', async () => {
  const old = deferred<ReturnType<typeof ok<FirewallRule[]>>>();
  vi.spyOn(commands, 'firewallRules').mockReturnValue(old.promise);
  vi.spyOn(commands, 'firewallRuleSave').mockResolvedValue(ok({ ...rule, action: 'allow' }));
  const store = useFirewallStore();
  store.rules = [rule];
  const read = store.loadRules();
  await store.saveRule({ id: rule.id, appKey: null, appPath: null, action: 'allow', remote: null, port: null });
  old.resolve(ok([rule]));
  await read;
  expect(store.rules[0]?.action).toBe('allow');
});

it('discards older rules even when both operations are reads', async () => {
  const old = deferred<ReturnType<typeof ok<FirewallRule[]>>>();
  vi.spyOn(commands, 'firewallRules').mockReturnValueOnce(old.promise).mockResolvedValueOnce(ok([]));
  const store = useFirewallStore();
  const first = store.loadRules();
  await store.loadRules();
  old.resolve(ok([rule]));
  await first;
  expect(store.rules).toEqual([]);
});

it('does not overwrite a pushed firewall status with an older read', async () => {
  const previous = await unwrap(commands.firewallStatus());
  const old = deferred<ReturnType<typeof ok<typeof previous>>>();
  vi.spyOn(commands, 'firewallStatus').mockReturnValue(old.promise);
  const store = useFirewallStore();
  const read = store.loadStatus();
  const current = { ...previous, mode: 'block' as const };
  store.applyStatus(current);
  old.resolve(ok(previous));
  await read;
  expect(store.status).toEqual(current);
});

const blocklist: FirewallBlocklist = {
  id: 'list-fixture',
  name: 'Fixture',
  source: 'https://example.com/hosts',
  enabled: true,
  entries: 1,
  updatedAt: null,
  error: null
};

it.each(['save', 'delete'] as const)(
  'finishes the initial rules read after %s and preserves unrelated rules',
  async action => {
    const pending = deferred<ReturnType<typeof ok<FirewallRule[]>>>();
    const unrelated = { ...rule, id: 'unrelated-rule' };
    const saved = { ...rule, action: 'allow' as const };
    const reads = vi
      .spyOn(commands, 'firewallRules')
      .mockReturnValueOnce(pending.promise)
      .mockResolvedValue(ok([unrelated]));
    vi.spyOn(commands, 'firewallRuleSave').mockResolvedValue(ok(saved));
    vi.spyOn(commands, 'firewallRuleDelete').mockResolvedValue(ok(null));
    const store = useFirewallStore();
    const read = store.loadRules();
    if (action === 'save')
      await store.saveRule({ id: rule.id, appKey: null, appPath: null, action: 'allow', remote: null, port: null });
    else await store.deleteRules([rule.id]);
    pending.resolve(ok([rule, unrelated]));
    await read;
    expect(store.rulesLoaded).toBe(true);
    expect(store.rules).toEqual(action === 'save' ? [saved, unrelated] : [unrelated]);
    store.applyChanged('rules');
    expect(reads).toHaveBeenCalledTimes(2);
  }
);

it.each(['add', 'set', 'refresh', 'remove'] as const)(
  'finishes the initial blocklists read after %s and preserves unrelated lists',
  async action => {
    const pending = deferred<ReturnType<typeof ok<FirewallBlocklist[]>>>();
    const unrelated = { ...blocklist, id: 'unrelated-list' };
    const saved = { ...blocklist, enabled: false, entries: 42 };
    const reads = vi
      .spyOn(commands, 'firewallBlocklists')
      .mockReturnValueOnce(pending.promise)
      .mockResolvedValue(ok([unrelated]));
    vi.spyOn(commands, 'firewallBlocklistAdd').mockResolvedValue(ok(saved));
    vi.spyOn(commands, 'firewallBlocklistSet').mockResolvedValue(ok(saved));
    vi.spyOn(commands, 'firewallBlocklistRefresh').mockResolvedValue(ok(saved));
    vi.spyOn(commands, 'firewallBlocklistRemove').mockResolvedValue(ok(null));
    const store = useFirewallStore();
    const read = store.loadBlocklists();
    if (action === 'add') await store.addBlocklist(saved.source);
    else if (action === 'set') await store.setBlocklist(saved.id, false);
    else if (action === 'refresh') await store.refreshBlocklist(saved.id);
    else await store.removeBlocklist(saved.id);
    pending.resolve(ok([blocklist, unrelated]));
    await read;
    expect(store.blocklistsLoaded).toBe(true);
    expect(store.blocklists).toEqual(action === 'remove' ? [unrelated] : [saved, unrelated]);
    store.applyChanged('blocklists');
    expect(reads).toHaveBeenCalledTimes(2);
  }
);

it('discards older blocklist reads without replaying mutations into future reads', async () => {
  const pending = deferred<ReturnType<typeof ok<FirewallBlocklist[]>>>();
  vi.spyOn(commands, 'firewallBlocklists').mockReturnValueOnce(pending.promise).mockResolvedValue(ok([]));
  vi.spyOn(commands, 'firewallBlocklistAdd').mockResolvedValue(ok(blocklist));
  const store = useFirewallStore();
  const first = store.loadBlocklists();
  await store.addBlocklist(blocklist.source);
  await store.loadBlocklists();
  pending.resolve(ok([blocklist]));
  await first;
  expect(store.blocklistsLoaded).toBe(true);
  expect(store.blocklists).toEqual([]);
});
