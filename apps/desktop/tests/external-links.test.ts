import { readFileSync } from 'node:fs';
import { flushPromises, mount } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { OnKeyValueList, OnVersionEntry } from '@opennavo/ui';
import type { ReleaseEntry } from '@opennavo/api';
import { startExternalLinks } from '@/utils/external';

const { isTauri, openUrl } = vi.hoisted(() => ({ isTauri: vi.fn(), openUrl: vi.fn() }));
vi.mock('@tauri-apps/api/core', () => ({ isTauri }));
vi.mock('@tauri-apps/plugin-opener', () => ({ openUrl }));

const homepage = 'https://code.visualstudio.com/';
const sourceUrl = 'https://code.visualstudio.com/updates/v1_95';
const entry: ReleaseEntry = {
  id: 1,
  version: '1.95.0',
  title: null,
  publishedAt: null,
  brewCommittedAt: null,
  source: 'webpage',
  sourceUrl,
  isLatest: true,
  isPrerelease: false,
  hasNotes: false,
  summary: null,
  sections: [],
  bodyMarkdown: null,
  translation: { status: 'none' }
};

let stop = () => {};
const unmounts: (() => void)[] = [];
beforeEach(() => {
  isTauri.mockReturnValue(true);
  openUrl.mockResolvedValue(undefined);
});
afterEach(() => {
  stop();
  unmounts.splice(0).forEach(unmount => unmount());
  document.body.innerHTML = '';
  vi.clearAllMocks();
});

function mountLinks(onOpenSource?: (event: MouseEvent) => void) {
  const facts = mount(OnKeyValueList, {
    props: { items: [{ key: 'homepage', label: 'Homepage', value: 'Visual Studio Code', href: homepage }] },
    attachTo: document.body
  });
  const version = mount(OnVersionEntry, { props: { entry, onOpenSource }, attachTo: document.body });
  unmounts.push(
    () => facts.unmount(),
    () => version.unmount()
  );
  return { facts, version };
}

function preventedByApp(element: Element, event: MouseEvent) {
  let prevented = false;
  document.addEventListener(
    event.type,
    value => {
      prevented = value.defaultPrevented;
      // Cancel after the app listener has run, so jsdom does not attempt navigation.
      value.preventDefault();
    },
    { once: true }
  );
  element.dispatchEvent(event);
  return prevented;
}

describe('Shared UI external links in the desktop app', () => {
  it('installs delegated handling during desktop startup', () => {
    const source = readFileSync('src/main.ts', 'utf8');
    expect(source.indexOf('[startExternalLinks()]')).toBeGreaterThan(-1);
    expect(source.indexOf('[startExternalLinks()]')).toBeLessThan(source.indexOf("app.mount('#app')"));
    expect(source).toContain('for (const cleanup of navigationCleanups) cleanup()');
  });

  it.each([
    ['click', 0, {}],
    ['click', 0, { metaKey: true }],
    ['click', 0, { ctrlKey: true }],
    ['auxclick', 1, {}]
  ] as const)(
    'opens homepage and release-source links via the opener for %s button %s %j',
    async (type, button, keys) => {
      const { facts, version } = mountLinks();
      stop = startExternalLinks();
      for (const [element, href] of [
        [facts.get('a span').element, homepage],
        [version.get('a').element, sourceUrl]
      ] as const) {
        const click = new MouseEvent(type, { bubbles: true, cancelable: true, button, ...keys });
        element.dispatchEvent(click);
        expect(click.defaultPrevented).toBe(true);
        await flushPromises();
        expect(openUrl).toHaveBeenLastCalledWith(href);
      }
      expect(openUrl).toHaveBeenCalledTimes(2);
    }
  );

  it('opens http links through the scoped opener', async () => {
    stop = startExternalLinks();
    const link = document.createElement('a');
    link.href = 'http://example.test/plain';
    link.target = '_blank';
    document.body.append(link);
    const click = new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 });
    expect(preventedByApp(link, click)).toBe(true);
    await flushPromises();
    expect(openUrl).toHaveBeenCalledExactlyOnceWith('http://example.test/plain');
  });

  it('opener capability matches the delegated scheme filter', () => {
    type CapabilityPermission = { identifier: string; allow?: { url: string }[] };
    const capability = JSON.parse(readFileSync('src-tauri/capabilities/main.json', 'utf8')) as {
      permissions: (string | CapabilityPermission)[];
    };
    const opener = capability.permissions.find(
      (permission): permission is CapabilityPermission & { allow: { url: string }[] } =>
        typeof permission !== 'string' && permission.identifier === 'opener:allow-open-url'
    );
    expect(opener).toBeDefined();
    const allowed = opener?.allow.map(item => item.url) ?? [];
    for (const url of ['http://*', 'https://*', 'x-apple.systempreferences:*']) {
      expect(allowed).toContain(url);
    }
  });

  it('respects an existing source-link handler without opening the URL twice', async () => {
    const { version } = mountLinks(event => {
      event.preventDefault();
      void openUrl((event.currentTarget as HTMLAnchorElement).href);
    });
    stop = startExternalLinks();
    await version.get('a').trigger('click');
    await flushPromises();
    expect(openUrl).toHaveBeenCalledExactlyOnceWith(sourceUrl);
  });

  it('leaves shared-component links as ordinary anchors in browser mode', () => {
    isTauri.mockReturnValue(false);
    const { facts, version } = mountLinks();
    stop = startExternalLinks();
    for (const wrapper of [facts, version]) {
      const link = wrapper.get('a');
      expect(link.attributes('target')).toBe('_blank');
      const click = new MouseEvent('click', { bubbles: true, cancelable: true });
      expect(preventedByApp(link.element, click)).toBe(false);
    }
    expect(openUrl).not.toHaveBeenCalled();
  });

  it('ignores internal navigation, unsupported schemes and right clicks, and removes its listeners', async () => {
    stop = startExternalLinks();
    const link = document.createElement('a');
    document.body.append(link);
    for (const [href, target, button] of [
      ['https://tauri.localhost/#/discover', '_self', 0],
      ['mailto:hello@example.test', '_blank', 0],
      [homepage, '_blank', 2]
    ] as const) {
      link.href = href;
      link.target = target;
      const click = new MouseEvent(button === 2 ? 'auxclick' : 'click', {
        bubbles: true,
        cancelable: true,
        button
      });
      expect(preventedByApp(link, click)).toBe(false);
    }
    stop();
    link.href = homepage;
    link.target = '_blank';
    for (const [type, button] of [
      ['click', 0],
      ['auxclick', 1]
    ] as const) {
      const click = new MouseEvent(type, { bubbles: true, cancelable: true, button });
      expect(preventedByApp(link, click)).toBe(false);
    }
    await flushPromises();
    expect(openUrl).not.toHaveBeenCalled();
  });
});
