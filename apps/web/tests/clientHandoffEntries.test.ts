// @vitest-environment jsdom
import { mount } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { resolveComponent } from 'vue';
import OpenInOpenNavo from '../app/components/background/OpenInOpenNavo.vue';
import { siteMessage } from '../server/utils/siteCopy';

const openUrl = vi.fn();

beforeEach(() => {
  vi.stubGlobal('resolveComponent', resolveComponent);
  vi.stubGlobal('useI18n', () => ({
    t: (key: string) => siteMessage('en-US', key)
  }));
  vi.stubGlobal('useLocalePath', () => (path: string) => path);
  vi.stubGlobal('useClientHandoff', () => ({ openUrl }));
});

afterEach(() => {
  openUrl.mockReset();
  vi.unstubAllGlobals();
});

const NuxtLink = {
  props: ['to'],
  template: '<a :href="to"><slot /></a>'
};

describe('background item handoff', () => {
  it.each([
    ['opennavo://startup?label=com.example.agent&preview=1', 'startup'],
    ['opennavo://scan/example?preview=1', 'scan'],
    ['opennavo://uninstall/example?preview=1', 'uninstall']
  ])('reports %s as %s', async (href, kind) => {
    const wrapper = mount(OpenInOpenNavo, {
      props: { href, label: 'Open in OpenNavo', hint: 'Preview only' },
      global: { stubs: { NuxtLink } }
    });
    await wrapper.get('a').trigger('click');
    expect(openUrl).toHaveBeenCalledWith(href, kind);
  });
});
