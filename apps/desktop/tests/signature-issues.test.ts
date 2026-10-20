import { flushPromises, mount } from '@vue/test-utils';
import type { VueWrapper } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ref } from 'vue';
import { createOnUi } from '@opennavo/ui';
import SignatureCheckCard from '@/components/security/SignatureCheckCard.vue';
import { i18n } from '@/i18n';
import type { AppSignatureEntry, AppSignatureIssue, AppSignatureReport, LocalApp } from '@/ipc/bindings';
import { commands } from '@/ipc/client';
import { useToasts } from '@/composables/useToasts';
import { useLocalAppsStore } from '@/stores/localApps';
import { useUninstallStore } from '@/stores/uninstall';
import { setup } from './helpers';

// The signature check-up card opens a window with every app that has a problem: look at it in Finder, or uninstall
// one or several, through the same review dialog as Installed.
const ui = createOnUi({ locale: ref('zh-CN' as const) });
const wrappers: VueWrapper[] = [];
afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount());
  vi.restoreAllMocks();
  useToasts().clear();
  document.body.innerHTML = '';
});

const entry = (app: Pick<LocalApp, 'path' | 'name' | 'bundleId'>, issues: AppSignatureIssue[]): AppSignatureEntry => ({
  path: app.path,
  name: app.name,
  bundleId: app.bundleId,
  signature: null,
  previousTeamId: null,
  issues,
  changed: false,
  errorCode: null
});

/** The card over a report the test builds from the apps the mock says are on this Mac. */
async function mountCard(build: (apps: LocalApp[]) => AppSignatureEntry[]) {
  const context = await setup('/security');
  const localApps = useLocalAppsStore();
  await localApps.load();
  let report: AppSignatureReport = { checkedAt: Date.now(), apps: build(localApps.items) };
  const get = vi.spyOn(commands, 'appSignatureGet').mockImplementation(async () => ({ status: 'ok', data: report }));
  const wrapper = mount(SignatureCheckCard, {
    global: { plugins: [context.pinia, context.router, i18n, ui] },
    attachTo: document.body
  });
  wrappers.push(wrapper);
  await flushPromises();
  return {
    wrapper,
    localApps,
    get,
    setReport: (next: AppSignatureReport) => {
      report = next;
    }
  };
}
const removable = (apps: LocalApp[]) => apps.filter(app => app.removable);
const dialog = () => document.body.querySelector('[role="dialog"]');
const rows = () => [...document.body.querySelectorAll<HTMLElement>('[role="dialog"] li')];
const dialogButton = (label: string) =>
  [...document.body.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find(
    button => button.textContent?.trim() === label
  );
// Rows are found by the path they show: names can be localized.
const rowOf = (app: { path: string }) => rows().find(row => row.querySelector(`[title="${app.path}"]`))!;
const buttonIn = (row: HTMLElement, label: string) =>
  [...row.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent?.trim() === label)!;
const open = async (wrapper: VueWrapper) => {
  await wrapper
    .findAll('button')
    .find(button => button.text().startsWith('查看全部'))!
    .trigger('click');
  await flushPromises();
};

describe('Signature problems window', () => {
  it('The card shows the first few and opens a window with all of them, most serious first', async () => {
    const { wrapper } = await mountCard(apps => {
      const list = removable(apps);
      return [
        ...list.slice(0, 6).map(app => entry(app, ['invalid'])),
        entry(list[6]!, ['unsigned']),
        entry(list[7]!, ['not_notarized']),
        entry(list[8]!, [])
      ];
    });
    expect(wrapper.text()).toContain('检查了 9 个 App，8 个有问题');
    expect(wrapper.findAll('section[aria-labelledby="security-signatures"] li')).toHaveLength(5);
    expect(dialog()).toBeNull();

    await open(wrapper);
    expect(dialog()?.textContent).toContain('签名有问题的 App');
    expect(dialog()?.textContent).toContain('签名有问题不一定有风险');
    // All eight, not five, grouped by what is wrong: the broken signatures first, the missing notarization last.
    expect(rows()).toHaveLength(8);
    const groups = [...document.body.querySelectorAll<HTMLElement>('[role="dialog"] section[data-issue]')];
    expect(groups.map(group => group.dataset.issue)).toEqual(['invalid', 'unsigned', 'not_notarized']);
    expect(groups.map(group => group.querySelectorAll('li').length)).toEqual([6, 1, 1]);
    // The problem, then how many apps have it.
    expect(groups.map(group => group.querySelector('h3')?.textContent?.trim())).toEqual([
      '签名无效6',
      '没有签名1',
      '未经公证1'
    ]);
    // What a problem means is said once for its group, not on every row.
    const text = dialog()?.textContent ?? '';
    expect(text.split('签名没有通过校验').length - 1).toBe(1);
    expect(text).toContain('没有经过 Apple 公证');
  });

  it('Uninstalls one app through the uninstall review and steps aside for it', async () => {
    const { wrapper, localApps } = await mountCard(apps => removable(apps).map(app => entry(app, ['invalid'])));
    const target = removable(localApps.items)[3]!;
    const start = vi.spyOn(useUninstallStore(), 'start').mockResolvedValue();
    await open(wrapper);
    await buttonIn(rowOf(target), '卸载').click();
    await flushPromises();
    expect(start).toHaveBeenCalledWith([target.id]);
    await vi.waitFor(() => expect(dialog()).toBeNull());
  });

  it('Uninstalls the chosen apps together; the app itself and unknown apps cannot be chosen', async () => {
    const { wrapper, localApps } = await mountCard(apps => [
      ...removable(apps)
        .slice(0, 4)
        .map(app => entry(app, ['invalid'])),
      entry(apps.find(app => !app.removable)!, ['invalid']),
      entry({ path: '/Applications/Gone.app', name: 'Gone', bundleId: null }, ['unsigned'])
    ]);
    const start = vi.spyOn(useUninstallStore(), 'start').mockResolvedValue();
    await open(wrapper);
    const box = (app: { path: string }) => rowOf(app).querySelector<HTMLInputElement>('input[type="checkbox"]')!;
    const gone = { path: '/Applications/Gone.app' };
    const button = () => dialogButton('卸载所选（0）') ?? dialogButton('卸载所选（2）');
    expect(button()?.disabled).toBe(true);

    // OpenNavo cannot uninstall itself, and an app that is gone cannot be uninstalled.
    const self = localApps.items.find(app => !app.removable)!;
    expect(box(self).disabled).toBe(true);
    expect(buttonIn(rowOf(self), '卸载').disabled).toBe(true);
    expect(box(gone).disabled).toBe(true);
    expect(buttonIn(rowOf(gone), '卸载').disabled).toBe(true);
    expect(rowOf(gone).querySelector('label')?.getAttribute('title')).toBe('找不到这个 App，可能已经卸载了');

    const [first, second] = removable(localApps.items).slice(0, 2);
    for (const app of [first!, second!]) {
      box(app).click();
      await flushPromises();
    }
    expect(button()?.textContent?.trim()).toBe('卸载所选（2）');
    expect(button()?.disabled).toBe(false);
    button()!.click();
    await flushPromises();
    expect(start).toHaveBeenCalledOnce();
    expect(new Set(start.mock.calls[0]![0])).toEqual(new Set([first!.id, second!.id]));
  });

  it('Shows an app in Finder', async () => {
    const { wrapper, localApps } = await mountCard(apps => removable(apps).map(app => entry(app, ['team_changed'])));
    const reveal = vi.spyOn(commands, 'localAppReveal');
    const target = removable(localApps.items)[1]!;
    await open(wrapper);
    await buttonIn(rowOf(target), '在访达中显示').click();
    await flushPromises();
    expect(reveal).toHaveBeenCalledWith(target.id);
  });

  it('Drops an app that was uninstalled, and says so when nothing is left', async () => {
    const { wrapper, localApps, get, setReport } = await mountCard(apps =>
      removable(apps)
        .slice(0, 2)
        .map(app => entry(app, ['invalid']))
    );
    await open(wrapper);
    expect(rows()).toHaveLength(2);
    const gone = removable(localApps.items)[0]!;
    const calls = get.mock.calls.length;

    // Rust leaves the uninstalled app out of the report it returns; the window reads it again when the apps change.
    const remaining = removable(localApps.items)[1]!;
    setReport({ checkedAt: Date.now(), apps: [entry(remaining, ['invalid'])] });
    localApps.items = localApps.items.filter(app => app.id !== gone.id);
    await flushPromises();
    expect(get.mock.calls.length).toBeGreaterThan(calls);
    expect(rows()).toHaveLength(1);
    expect(wrapper.text()).toContain('检查了 1 个 App，1 个有问题');

    setReport({ checkedAt: Date.now(), apps: [] });
    localApps.items = localApps.items.filter(app => app.id !== remaining.id);
    await flushPromises();
    expect(document.body.querySelector('[data-testid="signature-issues-empty"]')?.textContent).toContain(
      '没有签名问题了'
    );
  });
});
