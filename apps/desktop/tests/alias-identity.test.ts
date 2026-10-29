import { flushPromises, mount } from '@vue/test-utils';
import { disposePinia } from 'pinia';
import { afterEach, expect, it, vi } from 'vitest';
import AppInfoSheet from '@/components/installed/AppInfoSheet.vue';
import InstalledPage from '@/pages/InstalledPage.vue';
import { i18n } from '@/i18n';
import type { LocalApp, Task } from '@/ipc/bindings';
import { commands, unwrap } from '@/ipc/client';
import { useLibraryStore } from '@/stores/library';
import { matchesCask, useLocalAppsStore } from '@/stores/localApps';
import { useNamesStore } from '@/stores/names';
import { usePermissionsStore } from '@/stores/permissions';
import { useTasksStore } from '@/stores/tasks';
import { useUpdatesStore } from '@/stores/updates';
import { matchLocalApps } from '@/utils/deepLinkPreview';
import { catalogBrewfile, catalogInstallCommand } from '@/utils/brew';
import { setup } from './helpers';

afterEach(() => vi.restoreAllMocks());

it.each(['unlisted-editor', 'homebrew/cask/unlisted-editor', 'vendor/tap/unlisted-editor'])(
  'Installed matches updates and tasks outside the catalog using the exact installed token %s',
  async token => {
    window.history.replaceState(null, '', '/?mock=full_disk_access');
    const context = await setup('/installed');
    const app: LocalApp = {
      ...(await unwrap(commands.localAppsList()))[0]!,
      id: 'unlisted-editor',
      name: 'Unlisted Editor',
      source: 'homebrew',
      cask: token.includes('/') ? token : `homebrew/cask/${token}`,
      catalogToken: null,
      removable: true,
      protectedReason: null
    };
    const installed = (await unwrap(commands.libraryList()))[0]!;
    const library = useLibraryStore();
    library.items = [{ ...installed, token, fullToken: app.cask, pinned: false }];
    const update = (await unwrap(commands.updatesList()))[0]!;
    const updates = useUpdatesStore();
    updates.items = [{ ...update, kind: 'cask', token, source: 'homebrew' }];
    vi.spyOn(commands, 'localAppsList').mockResolvedValue({ status: 'ok', data: [app] });
    await usePermissionsStore().refresh();
    const wrapper = mount(InstalledPage, {
      global: { plugins: [context.pinia, context.router, i18n] }
    });
    try {
      await flushPromises();
      const row = () => wrapper.get('tbody tr');
      expect(row().text()).toContain(i18n.global.t('library.status.outdated'));
      expect(row().text()).not.toContain(i18n.global.t('library.status.latest'));

      // An update for another tap with the same short name must not match.
      updates.items = [{ ...updates.items[0]!, token: 'other/tap/unlisted-editor' }];
      await flushPromises();
      expect(row().text()).toContain(i18n.global.t('library.status.latest'));
      expect(row().text()).not.toContain(i18n.global.t('library.status.outdated'));

      const tasks = useTasksStore();
      tasks.active = [{ state: 'queued', op: 'upgrade', target: { kind: 'cask', token } } as Task];
      await flushPromises();
      expect(row().text()).toContain(i18n.global.t('library.status.queued'));
      expect(row().get('input[type="checkbox"]').attributes('disabled')).toBeDefined();
      tasks.active = [{ ...tasks.active[0]!, target: { kind: 'cask', token: 'other/tap/unlisted-editor' } }];
      await flushPromises();
      expect(row().text()).toContain(i18n.global.t('library.status.latest'));
      expect(row().get('input[type="checkbox"]').attributes('disabled')).toBeUndefined();
    } finally {
      wrapper.unmount();
      disposePinia(context.pinia);
      window.history.replaceState(null, '', '/');
    }
  }
);

it('Homebrew command text and Brewfiles resolve aliases without changing catalog identities', () => {
  const alias = { tap: 'stablyai/orca', brewToken: 'orca' };
  expect(catalogInstallCommand('cask', 'stablyai-orca', alias)).toBe('brew install --cask stablyai/orca/orca');
  expect(catalogInstallCommand('cask', 'orca')).toBe('brew install --cask orca');
  expect(
    catalogBrewfile(
      [
        { kind: 'cask', token: 'stablyai-orca' },
        { kind: 'cask', token: 'orca' }
      ],
      (_kind, token) => (token === 'stablyai-orca' ? alias : null),
      'Apps'
    )
  ).toBe('# Apps\ncask "stablyai/orca/orca"\ncask "orca"\n');
});

it('coexisting alias and official apps use their own catalog names and detail links', async () => {
  const context = await setup('/installed');
  const sample = (await unwrap(commands.localAppsList()))[0]!;
  const names = useNamesStore();
  for (const [token, name] of [
    ['stablyai-orca', 'Stably Orca'],
    ['orca', 'Plotly Orca']
  ]) {
    names.entries[`cask/${token}`] = {
      name: name!,
      sourceLocale: 'en-US',
      displayName: {},
      iconUrl: null,
      accentColor: null
    };
  }
  const alias: LocalApp = {
    ...sample,
    id: 'alias',
    name: 'Local Stably',
    cask: 'stablyai/orca/orca',
    catalogToken: 'stablyai-orca'
  };
  const official: LocalApp = {
    ...sample,
    id: 'official',
    name: 'Local Plotly',
    cask: 'homebrew/cask/orca',
    catalogToken: 'orca'
  };
  const wrapper = mount(AppInfoSheet, {
    props: { app: alias, open: true },
    global: {
      plugins: [context.pinia, context.router, i18n],
      stubs: { OnSheet: { template: '<section><slot /></section>' } }
    }
  });
  try {
    const detail = () => wrapper.findAll('button').find(button => button.text() === i18n.global.t('appInfo.detail'));
    expect(wrapper.text()).toContain('Stably Orca');
    expect(wrapper.text()).not.toContain('Plotly Orca');
    await detail()!.trigger('click');
    expect(wrapper.emitted('detail')?.at(-1)).toEqual(['stablyai-orca']);
    await wrapper.setProps({ app: official });
    expect(wrapper.text()).toContain('Plotly Orca');
    await detail()!.trigger('click');
    expect(wrapper.emitted('detail')?.at(-1)).toEqual(['orca']);
    await wrapper.setProps({ app: { ...alias, cask: 'orca', catalogToken: null } });
    expect(wrapper.text()).toContain('Local Stably');
    expect(wrapper.text()).not.toContain('Plotly Orca');
    expect(detail()).toBeUndefined();
    await flushPromises();
  } finally {
    wrapper.unmount();
    disposePinia(context.pinia);
  }
});

it('alias tasks settle only their installation, and previews require a verified catalog identity', async () => {
  const context = await setup('/installed');
  try {
    const sample = (await unwrap(commands.localAppsList()))[0]!;
    const alias: LocalApp = {
      ...sample,
      id: 'alias',
      source: 'homebrew',
      cask: 'stablyai/orca/orca',
      catalogToken: 'stablyai-orca'
    };
    const official: LocalApp = {
      ...sample,
      id: 'official',
      source: 'homebrew',
      cask: 'homebrew/cask/orca',
      catalogToken: 'orca'
    };
    const apps = useLocalAppsStore();
    apps.items = [alias, official];
    expect(apps.findCask('stablyai-orca')?.id).toBe('alias');
    expect(apps.findCask('stablyai/orca/orca')?.id).toBe('alias');
    expect(apps.findCask('orca')?.id).toBe('official');
    expect(matchesCask(alias, 'orca')).toBe(false);
    expect(
      matchLocalApps([alias, official, { ...alias, id: 'unverified', cask: 'orca', catalogToken: null }], 'orca').map(
        app => app.id
      )
    ).toEqual(['official']);
    const installed = (await unwrap(commands.libraryList()))[0]!;
    const library = useLibraryStore();
    library.items = [
      { ...installed, kind: 'cask', token: 'stablyai-orca', fullToken: alias.cask! },
      { ...installed, kind: 'cask', token: 'orca', fullToken: official.cask! }
    ];
    expect(library.find('cask', alias.cask!)?.token).toBe('stablyai-orca');
    apps.settle({
      state: 'succeeded',
      op: 'uninstall',
      target: { kind: 'cask', token: 'stablyai-orca' },
      finishedAt: Date.now()
    } as Task);
    expect(apps.items.map(app => app.id)).toEqual(['official']);
  } finally {
    disposePinia(context.pinia);
  }
});
