import { flushPromises, mount } from '@vue/test-utils';
import type { VueWrapper } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import CommandPalette from '@/components/palette/CommandPalette.vue';
import SearchPage from '@/pages/SearchPage.vue';
import { i18n } from '@/i18n';
import { commands, events, unwrap } from '@/ipc/client';
import type { LocalItem, LocalSearchHit } from '@/ipc/client';
import type { Page } from '@/ipc/bindings';
import { startLocalState, stopStateSubscriptions } from '@/stores';
import { useLinksStore } from '@/stores/links';
import { useLocalAppsStore } from '@/stores/localApps';
import { setup } from './helpers';

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => {
    resolve = done;
  });
  return { promise, resolve };
}
const ok = <T>(data: T) => ({ status: 'ok' as const, data });
const wrappers: VueWrapper[] = [];
afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount());
  stopStateSubscriptions();
  vi.restoreAllMocks();
  document.body.innerHTML = '';
});

describe.each(['SearchPage', 'CommandPalette'] as const)('%s search cache lifecycle', component => {
  it.each(['catalog sync', 'query change', 'unmount'] as const)(
    'a response arriving after %s cannot replace current link metadata',
    async invalidation => {
      const { pinia, router } = await setup('/search?q=lumen');
      await startLocalState();
      await flushPromises();
      const fixture = (await unwrap(commands.catalogGet('link', 'lumen-notes'))) as LocalItem;
      const oldItem: LocalItem = {
        ...fixture,
        bundleIds: ['old.bundle'],
        link: { ...fixture.link!, downloadUrl: 'https://example.test/old-download' }
      };
      const newItem: LocalItem = {
        ...fixture,
        bundleIds: ['new.bundle'],
        link: { ...fixture.link!, downloadUrl: null, github: null, homepage: 'https://example.test/new' }
      };
      const localApps = useLocalAppsStore();
      expect(localApps.items[0]).toBeDefined();
      const installed = { ...localApps.items[0]!, bundleId: 'new.bundle' };
      localApps.items = [installed];
      const links = useLinksStore();
      links.remember([oldItem]);
      const page = (item: LocalItem): Page<LocalSearchHit> => ({
        items: [{ item, matchedName: null, score: 1 }],
        total: 1
      });
      const oldResponse = deferred<ReturnType<typeof ok<Page<LocalSearchHit>>>>();
      const search = vi
        .spyOn(commands, 'catalogSearch')
        .mockReturnValueOnce(oldResponse.promise)
        .mockResolvedValue(ok(page(newItem)));
      vi.spyOn(commands, 'catalogGet').mockResolvedValue(ok(newItem));
      const global = { plugins: [pinia, router, i18n] };
      const wrapper =
        component === 'SearchPage'
          ? mount(SearchPage, { global, attachTo: document.body })
          : mount(CommandPalette, { props: { open: true }, global, attachTo: document.body });
      wrappers.push(wrapper);
      await flushPromises();
      const type = async (query: string) => {
        // The palette teleports its input; both tests exercise the actual component's input handler.
        const input = document.body.querySelector<HTMLInputElement>('input')!;
        input.value = query;
        input.dispatchEvent(new Event('input', { bubbles: true }));
        await flushPromises();
      };
      if (component === 'CommandPalette') await type('lumen');
      expect(search).toHaveBeenCalledTimes(1);

      if (invalidation === 'catalog sync') {
        await events.catalogSynced.emit(await unwrap(commands.catalogStatus()));
      } else if (invalidation === 'query change') {
        await type('lumen notes');
      } else {
        wrapper.unmount();
        wrappers.pop();
        // Another consumer now holds current metadata; the disposed page must not write to shared state.
        links.remember([newItem]);
      }
      await flushPromises();
      const expectCurrent = () => {
        expect(links.get(fixture.token)?.bundleIds).toEqual(['new.bundle']);
        expect(links.installed(fixture.token)?.id).toBe(installed.id);
        expect(links.target(links.get(fixture.token))).toBe('homepage');
      };
      expectCurrent();
      oldResponse.resolve(ok(page(oldItem)));
      await flushPromises();
      expectCurrent();
      expect(search).toHaveBeenCalledTimes(invalidation === 'unmount' ? 1 : 2);
    }
  );
});
